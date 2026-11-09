package state

import (
	"context"
	"time"
)

// Entry is one thing someone did: signing in, changing a user, forwarding an alert.
type Entry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail,omitempty"`
}

// Audit records e; its time defaults to now. A nil DB records nothing.
func (d *DB) Audit(ctx context.Context, e Entry) error {
	if d == nil {
		return nil
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	_, err := d.sql.ExecContext(ctx, `INSERT INTO audit (time, actor, action, target, detail) VALUES (?, ?, ?, ?, ?)`,
		e.Time.UnixMilli(), e.Actor, e.Action, e.Target, e.Detail)
	return err
}

// AuditFilter picks entries: before an ID (for paging back), by actor, and how many.
type AuditFilter struct {
	Before int64
	Actor  string
	Limit  int
}

// AuditLog returns entries newest first.
func (d *DB) AuditLog(ctx context.Context, f AuditFilter) ([]Entry, error) {
	if d == nil {
		return nil, nil
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q := `SELECT id, time, actor, action, target, detail FROM audit WHERE 1 = 1`
	var args []any
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	if f.Actor != "" {
		q += ` AND actor = ?`
		args = append(args, f.Actor)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var ms int64
		if err := rows.Scan(&e.ID, &ms, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ms)
		out = append(out, e)
	}
	return out, rows.Err()
}
