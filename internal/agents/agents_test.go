package agents

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/state"
)

type told struct{ titles []string }

func (t *told) Announce(_ context.Context, _ []string, title, _ string) error {
	t.titles = append(t.titles, title)
	return nil
}

func setup(t *testing.T, cfg string) (*Service, *time.Time, *told) {
	t.Helper()
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Config: c, DB: db, Log: slog.New(slog.DiscardHandler), HostState: func(_ context.Context, h string) (string, bool) {
		return map[string]string{"pi-1": "asleep", "mm01": "ready"}[h], h != ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	tl := &told{}
	s.Bind(tl)
	return s, &now, tl
}

func TestIncidentLifecycle(t *testing.T) {
	ctx := context.Background()
	s, now, _ := setup(t, `{"enabled":true,"first":true,"grace":"20m","claimWait":"5m","always":["page"],"notify":["slack"]}`)
	a := alerts.Alert{ID: "disk/mm02", Rule: "disk", Key: "mm02", Summary: "disk 95% on mm02", Severity: "warn", FiringSince: *now}

	// People wait while agents get a chance to claim it.
	if !s.Hold(a, *now) {
		t.Error("held before it's opened")
	}
	s.Fired(a)
	list, _ := s.List(ctx, Filter{Status: []string{Open}})
	if len(list) != 1 || list[0].Alert != a.ID || list[0].Kind != Incident {
		t.Fatalf("open = %+v", list)
	}
	id := list[0].ID
	*now = now.Add(6 * time.Minute)
	if s.Hold(a, *now) {
		t.Error("no agent claimed it in 5m: people should hear")
	}

	// Claimed: held until the grace ends.
	if _, err := s.Claim(ctx, id, "claude", true); err != nil {
		t.Fatal(err)
	}
	if !s.Hold(a, *now) {
		t.Error("claimed: should hold")
	}
	if s.Hold(alerts.Alert{ID: "x", Severity: "page", FiringSince: *now}, *now) {
		t.Error("always-severity held")
	}
	if !s.Hold(a, now.Add(13*time.Minute)) || s.Hold(a, now.Add(15*time.Minute)) {
		t.Error("grace of 20m from firing")
	}

	// Another agent can't take it; a person can still write.
	var claimed ClaimedError
	if _, err := s.Record(ctx, id, "goose", true, "finding", "x"); !errors.As(err, &claimed) || claimed.By != "claude" {
		t.Errorf("second agent = %v", err)
	}
	if _, err := s.Record(ctx, id, "tp", false, "note", "looking too"); err != nil {
		t.Errorf("person = %v", err)
	}

	// Escalating releases people's notifications.
	if _, err := s.Escalate(ctx, id, "claude", true, "cleaned logs, still 95%"); err != nil {
		t.Fatal(err)
	}
	if s.Hold(a, *now) {
		t.Error("escalated: should notify")
	}

	// Firing again appends; closing as fixed needs a fix.
	s.Fired(a)
	if _, err := s.Close(ctx, id, "claude", true, Fixed, "", "", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("fixed without a fix = %v", err)
	}
	i, err := s.Close(ctx, id, "claude", true, Fixed, "journal filled the disk", "vacuumed journald to 500M", "disk")
	if err != nil || i.Status != Fixed || i.Fix == "" {
		t.Fatalf("close = %+v %v", i, err)
	}
	if _, err := s.Record(ctx, id, "claude", true, "note", "x"); !errors.Is(err, ErrClosed) {
		t.Errorf("record on closed = %v", err)
	}
	kinds := map[string]int{}
	for _, e := range i.Events {
		kinds[e.Kind]++
	}
	if kinds["opened"] != 1 || kinds["claimed"] != 2 || kinds["escalated"] != 1 || kinds["closed"] != 1 || kinds["note"] != 2 {
		t.Errorf("events = %v", kinds)
	}

	// The next firing opens a new incident linked to this one.
	s.Fired(a)
	list, _ = s.List(ctx, Filter{Status: []string{Open}})
	if len(list) != 1 || list[0].Previous != id {
		t.Errorf("next = %+v", list)
	}
	// Resolving an unclaimed incident closes it.
	s.Resolved(a, *now)
	if list, _ = s.List(ctx, Filter{Status: []string{Open}}); len(list) != 0 {
		t.Errorf("still open = %+v", list)
	}
}

func TestRails(t *testing.T) {
	ctx := context.Background()
	s, now, tl := setup(t, `{"enabled":true,"maxActionsPerHour":3,"maxDisruptivePerHour":1,"notify":["slack"],
		"protect":[{"name":"control plane","hosts":["cp1","cp2","cp3"],"maxAtOnce":1}]}`)
	i, err := s.Open(ctx, Tuning, "memory limits for gridtie", "", "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	act := func(a Action) error {
		_, err := s.BeginAction(ctx, i.ID, "claude", true, a)
		return err
	}
	var refused Refusal
	if err := act(Action{Action: "drain", Host: "pi-1", Target: "node/pi-1", Disruptive: true}); !errors.As(err, &refused) {
		t.Errorf("asleep host = %v", err)
	}
	if err := act(Action{Action: "restart", Host: "cp1", Target: "node/cp1", Disruptive: true}); err != nil {
		t.Fatal(err)
	}
	if err := act(Action{Action: "restart", Host: "cp2", Target: "node/cp2", Disruptive: true}); !errors.As(err, &refused) {
		t.Errorf("disruptive budget = %v", err)
	}
	if err := act(Action{Action: "describe", Host: "cp2", Target: "node/cp2"}); !errors.As(err, &refused) {
		t.Errorf("protected group busy = %v", err)
	}
	// Finishing the first frees the group.
	st, _ := s.State(ctx)
	if st.Actions["claude"] != 1 || len(st.Recent) < 3 {
		t.Errorf("state = %+v", st)
	}
	var first int64
	for _, e := range st.Recent {
		if e.Kind == "action" {
			first = e.ID
		}
	}
	if err := s.EndAction(ctx, i.ID, first, "claude", true, "done, node Ready"); err != nil {
		t.Fatal(err)
	}
	if err := act(Action{Action: "describe", Host: "cp2", Target: "node/cp2"}); err != nil {
		t.Errorf("after finishing = %v", err)
	}
	if err := act(Action{Action: "scale", Namespace: "gridtie", Target: "deploy/gridtie"}); err != nil {
		t.Fatal(err)
	}
	if err := act(Action{Action: "scale", Namespace: "gridtie", Target: "deploy/gridtie"}); !errors.As(err, &refused) {
		t.Errorf("action budget = %v", err)
	}

	// Paused: everything is refused, and people are told.
	*now = now.Add(2 * time.Hour)
	if err := s.Pause(ctx, "tp", "testing"); err != nil {
		t.Fatal(err)
	}
	if err := act(Action{Action: "scale", Target: "deploy/x"}); !errors.As(err, &refused) {
		t.Errorf("paused = %v", err)
	}
	_ = s.Resume(ctx, "tp")
	if err := act(Action{Action: "scale", Target: "deploy/x"}); err != nil {
		t.Errorf("resumed = %v", err)
	}
	if len(tl.titles) != 2 {
		t.Errorf("told %v", tl.titles)
	}
	// A gap escalated with no alert tells people directly.
	if _, err := s.Escalate(ctx, i.ID, "claude", true, "needs a new node"); err != nil || len(tl.titles) != 3 {
		t.Errorf("escalate = %v, told %v", err, tl.titles)
	}
}
