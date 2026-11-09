package hosts

import (
	"context"
	"errors"
	"time"

	"github.com/tpural/tremolo/internal/hoststate"
	"github.com/tpural/tremolo/internal/state"
)

// Window is a stretch of time a host is down or busy on purpose.
type Window struct {
	Since time.Time `json:"since"`
	// Until is when it ends; zero lasts until it is cleared.
	Until time.Time `json:"until,omitzero"`
	Note  string    `json:"note,omitempty"`
}

type windows struct {
	Hosts map[string]Window `json:"hosts"`
}

// Maintenance marks hosts as in maintenance for a while, so a host being
// worked on reads as intentional rather than broken.
type Maintenance struct {
	store *state.Store[windows]
	now   func() time.Time
}

// OpenMaintenance keeps windows in db, or in memory when db is nil.
func OpenMaintenance(db *state.DB) (*Maintenance, error) {
	s, err := state.Load[windows](db, "maintenance")
	if err != nil {
		return nil, err
	}
	return &Maintenance{store: s, now: time.Now}, nil
}

// Name implements Provider.
func (*Maintenance) Name() string { return "maintenance" }

// Apply implements Provider. It wins over every other state.
func (m *Maintenance) Apply(_ context.Context, hosts map[string]*Host) {
	now := m.now()
	m.store.Read(func(w *windows) {
		for name, win := range w.Hosts {
			h, ok := hosts[name]
			if !ok || (!win.Until.IsZero() && !now.Before(win.Until)) {
				continue
			}
			h.State, h.Since, h.Until, h.Reason, h.StateBy = hoststate.Maintenance, win.Since, win.Until, win.Note, m.Name()
		}
	})
}

// ErrPast means a window would already be over.
var ErrPast = errors.New("until is in the past")

// Set puts host in maintenance until the given time; zero lasts until Clear.
func (m *Maintenance) Set(host string, until time.Time, note string) error {
	now := m.now()
	if !until.IsZero() && !until.After(now) {
		return ErrPast
	}
	return m.store.Update(func(w *windows) error {
		if w.Hosts == nil {
			w.Hosts = map[string]Window{}
		}
		// Drop windows that have ended while here.
		for name, win := range w.Hosts {
			if !win.Until.IsZero() && !now.Before(win.Until) {
				delete(w.Hosts, name)
			}
		}
		w.Hosts[host] = Window{Since: now, Until: until, Note: note}
		return nil
	})
}

// Clear ends host's maintenance.
func (m *Maintenance) Clear(host string) error {
	return m.store.Update(func(w *windows) error {
		delete(w.Hosts, host)
		return nil
	})
}
