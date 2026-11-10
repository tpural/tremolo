package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tpural/tremolo/internal/calendar"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestCalendar(t *testing.T) {
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := users.Open(db)
	_ = dir.Create("ed", "", users.Editor, "editor-password")
	_ = dir.Create("vi", "", users.Viewer, "viewer-password")
	edSec, _, _ := dir.Login("ed", "editor-password", users.Client{})
	viSec, _, _ := dir.Login("vi", "viewer-password", users.Client{})
	cal, _ := calendar.Open(db, time.UTC)
	store, _ := dashboard.Load("")
	h := New(Options{Dashboards: store, Users: dir, DB: db, Calendar: cal, Log: slog.New(slog.DiscardHandler), UI: fstest.MapFS{"index.html": {}}})
	do := func(sec, method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sec})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour).Format(time.RFC3339)
	ops := `{"members":["ed","vi"],"start":"` + start + `","days":7}`

	if code, _ := do(viSec, "PUT", "/api/schedules/ops", ops); code != 403 {
		t.Errorf("viewer saving a schedule = %d", code)
	}
	if code, body := do(edSec, "PUT", "/api/schedules/ops", ops); code != 200 || !strings.Contains(body, `"onCall":"ed"`) {
		t.Fatalf("save = %d %s", code, body)
	}
	if code, _ := do(edSec, "PUT", "/api/schedules/vi", ops); code != 409 {
		t.Errorf("schedule named like a user = %d", code)
	}
	if code, _ := do(edSec, "POST", "/api/users", `{"name":"ops","role":"viewer","password":"long-enough-pw"}`); code < 400 {
		t.Errorf("user named like a schedule = %d", code)
	}

	// A viewer can cover a shift themselves, but not hand one to someone else.
	soon, later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339), time.Now().Add(3*time.Hour).UTC().Format(time.RFC3339)
	if code, _ := do(viSec, "POST", "/api/schedules/ops/overrides", `{"person":"ed","start":"`+soon+`","end":"`+later+`"}`); code != 403 {
		t.Errorf("viewer overriding for someone else = %d", code)
	}
	if code, body := do(viSec, "POST", "/api/schedules/ops/overrides", `{"start":"`+soon+`","end":"`+later+`","note":"swap"}`); code != 200 || !strings.Contains(body, `"person":"vi"`) {
		t.Errorf("viewer covering = %d %s", code, body)
	}

	if code, _ := do(viSec, "POST", "/api/maintenance", `{"title":"x","start":"`+soon+`","end":"`+later+`"}`); code != 403 {
		t.Errorf("viewer planning maintenance = %d", code)
	}
	code, body := do(edSec, "POST", "/api/maintenance", `{"title":"patching","start":"`+soon+`","end":"`+later+`","every":"week","hosts":["nas"]}`)
	if code != 200 || !strings.Contains(body, `"every":"week"`) {
		t.Fatalf("planning maintenance = %d %s", code, body)
	}
	if code, _ := do(edSec, "POST", "/api/maintenance", `{"title":"bad","start":"`+later+`","end":"`+soon+`"}`); code != 400 {
		t.Errorf("backwards maintenance = %d", code)
	}

	from, to := time.Now().UTC().Format(time.RFC3339), time.Now().Add(15*24*time.Hour).UTC().Format(time.RFC3339)
	code, body = do(viSec, "GET", "/api/calendar?from="+from+"&to="+to, "")
	if code != 200 || strings.Count(body, `"title":"patching"`) != 3 || !strings.Contains(body, `"override":true`) || !strings.Contains(body, `"timezone":"UTC"`) {
		t.Errorf("calendar = %d %s", code, body)
	}
	if code, _ := do(viSec, "GET", "/api/calendar?from="+from+"&to="+time.Now().Add(200*24*time.Hour).UTC().Format(time.RFC3339), ""); code != 400 {
		t.Errorf("a calendar too long = %d", code)
	}
	if code, body := do(viSec, "GET", "/api/audit", ""); code != 403 {
		t.Errorf("audit as viewer = %d %s", code, body)
	}
	entries, _ := db.AuditLog(t.Context(), state.AuditFilter{})
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	if got := strings.Join(actions, ","); !strings.Contains(got, "planned maintenance") || !strings.Contains(got, "added on-call override") || !strings.Contains(got, "saved schedule") {
		t.Errorf("audit = %s", got)
	}
}
