// Package agents is where AI agents working the lab coordinate: an
// investigation for each alert that fires, which one agent claims and
// records its findings and actions in, rails that refuse risky actions, and
// a switch that pauses them all. Agents run elsewhere; tremolo never acts
// on the cluster for them.
package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/state"
)

// Config is how agents may work, from --agents.
type Config struct {
	Enabled bool `json:"enabled"`
	// First holds people's notifications of a firing alert for Grace while
	// an agent works on it. They hear at once when no agent claims it within
	// ClaimWait, when it's escalated, or when it matches Always.
	First     bool            `json:"first"`
	Grace     alerts.Duration `json:"grace"`
	ClaimWait alerts.Duration `json:"claimWait"`
	// Always lists rules and severities people hear of at once.
	Always []string `json:"always,omitempty"`
	// Notify is the channels told when agents are paused, and sent reports.
	Notify []string `json:"notify,omitempty"`
	// MaxActionsPerHour and MaxDisruptivePerHour limit actions per token,
	// and all agents together.
	MaxActionsPerHour    int `json:"maxActionsPerHour"`
	MaxDisruptivePerHour int `json:"maxDisruptivePerHour"`
	// Protect limits how many of a group's hosts or namespaces agents act on at once.
	Protect []Group `json:"protect,omitempty"`
}

// Group is hosts and namespaces agents may act on only MaxAtOnce at a time.
type Group struct {
	Name       string   `json:"name"`
	Hosts      []string `json:"hosts,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
	MaxAtOnce  int      `json:"maxAtOnce"`
}

func (g Group) has(host, namespace string) bool {
	return (host != "" && slices.Contains(g.Hosts, host)) || (namespace != "" && slices.Contains(g.Namespaces, namespace))
}

// ParseConfig reads --agents, JSON, filling in defaults.
func ParseConfig(s string) (Config, error) {
	c := Config{}
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &c); err != nil {
			return c, fmt.Errorf("--agents: %w", err)
		}
	}
	if c.Grace.Duration == 0 {
		c.Grace.Duration = 20 * time.Minute
	}
	if c.ClaimWait.Duration == 0 {
		c.ClaimWait.Duration = 5 * time.Minute
	}
	if c.MaxActionsPerHour == 0 {
		c.MaxActionsPerHour = 10
	}
	if c.MaxDisruptivePerHour == 0 {
		c.MaxDisruptivePerHour = 3
	}
	for i, g := range c.Protect {
		if g.MaxAtOnce < 1 {
			c.Protect[i].MaxAtOnce = 1
		}
	}
	return c, nil
}

// Kinds of investigation.
const (
	Incident      = "incident"
	Gap           = "gap"
	Tuning        = "tuning"
	RunbookReview = "runbook-review"
)

// Statuses of an investigation.
const (
	Open      = "open"
	Escalated = "escalated"
	Fixed     = "fixed"
	Resolved  = "resolved" // the alert went away without a recorded fix
	Abandoned = "abandoned"
)

// ClaimFor is how long a claim lasts without activity.
const ClaimFor = 15 * time.Minute

// Investigation is one piece of work: an alert to fix, a gap to close, or a
// runbook to review.
type Investigation struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Alert        string    `json:"alert,omitempty"`
	Rule         string    `json:"rule,omitempty"`
	Key          string    `json:"key,omitempty"`
	Title        string    `json:"title"`
	Severity     string    `json:"severity,omitempty"`
	Status       string    `json:"status"`
	ClaimedBy    string    `json:"claimedBy,omitempty"`
	ClaimExpires time.Time `json:"claimExpires,omitzero"`
	Opened       time.Time `json:"opened"`
	Closed       time.Time `json:"closed,omitzero"`
	// AlertResolved is when the alert went away, while this was open.
	AlertResolved time.Time `json:"alertResolved,omitzero"`
	Summary       string    `json:"summary,omitempty"`
	Fix           string    `json:"fix,omitempty"`
	Runbook       string    `json:"runbook,omitempty"`
	Outcome       string    `json:"outcome,omitempty"`
	OutcomeAt     time.Time `json:"outcomeAt,omitzero"`
	// Previous is the last investigation of the same alert.
	Previous int64   `json:"previous,omitempty"`
	Events   []Event `json:"events,omitempty"`
}

// Claimed reports whether someone holds the investigation at now.
func (i Investigation) Claimed(now time.Time) bool {
	return i.ClaimedBy != "" && i.ClaimExpires.After(now)
}

// Event is one entry in an investigation's timeline. An action is an event
// with Action set, and Result once it's done.
type Event struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	By         string    `json:"by"`
	Kind       string    `json:"kind"`
	Text       string    `json:"text"`
	Action     string    `json:"action,omitempty"`
	Host       string    `json:"host,omitempty"`
	Namespace  string    `json:"namespace,omitempty"`
	Target     string    `json:"target,omitempty"`
	Disruptive bool      `json:"disruptive,omitempty"`
	Result     string    `json:"result,omitempty"`
	// Investigation and Title are set when events are listed across investigations.
	Investigation int64  `json:"investigation,omitempty"`
	Title         string `json:"title,omitempty"`
}

// Kinds of event an agent records.
var recordable = []string{"finding", "query", "note", "change"}

// Errors.
var (
	ErrNotFound = errors.New("no such investigation")
	ErrClosed   = errors.New("the investigation is closed")
	ErrInvalid  = errors.New("invalid")
)

// ClaimedError means another agent holds the investigation.
type ClaimedError struct {
	By    string
	Until time.Time
}

func (e ClaimedError) Error() string {
	left := time.Until(e.Until).Round(time.Minute)
	return fmt.Sprintf("%s is working on this for up to %s more; pick another or wait", e.By, max(left, time.Minute))
}

// Refusal is why an action may not go ahead.
type Refusal struct{ Why string }

func (r Refusal) Error() string { return "refused: " + r.Why }

type paused struct {
	Paused bool      `json:"paused"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at,omitzero"`
	Note   string    `json:"note,omitempty"`
}

