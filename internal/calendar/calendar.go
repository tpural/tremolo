// Package calendar keeps maintenance periods: when alerts are held.
package calendar

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/tpural/tremolo/internal/state"
)

// ErrNotFound means no period has that id.
var ErrNotFound = errors.New("not found")

// Calendar is the maintenance periods kept in the database.
type Calendar struct {
	db  *sql.DB
	loc *time.Location
	now func() time.Time
}

// Open returns the calendar kept in db. Repeating periods keep their time of
// day in loc.
func Open(db *state.DB, loc *time.Location) (*Calendar, error) {
	if db == nil {
		var err error
		if db, err = state.Open(""); err != nil {
			return nil, err
		}
	}
	c := &Calendar{db: db.SQL(), loc: loc, now: time.Now}
	if err := c.importWindows(db); err != nil {
		return nil, err
	}
	return c, nil
}

// Location is the zone the calendar's times of day are in.
func (c *Calendar) Location() *time.Location { return c.loc }

// importWindows turns hosts' maintenance windows, as tremolo 0.12 kept them,
// into periods, once.
func (c *Calendar) importWindows(db *state.DB) error {
	type window struct {
		Since time.Time `json:"since"`
		Until time.Time `json:"until"`
		Note  string    `json:"note"`
	}
	old, err := state.Load[struct {
		Hosts map[string]window `json:"hosts"`
	}](db, "maintenance")
	if err != nil {
		return err
	}
	var hosts map[string]window
	old.Read(func(v *struct {
		Hosts map[string]window `json:"hosts"`
	},
	) {
		hosts = v.Hosts
	})
	tx, err := c.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := c.now()
	for host, w := range hosts {
		if !w.Until.IsZero() && !w.Until.After(now) {
			continue
		}
		p := Period{ID: id(), Title: host + " maintenance", Start: w.Since, End: w.Until, Hosts: []string{host}, Note: w.Note, Created: now}
		if err := insertPeriod(tx, p); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM state WHERE name = 'maintenance'`); err != nil {
		return err
	}
	return tx.Commit()
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// nullMS is a time for a nullable column: NULL when zero.
func nullMS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func at(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64)
}

func id() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func list(s []string) string {
	if s == nil {
		s = []string{}
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// Now is the calendar's clock.
func (c *Calendar) Now() time.Time { return c.now() }

// SetClock replaces the calendar's clock, for tests.
func (c *Calendar) SetClock(now func() time.Time) { c.now = now }
