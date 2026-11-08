package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type received struct {
	path, query string
	header      http.Header
	body        []byte
}

type storage struct {
	*httptest.Server
	mu     sync.Mutex
	got    []received
	status int
}

func newStorage(t *testing.T) *storage {
	s := &storage{status: http.StatusNoContent}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.got = append(s.got, received{r.URL.Path, r.URL.RawQuery, r.Header.Clone(), b})
		code := s.status
		s.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *storage) last(t *testing.T) received {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.got) == 0 {
		t.Fatal("nothing forwarded")
	}
	return s.got[len(s.got)-1]
}

func gateway(t *testing.T, tokens map[string]string) (*Gateway, *storage, *storage) {
	m, l := newStorage(t), newStorage(t)
	mu, _ := url.Parse(m.URL + "/vm")
	lu, _ := url.Parse(l.URL)
	return New(Options{MetricsURL: mu, LogsURL: lu, Tokens: tokens, Log: slog.New(slog.DiscardHandler)}), m, l
}

func push(h http.Handler, path, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPRoutes(t *testing.T) {
	g, m, l := gateway(t, nil)
	h := g.Handler()
	tests := []struct {
		path, want string
		store      *storage
	}{
		{"/v1/metrics", "/vm/opentelemetry/v1/metrics", m},
		{"/api/v1/write", "/vm/api/v1/write", m},
		{"/v1/logs", "/insert/opentelemetry/v1/logs", l},
		{"/loki/api/v1/push?_stream_fields=app", "/insert/loki/api/v1/push", l},
	}
	for _, tt := range tests {
		rec := push(h, tt.path, "payload", "Content-Type", "application/x-protobuf", "Content-Encoding", "gzip",
			"Authorization", "Bearer secret", "Cookie", "c=1", "VL-Stream-Fields", "app")
		got := tt.store.last(t)
		if rec.Code != http.StatusNoContent || got.path != tt.want || string(got.body) != "payload" {
			t.Fatalf("%s: %d forwarded to %q with %q", tt.path, rec.Code, got.path, got.body)
		}
		if got.header.Get("Content-Encoding") != "gzip" || got.header.Get("Content-Type") != "application/x-protobuf" ||
			got.header.Get("VL-Stream-Fields") != "app" {
			t.Fatalf("%s: headers not kept: %v", tt.path, got.header)
		}
		if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" {
			t.Fatalf("%s: credentials forwarded: %v", tt.path, got.header)
		}
	}
	if q := l.last(t).query; q != "_stream_fields=app" {
		t.Fatalf("loki query = %q", q)
	}
	if rec := push(h, "/v1/traces", "x"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("traces without storage = %d", rec.Code)
	}
	if _, err := coltrace.NewTraceServiceClient(dialGRPC(t, g)).Export(context.Background(), &coltrace.ExportTraceServiceRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("grpc traces without storage: %v", err)
	}
}

func TestTraces(t *testing.T) {
	g, _, _ := gateway(t, map[string]string{"ci": "t1"})
	tr := newStorage(t)
	g.opts.TracesURL, _ = url.Parse(tr.URL)
	h := g.Handler()

	rec := push(h, "/v1/traces", `{"resourceSpans":[{"schemaUrl":"z"}]}`, "Content-Type", "application/json",
		"Authorization", "Bearer t1", "VT-Extra-Fields", "env=lab")
	got := tr.last(t)
	var req coltrace.ExportTraceServiceRequest
	if rec.Code != http.StatusNoContent || got.path != "/insert/opentelemetry/v1/traces" ||
		proto.Unmarshal(got.body, &req) != nil || req.ResourceSpans[0].SchemaUrl != "z" {
		t.Fatalf("http: %d %+v", rec.Code, got)
	}
	if v := got.header.Get("VT-Extra-Fields"); v != "env=lab,source=ci" {
		t.Fatalf("extra fields = %q", v)
	}

	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer t1")
	if _, err := coltrace.NewTraceServiceClient(dialGRPC(t, g)).Export(ctx, &coltrace.ExportTraceServiceRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := tr.last(t); got.path != "/insert/opentelemetry/v1/traces" || got.header.Get("VT-Extra-Fields") != "source=ci" {
		t.Fatalf("grpc: %+v", got)
	}
}

func TestOTLPJSONBecomesProtobuf(t *testing.T) {
	g, m, l := gateway(t, nil)
	h := g.Handler()
	body := `{"resourceMetrics":[{"schemaUrl":"x","unknownField":1}]}`
	if rec := push(h, "/v1/metrics", body, "Content-Type", "application/json; charset=utf-8"); rec.Code != http.StatusNoContent {
		t.Fatalf("json = %d %s", rec.Code, rec.Body)
	}
	got := m.last(t)
	var req colmetrics.ExportMetricsServiceRequest
	if got.header.Get("Content-Type") != "application/x-protobuf" || proto.Unmarshal(got.body, &req) != nil || req.ResourceMetrics[0].SchemaUrl != "x" {
		t.Fatalf("forwarded %+v", got)
	}

	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write([]byte(`{"resourceLogs":[{"schemaUrl":"y"}]}`))
	_ = zw.Close()
	if rec := push(h, "/v1/logs", zipped.String(), "Content-Type", "application/json", "Content-Encoding", "gzip"); rec.Code != http.StatusNoContent {
		t.Fatalf("gzip json = %d %s", rec.Code, rec.Body)
	}
	if got := l.last(t); got.header.Get("Content-Encoding") != "" || got.header.Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("forwarded headers %v", got.header)
	}
	if rec := push(h, "/v1/logs", "{nope", "Content-Type", "application/json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d", rec.Code)
	}
}