// watch is what Hold needs of an open incident, kept in memory.
type watch struct {
	claimed   time.Time // claim expiry
	escalated bool
	opened    time.Time
}

// Options configures a Service.
type Options struct {
	Config Config
	DB     *state.DB
	// HostState is a host's state (ready, asleep, off, maintenance…), for
	// refusing actions on hosts that shouldn't be touched; nil skips it.
	HostState func(ctx context.Context, host string) (string, bool)
	Log       *slog.Logger
}

// Announcer sends a message to channels; the alerts engine is one.
type Announcer interface {
	Announce(ctx context.Context, channels []string, title, text string) error
}

// Service keeps the investigations.
type Service struct {
	opts  Options
	db    *sql.DB
	now   func() time.Time
	pause *state.Store[paused]

	mu       sync.Mutex
	open     map[string]watch // by alert ID
	announce Announcer
}

// New returns a Service.
func New(opts Options) (*Service, error) {
	if opts.DB == nil {
		return nil, errors.New("agents need the database")
	}
	p, err := state.Load[paused](opts.DB, "agents")
	if err != nil {
		return nil, err
	}
	s := &Service{opts: opts, db: opts.DB.SQL(), now: time.Now, pause: p, open: map[string]watch{}}
	list, err := s.List(context.Background(), Filter{Status: []string{Open, Escalated}, Kind: Incident, Limit: 1000})
	if err != nil {
		return nil, err
	}
	for _, i := range list {
		s.open[i.Alert] = watch{claimed: i.ClaimExpires, escalated: i.Status == Escalated, opened: i.Opened}
	}
	return s, nil
}

// Bind sets who sends announcements, once the alerts engine exists.
func (s *Service) Bind(a Announcer) {
	s.mu.Lock()
	s.announce = a
	s.mu.Unlock()
}

// Config is the service's settings.
func (s *Service) Config() Config { return s.opts.Config }

func (s *Service) tell(ctx context.Context, title, text string) {
	s.mu.Lock()
	a := s.announce
	s.mu.Unlock()
	if a == nil || len(s.opts.Config.Notify) == 0 {
		return
	}
	if err := a.Announce(ctx, s.opts.Config.Notify, title, text); err != nil {
		s.opts.Log.Warn("telling about agents", "err", err)
	}
}

