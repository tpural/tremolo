package calendar

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/state"
)

func open(t *testing.T, loc *time.Location, users ...string) (*Calendar, *state.DB) {
	t.Helper()
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, u := range users {
		if _, err := db.SQL().Exec(`INSERT INTO users (name, role, created) VALUES (?, 'editor', 0)`, u); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Open(db, loc)
	if err != nil {
		t.Fatal(err)
	}
	return c, db
}

func shifts(s []Shift) string {
	var out []string
	for _, sh := range s {
		p := sh.Person
		if sh.Override {
			p += "*"
		}
		out = append(out, sh.Start.Format("Jan 2 15:04")+" "+p)
	}
	return strings.Join(out, ", ")
}

func TestSchedule(t *testing.T) {
	ctx := context.Background()
	chicago, _ := time.LoadLocation("America/Chicago")
	c, _ := open(t, chicago, "alex", "sam", "tim")
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, chicago)
	c.SetClock(func() time.Time { return now })

	// Weekly turns handing off Mondays at 09:00, across the change to
	// daylight saving on March 8th: the handoff stays at 09:00.
	start := time.Date(2026, 3, 2, 9, 0, 0, 0, chicago)
	if err := c.SaveSchedule(ctx, Schedule{Name: "alex", Members: []string{"alex"}, Start: start, Days: 7}, "tim"); !errors.Is(err, ErrExists) {
		t.Errorf("a schedule named like a user: %v", err)
	}
	if err := c.SaveSchedule(ctx, Schedule{Name: "ops", Members: []string{"alex", "ghost"}, Start: start, Days: 7}, "tim"); err == nil {
		t.Error("saved a schedule with an unknown member")
	}
	if err := c.SaveSchedule(ctx, Schedule{Name: "ops", Members: []string{"alex", "sam", "tim"}, Start: start, Days: 7}, "tim"); err != nil {
		t.Fatal(err)
	}
	got, err := c.Shifts(ctx, "ops", time.Date(2026, 3, 1, 0, 0, 0, 0, chicago), time.Date(2026, 3, 31, 0, 0, 0, 0, chicago))
	if err != nil {
		t.Fatal(err)
	}
	if s := shifts(got); s != "Mar 2 09:00 alex, Mar 9 09:00 sam, Mar 16 09:00 tim, Mar 23 09:00 alex, Mar 30 09:00 sam" {
		t.Errorf("rotation = %s", s)
	}

	// Sam covers Wednesday night; a later override wins where they overlap.
	if _, err := c.AddOverride(ctx, Override{Schedule: "ops", Person: "sam", Start: now.Add(6 * time.Hour), End: now.Add(20 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := c.AddOverride(ctx, Override{Schedule: "ops", Person: "tim", Start: now.Add(10 * time.Hour), End: now.Add(12 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddOverride(ctx, Override{Schedule: "ops", Person: "sam", Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}); err == nil {
		t.Error("added an override that's over")
	}
	got, _ = c.Shifts(ctx, "ops", now, now.Add(48*time.Hour))
	if s := shifts(got); s != "Mar 4 12:00 alex, Mar 4 18:00 sam*, Mar 4 22:00 tim*, Mar 5 00:00 sam*, Mar 5 08:00 alex" {
		t.Errorf("with overrides = %s", s)
	}
	if who, ok := c.OnCall("ops", now.Add(11*time.Hour)); !ok || who != "tim" {
		t.Errorf("on call at 23:00 = %q %v", who, ok)
	}
	if _, ok := c.OnCall("nope", now); ok {
		t.Error("on call for a schedule that doesn't exist")
	}
	if who, _ := c.OnCall("ops", start.Add(-time.Hour)); who != "" {
		t.Errorf("on call before the start = %q", who)
	}
	list, _ := c.Schedules(ctx)
	if len(list) != 1 || list[0].OnCall != "alex" || len(list[0].Overrides) != 2 || !list[0].Until.Equal(now.Add(6*time.Hour-time.Second)) {
		t.Errorf("schedules = %+v", list)
	}

	// Deleting a user takes them out of the rotation and their overrides.
	if _, err := c.db.Exec(`DELETE FROM users WHERE name = 'sam'`); err != nil {
		t.Fatal(err)
	}
	s, _ := c.Schedule(ctx, "ops")
	if strings.Join(s.Members, ",") != "alex,tim" || len(s.Overrides) != 1 {
		t.Errorf("after deleting sam: %+v", s)
	}
}

func TestMaintenance(t *testing.T) {
	ctx := context.Background()
	c, db := open(t, time.UTC)
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC) // a Wednesday
	c.SetClock(func() time.Time { return now })

	// Every Sunday 02:00 to 04:00, for everything, through March.
	sunday := time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
	weekly, err := c.SavePeriod(ctx, Period{
		Title: "patching", Start: sunday, End: sunday.Add(2 * time.Hour), Every: "week",
		RepeatUntil: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The nas, for the disk rule only, tonight.
	if _, err := c.SavePeriod(ctx, Period{Title: "new disk", Start: now.Add(8 * time.Hour), End: now.Add(10 * time.Hour), Rules: []string{"disk-full"}, Hosts: []string{"nas"}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Period{
		{Title: "", Start: now, End: now.Add(time.Hour)},
		{Title: "x", Start: now, End: now.Add(-time.Hour)},
		{Title: "x", Start: now, Every: "week"},
		{Title: "x", Start: now, End: now.Add(30 * time.Hour), Every: "day"},
		{Title: "x", Start: now, End: now.Add(time.Hour), Every: "month"},
	} {
		if _, err := c.SavePeriod(ctx, bad); err == nil {
			t.Errorf("saved %+v", bad)
		}
	}

	occ, err := c.Occurrences(ctx, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var days []string
	for _, o := range occ {
		days = append(days, o.Start.Format("Jan 2 15:04")+" "+o.Period.Title)
	}
	if s := strings.Join(days, ", "); s != "Mar 1 02:00 patching, Mar 4 20:00 new disk, Mar 8 02:00 patching, Mar 15 02:00 patching, Mar 22 02:00 patching, Mar 29 02:00 patching" {
		t.Errorf("occurrences = %s", s)
	}

	held := func(at time.Time, rule, host string) string {
		o, ok := c.Holding(at, rule, host)
		if !ok {
			return "-"
		}
		return o.Period.Title
	}
	sun := time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC)
	tonight := now.Add(9 * time.Hour)
	for _, tc := range []struct {
		at         time.Time
		rule, host string
		want       string
	}{
		{sun, "host-down", "pi-1", "patching"},
		{sun, "anything", "", "patching"},
		{sun.Add(time.Hour), "host-down", "pi-1", "-"},
		{tonight, "disk-full", "nas", "new disk"},
		{tonight, "disk-full", "pi-1", "-"},
		{tonight, "host-down", "nas", "-"},
		{time.Date(2026, 4, 5, 3, 0, 0, 0, time.UTC), "host-down", "pi-1", "-"},
	} {
		if got := held(tc.at, tc.rule, tc.host); got != tc.want {
			t.Errorf("%s %s on %q: held by %s, want %s", tc.at.Format(time.DateTime), tc.rule, tc.host, got, tc.want)
		}
	}

	// A repeating period isn't ended from a host; a one-off one is.
	if err := c.End(ctx, weekly.ID); !errors.Is(err, ErrRepeats) {
		t.Errorf("ended a repeating period: %v", err)
	}
	once, _ := c.SavePeriod(ctx, Period{Title: "now", Start: now.Add(-time.Hour), Hosts: []string{"pi-1"}})
	if _, ok := c.Holding(now, "host-down", "pi-1"); !ok {
		t.Error("an open-ended period doesn't hold")
	}
	if err := c.End(ctx, once.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Holding(now.Add(time.Second), "host-down", "pi-1"); ok {
		t.Error("an ended period still holds")
	}

	// Host maintenance windows from tremolo 0.12 become periods.
	if _, err := db.SQL().Exec(`INSERT INTO state (name, value, updated) VALUES ('maintenance', ?, 0)`,
		`{"hosts":{"gpu01":{"since":"2026-03-04T11:00:00Z","note":"new fan"},"old":{"since":"2026-03-01T00:00:00Z","until":"2026-03-02T00:00:00Z"}}}`); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(db, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	c2.SetClock(c.now)
	o, ok := c2.Holding(now, "host-down", "gpu01")
	if !ok || o.Period.Note != "new fan" || !o.End.IsZero() || !o.Period.HostWide("gpu01") {
		t.Errorf("imported window = %+v %v", o, ok)
	}
	if _, ok := c2.Holding(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC), "host-down", "old"); ok {
		t.Error("imported a window that was over")
	}
}
