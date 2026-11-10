package ingest

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const faroBatch = `{
  "meta": {
    "app": {"name": "angelorooms", "version": "1.2.0", "environment": "production"},
    "session": {"id": "abc123"},
    "page": {"url": "https://angelorooms.com/posts/42?token=secret#top"},
    "browser": {"name": "Firefox", "version": "140", "os": "Linux", "mobile": false}
  },
  "exceptions": [{"type": "TypeError", "value": "x is undefined", "timestamp": "2026-11-10T12:00:00Z",
    "stacktrace": {"frames": [{"filename": "https://angelorooms.com/_next/app.js?v=1", "function": "render", "lineno": 10, "colno": 5}]}}],
  "measurements": [{"type": "web-vitals", "values": {"lcp": 1234.5, "cls": 0.02, "delta": 5}, "timestamp": "2026-11-10T12:00:01Z"}],
  "events": [{"name": "page_view", "attributes": {"path": "/posts/7?ref=x"}, "timestamp": "2026-11-10T12:00:02Z"}]
}`

func TestFaro(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] += string(b)
		mu.Unlock()
	}))
	defer store.Close()
	u, _ := url.Parse(store.URL)
	g := New(Options{MetricsURL: u, LogsURL: u, Tokens: map[string]string{"angelorooms": "tok"}, Log: slog.New(slog.DiscardHandler)})
	h := g.Handler()

	post := func(auth string) int {
		req := httptest.NewRequest(http.MethodPost, "/faro/collect", strings.NewReader(faroBatch))
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post(""); code != http.StatusUnauthorized {
		t.Fatalf("without a token = %d", code)
	}
	if code := post("Bearer tok"); code != http.StatusAccepted {
		t.Fatalf("with a token = %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	logs, metrics := got["/insert/jsonline"], got["/api/v1/import/prometheus"]
	for _, want := range []string{
		`"kind":"exception"`, `"_msg":"TypeError: x is undefined"`, `"page":"/posts/:id"`, `"source":"angelorooms"`,
		`at render (/_next/app.js:10:5)`, `"kind":"page_view"`, `"stream":"browser"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %s:\n%s", want, logs)
		}
	}
	if strings.Contains(logs+metrics, "secret") || strings.Contains(logs+metrics, "ref=x") {
		t.Errorf("a query string was stored:\n%s\n%s", logs, metrics)
	}
	for _, want := range []string{`browser_web_vitals{app="angelorooms",page="/posts/:id",name="lcp"} 1234.5 `, `name="cls"} 0.02 `, `browser_events{app="angelorooms",kind="exception",page="/posts/:id"} 1 `} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics lack %s:\n%s", want, metrics)
		}
	}
	if strings.Contains(metrics, `name="delta"`) {
		t.Error("kept a measurement that isn't a Web Vital")
	}
}
