package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/runbooks"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestRunbooks(t *testing.T) {
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := alerts.LoadConfig("", http.DefaultClient)
	books, err := runbooks.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	e, err := alerts.New(alerts.Options{Config: cfg, DB: db, Runbooks: books, Client: http.DefaultClient, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := users.Open(db)
	_ = dir.Create("ed", "", users.Editor, "editor-password")
	_ = dir.Create("vi", "", users.Viewer, "viewer-password")
	edSec, _, _ := dir.Login("ed", "editor-password", users.Client{})
	viSec, _, _ := dir.Login("vi", "viewer-password", users.Client{})
	store, _ := dashboard.Load("")
	h := New(Options{Dashboards: store, Users: dir, DB: db, Alerts: e, Runbooks: books, Log: slog.New(slog.DiscardHandler), UI: fstest.MapFS{"index.html": {}}})
	do := func(sec, method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sec})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	rb := `{"title":"Restore the database","body":"# Restore\n\nScale to 0.","rules":["backup-stale"]}`
	if code, _ := do(viSec, "POST", "/api/runbooks", rb); code != 403 {
		t.Errorf("viewer creating = %d", code)
	}
	if code, body := do(edSec, "POST", "/api/runbooks", rb); code != 200 || !strings.Contains(body, `"id":"restore-the-database"`) {
		t.Fatalf("create = %d %s", code, body)
	}
	if code, _ := do(edSec, "POST", "/api/runbooks", rb); code != 409 {
		t.Errorf("duplicate = %d", code)
	}
	if code, _ := do(edSec, "POST", "/api/runbooks", `{"id":"Bad!","title":"x"}`); code != 400 {
		t.Errorf("bad id = %d", code)
	}
	if code, body := do(viSec, "GET", "/api/runbooks?rule=backup-stale", ""); code != 200 || !strings.Contains(body, "restore-the-database") {
		t.Errorf("by rule = %d %s", code, body)
	}
	if code, body := do(viSec, "GET", "/api/runbooks/restore-the-database/markdown", ""); code != 200 || body != "# Restore\n\nScale to 0.\n" {
		t.Errorf("markdown = %d %q", code, body)
	}
	if code, body := do(viSec, "GET", "/api/alerts", ""); code != 200 || !strings.Contains(body, `"runbooks":{"backup-stale":["restore-the-database"]}`) {
		t.Errorf("alerts = %d %s", code, body)
	}

	// A resolved alert, fixed: the note is kept and a crash-loop runbook made for it.
	fired := time.Date(2026, 11, 9, 3, 10, 0, 0, time.UTC)
	a, _ := json.Marshal(alerts.Alert{ID: "crash-loop/ns/pod", Rule: "crash-loop", Severity: "warn", Summary: "ns/pod restarted 13 times", FiringSince: fired})
	res, err := db.SQL().Exec(`INSERT INTO alert_history (alert, rule, severity, summary, fired, resolved, acked_by, assignee, data)
		VALUES ('crash-loop/ns/pod', 'crash-loop', 'warn', 'ns/pod restarted 13 times', ?, ?, '', '', ?)`,
		fired.UnixMilli(), fired.Add(30*time.Minute).UnixMilli(), string(a))
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := res.LastInsertId()
	path := "/api/alerts/history/" + jsonNumber(entry) + "/fix"
	if code, _ := do(viSec, "POST", path, `{"fix":"x"}`); code != 403 {
		t.Errorf("viewer fixing = %d", code)
	}
	code, body := do(edSec, "POST", path, `{"fix":"False alarm: counted lifetime restarts. Use increase_prometheus.","runbook":"new"}`)
	if code != 200 {
		t.Fatalf("fix = %d %s", code, body)
	}
	got, _ := books.Get(t.Context(), "crash-loop")
	for _, want := range []string{"# crash-loop", "## 2026-11-09: ns/pod restarted 13 times", "resolved after 30m", "Fix noted by ed", "Use increase_prometheus."} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("runbook body lacks %q:\n%s", want, got.Body)
		}
	}
	if code, body := do(viSec, "GET", "/api/alerts/history", ""); code != 200 || !strings.Contains(body, `"fix":"False alarm`) {
		t.Errorf("history = %d %s", code, body)
	}
	if code, _ := do(edSec, "POST", "/api/alerts/history/999/fix", `{"fix":"x"}`); code != 404 {
		t.Errorf("missing entry = %d", code)
	}
	if code, _ := do(edSec, "DELETE", "/api/runbooks/crash-loop", ""); code != 204 {
		t.Errorf("delete = %d", code)
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