// Fired opens an incident for an alert that started firing, or notes that it
// fired again on the open one.
func (s *Service) Fired(a alerts.Alert) {
	ctx := context.Background()
	now := s.now()
	if open, err := s.byAlert(ctx, a.ID); err == nil {
		_ = s.event(ctx, open.ID, Event{Time: now, By: "tremolo", Kind: "note", Text: "fired again: " + a.Summary})
		return
	}
	var prev int64
	_ = s.db.QueryRowContext(ctx, `SELECT id FROM investigations WHERE alert = ? ORDER BY id DESC LIMIT 1`, a.ID).Scan(&prev)
	inv, err := s.create(ctx, Investigation{
		Kind: Incident, Alert: a.ID, Rule: a.Rule, Key: a.Key, Title: a.Summary, Severity: a.Severity,
		Status: Open, Opened: now, Previous: prev,
	}, "tremolo", "alert fired: "+a.Summary)
	if err != nil {
		s.opts.Log.Warn("opening an investigation", "alert", a.ID, "err", err)
		return
	}
	s.mu.Lock()
	s.open[a.ID] = watch{opened: inv.Opened}
	s.mu.Unlock()
}

// Resolved notes that an alert went away. An unclaimed incident closes as
// resolved; a claimed one stays for its agent to finish.
func (s *Service) Resolved(a alerts.Alert, at time.Time) {
	ctx := context.Background()
	s.mu.Lock()
	delete(s.open, a.ID)
	s.mu.Unlock()
	inv, err := s.byAlert(ctx, a.ID)
	if err != nil {
		return
	}
	_ = s.event(ctx, inv.ID, Event{Time: at, By: "tremolo", Kind: "note", Text: "the alert resolved"})
	if inv.Claimed(at) {
		_, _ = s.db.ExecContext(ctx, `UPDATE investigations SET alert_resolved = ? WHERE id = ?`, at.UnixMilli(), inv.ID)
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE investigations SET status = ?, closed = ?, alert_resolved = ? WHERE id = ?`,
		Resolved, at.UnixMilli(), at.UnixMilli(), inv.ID)
}

// Hold reports whether people's notifications of a should wait while an agent works on it.
func (s *Service) Hold(a alerts.Alert, now time.Time) bool {
	c := s.opts.Config
	if !c.First || slices.Contains(c.Always, a.Rule) || slices.Contains(c.Always, a.Severity) {
		return false
	}
	if p := s.Paused(); p.Paused {
		return false
	}
	if now.Sub(a.FiringSince) >= c.Grace.Duration {
		return false
	}
	s.mu.Lock()
	w, ok := s.open[a.ID]
	s.mu.Unlock()
	switch {
	case !ok:
		// Not opened yet: wait no longer than an agent would have to claim it.
		return now.Sub(a.FiringSince) < c.ClaimWait.Duration
	case w.escalated:
		return false
	case w.claimed.After(now):
		return true
	}
	// Unclaimed, or the claim lapsed: give agents ClaimWait from firing.
	return now.Sub(a.FiringSince) < c.ClaimWait.Duration
}

// Paused reports whether agents are paused, by whom and when.
func (s *Service) Paused() (p paused) {
	s.pause.Read(func(v *paused) { p = *v })
	return p
}

// Pause stops agents: their tokens can't change anything, and actions are refused.
func (s *Service) Pause(ctx context.Context, by, note string) error {
	err := s.pause.Update(func(v *paused) error {
		*v = paused{Paused: true, By: by, At: s.now(), Note: note}
		return nil
	})
	if err == nil {
		s.tell(ctx, "Agents paused", fmt.Sprintf("%s paused the agents%s. People hear of every alert until they're resumed.", by, suffix(note)))
	}
	return err
}

// Resume lets agents work again.
func (s *Service) Resume(ctx context.Context, by string) error {
	err := s.pause.Update(func(v *paused) error {
		*v = paused{}
		return nil
	})
	if err == nil {
		s.tell(ctx, "Agents resumed", by+" resumed the agents.")
	}
	return err
}

func suffix(note string) string {
	if note == "" {
		return ""
	}
	return ": " + note
}

// Filter narrows List.
type Filter struct {
	Status []string
	Kind   string
	Alert  string
	Rule   string
	Before int64
	Limit  int
}

const columns = `id, kind, alert, rule, key, title, severity, status, claimed_by, claim_expires, opened, closed,
	alert_resolved, summary, fix, runbook, outcome, outcome_at, previous`

func scan(row interface{ Scan(...any) error }) (Investigation, error) {
	var i Investigation
	var claim, opened, closed, resolved, outcomeAt int64
	err := row.Scan(&i.ID, &i.Kind, &i.Alert, &i.Rule, &i.Key, &i.Title, &i.Severity, &i.Status, &i.ClaimedBy, &claim,
		&opened, &closed, &resolved, &i.Summary, &i.Fix, &i.Runbook, &i.Outcome, &outcomeAt, &i.Previous)
	i.ClaimExpires, i.Opened, i.Closed, i.AlertResolved, i.OutcomeAt = ms(claim), ms(opened), ms(closed), ms(resolved), ms(outcomeAt)
	return i, err
}

func ms(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// List returns investigations, newest first.
func (s *Service) List(ctx context.Context, f Filter) ([]Investigation, error) {
	q := `SELECT ` + columns + ` FROM investigations WHERE 1=1`
	var args []any
	if len(f.Status) > 0 {
		q += ` AND status IN (?` + strings.Repeat(", ?", len(f.Status)-1) + `)`
		for _, st := range f.Status {
			args = append(args, st)
		}
	}
	for col, v := range map[string]string{"kind": f.Kind, "alert": f.Alert, "rule": f.Rule} {
		if v != "" {
			q += ` AND ` + col + ` = ?` //nolint:gosec // col is one of three fixed names
			args = append(args, v)
		}
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Investigation{}
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Get returns an investigation with its timeline.
func (s *Service) Get(ctx context.Context, id int64) (Investigation, error) {
	i, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM investigations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	i.Events, err = s.events(ctx, `WHERE investigation = ? ORDER BY id`, id)
	return i, err
}

const eventColumns = `id, investigation, time, actor, kind, text, action, host, namespace, target, disruptive, result`

func (s *Service) events(ctx context.Context, where string, args ...any) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventColumns+` FROM investigation_events `+where, args...) //nolint:gosec // where is written here, values are args
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var t int64
		if err := rows.Scan(&e.ID, &e.Investigation, &t, &e.By, &e.Kind, &e.Text, &e.Action, &e.Host, &e.Namespace,
			&e.Target, &e.Disruptive, &e.Result); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(t)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) byAlert(ctx context.Context, alert string) (Investigation, error) {
	i, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM investigations
		WHERE alert = ? AND kind = ? AND status IN (?, ?) ORDER BY id DESC LIMIT 1`, alert, Incident, Open, Escalated))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

