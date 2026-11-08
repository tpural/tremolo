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
	// Sent is when each channel was last told, by channel name.
	Sent map[string]time.Time `json:"sent,omitempty"`
	// Acked stops escalation and repeats until the alert resolves.
	Acked   time.Time `json:"acked,omitzero"`
	AckedBy string    `json:"ackedBy,omitempty"`
	// AckToken lets a link in a notification acknowledge this alert.
	AckToken string `json:"ackToken,omitempty"`
	Silenced bool   `json:"silenced"`
	// Plan is where the alert goes and when, for the UI.
	Plan []Planned `json:"plan,omitempty"`
}

// Silence stops notifications for a rule, or one of its keys, for a while.
type Silence struct {
	ID      string    `json:"id"`
	Rule    string    `json:"rule"`
	Key     string    `json:"key,omitempty"`
	Until   time.Time `json:"until"`
	Note    string    `json:"note,omitempty"`
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
	Config  Config
	Data    *Data
	DataDir string
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

// New returns an Engine. Alerts and silences are kept in opts.DataDir.
func New(opts Options) (*Engine, error) {
	store, err := state.Open[saved](opts.DataDir, "alerts.json")
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
				a.State, a.FiringSince = "firing", now
				history = append(history, e.notification(a, "firing", 0))
			}
			if a.State != "firing" || !a.Acked.IsZero() || slices.ContainsFunc(s.Silences, func(si Silence) bool { return si.matches(a, now) }) {
				continue
			}
			plan, unknown := e.opts.Config.plan(e.targets(a))
			for _, u := range unknown {
				warnings = append(warnings, fmt.Sprintf("no channel or escalation %q", u))
			}
			// Steps reached so far; repeats go to the latest one only.
			elapsed := now.Sub(a.FiringSince)
			var latest time.Duration
			for _, p := range plan {
				if p.After <= elapsed {
					latest = p.After
				}
			}
			for _, p := range plan {
				if p.After > elapsed {
					continue
				}
				switch last := a.Sent[p.Channel]; {
				case last.IsZero():
					out = append(out, e.outgoing(a, p.Channel, "firing", p.After))
				case p.After == latest && e.opts.Config.Repeat.Duration > 0 && now.Sub(last) >= e.opts.Config.Repeat.Duration:
					out = append(out, e.outgoing(a, p.Channel, "repeat", p.After))
				}
			}
		}
		for id, a := range s.Alerts {
			if a.Rule != r.Name || seen[id] {
				continue
			}
			if a.State == "firing" {
				history = append(history, e.notification(a, "resolved", 0))
				for ch := range a.Sent {
					out = append(out, e.outgoing(a, ch, "resolved", 0))
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
}

type outgoing struct {
	id, channel string
	n           notify.Notification
}

func (e *Engine) outgoing(a *Alert, channel, event string, unacked time.Duration) outgoing {
	return outgoing{a.ID, channel, e.notification(a, event, unacked)}
}

func (e *Engine) notification(a *Alert, event string, unacked time.Duration) notify.Notification {
	n := notify.Notification{Event: event, Rule: a.Rule, Key: a.Key, Summary: a.Summary, Severity: a.Severity, Labels: a.Labels, Since: a.Since, Unacked: unacked}
	if !a.FiringSince.IsZero() {
		n.Since = a.FiringSince
	}
	if u := strings.TrimRight(e.opts.Config.URL, "/"); u != "" {
		n.URL = u + "/alerts"
		if a.AckToken != "" && a.Acked.IsZero() {
			n.AckURL = u + "/alerts?ack=" + a.AckToken
		}
	}
	return n
}

// targets are where an alert goes: the rule's choice, else its severity's default.
func (e *Engine) targets(a *Alert) []string {
	if len(a.Notify) > 0 {
		return a.Notify
	}
	return e.opts.Config.Notify[a.Severity]
}

// deliver sends each notification and records which channels got theirs, so
// a channel that failed is tried again on the next run. It returns problems.
func (e *Engine) deliver(ctx context.Context, now time.Time, out []outgoing) []string {
	var problems []string
	type done struct{ id, channel string }
	var sent []done
	for _, o := range out {
		p, ok := e.opts.Config.providers[o.channel]
		if !ok {
			continue
		}
		if err := p.Send(ctx, o.n); err != nil {
			e.opts.Log.Warn("notification failed", "rule", o.n.Rule, "key", o.n.Key, "channel", o.channel, "err", err)
			problems = append(problems, o.channel+": "+err.Error())
			continue
		}
		e.opts.Log.Info("notified", "rule", o.n.Rule, "key", o.n.Key, "event", o.n.Event, "channel", o.channel)
		if o.n.Event == "firing" || o.n.Event == "repeat" {
			sent = append(sent, done{o.id, o.channel})
		}
	}
	if len(sent) > 0 {
		_ = e.store.Update(func(s *saved) error {
			for _, d := range sent {
				if a, ok := s.Alerts[d.id]; ok {
					if a.Sent == nil {
						a.Sent = map[string]time.Time{}
					}
					a.Sent[d.channel] = now
				}
			}
			return nil
		})
	}
	return problems
}

// ErrNoAlert means no alert matches.
var ErrNoAlert = errors.New("no such alert, or it has resolved")

// Ack acknowledges the alert with the given id or ack token: escalation and
// repeats stop, and the channels already told hear who has it.
func (e *Engine) Ack(ctx context.Context, idOrToken, by string) (Alert, error) {
	now := e.now()
	var acked Alert
	var out []outgoing
	err := e.store.Update(func(s *saved) error {
		for _, a := range s.Alerts {
			if a.ID != idOrToken && (a.AckToken == "" || a.AckToken != idOrToken) {
				continue
			}
			if a.Acked.IsZero() {
				a.Acked, a.AckedBy = now, by
				for ch := range a.Sent {
					out = append(out, e.outgoing(a, ch, "acknowledged", 0))
				}
			}
			acked = *a
			return nil
		}
		return ErrNoAlert
	})
	if err != nil {
		return Alert{}, err
	}
	n := e.notification(&acked, "acknowledged", 0)
	if by != "" {
		n.Summary += " (by " + by + ")"
	}
	for i := range out {
		out[i].n.Summary = n.Summary
	}
	// Tell people without holding up the request.
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		e.deliver(ctx, now, out)
		e.record(ctx, []notify.Notification{n})
	}()
	return acked, nil
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
		if n.Event == "acknowledged" {
			row["_msg"] = n.Summary
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
			c := *a
			c.Silenced = slices.ContainsFunc(snap.Silences, func(si Silence) bool { return si.matches(&c, now) })
			c.Plan, _ = e.opts.Config.plan(e.targets(&c))
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
	snap.Timezone = e.opts.Config.Timezone
	return snap
}

// ErrNoRule means no rule has that name.
var ErrNoRule = errors.New("no such rule")

// Silence adds a silence and returns it.
func (e *Engine) Silence(rule, key string, until time.Time, note string) (Silence, error) {
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
	si := Silence{ID: token()[:12], Rule: rule, Key: key, Until: until, Note: note, Created: now}
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
