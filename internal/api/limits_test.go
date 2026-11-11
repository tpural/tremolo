package api

import (
	"context"
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
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestFit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	lim := Limits{MaxRange: 7 * 24 * time.Hour, MaxPoints: 100, MaxLogRows: 50}
	for _, tt := range []struct {
		signal, target, query string
		err                   string
		want                  map[string]string
	}{
		{users.Metrics, "/api/v1/query", "query=rate(x[5m])", "", nil},
		{users.Metrics, "/api/v1/query", "query=rate(x[30d])", "window [30d] is over 7d", nil},
		{users.Metrics, "/api/v1/query", "query=max_over_time(x[1h:1m] offset 8w)", "offset 8w", nil},
		{users.Metrics, "/api/v1/query_range", "query=up&start=-30d", "time range is over 7d", nil},
		// 1 day at 100 points is at least 864s a step.
		{users.Metrics, "/api/v1/query_range", "query=up&start=-1d&step=15s", "", map[string]string{"step": "864s"}},
		{users.Metrics, "/api/v1/query_range", "query=up&start=-1d&step=1h", "", map[string]string{"step": "3600s"}},
		{users.Logs, "/select/logsql/query", "query=*", "", map[string]string{"limit": "50", "start": "1799996400"}},
		{users.Logs, "/select/logsql/query", "query=*&limit=10&start=2027-01-14T00:00:00Z", "", map[string]string{"limit": "10"}},
		{users.Logs, "/select/logsql/query", "query=*&limit=9999&start=-1h", "", map[string]string{"limit": "50"}},
		{users.Logs, "/select/logsql/hits", "query=*&start=1700000000", "time range is over 7d", nil},
		{users.Logs, "/select/logsql/query", "query=*&start=yesterday", "can't read start", nil},
		{users.Logs, "/select/logsql/tail", "query=*", "", map[string]string{"start": ""}},
	} {
		q, _ := url.ParseQuery(tt.query)
		err := fit(lim, tt.signal, tt.target, q, now)
		switch {
		case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
			t.Errorf("%s %s: err %v, want %q", tt.target, tt.query, err, tt.err)
		case tt.err == "" && err != nil:
			t.Errorf("%s %s: %v", tt.target, tt.query, err)
		}
		for k, v := range tt.want {
			if q.Get(k) != v {
				t.Errorf("%s %s: %s = %q, want %q", tt.target, tt.query, k, q.Get(k), v)
			}
		}
	}
}

func TestNoRangeLimit(t *testing.T) {
	q, _ := url.ParseQuery("query=up&start=-365d&step=60s")
	if err := fit(Limits{MaxPoints: 100}, users.Metrics, "/api/v1/query_range", q, time.Now()); err != nil {
		t.Fatal(err)
	}
	if q.Get("step") != "315360s" {
		t.Errorf("step = %s", q.Get("step"))
	}
	if Tighter(0, time.Hour) != time.Hour || Tighter(time.Hour, 0) != time.Hour || Tighter(2*time.Hour, time.Hour) != time.Hour {
		t.Error("Tighter")
	}
}