func (s *Service) create(ctx context.Context, i Investigation, by, text string) (Investigation, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO investigations
		(kind, alert, rule, key, title, severity, status, claimed_by, claim_expires, opened, previous)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.Kind, i.Alert, i.Rule, i.Key, i.Title, i.Severity, i.Status, i.ClaimedBy, i.ClaimExpires.UnixMilli(), i.Opened.UnixMilli(), i.Previous)
	if err != nil {
		return i, err
	}
	i.ID, _ = res.LastInsertId()
	return i, s.event(ctx, i.ID, Event{Time: i.Opened, By: by, Kind: "opened", Text: text})
}

func (s *Service) event(ctx context.Context, inv int64, e Event) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO investigation_events
		(investigation, time, actor, kind, text, action, host, namespace, target, disruptive, result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv, e.Time.UnixMilli(), e.By, e.Kind, clip(e.Text, 8000), e.Action, e.Host, e.Namespace, e.Target, e.Disruptive, clip(e.Result, 2000))
	return err
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Open starts an investigation that isn't an incident: a gap, tuning, or a runbook review.
func (s *Service) Open(ctx context.Context, kind, title, text, by string, agent bool) (Investigation, error) {
	if !slices.Contains([]string{Gap, Tuning, RunbookReview}, kind) {
		return Investigation{}, fmt.Errorf("%w: kind must be gap, tuning or runbook-review", ErrInvalid)
	}
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 300 {
		return Investigation{}, fmt.Errorf("%w: a title of 1 to 300 characters", ErrInvalid)
	}
	now := s.now()
	i := Investigation{Kind: kind, Title: title, Status: Open, Opened: now}
	if agent {
		i.ClaimedBy, i.ClaimExpires = by, now.Add(ClaimFor)
	}
	return s.create(ctx, i, by, text)
}

