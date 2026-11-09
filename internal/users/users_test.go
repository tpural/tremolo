package users

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/state"
)

func TestUsers(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	// No password: nobody is created, and the first person sets up the admin.
	if err := d.Bootstrap(""); err != nil || !d.Empty() {
		t.Fatalf("bootstrap without a password: %v", err)
	}
	if err := d.Setup("admin", "short"); err == nil {
		t.Error("setup took a short password")
	}
	if err := d.Setup("admin", "admin-password"); err != nil || d.Empty() {
		t.Fatalf("setup: %v", err)
	}
	if u, ok := d.Get("admin"); !ok || u.Role != Admin {
		t.Fatalf("setup made %+v", u)
	}
	if err := d.Setup("mallory", "password1"); !errors.Is(err, ErrSetUp) {
		t.Fatalf("second setup: %v", err)
	}
	if err := d.Bootstrap("other-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Login("admin", "other-password", Client{}); err == nil {
		t.Fatal("bootstrap changed an existing admin")
	}
	if err := d.Create("Bad Name", "", Viewer, "password1"); err == nil {
		t.Error("bad name accepted")
	}
	if err := d.Create("alex", "", Viewer, "short"); err == nil {
		t.Error("short password accepted")
	}
	if err := d.Create("alex", "Alex", Editor, "password1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Create("alex", "", Viewer, "password1"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}

	// Sessions survive a restart; the file holds no secrets in the clear.
	sec, u, err := d.Login("alex", "password1", Client{})
	if err != nil || u.Role != Editor {
		t.Fatalf("login: %+v %v", u, err)
	}
	_ = db.Close()
	if db, err = state.Open(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := d2.Session(sec); !ok || u.Name != "alex" {
		t.Fatalf("session after restart: %+v %v", u, ok)
	}
	tok, _, err := d2.CreateToken("alex", "ci")
	if err != nil || !strings.HasPrefix(tok, TokenPrefix) {
		t.Fatalf("token: %q %v", tok, err)
	}
	if u, ok := d2.TokenUser(tok); !ok || u.Name != "alex" {
		t.Fatal("token user")
	}
	if file, err := os.ReadFile(filepath.Join(dir, state.File)); err != nil || bytes.Contains(file, []byte(tok)) {
		t.Fatalf("database holds a token in the clear (%v)", err)
	}
	if file, err := os.ReadFile(filepath.Join(dir, state.File)); err != nil || bytes.Contains(file, []byte("password1")) || bytes.Contains(file, []byte(sec)) {
		t.Fatalf("database holds a secret in the clear (%v)", err)
	}

	// Sessions expire, and a new password ends them.
	d2.now = func() time.Time { return time.Now().Add(SessionLength + time.Hour) }
	if _, ok := d2.Session(sec); ok {
		t.Error("expired session accepted")
	}
	d2.now = time.Now
	sec, _, _ = d2.Login("alex", "password1", Client{})
	if err := d2.ChangePassword("alex", "wrong", "password2"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("change with wrong password: %v", err)
	}
	if err := d2.ChangePassword("alex", "password1", "password2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d2.Session(sec); ok {
		t.Error("session survived a password change")
	}

	// Five wrong passwords lock the name for a minute.
	for range 5 {
		if _, _, err := d2.Login("alex", "nope", Client{}); !errors.Is(err, ErrBadLogin) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	if _, _, err := d2.Login("alex", "password2", Client{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked: %v", err)
	}

	// There's always an admin.
	viewer := Viewer
	if err := d2.Update("admin", nil, &viewer, nil); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin: %v", err)
	}
	if err := d2.Delete("admin"); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: %v", err)
	}

	// Teams and contacts feed alert routing.
	contacts := []alerts.Contact{{Channel: "sms", To: "+1555", After: alerts.Duration{Duration: 5 * time.Minute}}}
	if err := d2.Update("alex", nil, nil, contacts); err != nil {
		t.Fatal(err)
	}
	if err := d2.SaveTeam(Team{Name: "platform", Members: []string{"alex", "ghost"}}); err == nil {
		t.Error("team with unknown member accepted")
	}
	if err := d2.SaveTeam(Team{Name: "alex"}); !errors.Is(err, ErrExists) {
		t.Errorf("team named like a user: %v", err)
	}
	if err := d2.SaveTeam(Team{Name: "platform", Members: []string{"alex", "admin", "alex"}}); err != nil {
		t.Fatal(err)
	}
	if c, ok := d2.Person("alex"); !ok || len(c) != 1 || c[0].To != "+1555" {
		t.Fatalf("person: %+v", c)
	}
	if m, _, ok := d2.Team("platform"); !ok || strings.Join(m, ",") != "admin,alex" {
		t.Fatalf("team: %v", m)
	}
	if got := d2.TeamsOf("alex"); len(got) != 1 {
		t.Fatalf("teams of: %v", got)
	}
	if err := d2.Delete("alex"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d2.TokenUser(tok); ok {
		t.Error("deleted user's token still works")
	}
	if m, _, _ := d2.Team("platform"); strings.Join(m, ",") != "admin" {
		t.Errorf("deleted user still in team: %v", m)
	}
}

func TestResetAdmin(t *testing.T) {
	d, err := Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ResetAdmin(""); err == nil {
		t.Fatal("reset without a password")
	}
	// No admin user at all: it's created.
	if err := d.ResetAdmin("first-password"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	// Demoted, password forgotten, signed in somewhere: all undone.
	_ = d.Create("boss", "", Admin, "password1")
	viewer := Viewer
	if err := d.Update("admin", nil, &viewer, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPassword("admin", "forgotten"); err != nil {
		t.Fatal(err)
	}
	sec, _, _ := d.Login("admin", "forgotten", Client{})
	if err := d.ResetAdmin("new-password"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, ok := d.Session(sec); ok {
		t.Error("old session still works")
	}
	if _, u, err := d.Login("admin", "new-password", Client{}); err != nil || u.Role != Admin {
		t.Errorf("login after reset: %+v %v", u, err)
	}
}

func TestSessionsAndCascades(t *testing.T) {
	d, err := Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Create("admin", "", Admin, "admin-password")
	_ = d.Create("alex", "", Editor, "password1")
	_ = d.SaveTeam(Team{Name: "db", Members: []string{"alex", "admin"}})
	phone, _, _ := d.Login("alex", "password1", Client{Address: "10.0.0.5", Agent: "phone"})
	laptop, _, _ := d.Login("alex", "password1", Client{Address: "10.0.0.6", Agent: "laptop"})
	tok, _, _ := d.CreateToken("alex", "ci")
	_ = d.Update("alex", nil, nil, []alerts.Contact{{Channel: "push", To: "alex-phone"}})

	list := d.Sessions("alex", laptop)
	if len(list) != 2 || list[0].Provider != "local" {
		t.Fatalf("sessions = %+v", list)
	}
	var phoneID string
	for _, s := range list {
		if s.Agent == "laptop" && !s.Current || s.Agent == "phone" && s.Current {
			t.Errorf("current is wrong: %+v", s)
		}
		if s.Agent == "phone" {
			phoneID = s.ID
		}
	}
	// Signing one session out leaves the other.
	if err := d.EndSession("admin", phoneID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ended someone else's session: %v", err)
	}
	if err := d.EndSession("alex", phoneID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Session(phone); ok {
		t.Error("phone still signed in")
	}
	if _, ok := d.Session(laptop); !ok {
		t.Error("laptop signed out")
	}
	if n, err := d.EndSessions("alex"); err != nil || n != 1 {
		t.Errorf("end all = %d %v", n, err)
	}

	// Deleting a user takes everything of theirs with it.
	_, _, _ = d.Login("alex", "password1", Client{})
	if err := d.Delete("alex"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.TokenUser(tok); ok {
		t.Error("deleted user's token works")
	}
	for table, want := range map[string]int{"sessions": 0, "tokens": 0, "contacts": 0, "team_members": 1} {
		var n int
		_ = d.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
		if n != want {
			t.Errorf("%s has %d rows, want %d", table, n, want)
		}
	}
	// Users and teams share names.
	if err := d.Create("db", "", Viewer, "password1"); !errors.Is(err, ErrExists) {
		t.Errorf("user named like a team: %v", err)
	}
}

func TestImportsOldUsers(t *testing.T) {
	dir := t.TempDir()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.MinCost)
	sessionSecret, tokenSecret := "old-session", TokenPrefix+"old-token"
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	old := `{"users":{"alex":{"name":"alex","role":"admin","created":"2026-01-01T00:00:00Z","passwordHash":"` + string(hash) + `",
		"contacts":[{"channel":"push","to":"alex-phone","after":"1m"}]}},
		"teams":{"db":{"name":"db","members":["alex","gone"],"notify":["dbchat"]}},
		"sessions":{"` + hash256(sessionSecret) + `":{"user":"alex","expires":"` + expires + `"}},
		"tokens":[{"id":"t1","name":"ci","user":"alex","created":"2026-01-02T00:00:00Z","hash":"` + hash256(tokenSecret) + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Login("alex", "password1", Client{}); err != nil {
		t.Errorf("old password: %v", err)
	}
	if u, ok := d.Session(sessionSecret); !ok || u.Name != "alex" || len(u.Contacts) != 1 || u.Contacts[0].After.Duration != time.Minute {
		t.Errorf("old session: %+v %v", u, ok)
	}
	if _, ok := d.TokenUser(tokenSecret); !ok {
		t.Error("old token")
	}
	if m, n, ok := d.Team("db"); !ok || strings.Join(m, ",") != "alex" || strings.Join(n, ",") != "dbchat" {
		t.Errorf("team = %v %v %v", m, n, ok)
	}
	// Once only: the old document is gone, so a restart doesn't import again.
	if _, err := Open(db); err != nil {
		t.Fatalf("second open: %v", err)
	}
	var left int
	_ = db.SQL().QueryRow(`SELECT count(*) FROM state WHERE name = 'users'`).Scan(&left)
	if left != 0 {
		t.Error("old users document still there")
	}
}
