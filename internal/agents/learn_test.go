package agents

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/runbooks"
	"github.com/tpural/tremolo/internal/state"
)

func TestLearning(t *testing.T) {
	ctx := context.Background()
	db, _ := state.Open("")
	books, err := runbooks.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := books.Create(ctx, runbooks.Runbook{ID: "disk", Title: "Disk full", Body: "Check journald.", Rules: []string{"disk"}}, "tp"); err != nil {
		t.Fatal(err)
	}
	c, _ := ParseConfig(`{"enabled":true,"holdWindow":"24h","notify":["slack"],"report":{"at":"08:00"}}`)
	s, err := New(Options{
		Config: c, DB: db, Log: slog.New(slog.DiscardHandler), Runbooks: books,
		IsAgent: func(n string) bool { return n == "claude" }, Location: time.UTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	tl := &told{}
	s.Bind(tl)

	// fix opens an incident for the alert and fixes it with text, using the runbook.
	fix := func(alert, text string) int64 {
		t.Helper()
		s.Fired(alerts.Alert{ID: alert, Rule: "disk", Summary: alert, FiringSince: now})
		list, _ := s.List(ctx, Filter{Alert: alert, Status: []string{Open}})
		if len(list) != 1 {
			t.Fatalf("open for %s = %+v", alert, list)
		}
		if _, err := s.Close(ctx, list[0].ID, "claude", true, Fixed, "", text, "disk"); err != nil {
			t.Fatal(err)
		}
		s.Resolved(alerts.Alert{ID: alert}, now)
		return list[0].ID
	}

	// A fix that comes back within the window recurred; one that doesn't, held.
	first := fix("disk/a", "Vacuumed journald")
	now = now.Add(time.Hour)
	fix("disk/a", "vacuumed  journald")
	if i, _ := s.Get(ctx, first); i.Outcome != Recurred {
		t.Errorf("first = %q, want recurred", i.Outcome)
	}
	for i := range 3 {
		now = now.Add(time.Hour)
		fix(fmt.Sprintf("disk/h%d", i), "Moved images to the NAS")
	}
	now = now.Add(25 * time.Hour)
	s.learn(ctx)
	stats, _ := s.FixStats(ctx, "disk")
	if len(stats) != 2 || stats[0].Text != "Moved images to the NAS" || stats[0].Held != 3 || stats[1].Recurred != 1 || stats[1].Held != 1 {
		t.Errorf("stats = %+v", stats)
	}

	// The fix that held 3 times isn't in the runbook: a review opens, once.
	s.learn(ctx)
	reviews, _ := s.List(ctx, Filter{Kind: RunbookReview})
	if len(reviews) != 1 || !strings.Contains(reviews[0].Title, "keeps working") {
		t.Fatalf("reviews = %+v", reviews)
	}

	// An agent's edit after which fixes come back more is reverted.
	rb, _ := books.Get(ctx, "disk")
	if _, err := books.Update(ctx, runbooks.Runbook{ID: "disk", Title: rb.Title, Body: "Reboot the host.", Rules: rb.Rules, Version: rb.Version}, "claude"); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		now = now.Add(time.Hour)
		fix(fmt.Sprintf("disk/r%d", i), "Rebooted")
		now = now.Add(time.Minute)
		s.Fired(alerts.Alert{ID: fmt.Sprintf("disk/r%d", i), Rule: "disk", FiringSince: now})
	}
	s.learn(ctx)
	if rb, _ := books.Get(ctx, "disk"); strings.TrimSpace(rb.Body) != "Check journald." || rb.UpdatedBy != "tremolo" {
		t.Errorf("not reverted: %+v", rb)
	}
	if !strings.Contains(strings.Join(tl.titles, ";"), "Runbook disk reverted") {
		t.Errorf("told %v", tl.titles)
	}

	// The daily report says what happened, and is due at 08:00.
	r := s.Report(ctx, now.Add(-72*time.Hour))
	for _, want := range []string{"fixed by agents", "Fixes that held: 4", "Came back:", "Runbooks edited by agents: disk v2 by claude"} {
		if !strings.Contains(r, want) {
			t.Errorf("report lacks %q:\n%s", want, r)
		}
	}
	if next := s.nextReport(now); next.Hour() != 8 || !next.After(now) {
		t.Errorf("next report %v", next)
	}
}