// work loads an open investigation for by to change, claiming it for an
// agent. A person may change one an agent holds; an agent may not.
func (s *Service) work(ctx context.Context, id int64, by string, agent bool) (Investigation, error) {
	i, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM investigations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	if i.Status != Open && i.Status != Escalated {
		return i, ErrClosed
	}
	now := s.now()
	if !agent {
		return i, nil
	}
	if i.Claimed(now) && i.ClaimedBy != by {
		return i, ClaimedError{By: i.ClaimedBy, Until: i.ClaimExpires}
	}
	first := i.ClaimedBy != by || !i.Claimed(now)
	i.ClaimedBy, i.ClaimExpires = by, now.Add(ClaimFor)
	if _, err := s.db.ExecContext(ctx, `UPDATE investigations SET claimed_by = ?, claim_expires = ? WHERE id = ?`,
		by, i.ClaimExpires.UnixMilli(), id); err != nil {
		return i, err
	}
	if first {
		_ = s.event(ctx, id, Event{Time: now, By: by, Kind: "claimed", Text: by + " is working on it"})
	}
	if i.Alert != "" {
		s.mu.Lock()
		if w, ok := s.open[i.Alert]; ok {
			w.claimed = i.ClaimExpires
			s.open[i.Alert] = w
		}
		s.mu.Unlock()
	}
	return i, nil
}

// Claim takes an investigation for an agent, or renews its claim.
func (s *Service) Claim(ctx context.Context, id int64, by string, agent bool) (Investigation, error) {
	if _, err := s.work(ctx, id, by, agent); err != nil {
		return Investigation{}, err
	}
	return s.Get(ctx, id)
}

// Record adds a finding, query, note or change to an investigation.
func (s *Service) Record(ctx context.Context, id int64, by string, agent bool, kind, text string) (Event, error) {
	if !slices.Contains(recordable, kind) {
		return Event{}, fmt.Errorf("%w: kind must be one of %s", ErrInvalid, strings.Join(recordable, ", "))
	}
	if strings.TrimSpace(text) == "" {
		return Event{}, fmt.Errorf("%w: text is empty", ErrInvalid)
	}
	if _, err := s.work(ctx, id, by, agent); err != nil {
		return Event{}, err
	}
	e := Event{Time: s.now(), By: by, Kind: kind, Text: text}
	return e, s.event(ctx, id, e)
}

