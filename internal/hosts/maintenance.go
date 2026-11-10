package hosts

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/tpural/tremolo/internal/calendar"
	"github.com/tpural/tremolo/internal/hoststate"
)

// Maintenance puts hosts in maintenance while a calendar period names them,
// so a host being worked on reads as intentional rather than broken. The
// same periods hold its alerts.
type Maintenance struct {
	cal *calendar.Calendar
}

// NewMaintenance reads maintenance from cal.
func NewMaintenance(cal *calendar.Calendar) *Maintenance { return &Maintenance{cal: cal} }

// Name implements Provider.
func (*Maintenance) Name() string { return "maintenance" }

// Apply implements Provider. It wins over every other state.
func (m *Maintenance) Apply(ctx context.Context, hosts map[string]*Host) {
	on, err := m.cal.Active(ctx, m.cal.Now())
	if err != nil {
		return
	}
	for _, o := range on {
		for _, name := range o.Period.Hosts {
			h, ok := hosts[name]
			if !ok || !o.Period.HostWide(name) {
				continue
			}
			reason := o.Period.Note
			if reason == "" {
				reason = o.Period.Title
			}
			h.State, h.Since, h.Until, h.Reason, h.StateBy = hoststate.Maintenance, o.Start, o.End, reason, m.Name()
		}
	}
}

// ErrPast means a window would already be over.
var ErrPast = errors.New("until is in the past")

// Set puts host in maintenance from now until the given time; zero lasts
// until Clear.
func (m *Maintenance) Set(ctx context.Context, host string, until time.Time, note, by string) error {
	now := m.cal.Now()
	if !until.IsZero() && !until.After(now) {
		return ErrPast
	}
	_, err := m.cal.SavePeriod(ctx, calendar.Period{Title: host + " maintenance", Start: now, End: until, Hosts: []string{host}, Note: note, By: by})
	return err
}

// Clear ends host's maintenance: periods for it alone end now, and it's
// taken out of ones that cover other hosts too.
func (m *Maintenance) Clear(ctx context.Context, host string) error {
	on, err := m.cal.Active(ctx, m.cal.Now())
	if err != nil {
		return err
	}
	for _, o := range on {
		p := o.Period
		if !p.HostWide(host) {
			continue
		}
		if len(p.Hosts) == 1 {
			err = m.cal.End(ctx, p.ID)
		} else {
			p.Hosts = slices.DeleteFunc(p.Hosts, func(h string) bool { return h == host })
			_, err = m.cal.SavePeriod(ctx, p)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
