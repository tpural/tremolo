package users

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	pw, err := d.Bootstrap("")
	if err != nil || len(pw) < 12 {
		t.Fatalf("bootstrap: %q %v", pw, err)
	}
	if again, _ := d.Bootstrap(""); again != "" {
		t.Fatal("bootstrapped twice")
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
	sec, u, err := d.Login("alex", "password1")
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
	var raw string
	d2.store.Read(func(s *data) {
		for _, a := range s.Users {
			raw += a.PasswordHash
		}
		for k := range s.Sessions {
			raw += k
		}
	})
	if strings.Contains(raw, "password1") || strings.Contains(raw, sec) {
		t.Fatal("secret stored in the clear")
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
	sec, _, _ = d2.Login("alex", "password1")
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
		if _, _, err := d2.Login("alex", "nope"); !errors.Is(err, ErrBadLogin) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	if _, _, err := d2.Login("alex", "password2"); !errors.Is(err, ErrLocked) {
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
	// No admin user at all: it's created.
	if pw, err := d.ResetAdmin(""); err != nil || len(pw) < 12 {
		t.Fatalf("reset: %q %v", pw, err)
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
	sec, _, _ := d.Login("admin", "forgotten")
	if pw, err := d.ResetAdmin("new-password"); err != nil || pw != "" {
		t.Fatalf("reset: %q %v", pw, err)
	}
	if _, ok := d.Session(sec); ok {
		t.Error("old session still works")
	}
	if _, u, err := d.Login("admin", "new-password"); err != nil || u.Role != Admin {
		t.Errorf("login after reset: %+v %v", u, err)
	}
}
