package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tpural/tremolo/internal/agents"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/hosts"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestAgentRoutes(t *testing.T) {
	dir, _ := users.Open(nil)
	_ = dir.Create("tp", "", users.Admin, "a-long-password")
	db, _ := state.Open("")
	cfg, _ := agents.ParseConfig(`{"enabled":true}`)
	svc, err := agents.New(agents.Options{Config: cfg, DB: db, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	stores := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	t.Cleanup(stores.Close)
	up, _ := url.Parse(stores.URL)
	store, _ := dashboard.Load("")
	h := New(Options{
		MetricsURL: up, LogsURL: up, Dashboards: store, Users: dir, DB: db, Hosts: hosts.New(up, http.DefaultClient, nil, time.Hour),
		UI: fstest.MapFS{"index.html": {}}, Log: slog.New(slog.DiscardHandler), Agents: svc,
	})
	agent, _, _ := dir.CreateToken("tp", "claude", &users.Scope{Allow: []string{users.Investigate}}, time.Time{})
	reader, _, _ := dir.CreateToken("tp", "reader", &users.Scope{Allow: []string{users.ReadAlerts}}, time.Time{})
	person, _, _ := dir.CreateToken("tp", "ci", nil, time.Time{})
	do := func(tok, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do(agent, "POST", "/api/investigations", `{"kind":"gap","title":"no logs from gpu01","text":"seen in coverage"}`)
	var inv agents.Investigation
	_ = json.Unmarshal(rec.Body.Bytes(), &inv)
	if rec.Code != 200 || inv.ClaimedBy != "claude" {
		t.Fatalf("open = %d %s", rec.Code, rec.Body)
	}
	path := "/api/investigations/" + strconv.FormatInt(inv.ID, 10)
	if rec := do(reader, "GET", path, ""); rec.Code != http.StatusForbidden {
		t.Errorf("without investigate = %d", rec.Code)
	}
	if rec := do(agent, "POST", path+"/actions", `{"action":"restart","target":"ds/otel","disruptive":true}`); rec.Code != 200 {
		t.Errorf("action = %d %s", rec.Code, rec.Body)
	}

	// Paused: an agent's writes are refused, reads still work; only people pause.
	if rec := do(agent, "POST", "/api/agents/pause", `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("agent pausing = %d", rec.Code)
	}
	if rec := do(person, "POST", "/api/agents/pause", `{"note":"upgrading"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("pause = %d %s", rec.Code, rec.Body)
	}
	if rec := do(agent, "POST", path+"/events", `{"kind":"note","text":"x"}`); rec.Code != http.StatusLocked {
		t.Errorf("paused write = %d", rec.Code)
	}
	if rec := do(agent, "GET", "/api/agents", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"paused":true`) {
		t.Errorf("state = %d %s", rec.Code, rec.Body)
	}
	_ = do(person, "POST", "/api/agents/resume", `{}`)
	if rec := do(agent, "POST", path+"/close", `{"status":"fixed","fix":"enabled the log preset on gpu01"}`); rec.Code != 200 {
		t.Errorf("close = %d %s", rec.Code, rec.Body)
	}
	log, _ := db.AuditLog(context.Background(), state.AuditFilter{Via: "claude"})
	if len(log) < 3 {
		t.Errorf("audit = %+v", log)
	}
}
