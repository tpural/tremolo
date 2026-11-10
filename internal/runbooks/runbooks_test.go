package runbooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tpural/tremolo/internal/state"
)

func open(t *testing.T) *Store {
	t.Helper()
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRunbooks(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	rb, err := s.Create(ctx, Runbook{Title: "Disk full on mm02!", Body: "Prune images.", Rules: []string{"disk-full", " disk-full", ""}}, "tim")
	if err != nil {
		t.Fatal(err)
	}
	if rb.ID != "disk-full-on-mm02" || rb.Version != 1 || strings.Join(rb.Rules, ",") != "disk-full" {
		t.Fatalf("created %+v", rb)
	}
	if _, err := s.Create(ctx, Runbook{Title: "Disk full on mm02"}, "tim"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if got := s.ForRule("disk-full"); len(got) != 1 || got[0] != rb.ID {
		t.Fatalf("for rule: %v", got)
	}

	rb, err = s.Append(ctx, rb.ID, "## 2026-11-10\n\nPruned 171 images.", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if rb.Version != 2 || !strings.HasSuffix(rb.Body, "Prune images.\n\n## 2026-11-10\n\nPruned 171 images.\n") {
		t.Fatalf("appended %q (v%d)", rb.Body, rb.Version)
	}
	stale := rb
	stale.Version = 1
	if _, err := s.Update(ctx, stale, "tim"); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale update: %v", err)
	}
	rb.Rules = []string{"pvc-filling"}
	if rb, err = s.Update(ctx, rb, "tim"); err != nil {
		t.Fatal(err)
	}
	if len(s.ForRule("disk-full")) != 0 || len(s.ForRule("pvc-filling")) != 1 {
		t.Fatalf("by rule after update: %v", s.ByRule())
	}
	vs, err := s.Versions(ctx, rb.ID)
	if err != nil || len(vs) != 3 || vs[0].Version != 3 || vs[2].Body != "Prune images.\n" {
		t.Fatalf("versions %+v, %v", vs, err)
	}
	if err := s.Delete(ctx, rb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, rb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := s.Create(ctx, Runbook{ID: "Bad ID", Title: "x"}, "tim"); err == nil {
		t.Fatal("accepted a bad id")
	}
}

func TestImport(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("crash-loop.md", "---\nrules: [crash-loop, pod-stuck]\n---\n# Crash loops\n\nLook at the logs.\n")
	write("restore.md", "# Restore the database\n\nScale to 0.\n")
	if n, err := s.Import(ctx, dir); err != nil || n != 2 {
		t.Fatalf("imported %d, %v", n, err)
	}
	rb, _ := s.Get(ctx, "crash-loop")
	if rb.Title != "Crash loops" || strings.Join(rb.Rules, ",") != "crash-loop,pod-stuck" || !strings.HasPrefix(rb.Body, "# Crash loops") {
		t.Fatalf("parsed %+v", rb)
	}
	// Edited in tremolo, then imported again: the edit stays.
	rb.Body = "Edited.\n"
	if _, err := s.Update(ctx, rb, "tim"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Import(ctx, dir); err != nil || n != 0 {
		t.Fatalf("second import %d, %v", n, err)
	}
	if rb, _ = s.Get(ctx, "crash-loop"); rb.Body != "Edited.\n" {
		t.Fatalf("import overwrote an edit: %q", rb.Body)
	}
}
