package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tpural/tremolo/internal/calendar"
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
	if m.path != "/prefix/api/v1/label/a/b/values" {
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

func TestTraces(t *testing.T) {
	h := handler(t, &url.URL{}, &url.URL{})
	if rec := get(h, "/api/traces/services"); rec.Code != http.StatusNotFound {
		t.Fatalf("traces off = %d", rec.Code)
	}
	if rec := get(h, "/api/version"); !strings.Contains(rec.Body.String(), `"traces":false`) {
		t.Fatalf("version = %s", rec.Body)
	}

	var got seen
	store, _ := dashboard.Load("")
	h = New(Options{
		MetricsURL: &url.URL{}, LogsURL: &url.URL{}, TracesURL: upstream(t, &got), Dashboards: store,
		Hosts: hosts.New(&url.URL{}, http.DefaultClient, nil, time.Hour), UI: fstest.MapFS{}, Log: slog.New(slog.DiscardHandler),
	})
	for _, tt := range []struct{ path, want, query string }{
		{"/api/traces/services", "/prefix/select/jaeger/api/services", ""},
		{"/api/traces/services/a%2Fb/operations", "/prefix/select/jaeger/api/services/a/b/operations", ""},
		{"/api/traces/search?service=x&limit=5", "/prefix/select/jaeger/api/traces", "service=x&limit=5"},
		{"/api/traces/trace/ABCDEF0123456789", "/prefix/select/jaeger/api/traces/abcdef0123456789", ""},
		{"/api/traces/query?query=span_id:*", "/prefix/select/logsql/query", "query=span_id:*"},
		{"/api/traces/field_values?field=name", "/prefix/select/logsql/field_values", "field=name"},
	} {
		if rec := get(h, tt.path); rec.Code != http.StatusOK || got.path != tt.want || got.query != tt.query {
			t.Errorf("%s = %d, upstream %q?%s, want %q?%s", tt.path, rec.Code, got.path, got.query, tt.want, tt.query)
		}
	}
	if rec := get(h, "/api/traces/trace/not-hex"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d", rec.Code)
	}
	if rec := get(h, "/api/version"); !strings.Contains(rec.Body.String(), `"traces":true`) {
		t.Errorf("version = %s", rec.Body)
	}
}

func TestMaintenance(t *testing.T) {
	cal, err := calendar.Open(nil, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	m := hosts.NewMaintenance(cal)
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"result":[{"metric":{"host_name":"nas"},"value":[1,"`+
			strconv.FormatInt(time.Now().Unix(), 10)+`"]}]}}`)
	}))
	defer metrics.Close()
	u, _ := url.Parse(metrics.URL)
	h := New(Options{
		MetricsURL: u, LogsURL: u, Maintenance: m, UI: fstest.MapFS{}, Log: slog.New(slog.DiscardHandler),
		Hosts: hosts.New(u, http.DefaultClient, nil, time.Hour, m),
	})
	send := func(method, path, body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec.Code
	}
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, c := range []struct {
		method, body string
		want         int
	}{
		{http.MethodPut, `{"until":"2000-01-01T00:00:00Z"}`, http.StatusBadRequest},
		{http.MethodPut, `not json`, http.StatusBadRequest},
		{http.MethodPut, `{"until":"` + until + `","note":"disk swap"}`, http.StatusNoContent},
	} {
		if got := send(c.method, "/api/hosts/nas/maintenance", c.body); got != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.body, got, c.want)
		}
	}
	state := func() string {
		var list []hosts.Host
		if err := json.NewDecoder(get(h, "/api/hosts").Body).Decode(&list); err != nil || len(list) != 1 {
			t.Fatalf("hosts = %v, %v", list, err)
		}
		return string(list[0].State) + "/" + list[0].Reason
	}
	if got := state(); got != "maintenance/disk swap" {
		t.Errorf("state = %s", got)
	}
	if got := send(http.MethodDelete, "/api/hosts/nas/maintenance", ""); got != http.StatusNoContent {
		t.Errorf("delete = %d", got)
	}
	if got := state(); got != "ready/" {
		t.Errorf("state after clear = %s", got)
	}
}
