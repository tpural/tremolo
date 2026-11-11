package agents

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/runbooks"
)

// Runbooks is the runbook store, for learning which fixes work and keeping
// runbooks current.
type Runbooks interface {
	Get(ctx context.Context, id string) (runbooks.Runbook, error)
	Update(ctx context.Context, rb runbooks.Runbook, by string) (runbooks.Runbook, error)
	Versions(ctx context.Context, id string) ([]runbooks.Runbook, error)
	ForRule(rule string) []string
}

// Outcomes of a fix.
const (
	Held     = "held"
	Recurred = "recurred"
)

// FixStat is how one fix in a runbook has gone.
type FixStat struct {
	Text     string    `json:"text"`
	Held     int       `json:"held"`
	Recurred int       `json:"recurred"`
	Pending  int       `json:"pending"`
	Last     time.Time `json:"last"`
}

// FixStats are the fixes recorded against a runbook, the ones that held most first.
func (s *Service) FixStats(ctx context.Context, runbook string) ([]FixStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fix, outcome, closed FROM investigations WHERE runbook = ? AND status = ?`, runbook, Fixed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*FixStat{}
	for rows.Next() {
		var fix, outcome string
		var closed int64
		if err := rows.Scan(&fix, &outcome, &closed); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.Join(strings.Fields(fix), " "))
		f := by[key]
		if f == nil {
			f = &FixStat{Text: strings.TrimSpace(fix)}
			by[key] = f
		}
		switch outcome {
		case Held:
			f.Held++
		case Recurred:
			f.Recurred++
		default:
			f.Pending++
		}
		if t := ms(closed); t.After(f.Last) {
			f.Last = t
		}
	}
	out := make([]FixStat, 0, len(by))
	for _, f := range by {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b FixStat) int {
		if d := (b.Held - b.Recurred) - (a.Held - a.Recurred); d != 0 {
			return d
		}
		return b.Last.Compare(a.Last)
	})
	return out, rows.Err()
}

// recurred marks the last fix of an alert as not holding when it fires again
// within HoldWindow, and says so on the new incident.
func (s *Service) recurred(ctx context.Context, alert string, now time.Time, next int64) {
	var id int64
	var fix string
	err := s.db.QueryRowContext(ctx, `SELECT id, fix FROM investigations WHERE alert = ? AND status = ? AND outcome = '' AND closed > ?
		ORDER BY id DESC LIMIT 1`, alert, Fixed, now.Add(-s.opts.Config.HoldWindow.Duration).UnixMilli()).Scan(&id, &fix)
	if err != nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE investigations SET outcome = ?, outcome_at = ? WHERE id = ?`, Recurred, now.UnixMilli(), id)
	_ = s.event(ctx, id, Event{Time: now, By: "tremolo", Kind: "outcome", Text: fmt.Sprintf("the alert came back: see #%d", next)})
	_ = s.event(ctx, next, Event{Time: now, By: "tremolo", Kind: "note", Text: fmt.Sprintf("the fix from #%d didn't hold: %s", id, fix)})
}

