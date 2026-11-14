package runbooks

import (
	"context"
	"errors"
	"testing"
)

func TestProposals(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	// A new runbook waits for a person.
	p, err := s.Propose(ctx, Runbook{Title: "Web scaled to zero", Body: "Scale it back.", Rules: []string{"workload-down"}}, "upkeeper", true)
	if err != nil || p.Runbook.ID != "web-scaled-to-zero" || p.Base != 0 || p.Status != "pending" {
		t.Fatalf("proposed %+v, %v", p, err)
	}
	if _, err := s.Get(ctx, "web-scaled-to-zero"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a proposal made a runbook: %v", err)
	}
	if s.ForRule("workload-down") != nil {
		t.Fatal("a proposal covers a rule")
	}
	rb, err := s.Approve(ctx, p.ID, "tim")
	if err != nil || rb.Version != 1 || rb.UpdatedBy != "upkeeper" || rb.Body != "Scale it back.\n" {
		t.Fatalf("approved %+v, %v", rb, err)
	}
	if _, err := s.Approve(ctx, p.ID, "tim"); !errors.Is(err, ErrDecided) {
		t.Fatalf("approved twice: %v", err)
	}

	// An edit is checked against the version it read, and again when approved.
	if _, err := s.Propose(ctx, Runbook{ID: rb.ID, Title: rb.Title, Body: "x", Version: 7}, "upkeeper", false); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale proposal: %v", err)
	}
	edit, err := s.Propose(ctx, Runbook{ID: rb.ID, Title: rb.Title, Body: "Check the drain is over, then scale back.", Version: 1}, "upkeeper", false)
	if err != nil || edit.Base != 1 {
		t.Fatalf("edit %+v, %v", edit, err)
	}
	other, _ := s.Propose(ctx, Runbook{ID: rb.ID, Title: rb.Title, Body: "Something else."}, "upkeeper", false)
	if _, err := s.Approve(ctx, edit.ID, "tim"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, other.ID, "tim"); !errors.Is(err, ErrChanged) {
		t.Fatalf("approved over a newer version: %v", err)
	}
	if err := s.Reject(ctx, other.ID, "tim"); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Proposals(ctx, "pending")
	all, _ := s.Proposals(ctx, "")
	if len(pending) != 0 || len(all) != 3 || all[0].Status != "rejected" || all[0].DecidedBy != "tim" {
		t.Fatalf("pending %v, all %+v", pending, all)
	}
	if _, err := s.Propose(ctx, Runbook{ID: "nope", Title: "x"}, "upkeeper", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("edit of a missing runbook: %v", err)
	}
	if _, err := s.Propose(ctx, Runbook{ID: "proposals", Title: "x"}, "upkeeper", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reserved id: %v", err)
	}
}