func TestTokenRange(t *testing.T) {
	dir, _ := users.Open(nil)
	_ = dir.Create("tp", "", users.Admin, "a-long-password")
	db, _ := state.Open("")
	up, _ := url.Parse(httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })).URL)
	store, _ := dashboard.Load("")
	h := New(Options{
		MetricsURL: up, LogsURL: up, Dashboards: store, Users: dir, DB: db, Hosts: hosts.New(up, http.DefaultClient, nil, time.Hour),
		UI: fstest.MapFS{"index.html": {}}, Log: slog.New(slog.DiscardHandler),
		Limits:          Limits{Timeout: time.Minute, Concurrent: 2},
		AssistantLimits: Limits{MaxRange: 30 * 24 * time.Hour, Timeout: time.Minute, Concurrent: 2},
	})
	personal, _, _ := dir.CreateToken("tp", "ci", nil, time.Time{})
	day, _, _ := dir.CreateToken("tp", "claude", &users.Scope{Logs: []string{"*"}, MaxRange: 86400}, time.Time{})
	wide, _, _ := dir.CreateToken("tp", "wide", &users.Scope{Logs: []string{"*"}}, time.Time{})
	do := func(tok, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, tt := range []struct {
		tok, start string
		code       int
	}{
		{personal, "-365d", 200}, // people have no range limit
		{day, "-12h", 200},
		{day, "-2d", 400}, // its own look-back
		{wide, "-7d", 200},
		{wide, "-60d", 400}, // the assistant limit
	} {
		if rec := do(tt.tok, "GET", "/api/logs/hits?query=*&start="+tt.start, ""); rec.Code != tt.code {
			t.Errorf("start %s = %d %s, want %d", tt.start, rec.Code, rec.Body, tt.code)
		}
	}
	if rec := do(day, "GET", "/api/me", ""); !strings.Contains(rec.Body.String(), `"maxRange":86400,`) {
		t.Errorf("me = %s", rec.Body)
	}
	// A token can't be given more than the assistant limit.
	if rec := do(personal, "POST", "/api/me/tokens", `{"name":"x","scope":{"logs":["*"],"maxRange":7776000}}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "at most 30d") {
		t.Errorf("create over the limit = %d %s", rec.Code, rec.Body)
	}
}

func TestParseTime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for v, want := range map[string]time.Time{
		"1800000000":          now,
		"1800000000.5":        now.Add(500 * time.Millisecond),
		"1800000000000":       now,
		"1800000000000000000": now,
		"-1h":                 now.Add(-time.Hour),
		"2d":                  now.Add(-48 * time.Hour),
		"now":                 now,
	} {
		if got, err := parseTime(v, now); err != nil || !got.Equal(want) {
			t.Errorf("%s = %v %v, want %v", v, got, err, want)
		}
	}
}

func TestQueryLimitsLive(t *testing.T) {
	dir, _ := users.Open(nil)
	_ = dir.Create("tp", "", users.Admin, "a-long-password")
	db, _ := state.Open("")
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "slow" {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(func() { close(release); slow.Close() })
	up, _ := url.Parse(slow.URL)
	store, _ := dashboard.Load("")
	h := New(Options{
		MetricsURL: up, LogsURL: up, Dashboards: store, Users: dir, DB: db, Hosts: hosts.New(up, http.DefaultClient, nil, time.Hour),
		UI: fstest.MapFS{"index.html": {}}, Log: slog.New(slog.DiscardHandler),
		Limits:          Limits{MaxRange: time.Hour, Timeout: 100 * time.Millisecond, Concurrent: 1},
		AssistantLimits: Limits{MaxRange: time.Hour, Timeout: time.Minute, Concurrent: 1},
	})
	tok, _, _ := dir.CreateToken("tp", "claude", &users.Scope{Logs: []string{"*"}}, time.Time{})
	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// The assistant's reads are in the audit log, once a minute per query.
	do("/api/logs/query?query=error")
	do("/api/logs/query?query=error")
	log, _ := db.AuditLog(context.Background(), state.AuditFilter{Via: "claude"})
	if len(log) != 1 || log[0].Action != "read logs" || log[0].Detail != "error" {
		t.Errorf("audit = %+v", log)
	}

	// One query at a time: a second waits, and is refused when the first doesn't finish.
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- do("/api/logs/query?query=slow") }()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if rec := do("/api/logs/query?query=other"); rec.Code != http.StatusTooManyRequests || time.Since(start) < queueWait {
		t.Errorf("second query = %d after %s", rec.Code, time.Since(start))
	}
	release <- struct{}{}
	if rec := <-done; rec.Code != 200 {
		t.Errorf("slow query = %d", rec.Code)
	}

	// People's queries time out with a reason.
	personal, _, _ := dir.CreateToken("tp", "ci", nil, time.Time{})
	req := httptest.NewRequest("GET", "/api/logs/query?query=slow", nil)
	req.Header.Set("Authorization", "Bearer "+personal)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "longer than 100ms") {
		t.Errorf("timeout = %d %s", rec.Code, rec.Body)
	}
}
