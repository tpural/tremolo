package alerts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/hosts"
	"github.com/tpural/tremolo/internal/hoststate"
)

// clock is a metrics store whose one series is 0, except between 02:00 and
// 03:00, and again from 05:00 on, when it's 5.
func clock(t *testing.T, base time.Time) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts, _ := strconv.ParseInt(r.URL.Query().Get("time"), 10, 64)
		h := time.Unix(ts, 0).Sub(base).Hours()
		v := "0"
		if h >= 2 && h < 3 || h >= 5 {
			v = "5"
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[` +
			strconv.FormatInt(ts, 10) + `,"` + v + `"]}]}}`))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

const highRule = `
EVERY = "1m"
FOR = "10m"
def check(now):
    v = metric("errors")
    print("errors", v)
    if v > 3:
        return alert("api errors at %d" % v, key = "api")
`

func TestEditorRules(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	data := &Data{MetricsURL: clock(t, base), Client: http.DefaultClient}
	e := engine(t, data, "")
	e.now = func() time.Time { return base.Add(6 * time.Hour) }
	dir := t.TempDir()
	write(t, dir, "from-file.star", "def check(now):\n    return ok()\n")
	go e.Watch(ctx, dir, time.Hour)
	t.Cleanup(e.Stop)
	waitFor(t, func() bool { _, ok := e.Rule("from-file"); return ok })

	// Saving checks the rule, keeps every version, and starts it running.
	if _, err := e.SaveRule(ctx, "api-errors", "def nope(:", "", "alex"); err == nil {
		t.Fatal("saved a broken rule")
	}
	if _, err := e.SaveRule(ctx, "api-errors", "def check(now):\n    return ok()\n", "first try", "alex"); err != nil {
		t.Fatal(err)
	}
	v2, err := e.SaveRule(ctx, "api-errors", highRule, "alert over 3", "tim")
	if err != nil || v2.Version != 2 {
		t.Fatalf("second save: %+v %v", v2, err)
	}
	if _, err := e.SaveRule(ctx, "api-errors", highRule, "", "tim"); err == nil {
		t.Error("saved an unchanged rule")
	}
	waitFor(t, func() bool { r, ok := e.Rule("api-errors"); return ok && r.Version == 2 })
	r, _ := e.Rule("api-errors")
	if r.Origin != FromEditor || r.For != 10*time.Minute {
		t.Errorf("running rule = %+v", r.Rule)
	}
	versions, _ := e.Versions(ctx, "api-errors")
	if len(versions) != 2 || versions[0].Author != "tim" || versions[1].Note != "first try" || versions[0].Source != "" {
		t.Errorf("versions = %+v", versions)
	}
	if v, err := e.VersionOf(ctx, "api-errors", 1); err != nil || !strings.Contains(v.Source, "ok()") {
		t.Errorf("version 1 = %+v %v", v, err)
	}

	// A built-in can be replaced from the editor; a file rule can't.
	if _, err := e.SaveRule(ctx, "from-file", highRule, "", "alex"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("replaced a file rule: %v", err)
	}
	if _, err := e.SaveRule(ctx, "crash-loop", "def check(now):\n    return ok()\n", "quieter", "alex"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { r, ok := e.Rule("crash-loop"); return ok && r.Origin == FromEditor })
	if err := e.DeleteRule(ctx, "host-down"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("deleted a built-in: %v", err)
	}
	if err := e.DeleteRule(ctx, "crash-loop"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { r, ok := e.Rule("crash-loop"); return ok && r.Origin == FromBuiltin })
	if v, _ := e.Versions(ctx, "crash-loop"); len(v) != 0 {
		t.Errorf("versions left after delete: %v", v)
	}
}

func TestReplay(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	data := &Data{MetricsURL: clock(t, base), Client: http.DefaultClient}
	e := engine(t, data, "")
	e.now = func() time.Time { return base.Add(6 * time.Hour) }

	// Errors are high 02:00 to 03:00 and from 05:00: with FOR 10m it fires at
	// 02:10, resolves at 03:00, fires again at 05:10 and is still firing.
	rep, err := e.Replay(ctx, "", highRule, base.Add(time.Hour), base.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Runs != 301 && rep.Step != time.Minute {
		t.Logf("runs %d step %s", rep.Runs, rep.Step)
	}
	if rep.Errors != 0 || len(rep.Episodes) != 2 {
		t.Fatalf("replay = %+v", rep)
	}
	first, second := rep.Episodes[0], rep.Episodes[1]
	if !first.Pending.Equal(base.Add(2*time.Hour)) || !first.Fired.Equal(base.Add(2*time.Hour+10*time.Minute)) ||
		!first.Resolved.Equal(base.Add(3*time.Hour)) || first.Summary != "api errors at 5" {
		t.Errorf("first = %+v", first)
	}
	if !second.Fired.Equal(base.Add(5*time.Hour+10*time.Minute)) || !second.Resolved.IsZero() {
		t.Errorf("second = %+v", second)
	}
	if len(rep.Found) != 1 || len(rep.Output) != 1 || rep.Output[0] != "errors 5.0" {
		t.Errorf("last run = %+v %q", rep.Found, rep.Output)
	}
	if len(rep.Firing) != rep.Runs || rep.Firing[len(rep.Firing)-1] != 1 {
		t.Errorf("firing = %v", rep.Firing)
	}

	if rep.Actual != nil {
		t.Errorf("a draft has actual firings: %v", rep.Actual)
	}

	// For a running rule, the replay says what it really fired: here once in
	// the stretch, and once long before it.
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	running, _ := ParseRule("api-errors", highRule, nil)
	e.SetRules(stopped, []*Rule{running}, nil)
	e.remember(ctx, []Alert{{ID: "api-errors/api", Rule: "api-errors", Key: "api", Summary: "api errors at 5", FiringSince: base.Add(-5 * time.Hour)}}, base.Add(-4*time.Hour))
	e.remember(ctx, []Alert{{ID: "api-errors/api", Rule: "api-errors", Key: "api", Summary: "api errors at 5", FiringSince: base.Add(2*time.Hour + 10*time.Minute)}}, base.Add(3*time.Hour))
	real, err := e.Replay(ctx, "api-errors", highRule, base.Add(time.Hour), base.Add(6*time.Hour))
	if err != nil || len(real.Actual) != 1 || !real.Actual[0].Fired.Equal(first.Fired) || !real.Actual[0].Resolved.Equal(first.Resolved) {
		t.Errorf("actual = %+v %v", real.Actual, err)
	}

	// Long ranges are checked less often, and say so.
	week, err := e.Replay(ctx, "", highRule, base.Add(6*time.Hour-maxReplayRange), base.Add(6*time.Hour))
	if err != nil || week.Runs > maxReplayRuns+1 || week.Step <= time.Minute || len(week.Warnings) == 0 {
		t.Errorf("week = runs %d step %s warnings %v, %v", week.Runs, week.Step, week.Warnings, err)
	}
	if day, _ := e.Replay(ctx, "", highRule, base.Add(-18*time.Hour), base.Add(6*time.Hour)); day.Step != 5*time.Minute || day.Runs != 289 {
		t.Errorf("a day is checked every %s in %d runs, want 5m in 289", day.Step, day.Runs)
	}
	if _, err := e.Replay(ctx, "", highRule, base, base.Add(-time.Hour)); err == nil {
		t.Error("replayed backwards")
	}

	// One run at a time.
	e.replaying <- struct{}{}
	if _, err := e.Replay(ctx, "", highRule, base, base); !errors.Is(err, ErrBusy) {
		t.Errorf("second replay: %v", err)
	}
	<-e.replaying

	// hosts() is replayed from the recorded states: nas was down 01:00 to
	// 01:30, recording began at 00:30, and today it's fine.
	list := fakeHosts{host("nas", hoststate.Ready)}
	e.opts.Data.Hosts = &list
	e.opts.Data.HostHistory = pastHosts{start: base.Add(30 * time.Minute), down: base.Add(time.Hour), up: base.Add(90 * time.Minute)}
	rep, err = e.Replay(ctx, "", downRule, base, base.Add(2*time.Hour))
	if err != nil || len(rep.Episodes) != 1 {
		t.Fatalf("replay = %+v %v", rep, err)
	}
	if ep := rep.Episodes[0]; !ep.Fired.Equal(base.Add(time.Hour)) || !ep.Resolved.Equal(base.Add(90*time.Minute)) {
		t.Errorf("episode = %+v", ep)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "30 of 121 runs") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	// Running once now uses the live state, with no warning.
	if now, _ := e.Replay(ctx, "", downRule, e.now(), e.now()); len(now.Warnings) != 0 || len(now.Found) != 0 {
		t.Errorf("run now = %+v", now)
	}
}

const downRule = `
def check(now):
    return [alert(h.name + " down", key = h.name) for h in hosts().values() if h.state == "notready"]
`

// pastHosts recorded nas as down between down and up, from start on.
type pastHosts struct{ start, down, up time.Time }

func (p pastHosts) At(_ context.Context, t time.Time) ([]hosts.Host, bool, error) {
	if t.Before(p.start) {
		return nil, false, nil
	}
	s := hoststate.Ready
	if !t.Before(p.down) && t.Before(p.up) {
		s = hoststate.NotReady
	}
	return []hosts.Host{host("nas", s)}, true, nil
}