// learn marks fixes that held, opens runbook reviews, and reverts runbook
// edits that made fixes worse.
func (s *Service) learn(ctx context.Context) {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE investigations SET outcome = ?, outcome_at = ?
		WHERE status = ? AND kind = ? AND outcome = '' AND closed < ?`,
		Held, now.UnixMilli(), Fixed, Incident, now.Add(-s.opts.Config.HoldWindow.Duration).UnixMilli()); err != nil {
		s.opts.Log.Warn("marking fixes that held", "err", err)
	}
	if s.opts.Runbooks == nil {
		return
	}
	s.reviews(ctx, now)
	s.guard(ctx, now)
}

// review opens a runbook review unless one with the same title was opened in the last 30 days.
func (s *Service) review(ctx context.Context, now time.Time, title, runbook, rule, text string) {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM investigations WHERE kind = ? AND title = ? AND opened > ?`,
		RunbookReview, title, now.Add(-30*24*time.Hour).UnixMilli()).Scan(&n)
	if n > 0 {
		return
	}
	i, err := s.create(ctx, Investigation{Kind: RunbookReview, Title: title, Rule: rule, Status: Open, Opened: now}, "tremolo", text)
	if err != nil {
		s.opts.Log.Warn("opening a runbook review", "err", err)
		return
	}
	if runbook != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE investigations SET runbook = ? WHERE id = ?`, runbook, i.ID)
	}
}

func (s *Service) reviews(ctx context.Context, now time.Time) {
	month := now.Add(-30 * 24 * time.Hour).UnixMilli()
	// Fixes that keep coming back.
	rows, err := s.db.QueryContext(ctx, `SELECT runbook, count(*) FROM investigations
		WHERE runbook != '' AND outcome = ? AND outcome_at > ? GROUP BY runbook HAVING count(*) >= 3`, Recurred, month)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				ids = append(ids, id)
			}
		}
		_ = rows.Close()
		for _, id := range ids {
			s.review(ctx, now, "Fixes from runbook "+id+" keep coming back", id, "",
				"Three or more fixes from this runbook didn't hold in the last 30 days. Find the real cause and rewrite the runbook.")
		}
	}
	// Alerts fixed with no runbook for their rule.
	rows, err = s.db.QueryContext(ctx, `SELECT id, rule, fix FROM investigations
		WHERE kind = ? AND status = ? AND rule != '' AND closed > ? ORDER BY id DESC`, Incident, Fixed, now.Add(-7*24*time.Hour).UnixMilli())
	if err == nil {
		type fixed struct {
			id        int64
			rule, fix string
		}
		var list []fixed
		for rows.Next() {
			var f fixed
			if rows.Scan(&f.id, &f.rule, &f.fix) == nil {
				list = append(list, f)
			}
		}
		_ = rows.Close()
		for _, f := range list {
			if len(s.opts.Runbooks.ForRule(f.rule)) == 0 {
				s.review(ctx, now, "Write a runbook for alert rule "+f.rule, "", f.rule,
					fmt.Sprintf("Investigation #%d fixed it with: %s. Write a runbook covering the rule from it.", f.id, f.fix))
			}
		}
	}
	// Fixes that keep working but aren't in their runbook.
	rows, err = s.db.QueryContext(ctx, `SELECT DISTINCT runbook FROM investigations WHERE runbook != '' AND outcome = ?`, Held)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		rb, err := s.opts.Runbooks.Get(ctx, id)
		if err != nil {
			continue
		}
		stats, err := s.FixStats(ctx, id)
		if err != nil {
			continue
		}
		body := strings.ToLower(rb.Body)
		for _, f := range stats {
			if f.Held >= 3 && f.Recurred == 0 && !strings.Contains(body, strings.ToLower(f.Text)) {
				s.review(ctx, now, "Add a fix that keeps working to runbook "+id, id, "",
					fmt.Sprintf("This fix held %d times but the runbook doesn't say it: %s", f.Held, f.Text))
			}
		}
	}
}

// guard reverts an agent's runbook edit when fixes made with it come back
// more often than fixes made with the version before.
func (s *Service) guard(ctx context.Context, now time.Time) {
	if s.opts.IsAgent == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT runbook FROM investigations WHERE runbook != '' AND runbook_version > 0`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		versions, err := s.opts.Runbooks.Versions(ctx, id)
		if err != nil || len(versions) < 2 || !s.opts.IsAgent(versions[0].UpdatedBy) {
			continue
		}
		latest, prev := versions[0], versions[1]
		after, afterBad := s.outcomes(ctx, id, `= ?`, latest.Version)
		before, beforeBad := s.outcomes(ctx, id, `< ?`, latest.Version)
		if after < 3 || before < 3 {
			continue
		}
		rateAfter, rateBefore := float64(afterBad)/float64(after), float64(beforeBad)/float64(before)
		if rateAfter <= rateBefore+0.25 {
			continue
		}
		if _, err := s.opts.Runbooks.Update(ctx, runbooks.Runbook{ID: id, Title: prev.Title, Body: prev.Body, Rules: prev.Rules, Version: latest.Version}, "tremolo"); err != nil {
			s.opts.Log.Warn("reverting a runbook", "runbook", id, "err", err)
			continue
		}
		why := fmt.Sprintf("%s's edit (version %d) was followed by %d of %d fixes coming back, against %d of %d before it, so tremolo put version %d back.",
			latest.UpdatedBy, latest.Version, afterBad, after, beforeBad, before, prev.Version)
		s.review(ctx, now, fmt.Sprintf("Reverted runbook %s version %d", id, latest.Version), id, "", why)
		s.tell(ctx, "Runbook "+id+" reverted", why)
	}
}

