package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Period is planned maintenance: while it's on, the alerts it covers are
// held. It covers alerts of its Rules on its Hosts; leaving either empty
// means all of them, so a period with neither covers every alert.
type Period struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	// End is zero for a period that lasts until someone ends it.
	End time.Time `json:"end,omitzero"`
	// Every repeats the period each day or week, until RepeatUntil if set.
	Every       string    `json:"every,omitempty"`
	RepeatUntil time.Time `json:"repeatUntil,omitzero"`
	Rules       []string  `json:"rules"`
	Hosts       []string  `json:"hosts"`
	Note        string    `json:"note,omitempty"`
	By          string    `json:"by,omitempty"`
	Created     time.Time `json:"created"`
}

// Occurrence is one time a period is on.
type Occurrence struct {
	Period Period    `json:"period"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end,omitzero"`
}

// Covers reports whether the period applies to an alert of rule on host
// (empty when the alert names no host).
func (p Period) Covers(rule, host string) bool {
	return (len(p.Rules) == 0 || slices.Contains(p.Rules, rule)) && (len(p.Hosts) == 0 || host != "" && slices.Contains(p.Hosts, host))
}

// HostWide reports whether the period puts host itself in maintenance: it
// names the host and isn't limited to some rules.
func (p Period) HostWide(host string) bool {
	return len(p.Rules) == 0 && slices.Contains(p.Hosts, host)
}

// step is how far apart a repeating period's occurrences are, in days.
func (p Period) step() int {
	switch p.Every {
	case "day":
		return 1
	case "week":
		return 7
	}
	return 0
}

// maxOccurrences bounds how many occurrences one call lays out.
const maxOccurrences = 1000

// occurrences returns the times p is on that overlap from..to.
func (p Period) occurrences(from, to time.Time, loc *time.Location) []Occurrence {
	step := p.step()
	if step == 0 {
		if p.Start.Before(to) && (p.End.IsZero() || p.End.After(from)) {
			return []Occurrence{{Period: p, Start: p.Start.In(loc), End: p.End}}
		}
		return nil
	}
	length := p.End.Sub(p.Start)
	k := 0
	if gap := from.Sub(p.Start) - length; gap > 0 {
		k = max(0, int(gap/(time.Duration(step)*24*time.Hour))-1)
	}
	var out []Occurrence
	for n := 0; n < maxOccurrences; n++ {
		start := p.Start.In(loc).AddDate(0, 0, (k+n)*step)
		if !start.Before(to) || !p.RepeatUntil.IsZero() && start.After(p.RepeatUntil) {
			break
		}
		if end := start.Add(length); end.After(from) {
			out = append(out, Occurrence{Period: p, Start: start, End: end.In(loc)})
		}
	}
	return out
}

func (p *Period) check() error {
	p.Title = strings.TrimSpace(p.Title)
	switch {
	case p.Title == "" || len(p.Title) > 200:
		return errors.New("a period needs a title of up to 200 characters")
	case p.Start.IsZero():
		return errors.New("a period needs a start")
	case !p.End.IsZero() && !p.End.After(p.Start):
		return errors.New("a period ends after it starts")
	case p.Every != "" && p.step() == 0:
		return errors.New(`every is "day", "week" or empty`)
	case p.step() > 0 && p.End.IsZero():
		return errors.New("a repeating period needs an end")
	case p.step() > 0 && p.End.Sub(p.Start) > time.Duration(p.step())*24*time.Hour:
		return errors.New("a repeating period can't be longer than how often it repeats")
	case p.step() == 0 && !p.RepeatUntil.IsZero():
		return errors.New("only a repeating period has an end to its repeats")
	case len(p.Note) > 500 || len(p.Rules) > 100 || len(p.Hosts) > 500:
		return errors.New("the note is over 500 characters, or too many rules or hosts")
	}
	p.Rules, p.Hosts = clean(p.Rules), clean(p.Hosts)
	return nil
}

