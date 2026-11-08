// Package ingest accepts metrics, logs and traces pushed by collectors, SDKs and
// log shippers, and forwards them to VictoriaMetrics, VictoriaLogs and VictoriaTraces.
package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/yaml"
)

// MaxBody is the largest push accepted, as sent (compressed bodies are not
// expanded: they are forwarded unchanged).
const MaxBody = 32 << 20

// Options configures a Gateway.
type Options struct {
	MetricsURL *url.URL
	LogsURL    *url.URL
	// TracesURL is VictoriaTraces; nil turns trace ingest off.
	TracesURL *url.URL
	// Tokens maps each sender's name to its bearer token. Empty means open.
	Tokens    map[string]string
	Transport http.RoundTripper
	Log       *slog.Logger
}

// Gateway forwards pushes to storage.
type Gateway struct {
	opts   Options
	client *http.Client
}

type kind int

const (
	metrics kind = iota
	logs
	traces
)

type route struct {
	kind kind
	path string
	// otlp is the request message, for routes that take OTLP, whose JSON form
	// storage doesn't accept: it is converted to protobuf here.
	otlp func() proto.Message
}

var (
	otlpMetrics = route{metrics, "/opentelemetry/v1/metrics", func() proto.Message { return &colmetrics.ExportMetricsServiceRequest{} }}
	otlpLogs    = route{logs, "/insert/opentelemetry/v1/logs", func() proto.Message { return &collogs.ExportLogsServiceRequest{} }}
	remoteWrite = route{metrics, "/api/v1/write", nil}
	lokiPush    = route{logs, "/insert/loki/api/v1/push", nil}
	otlpTraces  = route{traces, "/insert/opentelemetry/v1/traces", func() proto.Message { return &coltrace.ExportTraceServiceRequest{} }}
)

// forwardHeaders are the request headers storage needs; everything else,
// including the sender's credentials, stays here.
var forwardHeaders = append([]string{
	"Content-Type", "Content-Encoding", "X-Prometheus-Remote-Write-Version",
}, fieldHeaders...)

// fieldHeaders tell VictoriaLogs and VictoriaTraces how to index a push. Over gRPC they arrive as metadata.
var fieldHeaders = []string{"VL-Stream-Fields", "VL-Msg-Field", "VL-Time-Field", "VL-Ignore-Fields", "VL-Extra-Fields", "VT-Extra-Fields"}

// New returns a Gateway.
func New(opts Options) *Gateway {
	return &Gateway{opts: opts, client: &http.Client{Transport: opts.Transport}}
}

// LoadTokens reads a YAML map of sender name to token.
func LoadTokens(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tokens map[string]string
	if err := yaml.UnmarshalStrict(b, &tokens); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, tok := range tokens {
		if name == "" || strings.TrimSpace(tok) == "" || strings.ContainsAny(name, ",=") {
			return nil, fmt.Errorf("%s: token %q needs a name without , or = and a value", path, name)
		}
	}
	return tokens, nil
}

// Handler serves OTLP/HTTP, Prometheus remote write and Loki push.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/metrics", g.httpRoute(otlpMetrics))
	mux.Handle("POST /v1/logs", g.httpRoute(otlpLogs))
	mux.Handle("POST /api/v1/write", g.httpRoute(remoteWrite))
	mux.Handle("POST /loki/api/v1/push", g.httpRoute(lokiPush))
	if g.opts.TracesURL != nil {
		mux.Handle("POST /v1/traces", g.httpRoute(otlpTraces))
	} else {
		mux.HandleFunc("POST /v1/traces", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "trace storage is not enabled on this tremolo", http.StatusNotImplemented)
		})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return mux
}

// sender returns the token's name, "" when no tokens are configured, or an
// error when tokens are configured and this one isn't among them.
func (g *Gateway) sender(authorization string) (string, error) {
	if len(g.opts.Tokens) == 0 {
		return "", nil
	}
	tok, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok {
		return "", errors.New("missing bearer token")
	}
	name := ""
	for n, want := range g.opts.Tokens {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1 {
			name = n
		}
	}
	if name == "" {
		return "", errors.New("unknown token")
	}
	return name, nil
}

func (g *Gateway) httpRoute(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, err := g.sender(r.Header.Get("Authorization"))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tremolo"`)
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
		if err != nil {
			http.Error(w, "body too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		header := http.Header{}
		for _, h := range forwardHeaders {
			if v := r.Header.Get(h); v != "" {
				header.Set(h, v)
			}
		}
		if rt.otlp != nil && isJSON(header.Get("Content-Type")) {
			if body, err = jsonToProto(body, header.Get("Content-Encoding"), rt.otlp()); err != nil {
				http.Error(w, "invalid OTLP JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			header.Set("Content-Type", "application/x-protobuf")
			header.Del("Content-Encoding")
		}
		resp, err := g.forward(r.Context(), rt, name, r.URL.Query(), header, body)
		if err != nil {
			g.opts.Log.Warn("forward push", "path", rt.path, "sender", name, "err", err)
			http.Error(w, "storage unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for _, h := range []string{"Content-Type", "Retry-After"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
	})
}

func isJSON(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt == "application/json"
}

// jsonToProto converts an OTLP/JSON body, gzip-compressed or not, to protobuf.
func jsonToProto(body []byte, encoding string, msg proto.Message) ([]byte, error) {
	switch encoding {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if body, err = io.ReadAll(io.LimitReader(zr, MaxBody)); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported Content-Encoding %q for JSON", encoding)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, msg); err != nil {
		return nil, err
	}
	return proto.Marshal(msg)
}

// forward sends a push to storage, labelling it with the sender's name.
func (g *Gateway) forward(ctx context.Context, rt route, sender string, query url.Values, header http.Header, body []byte) (*http.Response, error) {
	base := map[kind]*url.URL{metrics: g.opts.MetricsURL, logs: g.opts.LogsURL, traces: g.opts.TracesURL}[rt.kind]
	if query == nil {
		query = url.Values{}
	}
	if sender != "" {
		switch rt.kind {
		case metrics:
			query.Add("extra_label", "source="+sender)
		case logs:
			addField(header, "VL-Extra-Fields", "source="+sender)
		case traces:
			addField(header, "VT-Extra-Fields", "source="+sender)
		}
	}
	target := url.URL{Scheme: base.Scheme, Host: base.Host, Path: strings.TrimRight(base.Path, "/") + rt.path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = header
	return g.client.Do(req)
}

func addField(header http.Header, name, field string) {
	if v := header.Get(name); v != "" {
		field = v + "," + field
	}
	header.Set(name, field)
}
