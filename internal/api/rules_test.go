package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestRuleEditor(t *testing.T) {
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"7"]}]}}`))
	}))
	t.Cleanup(metrics.Close)
	mu, _ := url.Parse(metrics.URL)
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := alerts.LoadConfig("", http.DefaultClient)
	e, err := alerts.New(alerts.Options{
		Config: cfg, DB: db, Data: &alerts.Data{MetricsURL: mu, Client: http.DefaultClient},
		Client: http.DefaultClient, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.Watch(ctx, "", time.Hour)
	dir, _ := users.Open(db)
	_ = dir.Create("ed", "", users.Editor, "editor-password")
	_ = dir.Create("vi", "", users.Viewer, "viewer-password")
	edSec, _, _ := dir.Login("ed", "editor-password", users.Client{})
	viSec, _, _ := dir.Login("vi", "viewer-password", users.Client{})
	store, _ := dashboard.Load("")
	h := New(Options{Dashboards: store, Users: dir, DB: db, Alerts: e, Log: slog.New(slog.DiscardHandler), UI: fstest.MapFS{"index.html": {}}})
	do := func(sec, method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sec})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	src, _ := json.Marshal(map[string]string{"source": "def check(now):\n    if metric('x') > 5:\n        return alert('x is high')\n", "note": "first"})

	if code, _ := do(viSec, "PUT", "/api/alerts/rules/x-high", string(src)); code != 403 {
		t.Errorf("viewer saving = %d", code)
	}
	if code, body := do(edSec, "PUT", "/api/alerts/rules/x-high", string(src)); code != 200 || !strings.Contains(body, `"version":1`) {
		t.Fatalf("save = %d %s", code, body)
	}
	if code, body := do(edSec, "PUT", "/api/alerts/rules/host-down", `{"source":"def nope(:"}`); code != 400 || !strings.Contains(body, "error") {
		t.Errorf("broken rule = %d %s", code, body)
	}
	if code, body := do(viSec, "GET", "/api/alerts/rules/x-high/versions", ""); code != 200 || !strings.Contains(body, `"author":"ed"`) {
		t.Errorf("versions = %d %s", code, body)
	}
	if code, body := do(viSec, "GET", "/api/alerts/rules/x-high/versions/1", ""); code != 200 || !strings.Contains(body, "metric('x')") {
		t.Errorf("version 1 = %d %s", code, body)
	}
	replay, _ := json.Marshal(map[string]any{"source": "def check(now):\n    if metric('x') > 5:\n        return alert('x is high')\n", "hours": 1})
	code, body := do(edSec, "POST", "/api/alerts/replay", string(replay))
	var rep alerts.Replay
	if err := json.Unmarshal([]byte(body), &rep); code != 200 || err != nil || rep.Runs != 61 || len(rep.Episodes) != 1 {
		t.Errorf("replay = %d %s", code, body)
	}
	if code, _ := do(viSec, "POST", "/api/alerts/channels/phone/test", ""); code != 403 {
		t.Errorf("viewer testing a channel = %d", code)
	}
	if code, _ := do(edSec, "POST", "/api/alerts/channels/phone/test", ""); code != 404 {
		t.Errorf("testing a channel that doesn't exist = %d", code)
	}
	if code, _ := do(edSec, "DELETE", "/api/alerts/rules/host-down", ""); code != 409 {
		t.Errorf("deleting a built-in = %d", code)
	}
	if code, _ := do(edSec, "DELETE", "/api/alerts/rules/x-high", ""); code != 204 {
		t.Errorf("delete = %d", code)
	}
	log, _ := db.AuditLog(context.Background(), state.AuditFilter{})
	if len(log) != 2 || log[0].Action != "deleted a rule" || log[1].Detail != "version 1: first" {
		t.Errorf("audit = %+v", log)
	}
}
