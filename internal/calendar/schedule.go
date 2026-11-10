package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Schedule is an on-call rotation: its members take turns of Days days
// each, in order, the first from Start, handing off at Start's time of day.
type Schedule struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Members     []string  `json:"members"`
	Start       time.Time `json:"start"`
	Days        int       `json:"days"`
	// Overrides are the current and upcoming ones, soonest first.
	Overrides []Override `json:"overrides"`
	// OnCall is who is on call now, if anyone.
	OnCall string `json:"onCall,omitempty"`
	// Until is when OnCall's turn ends.
	Until time.Time `json:"until,omitzero"`
}

// Override puts someone on call instead of the rotation for a while.
type Override struct {
	ID       string    `json:"id"`
	Schedule string    `json:"schedule"`
	Person   string    `json:"person"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Note     string    `json:"note,omitempty"`
	By       string    `json:"by,omitempty"`
	Created  time.Time `json:"created"`
}

// Shift is a stretch of time one person is on call.
type Shift struct {
	Person   string    `json:"person"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Override bool      `json:"override,omitempty"`
}

// maxDays bounds a turn: a year.
const maxDays = 366

// Schedules returns every schedule, with who is on call now.
func (c *Calendar) Schedules(ctx context.Context) ([]Schedule, error) {
	return c.schedules(ctx, "")
}

// Schedule returns one schedule.
func (c *Calendar) Schedule(ctx context.Context, name string) (Schedule, error) {
	s, err := c.schedules(ctx, name)
	if err != nil {
		return Schedule{}, err
	}
	if len(s) == 0 {
		return Schedule{}, ErrNotFound
	}
	return s[0], nil
}

