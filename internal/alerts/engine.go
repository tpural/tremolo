package alerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/alerts/notify"
	"github.com/tpural/tremolo/internal/state"
)

// Alert is a rule's finding that is pending or firing.
type Alert struct {
	ID       string            `json:"id"`
	Rule     string            `json:"rule"`
	Key      string            `json:"key,omitempty"`
	Summary  string            `json:"summary"`
	Severity string            `json:"severity"`
	Notify   []string          `json:"notify,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	// State is pending until the rule's FOR has passed, then firing.
	State       string    `json:"state"`
	Since       time.Time `json:"since"`
	FiringSince time.Time `json:"firingSince,omitzero"`
	// Escalating is when the current round of notifications began: when the
	// alert started firing, or when it was last forwarded.
	Escalating time.Time `json:"escalating,omitzero"`
	// Sent is when each recipient of this round was last told, by Planned.Key.
	Sent map[string]time.Time `json:"sent,omitempty"`
	// Told is everyone told about the alert so far, who hear when it's
	// acknowledged or resolved.
	Told map[string]Planned `json:"told,omitempty"`
	// Acked stops escalation and repeats until the alert resolves or is forwarded.
	Acked   time.Time `json:"acked,omitzero"`
	AckedBy string    `json:"ackedBy,omitempty"`
	// Assignee is who has the alert: a person or team it was forwarded to or
	// taken by. It replaces the rule's notify targets.
	Assignee    string `json:"assignee,omitempty"`
	ForwardedBy string `json:"forwardedBy,omitempty"`
	ForwardNote string `json:"forwardNote,omitempty"`
	// AckToken lets a link in a notification acknowledge this alert; Tokens
	// are each person's own, so their link says who acknowledged.
	AckToken string            `json:"ackToken,omitempty"`
	Tokens   map[string]string `json:"tokens,omitempty"`
	Timeline []Event           `json:"timeline,omitempty"`
	Silenced bool              `json:"silenced"`
	// Plan is where the alert goes and when, for the UI.
	Plan []Planned `json:"plan,omitempty"`
}

// Event is one thing that happened to an alert.
type Event struct {
	Time time.Time `json:"time"`
	// Kind is firing, notified, failed, acknowledged, forwarded, taken, note or resolved.
	Kind string `json:"kind"`
	By   string `json:"by,omitempty"`
	To   string `json:"to,omitempty"`
	Note string `json:"note,omitempty"`
}

// maxTimeline is how many events an alert keeps.
const maxTimeline = 200

func (a *Alert) log(ev Event) {
	a.Timeline = append(a.Timeline, ev)
	if n := len(a.Timeline); n > maxTimeline {
		a.Timeline = slices.Delete(a.Timeline, 0, n-maxTimeline)
	}
}

// Silence stops notifications for a rule, or one of its keys, for a while.
type Silence struct {
	ID      string    `json:"id"`
	Rule    string    `json:"rule"`
	Key     string    `json:"key,omitempty"`
	Until   time.Time `json:"until"`
	Note    string    `json:"note,omitempty"`
	By      string    `json:"by,omitempty"`
	Created time.Time `json:"created"`
}

func (s Silence) matches(a *Alert, now time.Time) bool {
	return s.Rule == a.Rule && (s.Key == "" || s.Key == a.Key) && now.Before(s.Until)
}

// RuleStatus is a rule and how its last run went.
type RuleStatus struct {
	*Rule
	LastRun time.Time     `json:"lastRun,omitzero"`
	Took    time.Duration `json:"took"`
	Error   string        `json:"error,omitempty"`
	// Warning is a problem that didn't stop the rule, e.g. an unknown channel.
	Warning string `json:"warning,omitempty"`
}

type saved struct {
	Alerts   map[string]*Alert `json:"alerts"`
	Silences []Silence         `json:"silences"`
}

// Options configures an Engine.
type Options struct {
	Config Config
	Data   *Data
	// DB keeps alerts, silences and history; nil keeps them in memory, without history.
	DB *state.DB
	// Directory is the people and teams alerts can go to; nil means none.
	Directory Directory
	// Client sends notifications and writes history.
	Client *http.Client
	Log    *slog.Logger
}

// Engine runs rules on their schedules and tracks what they find.
type Engine struct {
	opts  Options
	store *state.Store[saved]
	now   func() time.Time

	mu      sync.Mutex
	rules   map[string]*RuleStatus
	loadErr string
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New returns an Engine.
func New(opts Options) (*Engine, error) {
	store, err := state.Load[saved](opts.DB, "alerts")
	if err != nil {
		return nil, err
	}
	return &Engine{opts: opts, store: store, now: time.Now, rules: map[string]*RuleStatus{}}, nil
}

// SetRules replaces the running rules. Alerts of rules that are gone are dropped.
func (e *Engine) SetRules(ctx context.Context, rules []*Rule, loadErr error) {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	e.wg.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()
	e.loadErr = ""
	if loadErr != nil {
		e.loadErr = loadErr.Error()
	}
	next := map[string]*RuleStatus{}
	for _, r := range rules {
		st := &RuleStatus{Rule: r}
		// Keep the last run of an unchanged rule.
		if old, ok := e.rules[r.Name]; ok && old.Source == r.Source {
			st.LastRun, st.Took, st.Error, st.Warning = old.LastRun, old.Took, old.Error, old.Warning
		}
		next[r.Name] = st
	}
	e.rules = next
	_ = e.store.Update(func(s *saved) error {
		for id, a := range s.Alerts {
			if _, ok := next[a.Rule]; !ok {
				delete(s.Alerts, id)
			}
		}
		return nil
	})
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	for _, r := range rules {
		e.wg.Go(func() { e.loop(runCtx, r) })
	}
}

// Stop stops every rule.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) loop(ctx context.Context, r *Rule) {
	// Spread rules out, so they don't all query at once.
	wait := mrand.N(min(r.Every, 15*time.Second)) //nolint:gosec // jitter, not security
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		e.Eval(ctx, r)
		wait = r.Every
	}
}

// Eval runs r once and updates its alerts, sending what's due.
func (e *Engine) Eval(ctx context.Context, r *Rule) {
	now := e.now().In(e.opts.Config.Location())
	start := time.Now()
	found, err := r.Run(ctx, &Env{Data: e.opts.Data, Now: now})
	took := time.Since(start)
	if ctx.Err() != nil {
		return
	}
	var warnings []string
	e.mu.Lock()
	if st, ok := e.rules[r.Name]; ok {
		st.LastRun, st.Took, st.Error = now, took, ""
		if err != nil {
			st.Error = err.Error()
		}
	}
	e.mu.Unlock()
	if err != nil {
		e.opts.Log.Warn("rule failed", "rule", r.Name, "err", err)
		return
	}

	var out []outgoing
	var history []notify.Notification
	var resolved []Alert
	_ = e.store.Update(func(s *saved) error {
		if s.Alerts == nil {
			s.Alerts = map[string]*Alert{}
		}
		seen := map[string]bool{}
		for _, f := range found {
			id := r.Name + "/" + f.Key
			seen[id] = true
			a, ok := s.Alerts[id]
			if !ok {
				a = &Alert{ID: id, Rule: r.Name, Key: f.Key, State: "pending", Since: now, AckToken: token()}
				s.Alerts[id] = a
			}
			a.Summary, a.Severity, a.Notify, a.Labels = f.Summary, f.Severity, f.Notify, f.Labels
			if a.State == "pending" && now.Sub(a.Since) >= r.For {
				a.State, a.FiringSince, a.Escalating = "firing", now, now
				a.log(Event{Time: now, Kind: "firing", Note: a.Summary})
				history = append(history, e.notification(a, Planned{}, "firing", 0))
			}
			if slices.ContainsFunc(s.Silences, func(si Silence) bool { return si.matches(a, now) }) {
				continue
			}
			due, problems := e.due(a, now)
			out = append(out, due...)
			warnings = append(warnings, problems...)
		}
		for id, a := range s.Alerts {
			if a.Rule != r.Name || seen[id] {
				continue
			}
			if a.State == "firing" {
				a.log(Event{Time: now, Kind: "resolved"})
				resolved = append(resolved, a.public())
				history = append(history, e.notification(a, Planned{}, "resolved", 0))
				for _, p := range a.Told {
					out = append(out, e.outgoing(a, p, "resolved", 0))
				}
			}
			delete(s.Alerts, id)
		}
		return nil
	})

	warnings = append(warnings, e.deliver(ctx, now, out)...)
	e.mu.Lock()
	if st, ok := e.rules[r.Name]; ok {
		st.Warning = strings.Join(slices.Compact(warnings), "; ")
	}
	e.mu.Unlock()
	e.record(ctx, history)
	e.remember(ctx, resolved, now)
}

// due returns the notifications a firing alert should send now: the steps
// of its plan reached so far, and repeats to the latest one. It's called
// inside a store update, as it may give people their ack tokens.
func (e *Engine) due(a *Alert, now time.Time) ([]outgoing, []string) {
	if a.State != "firing" || !a.Acked.IsZero() {
		return nil, nil
	}
	plan, problems := e.planner().plan(e.targets(a))
	start := a.Escalating
	if start.IsZero() {
		start = a.FiringSince
	}
	elapsed := now.Sub(start)
	var latest time.Duration
	for _, p := range plan {
		if p.After <= elapsed {
			latest = p.After
		}
	}
	var out []outgoing
	for _, p := range plan {
		if p.After > elapsed {
			continue
		}
		switch last := a.Sent[p.Key]; {
		case last.IsZero():
			out = append(out, e.outgoing(a, p, "firing", p.After))
		case p.After == latest && e.opts.Config.Repeat.Duration > 0 && now.Sub(last) >= e.opts.Config.Repeat.Duration:
			out = append(out, e.outgoing(a, p, "repeat", p.After))
		}
	}
	return out, problems
}

func (e *Engine) planner() planner { return planner{e.opts.Config, e.opts.Directory} }

type outgoing struct {
	id string
	to Planned
	n  notify.Notification
}

func (e *Engine) outgoing(a *Alert, to Planned, event string, unacked time.Duration) outgoing {
	return outgoing{a.ID, to, e.notification(a, to, event, unacked)}
}

func (e *Engine) notification(a *Alert, to Planned, event string, unacked time.Duration) notify.Notification {
	n := notify.Notification{
		Event: event, Rule: a.Rule, Key: a.Key, Summary: a.Summary, Severity: a.Severity, Labels: a.Labels, Since: a.Since, Unacked: unacked,
		To: to.To, ForwardedBy: a.ForwardedBy, Note: a.ForwardNote,
	}
	if !a.FiringSince.IsZero() {
		n.Since = a.FiringSince
	}
	if u := strings.TrimRight(e.opts.Config.URL, "/"); u != "" {
		n.URL = u + "/alerts"
		if tok := a.tokenFor(to.Person); tok != "" && a.Acked.IsZero() {
			n.AckURL = u + "/ack/" + tok
		}
	}
	return n
}

// tokenFor returns the ack token for a person's links, making one if needed,
// or the alert's shared one.
func (a *Alert) tokenFor(person string) string {
	if person == "" {
		return a.AckToken
	}
	if a.Tokens == nil {
		a.Tokens = map[string]string{}
	}
	if a.Tokens[person] == "" {
		a.Tokens[person] = token()
	}
	return a.Tokens[person]
}

// targets are where an alert goes: whoever it was forwarded to, else the
// rule's choice, else its severity's default.
func (e *Engine) targets(a *Alert) []string {
	if a.Assignee != "" {
		return []string{a.Assignee}
	}
	if len(a.Notify) > 0 {
		return a.Notify
	}
	return e.opts.Config.Notify[a.Severity]
}

// deliver sends each notification and records which channels got theirs, so
// a channel that failed is tried again on the next run. It returns problems.
func (e *Engine) deliver(ctx context.Context, now time.Time, out []outgoing) []string {
	var problems []string
	type result struct {
		o   outgoing
		err error
	}
	var results []result
	for _, o := range out {
		p, ok := e.opts.Config.providers[o.to.Channel]
		if !ok {
			continue
		}
		err := p.Send(ctx, o.n)
		if err != nil {
			e.opts.Log.Warn("notification failed", "rule", o.n.Rule, "key", o.n.Key, "to", o.to.Key, "err", err)
			problems = append(problems, o.to.Key+": "+err.Error())
		} else {
			e.opts.Log.Info("notified", "rule", o.n.Rule, "key", o.n.Key, "event", o.n.Event, "to", o.to.Key)
		}
		if o.n.Event == "firing" || o.n.Event == "repeat" {
			results = append(results, result{o, err})
		}
	}
	if len(results) > 0 {
		_ = e.store.Update(func(s *saved) error {
			for _, r := range results {
				a, ok := s.Alerts[r.o.id]
				if !ok {
					continue
				}
				who := r.o.to.Channel
				if r.o.to.Person != "" {
					who = r.o.to.Person + " (" + r.o.to.Channel + ")"
				}
				if r.err != nil {
					// Note a failure once per recipient, not on every retry.
					if !slices.ContainsFunc(a.Timeline, func(ev Event) bool { return ev.Kind == "failed" && ev.To == who && !ev.Time.Before(a.Escalating) }) {
						a.log(Event{Time: now, Kind: "failed", To: who, Note: r.err.Error()})
					}
					continue
				}
				if a.Sent == nil {
					a.Sent = map[string]time.Time{}
				}
				if a.Told == nil {
					a.Told = map[string]Planned{}
				}
				a.Sent[r.o.to.Key] = now
				a.Told[r.o.to.Key] = r.o.to
				if r.o.n.Event == "firing" {
					a.log(Event{Time: now, Kind: "notified", To: who})
				}
			}
			return nil
		})
	}
	return problems
}

// ErrNoAlert means no alert matches.
var ErrNoAlert = errors.New("no such alert, or it has resolved")

// find returns the alert with an id or ack token, and the person a personal
// token belongs to.
func find(s *saved, idOrToken string) (*Alert, string) {
	if a, ok := s.Alerts[idOrToken]; ok {
		return a, ""
	}
	for _, a := range s.Alerts {
		if a.AckToken != "" && a.AckToken == idOrToken {
			return a, ""
		}
		for person, t := range a.Tokens {
			if t == idOrToken {
				return a, person
			}
		}
	}
	return nil, ""
}

// Ack acknowledges the alert with the given id or ack token: escalation and
// repeats stop, and everyone already told hears who has it. When by is empty
// and the token is someone's own, it was them.
func (e *Engine) Ack(ctx context.Context, idOrToken, by string) (Alert, error) {
	return e.ack(ctx, idOrToken, by, false)
}

// Take acknowledges an alert and assigns it to by.
func (e *Engine) Take(ctx context.Context, id, by string) (Alert, error) {
	return e.ack(ctx, id, by, true)
}

func (e *Engine) ack(ctx context.Context, idOrToken, by string, take bool) (Alert, error) {
	now := e.now()
	var acked Alert
	var out []outgoing
	err := e.store.Update(func(s *saved) error {
		a, person := find(s, idOrToken)
		if a == nil {
			return ErrNoAlert
		}
		if by == "" {
			by = person
		}
		if take {
			if by == "" {
				return errors.New("taking an alert needs a name")
			}
			if a.Assignee != by {
				a.Assignee, a.ForwardedBy, a.ForwardNote = by, "", ""
				a.log(Event{Time: now, Kind: "taken", By: by})
			}
		}
		if a.Acked.IsZero() {
			a.Acked, a.AckedBy = now, by
			a.log(Event{Time: now, Kind: "acknowledged", By: by})
			for _, p := range a.Told {
				out = append(out, e.outgoing(a, p, "acknowledged", 0))
			}
		}
		acked = a.public()
		return nil
	})
	if err != nil {
		return Alert{}, err
	}
	n := e.notification(&acked, Planned{}, "acknowledged", 0)
	if by != "" {
		n.Summary += " (by " + by + ")"
	}
	for i := range out {
		out[i].n.Summary = n.Summary
	}
	e.send(ctx, now, out, n)
	return acked, nil
}

// send delivers and records in the background, so a request isn't held up.
func (e *Engine) send(ctx context.Context, now time.Time, out []outgoing, history ...notify.Notification) {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		e.deliver(ctx, now, out)
		e.record(ctx, history)
	}()
}

// Forward hands an alert to a person or team (or any notify target): it's
// unacknowledged again and escalates to them from now, starting at once.
func (e *Engine) Forward(ctx context.Context, id, to, by, note string) (Alert, error) {
	if plan, problems := e.planner().plan([]string{to}); len(plan) == 0 {
		msg := "nobody to tell"
		if len(problems) > 0 {
			msg = strings.Join(problems, "; ")
		}
		return Alert{}, fmt.Errorf("can't forward to %s: %s", to, msg)
	}
	now := e.now()
	var fwd Alert
	var out []outgoing
	err := e.store.Update(func(s *saved) error {
		a, ok := s.Alerts[id]
		if !ok {
			return ErrNoAlert
		}
		a.Assignee, a.ForwardedBy, a.ForwardNote = to, by, note
		a.Acked, a.AckedBy = time.Time{}, ""
		a.Escalating, a.Sent = now, nil
		a.log(Event{Time: now, Kind: "forwarded", By: by, To: to, Note: note})
		out, _ = e.due(a, now)
		fwd = a.public()
		return nil
	})
	if err != nil {
		return Alert{}, err
	}
	n := e.notification(&fwd, Planned{}, "forwarded", 0)
	n.Summary += " (to " + to + " by " + by + ")"
	e.send(ctx, now, out, n)
	return fwd, nil
}

// Note adds a note to an alert's timeline.
func (e *Engine) Note(id, by, note string) (Alert, error) {
	now := e.now()
	var a2 Alert
	err := e.store.Update(func(s *saved) error {
		a, ok := s.Alerts[id]
		if !ok {
			return ErrNoAlert
		}
		a.log(Event{Time: now, Kind: "note", By: by, Note: note})
		a2 = a.public()
		return nil
	})
	return a2, err
}

// Peek returns the alert an ack token is for and whose token it is.
func (e *Engine) Peek(tok string) (Alert, string, error) {
	var a Alert
	var person string
	err := ErrNoAlert
	e.store.Read(func(s *saved) {
		found, p := find(s, tok)
		// Only tokens: an id isn't a secret.
		if found != nil && found.ID != tok {
			a, person, err = found.public(), p, nil
		}
	})
	return a, person, err
}

// Targets lists the channels and escalations by name.
func (e *Engine) Targets() (channels, escalations []string) {
	return e.opts.Config.targetNames()
}

// public is a copy without secrets, for the API.
func (a *Alert) public() Alert {
	c := *a
	c.AckToken, c.Tokens = "", nil
	c.Timeline = slices.Clone(a.Timeline)
	return c
}

func token() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HistoryStream is the VictoriaLogs stream alert history is written to.
const HistoryStream = "tremolo-alerts"

// record writes firing and resolved events to VictoriaLogs, so history is
// searchable and kept as long as logs are.
func (e *Engine) record(ctx context.Context, events []notify.Notification) {
	if len(events) == 0 || e.opts.Data == nil || e.opts.Data.LogsURL == nil {
		return
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, n := range events {
		row := map[string]string{
			"_time": time.Now().UTC().Format(time.RFC3339Nano), "_msg": n.Summary, "stream": HistoryStream,
			"event": n.Event, "rule": n.Rule, "key": n.Key, "severity": n.Severity,
		}
		for k, v := range n.Labels {
			if _, taken := row[k]; !taken {
				row[k] = v
			}
		}
		_ = enc.Encode(row)
	}
	u := *e.opts.Data.LogsURL
	u.Path = strings.TrimRight(u.Path, "/") + "/insert/jsonline"
	u.RawQuery = "_stream_fields=stream,rule&_msg_field=_msg&_time_field=_time"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return
	}
	resp, err := e.opts.Client.Do(req)
	if err != nil {
		e.opts.Log.Warn("writing alert history", "err", err)
		return
	}
	_ = resp.Body.Close()
}

// Snapshot is everything the UI shows.
type Snapshot struct {
	Alerts      []Alert       `json:"alerts"`
	Rules       []*RuleStatus `json:"rules"`
	Silences    []Silence     `json:"silences"`
	Channels    []string      `json:"channels"`
	Escalations []string      `json:"escalations"`
	People      []string      `json:"people"`
	Teams       []string      `json:"teams"`
	LoadError   string        `json:"loadError,omitempty"`
	Timezone    string        `json:"timezone"`
}

// Snapshot returns the current alerts, rules and silences.
func (e *Engine) Snapshot() Snapshot {
	now := e.now()
	var snap Snapshot
	e.store.Read(func(s *saved) {
		for _, si := range s.Silences {
			if now.Before(si.Until) {
				snap.Silences = append(snap.Silences, si)
			}
		}
		for _, a := range s.Alerts {
			c := a.public()
			c.Silenced = slices.ContainsFunc(snap.Silences, func(si Silence) bool { return si.matches(&c, now) })
			c.Plan, _ = e.planner().plan(e.targets(&c))
			snap.Alerts = append(snap.Alerts, c)
		}
	})
	rank := func(a Alert) int {
		return map[string]int{"firing": 0, "pending": 10}[a.State] + slices.Index(Severities, a.Severity)
	}
	slices.SortFunc(snap.Alerts, func(a, b Alert) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	e.mu.Lock()
	for _, st := range e.rules {
		c := *st
		snap.Rules = append(snap.Rules, &c)
	}
	snap.LoadError = e.loadErr
	e.mu.Unlock()
	slices.SortFunc(snap.Rules, func(a, b *RuleStatus) int { return strings.Compare(a.Name, b.Name) })
	snap.Channels, snap.Escalations = e.opts.Config.targetNames()
	snap.People, snap.Teams = []string{}, []string{}
	if d := e.opts.Directory; d != nil {
		snap.People, snap.Teams = d.Names()
	}
	snap.Timezone = e.opts.Config.Timezone
	return snap
}

// ErrNoRule means no rule has that name.
var ErrNoRule = errors.New("no such rule")

// Silence adds a silence and returns it.
func (e *Engine) Silence(rule, key string, until time.Time, note, by string) (Silence, error) {
	now := e.now()
	e.mu.Lock()
	_, ok := e.rules[rule]
	e.mu.Unlock()
	if !ok {
		return Silence{}, ErrNoRule
	}
	if !until.After(now) {
		return Silence{}, errors.New("until is in the past")
	}
	si := Silence{ID: token()[:12], Rule: rule, Key: key, Until: until, Note: note, By: by, Created: now}
	err := e.store.Update(func(s *saved) error {
		s.Silences = slices.DeleteFunc(s.Silences, func(old Silence) bool { return !now.Before(old.Until) })
		s.Silences = append(s.Silences, si)
		return nil
	})
	return si, err
}

// Unsilence removes a silence.
func (e *Engine) Unsilence(id string) error {
	return e.store.Update(func(s *saved) error {
		s.Silences = slices.DeleteFunc(s.Silences, func(si Silence) bool { return si.ID == id })
		return nil
	})
}

// TestRun is one run of a rule that changes nothing.
type TestRun struct {
	Found  []Found       `json:"found"`
	Output []string      `json:"output"`
	Error  string        `json:"error,omitempty"`
	Took   time.Duration `json:"took"`
}

// Test runs a rule now without touching its alerts or sending anything.
func (e *Engine) Test(ctx context.Context, name string) (TestRun, error) {
	e.mu.Lock()
	st, ok := e.rules[name]
	e.mu.Unlock()
	if !ok {
		return TestRun{}, ErrNoRule
	}
	var run TestRun
	start := time.Now()
	found, err := st.Run(ctx, &Env{Data: e.opts.Data, Now: e.now().In(e.opts.Config.Location()), Print: func(s string) {
		if len(run.Output) < 200 {
			run.Output = append(run.Output, s)
		}
	}})
	run.Took = time.Since(start)
	run.Found = found
	if err != nil {
		run.Error = err.Error()
	}
	return run, nil
}

// Watch loads rules from dir now and whenever its files change.
func (e *Engine) Watch(ctx context.Context, dir string, every time.Duration) {
	last := ""
	for {
		if fp := fingerprint(dir); fp != last {
			rules, err := LoadRules(dir, e.opts.Config.Disable)
			if err != nil {
				e.opts.Log.Error("loading rules", "err", err)
			}
			e.SetRules(ctx, rules, err)
			e.opts.Log.Info("rules loaded", "rules", len(rules), "dir", dir)
			last = fp
		}
		select {
		case <-ctx.Done():
			e.Stop()
			return
		case <-time.After(every):
		}
	}
}

// fingerprint changes when any file under dir does.
func fingerprint(dir string) string {
	if dir == "" {
		return "-"
	}
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// ConfigMap mounts swap a symlink; follow it.
		info, err := os.Stat(p)
		if err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", p, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}
