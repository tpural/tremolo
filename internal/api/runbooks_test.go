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

	// An agent's edits wait for a person.
	tok, _, err := dir.CreateToken("ed", "upkeeper", &users.Scope{Allow: []string{users.ReadRunbooks, users.WriteRunbooks, users.WriteFixNotes}}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	agent := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := agent("POST", "/api/runbooks", `{"title":"Web scaled to zero","body":"It's on purpose."}`); code != 202 || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("agent create = %d %s", code, body)
	}
	if _, err := books.Get(t.Context(), "web-scaled-to-zero"); err == nil {
		t.Error("an agent made a runbook without approval")
	}
	before, _ := books.Get(t.Context(), "crash-loop")
	if code, _ := agent("PUT", "/api/runbooks/crash-loop", `{"title":"crash-loop","body":"Restart everything.","version":`+jsonNumber(int64(before.Version))+`}`); code != 202 {
		t.Errorf("agent edit = %d", code)
	}
	if code, _ := agent("POST", path, `{"fix":"Deleted the stuck pod.","runbook":"crash-loop"}`); code != 200 {
		t.Errorf("agent fix note = %d", code)
	}
	if after, _ := books.Get(t.Context(), "crash-loop"); after.Body != before.Body {
		t.Error("an agent changed a runbook without approval")
	}
	if code, _ := agent("DELETE", "/api/runbooks/crash-loop", ""); code != 403 {
		t.Errorf("agent delete = %d", code)
	}
	var pending []runbooks.Proposal
	code, body = agent("GET", "/api/runbooks/proposals", "")
	if err := json.Unmarshal([]byte(body), &pending); code != 200 || err != nil || len(pending) != 3 {
		t.Fatalf("proposals = %d %s", code, body)
	}
	first := "/api/runbooks/proposals/" + jsonNumber(pending[2].ID)
	if code, _ := agent("POST", first+"/approve", ""); code != 403 {
		t.Errorf("agent approving = %d", code)
	}
	if code, _ := do(viSec, "POST", first+"/approve", ""); code != 403 {
		t.Errorf("viewer approving = %d", code)
	}
	if code, body := do(edSec, "POST", first+"/approve", ""); code != 200 || !strings.Contains(body, `"updatedBy":"upkeeper"`) {
		t.Errorf("approve = %d %s", code, body)
	}
	if code, _ := do(edSec, "POST", "/api/runbooks/proposals/"+jsonNumber(pending[1].ID)+"/reject", ""); code != 204 {
		t.Errorf("reject = %d", code)
	}
	if code, _ := do(edSec, "POST", "/api/runbooks/proposals/"+jsonNumber(pending[0].ID)+"/approve", ""); code != 200 {
		t.Errorf("approve the fix note = %d", code)
	}
	if got, _ := books.Get(t.Context(), "crash-loop"); !strings.Contains(got.Body, "Deleted the stuck pod.") || strings.Contains(got.Body, "Restart everything") {
		t.Errorf("crash-loop after decisions:\n%s", got.Body)
	}
	if code, _ := do(edSec, "DELETE", "/api/runbooks/crash-loop", ""); code != 204 {
		t.Errorf("delete = %d", code)
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