func (c *Calendar) schedules(ctx context.Context, name string) ([]Schedule, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT name, description, start, days FROM schedules WHERE ? = '' OR name = ? ORDER BY name`, name, name)
	if err != nil {
		return nil, err
	}
	out := []Schedule{}
	for rows.Next() {
		var s Schedule
		var start int64
		if err := rows.Scan(&s.Name, &s.Description, &start, &s.Days); err != nil {
			_ = rows.Close()
			return nil, err
		}
		s.Start = time.UnixMilli(start).In(c.loc)
		out = append(out, s)
	}
	_ = rows.Close()
	now := c.now()
	for i := range out {
		s := &out[i]
		if s.Members, err = c.members(ctx, s.Name); err != nil {
			return nil, err
		}
		if s.Overrides, err = c.overrides(ctx, s.Name, now, time.Time{}); err != nil {
			return nil, err
		}
		if shifts := s.shifts(now, now.Add(time.Duration(maxDays)*24*time.Hour), c.loc); len(shifts) > 0 && !shifts[0].Start.After(now) {
			s.OnCall, s.Until = shifts[0].Person, shifts[0].End
		}
	}
	return out, nil
}

func (c *Calendar) members(ctx context.Context, schedule string) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT user FROM schedule_members WHERE schedule = ? ORDER BY position`, schedule)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// overrides returns a schedule's overrides that end after from and, unless
// to is zero, start before to.
func (c *Calendar) overrides(ctx context.Context, schedule string, from, to time.Time) ([]Override, error) {
	end := int64(1 << 62)
	if !to.IsZero() {
		end = ms(to)
	}
	rows, err := c.db.QueryContext(ctx, `SELECT id, user, start, end, note, created, created_by FROM schedule_overrides
		WHERE schedule = ? AND end > ? AND start < ? ORDER BY start, created`, schedule, ms(from), end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Override{}
	for rows.Next() {
		o := Override{Schedule: schedule}
		var start, end, created int64
		if err := rows.Scan(&o.ID, &o.Person, &start, &end, &o.Note, &created, &o.By); err != nil {
			return nil, err
		}
		o.Start, o.End, o.Created = time.UnixMilli(start).In(c.loc), time.UnixMilli(end).In(c.loc), time.UnixMilli(created)
		out = append(out, o)
	}
	return out, rows.Err()
}

// SaveSchedule creates or replaces a schedule's rotation. Its overrides stay.
func (c *Calendar) SaveSchedule(ctx context.Context, s Schedule, by string) error {
	switch {
	case s.Days < 1 || s.Days > maxDays:
		return fmt.Errorf("turns are 1 to %d days", maxDays)
	case s.Start.IsZero():
		return errors.New("a schedule needs a start")
	case len(s.Description) > 500:
		return errors.New("the description is over 500 characters")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var clash int
	if err := tx.QueryRow(`SELECT (SELECT count(*) FROM users WHERE name = ?1) + (SELECT count(*) FROM teams WHERE name = ?1)`,
		s.Name).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 {
		return ErrExists
	}
	if _, err := tx.Exec(`INSERT INTO schedules (name, description, start, days, created, created_by) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET description = excluded.description, start = excluded.start, days = excluded.days`,
		s.Name, strings.TrimSpace(s.Description), ms(s.Start), s.Days, ms(c.now()), by); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM schedule_members WHERE schedule = ?`, s.Name); err != nil {
		return err
	}
	for i, m := range s.Members {
		if _, err := tx.Exec(`INSERT INTO schedule_members (schedule, position, user) VALUES (?, ?, ?)`, s.Name, i, m); err != nil {
			if !userExists(tx, m) {
				return fmt.Errorf("no user %q", m)
			}
			return err
		}
	}
	return tx.Commit()
}

func userExists(tx *sql.Tx, name string) bool {
	var one int
	return tx.QueryRow(`SELECT 1 FROM users WHERE name = ?`, name).Scan(&one) == nil
}

// DeleteSchedule deletes a schedule and its overrides.
func (c *Calendar) DeleteSchedule(ctx context.Context, name string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM schedules WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddOverride puts someone on call for a schedule from start to end.
func (c *Calendar) AddOverride(ctx context.Context, o Override) (Override, error) {
	switch {
	case !o.End.After(o.Start):
		return Override{}, errors.New("an override ends after it starts")
	case !o.End.After(c.now()):
		return Override{}, errors.New("that override is already over")
	case o.End.Sub(o.Start) > time.Duration(maxDays)*24*time.Hour:
		return Override{}, errors.New("overrides last at most a year")
	case len(o.Note) > 500:
		return Override{}, errors.New("the note is over 500 characters")
	}
	o.ID, o.Created = id(), c.now()
	_, err := c.db.ExecContext(ctx, `INSERT INTO schedule_overrides (id, schedule, user, start, end, note, created, created_by)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM schedules WHERE name = ?)`,
		o.ID, o.Schedule, o.Person, ms(o.Start), ms(o.End), strings.TrimSpace(o.Note), ms(o.Created), o.By, o.Schedule)
	if err != nil {
		return Override{}, fmt.Errorf("no user %q", o.Person)
	}
	if _, err := c.Schedule(ctx, o.Schedule); err != nil {
		return Override{}, err
	}
	return o, nil
}

// DeleteOverride removes an override.
func (c *Calendar) DeleteOverride(ctx context.Context, schedule, id string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM schedule_overrides WHERE schedule = ? AND id = ?`, schedule, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Shifts returns who is on call for a schedule from from to to.
func (c *Calendar) Shifts(ctx context.Context, name string, from, to time.Time) ([]Shift, error) {
	s, err := c.Schedule(ctx, name)
	if err != nil {
		return nil, err
	}
	if s.Overrides, err = c.overrides(ctx, name, from, to); err != nil {
		return nil, err
	}
	return s.shifts(from, to, c.loc), nil
}

// OnCall returns who is on call for the named schedule at t. ok is false
// when there's no such schedule.
func (c *Calendar) OnCall(name string, t time.Time) (person string, ok bool) {
	ctx := context.Background()
	s, err := c.Schedule(ctx, name)
	if err != nil {
		return "", false
	}
	if s.Overrides, err = c.overrides(ctx, name, t, t.Add(time.Millisecond)); err != nil {
		return "", true
	}
	if shifts := s.shifts(t, t.Add(time.Millisecond), c.loc); len(shifts) > 0 {
		return shifts[0].Person, true
	}
	return "", true
}

// turn is the start of the rotation's kth turn: Start plus k turns of
// calendar days, so a handoff keeps its time of day across daylight saving.
func (s Schedule) turn(k int, loc *time.Location) time.Time {
	return s.Start.In(loc).AddDate(0, 0, k*s.Days)
}

// turnAt is the number of the turn t falls in; -1 before the start.
func (s Schedule) turnAt(t time.Time, loc *time.Location) int {
	if t.Before(s.Start) {
		return -1
	}
	k := int(t.Sub(s.Start) / (time.Duration(s.Days) * 24 * time.Hour))
	for k > 0 && s.turn(k, loc).After(t) {
		k--
	}
	for !s.turn(k+1, loc).After(t) {
		k++
	}
	return k
}

// shifts lays the rotation and overrides out from from to to. s.Overrides
// must hold the ones that overlap.
func (s Schedule) shifts(from, to time.Time, loc *time.Location) []Shift {
	if !to.After(from) {
		return nil
	}
	cuts := []time.Time{from, to}
	if len(s.Members) > 0 {
		k := max(s.turnAt(from, loc), -1)
		for i := k + 1; ; i++ {
			t := s.turn(i, loc)
			if !t.Before(to) {
				break
			}
			if t.After(from) {
				cuts = append(cuts, t)
			}
		}
	}
	for _, o := range s.Overrides {
		for _, t := range []time.Time{o.Start, o.End} {
			if t.After(from) && t.Before(to) {
				cuts = append(cuts, t)
			}
		}
	}
	slices.SortFunc(cuts, func(a, b time.Time) int { return a.Compare(b) })
	cuts = slices.CompactFunc(cuts, func(a, b time.Time) bool { return a.Equal(b) })
	var out []Shift
	for i := 0; i+1 < len(cuts); i++ {
		start, end := cuts[i], cuts[i+1]
		person, override := s.at(start, loc)
		if person == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Person == person && out[n-1].Override == override && out[n-1].End.Equal(start) {
			out[n-1].End = end
			continue
		}
		out = append(out, Shift{Person: person, Start: start.In(loc), End: end.In(loc), Override: override})
	}
	return out
}

// at is who is on call at t: the newest override covering it, else the
// rotation's member for that turn.
func (s Schedule) at(t time.Time, loc *time.Location) (string, bool) {
	var best *Override
	for i, o := range s.Overrides {
		if !t.Before(o.Start) && t.Before(o.End) && (best == nil || !o.Created.Before(best.Created)) {
			best = &s.Overrides[i]
		}
	}
	if best != nil {
		return best.Person, true
	}
	if k := s.turnAt(t, loc); k >= 0 && len(s.Members) > 0 {
		return s.Members[k%len(s.Members)], false
	}
	return "", false
}
