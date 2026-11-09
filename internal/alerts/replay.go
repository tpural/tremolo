package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Replays are bounded so one can't swamp the metrics and logs stores.
const (
	maxReplayRuns  = 300
	maxReplayRange = 7 * 24 * time.Hour
	replayWorkers  = 6
)

// ErrBusy means another replay is running; try again when it's done.
var ErrBusy = errors.New("another replay is running; try again in a moment")

// Episode is one time a replayed rule would have fired.
type Episode struct {
	Key      string    `json:"key"`
	Summary  string    `json:"summary"`
	Severity string    `json:"severity"`
	Pending  time.Time `json:"pending"`
	Fired    time.Time `json:"fired"`
	// Resolved is zero when it was still firing at the end.
	Resolved time.Time `json:"resolved,omitzero"`
}

// Replay is what a rule would have done over a past stretch of time.
type Replay struct {
	From  time.Time     `json:"from"`
	To    time.Time     `json:"to"`
	Step  time.Duration `json:"step"`
	Every time.Duration `json:"every"`
	For   time.Duration `json:"for"`
	Runs  int           `json:"runs"`
	// Episodes are the alerts that would have fired, oldest first.
	Episodes []Episode `json:"episodes"`
	// Firing is how many alerts were firing at each run.
	Firing     []int    `json:"firing"`
	Errors     int      `json:"errors"`
	FirstError string   `json:"firstError,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	// Found and Output are the last run's alerts and print() output.
	Found  []Found       `json:"found"`
	Output []string      `json:"output"`
	Took   time.Duration `json:"took"`
	// Actual is what the running rule of that name really fired over the
	// stretch, from its history; nil for a rule that isn't running.
	Actual []Episode `json:"actual"`
}

type replayRun struct {
	found    []Found
	err      error
	hostsNow bool
}

// Replay runs a rule's source as if at evenly spaced moments from from to to,
// and plays what it found through pending, firing and resolved, as the engine
// would have. Nothing is saved or sent. With from equal to to, it's one run.
func (e *Engine) Replay(ctx context.Context, name, source string, from, to time.Time) (Replay, error) {
	select {
	case e.replaying <- struct{}{}:
		defer func() { <-e.replaying }()
	default:
		return Replay{}, ErrBusy
	}
	now := e.now()
	if to.After(now) {
		to = now
	}
	if from.After(to) || to.Sub(from) > maxReplayRange {
		return Replay{}, fmt.Errorf("replay up to %s, ending no later than now", maxReplayRange)
	}
	_, running := e.Rule(name)
	if name == "" {
		name = "draft"
	}
	r, err := ParseRule(name, source, rulesFS(e.rulesDir()))
	if err != nil {
		return Replay{}, err
	}
	loc := e.opts.Config.Location()
	out := Replay{From: from.In(loc), To: to.In(loc), Every: r.Every, For: r.For, Step: r.Every, Episodes: []Episode{}, Found: []Found{}, Output: []string{}}
	span := to.Sub(from)
	if n := span/out.Step + 1; n > maxReplayRuns {
		// The shortest whole number of minutes that fits.
		per := span / (maxReplayRuns - 1)
		out.Step = (per + time.Minute - 1) / time.Minute * time.Minute
		out.Warnings = append(out.Warnings, fmt.Sprintf("checked every %s rather than every %s, to keep the replay quick; alerts shorter than that may not show", out.Step, r.Every))
	}
	var times []time.Time
	for t := from; !t.After(to); t = t.Add(out.Step) {
		times = append(times, t.In(loc))
	}
	if last := times[len(times)-1]; !last.Equal(to.In(loc)) && span > 0 {
		times = append(times, to.In(loc))
	}
	out.Runs = len(times)

	start := time.Now()
	runs := make([]replayRun, len(times))
	var wg sync.WaitGroup
	work := make(chan int)
	for range replayWorkers {
		wg.Go(func() {
			for i := range work {
				env := &Env{Data: e.opts.Data, Now: times[i], Past: span > 0}
				if i == len(times)-1 {
					env.Print = func(s string) {
						if len(out.Output) < 200 {
							out.Output = append(out.Output, s)
						}
					}
				}
				runs[i].found, runs[i].err = r.Run(ctx, env)
				runs[i].hostsNow = env.hostsNow
			}
		})
	}
	for i := range times {
		if ctx.Err() != nil {
			break
		}
		work <- i
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Replay{}, err
	}
	out.Took = time.Since(start)

	open := map[string]*Episode{}
	var hostsNow []time.Time
	for i, t := range times {
		run := runs[i]
		if run.hostsNow {
			hostsNow = append(hostsNow, t)
		}
		if run.err != nil {
			// A failed run changes nothing, as in the engine.
			out.Errors++
			if out.FirstError == "" {
				out.FirstError = fmt.Sprintf("at %s: %v", t.Format(time.DateTime), run.err)
			}
			out.Firing = append(out.Firing, firing(open))
			continue
		}
		seen := map[string]bool{}
		for _, f := range run.found {
			seen[f.Key] = true
			ep, ok := open[f.Key]
			if !ok {
				ep = &Episode{Key: f.Key, Pending: t}
				open[f.Key] = ep
			}
			ep.Summary, ep.Severity = f.Summary, f.Severity
			if ep.Fired.IsZero() && t.Sub(ep.Pending) >= r.For {
				ep.Fired = t
			}
		}
		for key, ep := range open {
			if seen[key] {
				continue
			}
			if !ep.Fired.IsZero() {
				ep.Resolved = t
				out.Episodes = append(out.Episodes, *ep)
			}
			delete(open, key)
		}
		out.Firing = append(out.Firing, firing(open))
		if i == len(times)-1 {
			out.Found = run.found
		}
	}
	for _, ep := range open {
		if !ep.Fired.IsZero() {
			out.Episodes = append(out.Episodes, *ep)
		}
	}
	sortEpisodes(out.Episodes)
	if running {
		if out.Actual, err = e.actual(ctx, name, from, to); err != nil {
			out.Warnings = append(out.Warnings, "couldn't read what the rule really fired: "+err.Error())
		}
	}
	if len(hostsNow) > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"%d of %d runs, up to %s, are from before host states were recorded, so hosts() gave each host's state now there",
			len(hostsNow), out.Runs, hostsNow[len(hostsNow)-1].Format(time.DateTime)))
	}
	return out, nil
}

func firing(open map[string]*Episode) int {
	n := 0
	for _, ep := range open {
		if !ep.Fired.IsZero() {
			n++
		}
	}
	return n
}

func sortEpisodes(eps []Episode) {
	slices.SortFunc(eps, func(a, b Episode) int {
		if c := a.Fired.Compare(b.Fired); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
}

// actual is what a rule fired that overlaps from..to: its history, and what's
// firing now.
func (e *Engine) actual(ctx context.Context, rule string, from, to time.Time) ([]Episode, error) {
	out := []Episode{}
	add := func(a Alert, resolved time.Time) {
		if a.FiringSince.IsZero() || a.FiringSince.After(to) || !resolved.IsZero() && resolved.Before(from) {
			return
		}
		ep := Episode{Key: a.Key, Summary: a.Summary, Severity: a.Severity, Pending: a.Since, Fired: a.FiringSince}
		if !resolved.IsZero() && !resolved.After(to) {
			ep.Resolved = resolved
		}
		out = append(out, ep)
	}
	e.store.Read(func(s *saved) {
		for _, a := range s.Alerts {
			if a.Rule == rule && a.State == "firing" {
				add(*a, time.Time{})
			}
		}
	})
	if db := e.opts.DB; db != nil {
		rows, err := db.SQL().QueryContext(ctx, `SELECT resolved, data FROM alert_history WHERE rule = ? AND resolved >= ? AND fired <= ?`,
			rule, from.UnixMilli(), to.UnixMilli())
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var ms int64
			var data string
			var a Alert
			if err := rows.Scan(&ms, &data); err != nil {
				return nil, err
			}
			if json.Unmarshal([]byte(data), &a) == nil {
				add(a, time.UnixMilli(ms))
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	sortEpisodes(out)
	return out, nil
}
