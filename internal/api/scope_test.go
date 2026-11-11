package api

import (
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
	"github.com/tpural/tremolo/internal/runbooks"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestAssistantTokens(t *testing.T) {
	dir, err := users.Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Create("tp", "Tp", users.Admin, "a-long-password"); err != nil {
		t.Fatal(err)
	}
	db, _ := state.Open("")
	books, err := runbooks.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	var got seen
	// The traces fake answers span counts: 3 spans, 2 of them in scope.
	traces := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = seen{r.URL.Path, r.URL.RawQuery, nil}
		if strings.Contains(r.URL.Query().Get("query"), "stats count()") {
			if r.URL.Query().Get("extra_filters") != "" {
				_, _ = io.WriteString(w, `{"n":"2"}`)
			} else {
				_, _ = io.WriteString(w, `{"n":"3"}`)
			}
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(traces.Close)
	tracesURL, _ := url.Parse(traces.URL)
	up := upstream(t, &got)
	store, _ := dashboard.Load("")
	h := New(Options{
		MetricsURL: up, LogsURL: up, TracesURL: tracesURL, Dashboards: store, Users: dir, DB: db, Runbooks: books,
		Hosts: hosts.New(up, http.DefaultClient, nil, time.Hour), UI: fstest.MapFS{"index.html": {Data: []byte("app")}},
		Log: slog.New(slog.DiscardHandler),
	})

	scoped := &users.Scope{
		Metrics: []string{`{k8s_namespace_name="angelorooms"}`, `{app="angelorooms"}`},
		Logs:    []string{`k8s.namespace.name:=angelorooms`},
		Traces:  []string{`"resource_attr:service.name":=angelorooms`},
		Allow:   []string{users.ReadRunbooks, users.WriteFixNotes},
	}
	sec, _, err := dir.CreateToken("tp", "claude", scoped, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	personal, _, _ := dir.CreateToken("tp", "ci", nil, time.Time{})
	do := func(method, path, tok string) (int, string) {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// Metrics get the token's selectors, joined with OR; a caller's own are dropped.
	got = seen{}
	if code, _ := do("GET", `/api/metrics/query?query=up&extra_filters[]={k8s_namespace_name="kube-system"}`, sec); code != 200 {
		t.Fatalf("metrics = %d", code)
	}
	q, _ := url.ParseQuery(got.query)
	if f := q["extra_filters[]"]; len(f) != 2 || f[0] != scoped.Metrics[0] || f[1] != scoped.Metrics[1] {
		t.Errorf("metrics filters = %q", f)
	}
	// Logs get one filter, since VictoriaLogs ANDs several.
	if code, _ := do("GET", `/api/logs/query?query=*&extra_filters=*`, sec); code != 200 {
		t.Fatalf("logs = %d", code)
	}
	if q, _ := url.ParseQuery(got.query); q.Get("extra_filters") != scoped.Logs[0] || len(q["extra_filters"]) != 1 {
		t.Errorf("logs filters = %q", q["extra_filters"])
	}
	// A personal token, like a browser, queries unchanged.
	if code, _ := do("GET", `/api/logs/query?limit=5&query=*&start=-1h`, personal); code != 200 || got.query != "limit=5&query=*&start=-1h" {
		t.Errorf("personal = %d %q", code, got.query)
	}

	// Traces: spans are filtered; Jaeger's API isn't, so it's refused, and a
	// trace with a span outside the scope is not found.
	if code, _ := do("GET", "/api/traces/services", sec); code != 403 {
		t.Errorf("jaeger services = %d", code)
	}
	if code, _ := do("GET", "/api/traces/trace/abc", sec); code != 404 {
		t.Errorf("partly visible trace = %d", code)
	}
	if code, _ := do("GET", "/api/traces/trace/abc", personal); code != 200 {
		t.Errorf("personal trace = %d", code)
	}

	// Only what's allowed, and never users, tokens or the audit log.
	for path, want := range map[string]int{
		"GET /api/runbooks":            200,
		"GET /api/me":                  200,
		"GET /api/runbooks/x/markdown": 404,
		"GET /api/hosts":               403,
		"GET /api/users":               403,
		"GET /api/audit":               403,
		"GET /api/me/tokens":           403,
		"POST /api/me/tokens":          403,
		"PUT /api/runbooks/x":          403,
		"POST /api/alerts/silences":    403,
		"GET /api/dashboards":          403,
		"DELETE /api/users/tp":         403,
		"GET /api/not-a-route":         404,
	} {
		method, p, _ := strings.Cut(path, " ")
		if code, body := do(method, p, sec); code != want {
			t.Errorf("%s = %d, want %d: %s", path, code, want, body)
		}
	}
	if _, body := do("GET", "/api/me", sec); !strings.Contains(body, `"allow":["runbooks","write:runbook-notes"]`) {
		t.Errorf("me doesn't show the scope: %s", body)
	}

	// A signal with no selectors can't be read.
	none, _, _ := dir.CreateToken("tp", "runbooks only", &users.Scope{Allow: []string{users.ReadRunbooks}}, time.Time{})
	if code, _ := do("GET", "/api/metrics/query?query=up", none); code != 403 {
		t.Errorf("unscoped metrics = %d", code)
	}
	// "*" reads everything, unfiltered.
	all, _, _ := dir.CreateToken("tp", "all logs", &users.Scope{Logs: []string{"*"}}, time.Time{})
	if code, _ := do("GET", "/api/logs/query?query=*&extra_filters=x&limit=5&start=-1h", all); code != 200 || got.query != "limit=5&query=%2A&start=-1h" {
		t.Errorf("all logs = %d %q", code, got.query)
	}

	// Expired tokens sign nobody in.
	if _, _, err := dir.CreateToken("tp", "old", scoped, time.Now().Add(-time.Hour)); err == nil {
		t.Error("made an already expired token")
	}
}

func TestScopeCheck(t *testing.T) {
	for _, s := range []users.Scope{
		{Allow: []string{"users"}},
		{Metrics: []string{`k8s_namespace_name="x"`}},
		{Logs: []string{""}},
		{Logs: []string{"a\nb"}},
	} {
		if err := s.Check(); err == nil {
			t.Errorf("%+v passed", s)
		}
	}
	ok := users.Scope{Metrics: []string{"*"}, Logs: []string{`app:=x`}, Allow: users.Capabilities}
	if err := ok.Check(); err != nil {
		t.Error(err)
	}
}
