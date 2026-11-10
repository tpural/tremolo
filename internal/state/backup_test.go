package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestBackup(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Audit(ctx, Entry{Actor: "tim", Action: "did a thing", Target: "x"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	at := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)
	var made []string
	for i := range 4 {
		p, err := db.Backup(ctx, dir, 3, at.Add(time.Duration(i)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, p)
	}
	got, err := Backups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != made[1] || got[2] != made[3] {
		t.Fatalf("kept %v, made %v", got, made)
	}
	if filepath.Base(got[2]) != "tremolo-20261113-030000.db" {
		t.Fatalf("name %s", got[2])
	}

	// A backup is a whole database, with the data.
	r, err := sql.Open("sqlite", "file:"+got[2]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var n int
	if err := r.QueryRow(`SELECT count(*) FROM audit WHERE actor = 'tim'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows %d, %v", n, err)
	}

	mem, _ := Open("")
	defer mem.Close()
	if _, err := mem.Backup(ctx, dir, 3, at); err == nil {
		t.Fatal("backed up an in-memory database")
	}
}