// Action is a change an agent is about to make outside tremolo.
type Action struct {
	Action     string `json:"action"`
	Host       string `json:"host,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Target     string `json:"target"`
	Disruptive bool   `json:"disruptive"`
	Why        string `json:"why,omitempty"`
}

// BeginAction records an action before it's made, or refuses it with why.
func (s *Service) BeginAction(ctx context.Context, id int64, by string, agent bool, a Action) (Event, error) {
	a.Action, a.Target = strings.TrimSpace(a.Action), strings.TrimSpace(a.Target)
	if a.Action == "" || a.Target == "" || len(a.Action) > 200 || len(a.Target) > 300 {
		return Event{}, fmt.Errorf("%w: say what the action is and what it's on", ErrInvalid)
	}
	if _, err := s.work(ctx, id, by, agent); err != nil {
		return Event{}, err
	}
	now := s.now()
	if err := s.allow(ctx, by, a, now); err != nil {
		_ = s.event(ctx, id, Event{
			Time: now, By: "tremolo", Kind: "refused", Text: err.Error(), Action: a.Action, Host: a.Host,
			Namespace: a.Namespace, Target: a.Target, Disruptive: a.Disruptive,
		})
		return Event{}, err
	}
	e := Event{
		Time: now, By: by, Kind: "action", Text: a.Why, Action: a.Action, Host: a.Host, Namespace: a.Namespace,
		Target: a.Target, Disruptive: a.Disruptive,
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO investigation_events
		(investigation, time, actor, kind, text, action, host, namespace, target, disruptive, result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		id, now.UnixMilli(), by, "action", clip(a.Why, 8000), a.Action, a.Host, a.Namespace, a.Target, a.Disruptive)
	if err != nil {
		return e, err
	}
	e.ID, _ = res.LastInsertId()
	e.Investigation = id
	return e, nil
}

// allow checks an action against the rails.
func (s *Service) allow(ctx context.Context, by string, a Action, now time.Time) error {
	c := s.opts.Config
	if p := s.Paused(); p.Paused {
		return Refusal{Why: "agents are paused by " + p.By}
	}
	if a.Host != "" && s.opts.HostState != nil {
		if st, ok := s.opts.HostState(ctx, a.Host); ok && slices.Contains([]string{"asleep", "off", "waking", "maintenance"}, st) {
			return Refusal{Why: fmt.Sprintf("host %s is %s; leave it alone", a.Host, st)}
		}
	}
	hour := now.Add(-time.Hour).UnixMilli()
	count := func(where string, args ...any) int {
		var n int
		_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM investigation_events WHERE kind = 'action' AND time > ? `+where,
			append([]any{hour}, args...)...).Scan(&n)
		return n
	}
	if n := count(`AND actor = ?`, by); n >= c.MaxActionsPerHour {
		return Refusal{Why: fmt.Sprintf("%s made %d actions in the last hour, the most allowed", by, n)}
	}
	if n := count(``); n >= c.MaxActionsPerHour*3 {
		return Refusal{Why: fmt.Sprintf("agents made %d actions in the last hour, the most allowed", n)}
	}
	if a.Disruptive {
		if n := count(`AND disruptive = 1 AND actor = ?`, by); n >= c.MaxDisruptivePerHour {
			return Refusal{Why: fmt.Sprintf("%s made %d disruptive actions in the last hour, the most allowed", by, n)}
		}
		if n := count(`AND disruptive = 1`); n >= c.MaxDisruptivePerHour*2 {
			return Refusal{Why: fmt.Sprintf("agents made %d disruptive actions in the last hour, the most allowed", n)}
		}
	}
	for _, g := range c.Protect {
		if !g.has(a.Host, a.Namespace) {
			continue
		}
		running, err := s.events(ctx, `WHERE kind = 'action' AND result = '' AND time > ?`, hour)
		if err != nil {
			return err
		}
		busy := map[string]bool{}
		for _, e := range running {
			if g.has(e.Host, e.Namespace) && e.Target != a.Target {
				busy[e.Target] = true
			}
		}
		if len(busy) >= g.MaxAtOnce {
			return Refusal{Why: fmt.Sprintf("%s allows %d at once and %d are being worked on; finish those first", g.Name, g.MaxAtOnce, len(busy))}
		}
	}
	return nil
}

// EndAction records how an action went.
func (s *Service) EndAction(ctx context.Context, id, event int64, by string, agent bool, result string) error {
	result = strings.TrimSpace(result)
	if result == "" {
		return fmt.Errorf("%w: say how it went", ErrInvalid)
	}
	if _, err := s.work(ctx, id, by, agent); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE investigation_events SET result = ? WHERE id = ? AND investigation = ? AND kind = 'action'`,
		clip(result, 2000), event, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no action %d in investigation %d", ErrInvalid, event, id)
	}
	return nil
}

// Close finishes an investigation: fixed, with what fixed it, or abandoned.
func (s *Service) Close(ctx context.Context, id int64, by string, agent bool, status, summary, fix, runbook string) (Investigation, error) {
	if status != Fixed && status != Abandoned {
		return Investigation{}, fmt.Errorf("%w: status must be fixed or abandoned", ErrInvalid)
	}
	if status == Fixed && strings.TrimSpace(fix) == "" {
		return Investigation{}, fmt.Errorf("%w: say what fixed it", ErrInvalid)
	}
	i, err := s.work(ctx, id, by, agent)
	if err != nil {
		return i, err
	}
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE investigations SET status = ?, closed = ?, summary = ?, fix = ?, runbook = ?,
		claimed_by = '', claim_expires = 0 WHERE id = ?`, status, now.UnixMilli(), clip(summary, 8000), clip(fix, 8000), runbook, id); err != nil {
		return i, err
	}
	text := status
	if fix != "" {
		text += ": " + fix
	}
	_ = s.event(ctx, id, Event{Time: now, By: by, Kind: "closed", Text: text})
	if i.Alert != "" {
		s.mu.Lock()
		delete(s.open, i.Alert)
		s.mu.Unlock()
	}
	return s.Get(ctx, id)
}