func TestTokens(t *testing.T) {
	g, m, l := gateway(t, map[string]string{"ci": "t1", "laptop": "t2"})
	h := g.Handler()
	for _, auth := range []string{"", "Bearer nope", "t1"} {
		if rec := push(h, "/v1/metrics", "x", "Authorization", auth); rec.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q = %d, want 401", auth, rec.Code)
		}
	}
	if rec := push(h, "/v1/metrics", "x", "Authorization", "Bearer t1"); rec.Code != http.StatusNoContent || m.last(t).query != "extra_label=source%3Dci" {
		t.Fatalf("metrics: %d %q", rec.Code, m.last(t).query)
	}
	push(h, "/v1/logs", "x", "Authorization", "Bearer t2", "VL-Extra-Fields", "env=lab")
	if got := l.last(t).header.Get("VL-Extra-Fields"); got != "env=lab,source=laptop" {
		t.Fatalf("logs extra fields = %q", got)
	}
}

func TestErrors(t *testing.T) {
	g, m, _ := gateway(t, nil)
	h := g.Handler()
	m.status = http.StatusBadRequest
	if rec := push(h, "/v1/metrics", "x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("storage 400 = %d", rec.Code)
	}
	m.Close()
	if rec := push(h, "/v1/metrics", "x"); rec.Code != http.StatusBadGateway {
		t.Fatalf("storage down = %d", rec.Code)
	}
	if rec := push(h, "/v1/metrics", strings.Repeat("x", MaxBody+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large = %d", rec.Code)
	}
}

func TestLoadTokens(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "tokens.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tokens, err := LoadTokens(write("ci: abc\nlaptop: def\n"))
	if err != nil || tokens["ci"] != "abc" || len(tokens) != 2 {
		t.Fatalf("tokens = %v, %v", tokens, err)
	}
	for _, bad := range []string{"ci: ''\n", "a=b: x\n", "- list\n"} {
		if _, err := LoadTokens(write(bad)); err == nil {
			t.Errorf("LoadTokens(%q) succeeded", bad)
		}
	}
}

func dialGRPC(t *testing.T, g *Gateway) *grpc.ClientConn {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := g.GRPCServer()
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGRPC(t *testing.T) {
	g, m, l := gateway(t, map[string]string{"ci": "t1"})
	conn := dialGRPC(t, g)
	req := &colmetrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{SchemaUrl: "x"}}}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer t1")

	if _, err := colmetrics.NewMetricsServiceClient(conn).Export(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no token: %v", err)
	}
	if _, err := colmetrics.NewMetricsServiceClient(conn).Export(ctx, req); err != nil {
		t.Fatal(err)
	}
	got := m.last(t)
	var back colmetrics.ExportMetricsServiceRequest
	if got.path != "/vm/opentelemetry/v1/metrics" || got.query != "extra_label=source%3Dci" ||
		got.header.Get("Content-Type") != "application/x-protobuf" || proto.Unmarshal(got.body, &back) != nil || back.ResourceMetrics[0].SchemaUrl != "x" {
		t.Fatalf("forwarded %+v", got)
	}

	if _, err := collogs.NewLogsServiceClient(conn).Export(metadata.AppendToOutgoingContext(ctx, "vl-stream-fields", "app"), &collogs.ExportLogsServiceRequest{}); err != nil {
		t.Fatal(err)
	}
	if h := l.last(t).header; h.Get("VL-Extra-Fields") != "source=ci" || h.Get("VL-Stream-Fields") != "app" {
		t.Fatalf("log headers = %v", h)
	}

	l.status = http.StatusServiceUnavailable
	if _, err := collogs.NewLogsServiceClient(conn).Export(ctx, &collogs.ExportLogsServiceRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("storage 503: %v", err)
	}
	l.status = http.StatusBadRequest
	if _, err := collogs.NewLogsServiceClient(conn).Export(ctx, &collogs.ExportLogsServiceRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("storage 400: %v", err)
	}
}
