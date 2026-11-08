package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/hosts"
)

type seen struct {
	path, query string
	header      http.Header
}

func upstream(t *testing.T, got *seen) *url.URL {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = seen{r.URL.Path, r.URL.RawQuery, r.Header.Clone()}
		_, _ = io.WriteString(w, `{"status":"success"}`)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/prefix")
	return u
}

func handler(t *testing.T, metrics, logs *url.URL) http.Handler {
	store, err := dashboard.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		MetricsURL: metrics, LogsURL: logs, Dashboards: store, Version: "test",
		Hosts: hosts.New(metrics, http.DefaultClient, nil, time.Hour),
		UI: fstest.MapFS{
			"index.html":    {Data: []byte("app")},
			"assets/app.js": {Data: []byte("js")},
			"favicon.svg":   {Data: []byte("<svg/>")},
		},
		Log: slog.New(slog.DiscardHandler),
	})
}

func get(h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProxiesOnlyReadEndpoints(t *testing.T) {
	var m, l seen
	h := handler(t, upstream(t, &m), upstream(t, &l))

	rec := get(h, "/api/metrics/query_range?query=up&step=15s", "Cookie", "secret=1", "Authorization", "Bearer x")
	if rec.Code != http.StatusOK || m.path != "/prefix/api/v1/query_range" || m.query != "query=up&step=15s" {
		t.Fatalf("metrics: %d %+v", rec.Code, m)
	}
	if m.header.Get("Cookie") != "" || m.header.Get("Authorization") != "" {
		t.Fatalf("browser credentials forwarded: %v", m.header)
	}
	get(h, "/api/metrics/label/a%2Fb/values")
	if m.path != "/prefix/api/v1/label/a/b/values" && m.path != "/prefix/api/v1/label/a%2Fb/values" {
		t.Fatalf("label path = %q", m.path)
	}
	get(h, "/api/logs/tail?query=*")
	if l.path != "/prefix/select/logsql/tail" {
		t.Fatalf("logs path = %q", l.path)
	}
	for _, path := range []string{"/api/metrics/admin/tsdb/delete_series", "/api/logs/delete"} {
		if rec := get(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/metrics/query", strings.NewReader("query=up"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
}

func TestUpstreamDown(t *testing.T) {
	down, _ := url.Parse("http://127.0.0.1:1")
	rec := get(handler(t, down, down), "/api/metrics/query?query=up")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "upstream unavailable") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestDashboardsAndNodes(t *testing.T) {
	h := handler(t, &url.URL{}, &url.URL{})
	var list []map[string]string
	if err := json.Unmarshal(get(h, "/api/dashboards").Body.Bytes(), &list); err != nil {
		t.Fatalf("dashboards = %v, %v", list, err)
	}
	for _, d := range list {
		if d["id"] == "host" {
			t.Fatal("hidden dashboard listed")
		}
	}
	if rec := get(h, "/api/dashboards/host"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"panels"`) {
		t.Fatalf("dashboard = %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/dashboards/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing dashboard = %d", rec.Code)
	}
	if rec := get(h, "/api/hosts"); rec.Code != http.StatusBadGateway {
		t.Fatalf("hosts with no sources = %d", rec.Code)
	}
	if rec := get(h, "/api/hosts/pi-1/pods"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("pods without kubernetes = %d", rec.Code)
	}
}

func TestServesTheApp(t *testing.T) {
	h := handler(t, &url.URL{}, &url.URL{})
	for path, want := range map[string]string{"/": "app", "/nodes/pi-1": "app", "/assets/app.js": "js", "/favicon.svg": "<svg/>"} {
		if rec := get(h, path); rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s = %d %q, want %q", path, rec.Code, rec.Body, want)
		}
	}
	if rec := get(h, "/assets/app.js"); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Error("assets are not cached")
	}
	if rec := get(h, "/api/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown api path = %d", rec.Code)
	}
}
