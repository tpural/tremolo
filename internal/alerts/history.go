package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Past is an alert that fired and has resolved.
type Past struct {
	Alert
	// Entry is its place in the history, for paging back.
	Entry    int64     `json:"entry"`
	Resolved time.Time `json:"resolved"`
	// Fix is how someone said it was fixed.
	Fix string `json:"fix,omitempty"`
}

// HistoryFilter picks resolved alerts: by rule, by words in the summary,
// before an entry (for paging back), and how many.
type HistoryFilter struct {
	Rule   string
	Text   string
	Before int64
	Limit  int
}

// remember keeps alerts that just resolved in the history.
func (e *Engine) remember(ctx context.Context, resolved []Alert, at time.Time) {
	if e.opts.DB == nil || len(resolved) == 0 {
		return
	}
	for _, a := range resolved {
		b, err := json.Marshal(a)
		if err != nil {
			continue
		}
		var acked any
		if !a.Acked.IsZero() {
			acked = a.Acked.UnixMilli()
		}
		if _, err := e.opts.DB.SQL().ExecContext(ctx, `INSERT INTO alert_history
			(alert, rule, severity, summary, fired, resolved, acked, acked_by, assignee, data)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.Rule, a.Severity, a.Summary, a.FiringSince.UnixMilli(), at.UnixMilli(), acked, a.AckedBy, a.Assignee, string(b)); err != nil {
			e.opts.Log.Warn("saving alert history", "alert", a.ID, "err", err)
		}
	}
}

// History returns resolved alerts, most recently resolved first.
func (e *Engine) History(ctx context.Context, f HistoryFilter) ([]Past, error) {
	out := []Past{}
	if e.opts.DB == nil {
		return out, nil
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q := `SELECT id, resolved, fix, data FROM alert_history WHERE 1 = 1`
	var args []any
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	if f.Rule != "" {
		q += ` AND rule = ?`
		args = append(args, f.Rule)
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		q += ` AND (summary LIKE ? ESCAPE '\' OR alert LIKE ? ESCAPE '\')`
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		args = append(args, like, like)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := e.opts.DB.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Past
		var ms int64
		var data string
		if err := rows.Scan(&p.Entry, &ms, &p.Fix, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &p.Alert); err != nil {
			return nil, err
		}
		p.Resolved = time.UnixMilli(ms)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrNoEntry is a history entry that doesn't exist.
var ErrNoEntry = errors.New("no such history entry")

// maxFix bounds a fix note.
const maxFix = 8000

// SetFix records how a resolved alert was fixed.
func (e *Engine) SetFix(ctx context.Context, entry int64, fix string) (Past, error) {
	var p Past
	if e.opts.DB == nil {
		return p, ErrNoEntry
	}
	fix = strings.TrimSpace(fix)
	if len(fix) > maxFix {
		return p, fmt.Errorf("a fix note must be under %d characters", maxFix)
	}
	res, err := e.opts.DB.SQL().ExecContext(ctx, `UPDATE alert_history SET fix = ? WHERE id = ?`, fix, entry)
	if err != nil {
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, ErrNoEntry
	}
	var ms int64
	var data string
	if err := e.opts.DB.SQL().QueryRowContext(ctx, `SELECT id, resolved, fix, data FROM alert_history WHERE id = ?`, entry).Scan(&p.Entry, &ms, &p.Fix, &data); err != nil {
		return p, err
	}
	p.Resolved = time.UnixMilli(ms)
	return p, json.Unmarshal([]byte(data), &p.Alert)
}