func (s *Service) outcomes(ctx context.Context, runbook, cmp string, version int) (total, bad int) {
	_ = s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(outcome = ?), 0) FROM investigations
		WHERE runbook = ? AND outcome IN (?, ?) AND runbook_version > 0 AND runbook_version `+cmp, //nolint:gosec // cmp is "= ?" or "< ?"
		Recurred, runbook, Held, Recurred, version).Scan(&total, &bad)
	return total, bad
}

// Report is what agents did since a time, in plain text for a message.
func (s *Service) Report(ctx context.Context, since time.Time) string {
	from := since.UnixMilli()
	count := func(q string, args ...any) int {
		var n int
		_ = s.db.QueryRowContext(ctx, q, args...).Scan(&n)
		return n
	}
	titles := func(q string, args ...any) []string {
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id int64
			var t string
			if rows.Scan(&id, &t) == nil {
				out = append(out, fmt.Sprintf("#%d %s", id, t))
			}
		}
		return out
	}
	var b strings.Builder
	opened := count(`SELECT count(*) FROM investigations WHERE opened > ?`, from)
	closed := map[string]int{}
	for _, st := range []string{Fixed, Resolved, Abandoned} {
		closed[st] = count(`SELECT count(*) FROM investigations WHERE status = ? AND closed > ?`, st, from)
	}
	fmt.Fprintf(&b, "%d investigations opened; %d fixed by agents, %d went away by themselves, %d abandoned.\n",
		opened, closed[Fixed], closed[Resolved], closed[Abandoned])
	held := count(`SELECT count(*) FROM investigations WHERE outcome = ? AND outcome_at > ?`, Held, from)
	back := titles(`SELECT id, title FROM investigations WHERE outcome = ? AND outcome_at > ?`, Recurred, from)
	fmt.Fprintf(&b, "Fixes that held: %d. Came back: %d.\n", held, len(back))
	for _, t := range back {
		b.WriteString("  ↺ " + t + "\n")
	}
	esc := titles(`SELECT id, title FROM investigations WHERE status = ?`, Escalated)
	if len(esc) > 0 {
		fmt.Fprintf(&b, "Waiting for a person: %d\n", len(esc))
		for _, t := range esc {
			b.WriteString("  ! " + t + "\n")
		}
	}
	fixes := titles(`SELECT id, title || ': ' || fix FROM investigations WHERE status = ? AND closed > ? ORDER BY id`, Fixed, from)
	if len(fixes) > 0 {
		b.WriteString("Fixed:\n")
		for _, t := range fixes[:min(len(fixes), 15)] {
			b.WriteString("  ✓ " + t + "\n")
		}
	}
	actions := count(`SELECT count(*) FROM investigation_events WHERE kind = 'action' AND time > ?`, from)
	disruptive := count(`SELECT count(*) FROM investigation_events WHERE kind = 'action' AND disruptive = 1 AND time > ?`, from)
	refused := count(`SELECT count(*) FROM investigation_events WHERE kind = 'refused' AND time > ?`, from)
	fmt.Fprintf(&b, "Actions: %d (%d disruptive), %d refused by the rails.\n", actions, disruptive, refused)
	gaps := titles(`SELECT id, title FROM investigations WHERE kind IN (?, ?) AND opened > ?`, Gap, Tuning, from)
	if len(gaps) > 0 {
		b.WriteString("Gaps and tuning found:\n")
		for _, t := range gaps {
			b.WriteString("  + " + t + "\n")
		}
	}
	reviews := titles(`SELECT id, title FROM investigations WHERE kind = ? AND opened > ?`, RunbookReview, from)
	for _, t := range reviews {
		b.WriteString("  📖 " + t + "\n")
	}
	if rows, err := s.db.QueryContext(ctx, `SELECT runbook, version, updated_by FROM runbook_versions WHERE updated > ? ORDER BY updated`, from); err == nil {
		type edit struct {
			id, by string
			v      int
		}
		var all []edit
		for rows.Next() {
			var e edit
			if rows.Scan(&e.id, &e.v, &e.by) == nil {
				all = append(all, e)
			}
		}
		// Close before asking IsAgent: the database has one connection.
		_ = rows.Close()
		var edits []string
		for _, e := range all {
			if s.opts.IsAgent != nil && s.opts.IsAgent(e.by) {
				edits = append(edits, fmt.Sprintf("%s v%d by %s", e.id, e.v, e.by))
			}
		}
		if len(edits) > 0 {
			b.WriteString("Runbooks edited by agents: " + strings.Join(edits, ", ") + "\n")
		}
	}
	if p := s.Paused(); p.Paused {
		fmt.Fprintf(&b, "Agents are paused (by %s).\n", p.By)
	}
	return b.String()
}

// SendReport sends the last day's report to Notify.
func (s *Service) SendReport(ctx context.Context) {
	s.tell(ctx, "tremolo agents: the last day", s.Report(ctx, s.now().Add(-24*time.Hour)))
}

// nextReport is when the daily report is next due, or zero for none.
func (s *Service) nextReport(now time.Time) time.Time {
	at := s.opts.Config.Report.At
	if at == "" || len(s.opts.Config.Notify) == 0 {
		return time.Time{}
	}
	t, err := time.Parse("15:04", at)
	if err != nil {
		return time.Time{}
	}
	loc := s.opts.Location
	if loc == nil {
		loc = time.Local
	}
	n := now.In(loc)
	next := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if !next.After(n) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