func clean(s []string) []string {
	out := []string{}
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SavePeriod creates a period, or replaces the one with p's ID.
func (c *Calendar) SavePeriod(ctx context.Context, p Period) (Period, error) {
	if err := p.check(); err != nil {
		return Period{}, err
	}
	if p.ID == "" {
		p.ID, p.Created = id(), c.now()
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return Period{}, err
		}
		defer func() { _ = tx.Rollback() }()
		if err := insertPeriod(tx, p); err != nil {
			return Period{}, err
		}
		return p, tx.Commit()
	}
	res, err := c.db.ExecContext(ctx, `UPDATE maintenance SET title = ?, start = ?, end = ?, every = ?, repeat_until = ?, rules = ?, hosts = ?, note = ?
		WHERE id = ?`, p.Title, ms(p.Start), nullMS(p.End), p.Every, nullMS(p.RepeatUntil), list(p.Rules), list(p.Hosts), p.Note, p.ID)
	if err != nil {
		return Period{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Period{}, ErrNotFound
	}
	return c.Period(ctx, p.ID)
}

func insertPeriod(tx *sql.Tx, p Period) error {
	_, err := tx.Exec(`INSERT INTO maintenance (id, title, start, end, every, repeat_until, rules, hosts, note, created, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Title, ms(p.Start), nullMS(p.End), p.Every, nullMS(p.RepeatUntil), list(p.Rules), list(p.Hosts), p.Note, ms(p.Created), p.By)
	return err
}

// DeletePeriod deletes a period.
func (c *Calendar) DeletePeriod(ctx context.Context, id string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM maintenance WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Period returns one period.
func (c *Calendar) Period(ctx context.Context, id string) (Period, error) {
	ps, err := c.periods(ctx, id, time.Time{})
	if err != nil {
		return Period{}, err
	}
	if len(ps) == 0 {
		return Period{}, ErrNotFound
	}
	return ps[0], nil
}

// Periods returns the periods that haven't finished by from, soonest first.
func (c *Calendar) Periods(ctx context.Context, from time.Time) ([]Period, error) {
	return c.periods(ctx, "", from)
}

// periods returns the one with id, or with no id, those not finished by
// from. A repeating period's last time starts by its repeat_until and lasts
// at most a week.
func (c *Calendar) periods(ctx context.Context, id string, from time.Time) ([]Period, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, title, start, end, every, repeat_until, rules, hosts, note, created, created_by FROM maintenance
		WHERE (?1 != '' AND id = ?1) OR (?1 = '' AND (
			(every = '' AND (end IS NULL OR end > ?2)) OR (every != '' AND (repeat_until IS NULL OR repeat_until > ?2 - 8 * 86400000))))
		ORDER BY start`, id, ms(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Period{}
	for rows.Next() {
		var p Period
		var start, created int64
		var end, until sql.NullInt64
		var rules, hosts string
		if err := rows.Scan(&p.ID, &p.Title, &start, &end, &p.Every, &until, &rules, &hosts, &p.Note, &created, &p.By); err != nil {
			return nil, err
		}
		p.Start, p.End, p.RepeatUntil, p.Created = time.UnixMilli(start).In(c.loc), inLoc(at(end), c.loc), inLoc(at(until), c.loc), time.UnixMilli(created)
		_ = json.Unmarshal([]byte(rules), &p.Rules)
		_ = json.Unmarshal([]byte(hosts), &p.Hosts)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Occurrences returns every time a period is on that overlaps from..to,
// earliest first.
func (c *Calendar) Occurrences(ctx context.Context, from, to time.Time) ([]Occurrence, error) {
	ps, err := c.Periods(ctx, from)
	if err != nil {
		return nil, err
	}
	var out []Occurrence
	for _, p := range ps {
		out = append(out, p.occurrences(from, to, c.loc)...)
	}
	slices.SortFunc(out, func(a, b Occurrence) int { return a.Start.Compare(b.Start) })
	return out, nil
}

// Active returns the periods on at t.
func (c *Calendar) Active(ctx context.Context, t time.Time) ([]Occurrence, error) {
	return c.Occurrences(ctx, t, t.Add(time.Millisecond))
}

// Holding returns the period, if any, that holds an alert of rule on host at t.
func (c *Calendar) Holding(t time.Time, rule, host string) (Occurrence, bool) {
	on, err := c.Active(context.Background(), t)
	if err != nil {
		return Occurrence{}, false
	}
	for _, o := range on {
		if o.Period.Covers(rule, host) {
			return o, true
		}
	}
	return Occurrence{}, false
}

// ErrRepeats means a repeating period can't be ended from a host; change it
// on the calendar.
var ErrRepeats = errors.New("that maintenance repeats; change it on the calendar")

// End ends a one-off period now, or deletes it if it hasn't started.
func (c *Calendar) End(ctx context.Context, id string) error {
	p, err := c.Period(ctx, id)
	if err != nil {
		return err
	}
	if p.step() > 0 {
		return ErrRepeats
	}
	now := c.now()
	if !p.Start.Before(now) {
		return c.DeletePeriod(ctx, id)
	}
	_, err = c.db.ExecContext(ctx, `UPDATE maintenance SET end = ? WHERE id = ?`, ms(now), id)
	return err
}

func inLoc(t time.Time, loc *time.Location) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(loc)
}
