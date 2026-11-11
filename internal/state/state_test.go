package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type data struct {
	Names map[string]int `json:"names"`
}

func open(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	s, err := Load[data](db, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(d *data) error {
		d.Names = map[string]int{"a": 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A failed update changes nothing.
	if err := s.Update(func(d *data) error {
		d.Names["a"] = 2
		return errors.New("no")
	}); err == nil {
		t.Fatal("want error")
	}
	_ = db.Close()
	again, err := Load[data](open(t, dir), "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []*Store[data]{s, again} {
		st.Read(func(d *data) {
			if d.Names["a"] != 1 {
				t.Errorf("names = %v", d.Names)
			}
		})
	}
}

func TestMemory(t *testing.T) {
	for _, db := range []*DB{nil, open(t, "")} {
		s, err := Load[data](db, "x")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(d *data) error { d.Names = map[string]int{"a": 1}; return nil }); err != nil {
			t.Fatal(err)
		}
		s.Read(func(d *data) {
			if d.Names["a"] != 1 {
				t.Errorf("names = %v", d.Names)
			}
		})
	}
}

func TestImportsJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte(`{"names":{"a":7}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load[data](open(t, dir), "users")
	if err != nil {
		t.Fatal(err)
	}
	s.Read(func(d *data) {
		if d.Names["a"] != 7 {
			t.Errorf("names = %v", d.Names)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "users.json.imported")); err != nil {
		t.Error("the old file wasn't renamed")
	}
	// The database wins from now on, even if the old file comes back.
	_ = s.Update(func(d *data) error { d.Names["a"] = 8; return nil })
	_ = os.WriteFile(filepath.Join(dir, "users.json"), []byte(`{"names":{"a":7}}`), 0o600)
	again, _ := Load[data](open(t, dir), "users")
	again.Read(func(d *data) {
		if d.Names["a"] != 8 {
			t.Errorf("names = %v", d.Names)
		}
	})
}

func TestNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	if _, err := db.sql.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(dir); err == nil {
		t.Fatal("opened a database from a newer tremolo")
	}
}

func TestDropsOnCallTables(t *testing.T) {
	// A database at schema 4 from the on-call release candidate.
	dir := t.TempDir()
	db := open(t, dir)
	if _, err := db.sql.Exec(`CREATE TABLE schedules (name TEXT PRIMARY KEY); CREATE TABLE schedule_members (schedule TEXT);
		CREATE TABLE schedule_overrides (id TEXT);
		DROP TABLE runbooks; DROP TABLE runbook_versions; ALTER TABLE alert_history DROP COLUMN fix;
		ALTER TABLE tokens DROP COLUMN scope; ALTER TABLE tokens DROP COLUMN expires; ALTER TABLE audit DROP COLUMN via;
		PRAGMA user_version = 4`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db = open(t, dir)
	var n int
	if err := db.sql.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name LIKE 'schedule%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("schedule tables left: %d %v", n, err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name = 'maintenance'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("maintenance table: %d %v", n, err)
	}
	// The database as it was before migrating is kept beside it.
	if _, err := os.Stat(filepath.Join(dir, "tremolo-schema-4.db")); err != nil {
		t.Errorf("no copy from before the migration: %v", err)
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	old := time.Now().Add(-48 * time.Hour)
	for i, e := range []Entry{
		{Time: old, Actor: "alex", Action: "signed in"},
		{Actor: "tim", Action: "forwarded", Target: "disk/db01", Detail: "to db"},
		{Actor: "alex", Action: "acknowledged", Target: "disk/db01"},
	} {
		if err := db.Audit(ctx, e); err != nil {
			t.Fatal(i, err)
		}
	}
	all, err := db.AuditLog(ctx, AuditFilter{})
	if err != nil || len(all) != 3 || all[0].Action != "acknowledged" {
		t.Fatalf("log = %+v, %v", all, err)
	}
	page, _ := db.AuditLog(ctx, AuditFilter{Before: all[0].ID, Limit: 1})
	if len(page) != 1 || page[0].Actor != "tim" {
		t.Errorf("page = %+v", page)
	}
	alex, _ := db.AuditLog(ctx, AuditFilter{Actor: "alex"})
	if len(alex) != 2 {
		t.Errorf("alex = %+v", alex)
	}
	if n, err := db.Prune(ctx, 24*time.Hour); err != nil || n != 1 {
		t.Errorf("pruned %d, %v", n, err)
	}
	var nilDB *DB
	if err := nilDB.Audit(ctx, Entry{}); err != nil {
		t.Error(err)
	}
}