// Escalate hands an investigation to people, with what was tried. For an
// alert, people are notified on its rule's next check.
func (s *Service) Escalate(ctx context.Context, id int64, by string, agent bool, why string) (Investigation, error) {
	if strings.TrimSpace(why) == "" {
		return Investigation{}, fmt.Errorf("%w: say what you tried and why it needs a person", ErrInvalid)
	}
	i, err := s.work(ctx, id, by, agent)
	if err != nil {
		return i, err
	}
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE investigations SET status = ?, claimed_by = '', claim_expires = 0 WHERE id = ?`, Escalated, id); err != nil {
		return i, err
	}
	_ = s.event(ctx, id, Event{Time: now, By: by, Kind: "escalated", Text: why})
	if i.Alert != "" {
		s.mu.Lock()
		w := s.open[i.Alert]
		w.escalated = true
		s.open[i.Alert] = w
		s.mu.Unlock()
	} else {
		s.tell(ctx, "Agents need a person: "+i.Title, why)
	}
	return s.Get(ctx, id)
}

// Status is the agents' state for the Agents page and for agents themselves.
type Status struct {
	Enabled bool   `json:"enabled"`
	Config  Config `json:"config"`
	Paused  paused `json:"paused"`
	// Actions and Disruptive are made in the last hour, by token.
	Actions    map[string]int `json:"actions"`
	Disruptive map[string]int `json:"disruptive"`
	Open       int            `json:"open"`
	Escalated  int            `json:"escalated"`
	// Recent is the latest actions and refusals.
	Recent []Event `json:"recent"`
}

// State returns the agents' state.
func (s *Service) State(ctx context.Context) (Status, error) {
	st := Status{Enabled: s.opts.Config.Enabled, Config: s.opts.Config, Paused: s.Paused(), Actions: map[string]int{}, Disruptive: map[string]int{}}
	hour := s.now().Add(-time.Hour).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT actor, count(*), sum(disruptive) FROM investigation_events
		WHERE kind = 'action' AND time > ? GROUP BY actor`, hour)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var by string
		var n, d int
		if err := rows.Scan(&by, &n, &d); err != nil {
			_ = rows.Close()
			return st, err
		}
		st.Actions[by], st.Disruptive[by] = n, d
	}
	_ = rows.Close()
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM investigations WHERE status = ?`, Open).Scan(&st.Open)
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM investigations WHERE status = ?`, Escalated).Scan(&st.Escalated)
	st.Recent, err = s.events(ctx, `WHERE kind IN ('action', 'refused') ORDER BY id DESC LIMIT 30`)
	if err != nil {
		return st, err
	}
	titles := map[int64]string{}
	for i, e := range st.Recent {
		if _, ok := titles[e.Investigation]; !ok {
			var t string
			_ = s.db.QueryRowContext(ctx, `SELECT title FROM investigations WHERE id = ?`, e.Investigation).Scan(&t)
			titles[e.Investigation] = t
		}
		st.Recent[i].Title = titles[e.Investigation]
	}
	return st, nil
}

// Run closes incidents whose alert resolved while an agent held them, once
// the claim lapses, and forgets lapsed claims for Hold.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		now := s.now().UnixMilli()
		if _, err := s.db.ExecContext(ctx, `UPDATE investigations SET status = ?, closed = alert_resolved, claimed_by = ''
			WHERE status = ? AND alert_resolved > 0 AND claim_expires < ?`, Resolved, Open, now); err != nil {
			s.opts.Log.Warn("closing resolved investigations", "err", err)
		}
	}
}
