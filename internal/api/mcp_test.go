package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tpural/tremolo/internal/agents"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/hosts"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCP(t *testing.T) {
	dir, _ := users.Open(nil)
	_ = dir.Create("tp", "", users.Admin, "a-long-password")
	db, _ := state.Open("")
	cfg, _ := agents.ParseConfig(`{"enabled":true}`)
	svc, _ := agents.New(agents.Options{Config: cfg, DB: db, Log: slog.New(slog.DiscardHandler)})
	var mu sync.Mutex
	var seen []url.Values
	stores := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query())
		mu.Unlock()
		if q := r.URL.Query().Get("query"); strings.HasSuffix(r.URL.Path, "/select/logsql/query") && strings.Contains(q, "stats count()") {
			n := "120"
			switch {
			case strings.Contains(q, "orders"):
				n = "0"
			case strings.Contains(q, "DATABASE_URL"):
				n = "3"
			}
			_, _ = w.Write([]byte(`{"n":"` + n + `"}` + "\n"))
			return
		} else if strings.Contains(q, "orders") {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/select/logsql/query") {
			_, _ = w.Write([]byte(`{"_time":"2026-11-11T10:00:00Z","_msg":"connection refused","k8s.namespace.name":"gridtie"}` + "\n"))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"pod":"a"},"values":[[1,"1"],[2,"5"],[3,"9"]]}]}}`))
	}))
	t.Cleanup(stores.Close)
	up, _ := url.Parse(stores.URL)
	store, _ := dashboard.Load("")
	srv := httptest.NewServer(New(Options{
		MetricsURL: up, LogsURL: up, Dashboards: store, Users: dir, DB: db, Hosts: hosts.New(up, http.DefaultClient, nil, time.Hour),
		UI: fstest.MapFS{"index.html": {}}, Log: slog.New(slog.DiscardHandler), Agents: svc,
	}))
	t.Cleanup(srv.Close)
	tok, _, _ := dir.CreateToken("tp", "claude", &users.Scope{
		Metrics: []string{`{k8s_namespace_name="gridtie"}`}, Logs: []string{`k8s.namespace.name:=gridtie`}, Allow: []string{users.Investigate},
	}, time.Time{})

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{tok}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	call := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res.Content[0].(*mcp.TextContent).Text, res.IsError
	}

	if got, _ := call("overview", nil); !strings.Contains(got, "You are claude (for tp)") || !strings.Contains(got, "logs matching k8s.namespace.name:=gridtie") {
		t.Errorf("overview = %s", got)
	}
	// Queries go through the token's scope.
	if got, isErr := call("logs", map[string]any{"query": "error"}); isErr || !strings.Contains(got, "gridtie | connection refused") {
		t.Errorf("logs = %s", got)
	}
	// A query that finds nothing says which word matched nothing on its own.
	if got, _ := call("logs", map[string]any{"query": "k8s.namespace.name:=gridtie orders DATABASE_URL"}); !strings.Contains(got,
		`k8s.namespace.name:=gridtie alone: 120 lines; with "orders": 0; with "DATABASE_URL": 3`) {
		t.Errorf("empty logs = %s", got)
	}
	if got, _ := call("metrics", map[string]any{"query": "up", "start": "1h"}); !strings.Contains(got, "last 9.00, min 1.00, max 9.00") || !strings.Contains(got, "rising") {
		t.Errorf("metrics = %s", got)
	}
	mu.Lock()
	scoped := 0
	for _, q := range seen {
		if strings.Contains(q.Get("extra_filters"), "gridtie") || strings.Contains(q.Get("extra_filters[]"), "gridtie") {
			scoped++
		}
	}
	mu.Unlock()
	if scoped < 2 {
		t.Errorf("store saw %d scoped queries of %v", scoped, seen)
	}

	// Work an investigation; actions ask first; paused, changes are refused but reads work.
	if got, _ := call("open_investigation", map[string]any{"kind": "gap", "title": "gridtie has no traces"}); !strings.Contains(got, "#1") {
		t.Fatalf("open = %s", got)
	}
	if got, _ := call("begin_action", map[string]any{"id": 1, "action": "restart", "target": "deploy/gridtie", "disruptive": true, "why": "test"}); !strings.Contains(got, "Go ahead") {
		t.Errorf("action = %s", got)
	}
	_ = svc.Pause(ctx, "tp", "")
	if got, isErr := call("record", map[string]any{"id": 1, "kind": "note", "text": "x"}); !isErr || !strings.Contains(got, "paused") {
		t.Errorf("paused record = %s", got)
	}
	if got, isErr := call("investigations", nil); isErr || !strings.Contains(got, "gridtie has no traces") {
		t.Errorf("paused read = %s", got)
	}
	if got, isErr := call("hosts", nil); !isErr || !strings.Contains(got, "needs the hosts permission") {
		t.Errorf("hosts without permission = %s", got)
	}

	prompts, err := sess.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 2 {
		t.Errorf("prompts = %v %v", prompts, err)
	}
}
