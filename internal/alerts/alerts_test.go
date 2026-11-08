package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/alerts/notify"
	"github.com/tpural/tremolo/internal/cluster"
	"github.com/tpural/tremolo/internal/hosts"
	"github.com/tpural/tremolo/internal/hoststate"
)

type fakeHosts []hosts.Host

func (f fakeHosts) List(context.Context) ([]hosts.Host, error) { return f, nil }

func host(name string, s hoststate.State) hosts.Host {
	return hosts.Host{Node: cluster.Node{Name: name, State: s}}
}

func write(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rule(t *testing.T, rules []*Rule, name string) *Rule {
	t.Helper()
	for _, r := range rules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no rule %s", name)
	return nil
}

// victoria answers metrics queries from a map and records log inserts.
type victoria struct {
	mu       sync.Mutex
	metrics  map[string]string
	logRows  string
	inserted []map[string]string
}

func (v *victoria) serve(t *testing.T) *url.URL {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v.mu.Lock()
		defer v.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/query":
			q := r.URL.Query().Get("query")
			for match, result := range v.metrics {
				if strings.Contains(q, match) {
					_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":`+result+`}}`)
					return
				}
			}
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		case "/select/logsql/query":
			_, _ = io.WriteString(w, v.logRows)
		case "/insert/jsonline":
			dec := json.NewDecoder(r.Body)
			for {
				var row map[string]string
				if dec.Decode(&row) != nil {
					break
				}
				v.inserted = append(v.inserted, row)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func TestLoadRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "mine.star", `
load("lib/util.star", "double")
EVERY = "30s"
FOR = "2m"
def check(now):
    return alert("x", value = double(2))
`)
	write(t, dir, "lib/util.star", "def double(n):\n    return n * 2\n")
	write(t, dir, "crash-loop.star", "def check(now):\n    return ok()\n")
	rules, err := LoadRules(dir, []string{"disk-full"})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, r := range rules {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "crash-loop,host-down,mine" {
		t.Fatalf("rules = %v", names)
	}
	mine := rule(t, rules, "mine")
	if mine.Every != 30*time.Second || mine.For != 2*time.Minute || mine.Builtin || !rule(t, rules, "host-down").Builtin || rule(t, rules, "crash-loop").Builtin {
		t.Errorf("rules = %+v", rules)
	}
	found, err := mine.Run(context.Background(), &Env{Now: time.Now()})
	if err != nil || len(found) != 1 || found[0].Labels["value"] != "4" || found[0].Severity != "warn" {
		t.Fatalf("found = %+v, %v", found, err)
	}

	bad := map[string]string{
		"nocheck.star": "X = 1\n",
		"every.star":   "EVERY = \"1s\"\ndef check(now):\n    return ok()\n",
		"syntax.star":  "def check(now)\n",
		"escape.star":  "load(\"../x.star\", \"y\")\ndef check(now):\n    return ok()\n",
	}
	for name, src := range bad {
		d := t.TempDir()
		write(t, d, name, src)
		if _, err := LoadRules(d, nil); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestRunChecksResults(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"dupe":     `return [alert("a"), alert("b")]`,
		"severity": `return alert("a", severity = "loud")`,
		"type":     `return 3`,
		"loop":     "while True:\n        pass",
		"data":     `return [alert(str(metric("up")))]`,
	}
	for name, body := range cases {
		write(t, dir, name+".star", "def check(now):\n    "+body+"\n")
	}
	rules, _ := LoadRules(dir, nil)
	for name := range cases {
		_, err := rule(t, rules, name).Run(context.Background(), &Env{Now: time.Now()})
		if err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestNow(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "hours.star", `
def check(now):
    if now.weekday() < 5 and now.hour in range(10, 17):
        return alert("work hours", severity = "page", day = now.date)
    return alert("off hours", severity = "info")
`)
	rules, err := LoadRules(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rule(t, rules, "hours")
	chicago, _ := time.LoadLocation("America/Chicago")
	for when, want := range map[time.Time]string{
		time.Date(2026, 11, 9, 11, 0, 0, 0, chicago):  "work hours", // Monday
		time.Date(2026, 11, 8, 11, 0, 0, 0, chicago):  "off hours",  // Sunday
		time.Date(2026, 11, 10, 18, 0, 0, 0, chicago): "off hours",
	} {
		found, err := r.Run(context.Background(), &Env{Now: when})
		if err != nil || len(found) != 1 || found[0].Summary != want {
			t.Errorf("%s: %+v, %v", when, found, err)
		}
	}
}

func TestBuiltins(t *testing.T) {
	v := &victoria{metrics: map[string]string{
		"k8s_container_restarts": `[{"metric":{"k8s_namespace_name":"web","k8s_pod_name":"api-1"},"value":[1,"4"]}]`,
		"> 0.9":                  `[{"metric":{"host_name":"nas","mountpoint":"/data"},"value":[1,"0.95"]}]`,
		"predict_linear":         `[{"metric":{"host_name":"nas","mountpoint":"/data"},"value":[1,"1"]},{"metric":{"host_name":"pi","mountpoint":"/"},"value":[1,"1"]}]`,
	}}
	u := v.serve(t)
	data := &Data{MetricsURL: u, LogsURL: u, Client: http.DefaultClient, Hosts: fakeHosts{
		host("ok", hoststate.Ready), host("broken", hoststate.NotReady), host("gone", hoststate.Unreachable),
		host("gpu", hoststate.Asleep), host("pi", hoststate.Off), host("fixing", hoststate.Maintenance),
	}}
	rules, err := LoadRules("", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"host-down":  {"broken is not ready", "gone stopped sending metrics"},
		"crash-loop": {"web/api-1 restarted 4 times in 30 minutes"},
		"disk-full":  {"nas /data is 95% full", "pi / will be full within a day"},
	}
	for name, summaries := range want {
		found, err := rule(t, rules, name).Run(context.Background(), &Env{Data: data, Now: time.Now()})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got []string
		for _, f := range found {
			got = append(got, f.Summary)
		}
		slices.Sort(got)
		if strings.Join(got, "|") != strings.Join(summaries, "|") {
			t.Errorf("%s = %v, want %v", name, got, summaries)
		}
	}
}

// hookServer collects what webhook channels receive, by channel path.
type hookServer struct {
	mu   sync.Mutex
	got  []string
	fail map[string]bool
}

func (h *hookServer) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n notify.Notification
		_ = json.NewDecoder(r.Body).Decode(&n)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.fail[r.URL.Path] {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		h.got = append(h.got, strings.TrimPrefix(r.URL.Path, "/")+":"+n.Event+":"+n.Key)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (h *hookServer) take() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	slices.Sort(h.got)
	out := strings.Join(h.got, ",")
	h.got = nil
	return out
}

func engine(t *testing.T, data *Data, config string) *Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	write(t, filepath.Dir(path), "config.yaml", config)
	cfg, err := LoadConfig(path, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{Config: cfg, Data: data, DataDir: t.TempDir(), Client: http.DefaultClient, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEngine(t *testing.T) {
	hooks := &hookServer{}
	url := hooks.serve(t)
	v := &victoria{}
	u := v.serve(t)
	list := fakeHosts{host("a", hoststate.NotReady), host("b", hoststate.NotReady)}
	data := &Data{MetricsURL: u, LogsURL: u, Client: http.DefaultClient, Hosts: &list}
	dir := t.TempDir()
	write(t, dir, "down.star", `
FOR = "5m"
def check(now):
    return [alert(h.name + " down", key = h.name) for h in hosts().values() if h.state == "notready"]
`)
	rules, err := LoadRules(dir, []string{"host-down", "crash-loop", "disk-full"})
	if err != nil {
		t.Fatal(err)
	}
	e := engine(t, data, `
timezone: UTC
repeat: 1h
url: http://tremolo
notify: {warn: hook}
channels:
  hook: {type: webhook, url: "`+url+`/hook"}
`)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no background loops; Eval is driven by hand
	e.SetRules(ctx, rules, nil)
	r := rules[0]
	eval := func(d time.Duration) {
		now = now.Add(d)
		e.Eval(context.Background(), r)
	}
	states := func() string {
		var out []string
		for _, a := range e.Snapshot().Alerts {
			s := a.State
			if a.Silenced {
				s += "(silenced)"
			}
			out = append(out, a.Key+"="+s)
		}
		return strings.Join(out, ",")
	}

	eval(0)
	if s := states(); s != "a=pending,b=pending" || hooks.take() != "" {
		t.Fatalf("start: %s", s)
	}
	if _, err := e.Silence("down", "b", now.Add(time.Hour), "known"); err != nil {
		t.Fatal(err)
	}
	eval(5 * time.Minute)
	if s, n := states(), hooks.take(); s != "a=firing,b=firing(silenced)" || n != "hook:firing:a" {
		t.Fatalf("firing: %s %v", s, n)
	}
	eval(time.Minute)
	if n := hooks.take(); n != "" {
		t.Fatalf("sent again too soon: %v", n)
	}
	// The silence ends: b is sent now; a repeats after an hour.
	eval(time.Hour)
	if n := hooks.take(); n != "hook:firing:b,hook:repeat:a" {
		t.Fatalf("after silence: %v", n)
	}
	list = fakeHosts{host("a", hoststate.Ready), host("b", hoststate.NotReady)}
	eval(time.Minute)
	if s, n := states(), hooks.take(); s != "b=firing" || n != "hook:resolved:a" {
		t.Fatalf("resolved: %s %v", s, n)
	}

	// A restart keeps firing alerts and doesn't send them again.
	e2, err := New(Options{Config: e.opts.Config, Data: data, DataDir: e.opts.DataDir, Client: http.DefaultClient, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	e2.now = e.now
	e2.SetRules(ctx, rules, nil)
	e2.Eval(context.Background(), r)
	if n := hooks.take(); n != "" {
		t.Fatalf("restart re-sent: %v", n)
	}

	// A failing rule keeps its alerts.
	write(t, dir, "down.star", "def check(now):\n    fail(\"broken\")\n")
	broken, _ := LoadRules(dir, []string{"host-down", "crash-loop", "disk-full"})
	e2.SetRules(ctx, broken, nil)
	e2.Eval(context.Background(), broken[0])
	snap := e2.Snapshot()
	if len(snap.Alerts) != 1 || !strings.Contains(snap.Rules[0].Error, "broken") {
		t.Fatalf("failing rule: %+v %+v", snap.Alerts, snap.Rules[0])
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	events := []string{}
	for _, row := range v.inserted {
		events = append(events, row["event"]+":"+row["key"])
	}
	slices.Sort(events)
	if strings.Join(events, ",") != "firing:a,firing:b,resolved:a" {
		t.Errorf("history = %v", events)
	}
}

func TestEscalationAndAck(t *testing.T) {
	hooks := &hookServer{fail: map[string]bool{}}
	url := hooks.serve(t)
	list := fakeHosts{host("a", hoststate.NotReady)}
	data := &Data{Client: http.DefaultClient, Hosts: &list}
	dir := t.TempDir()
	write(t, dir, "down.star", `
def check(now):
    return [alert(h.name + " down", key = h.name, severity = "page") for h in hosts().values() if h.state == "notready"]
`)
	rules, err := LoadRules(dir, []string{"host-down", "crash-loop", "disk-full"})
	if err != nil {
		t.Fatal(err)
	}
	e := engine(t, data, `
repeat: 1h
url: http://tremolo/
notify: {page: [oncall, log]}
channels:
  email: {type: webhook, url: "`+url+`/email"}
  slack: {type: webhook, url: "`+url+`/slack"}
  sms: {type: webhook, url: "`+url+`/sms"}
  log: {type: webhook, url: "`+url+`/log"}
escalations:
  oncall:
    - notify: email
    - after: 15m
      notify: slack
    - after: 30m
      notify: [sms]
`)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.SetRules(ctx, rules, nil)
	eval := func(d time.Duration) string {
		now = now.Add(d)
		e.Eval(context.Background(), rules[0])
		return hooks.take()
	}

	if n := eval(0); n != "email:firing:a,log:firing:a" {
		t.Fatalf("step 1: %s", n)
	}
	// Slack is down when its turn comes, so it is tried again next run.
	hooks.mu.Lock()
	hooks.fail["/slack"] = true
	hooks.mu.Unlock()
	if n := eval(15 * time.Minute); n != "" {
		t.Fatalf("step 2 while slack is down: %s", n)
	}
	if w := e.Snapshot().Rules[0].Warning; !strings.Contains(w, "slack") {
		t.Errorf("warning = %q", w)
	}
	hooks.mu.Lock()
	hooks.fail["/slack"] = false
	hooks.mu.Unlock()
	if n := eval(time.Minute); n != "slack:firing:a" {
		t.Fatalf("step 2: %s", n)
	}
	a := e.Snapshot().Alerts[0]
	if len(a.Plan) != 4 || a.Plan[3].Channel != "sms" || a.Plan[3].After != 30*time.Minute || a.AckToken == "" {
		t.Fatalf("plan = %+v", a)
	}

	// Acknowledging by the link's token stops the chain and tells who was told.
	if _, err := e.Ack(context.Background(), "nope", ""); !errors.Is(err, ErrNoAlert) {
		t.Fatalf("ack unknown: %v", err)
	}
	acked, err := e.Ack(context.Background(), a.AckToken, "tim")
	if err != nil || acked.AckedBy != "tim" {
		t.Fatalf("ack: %+v %v", acked, err)
	}
	waitFor(t, func() bool {
		hooks.mu.Lock()
		defer hooks.mu.Unlock()
		return len(hooks.got) == 3
	})
	if n := hooks.take(); n != "email:acknowledged:a,log:acknowledged:a,slack:acknowledged:a" {
		t.Fatalf("ack sent: %s", n)
	}
	if n := eval(2 * time.Hour); n != "" {
		t.Fatalf("acknowledged alert escalated or repeated: %s", n)
	}
	list = fakeHosts{host("a", hoststate.Ready)}
	if n := eval(time.Minute); n != "email:resolved:a,log:resolved:a,slack:resolved:a" {
		t.Fatalf("resolved: %s", n)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestConfig(t *testing.T) {
	bad := map[string]string{
		"unknown type":      "channels: {x: {type: pager}}",
		"missing field":     "channels: {x: {type: slack}}",
		"typo":              "channels: {x: {type: slack, url: u, chanel: c}}",
		"unknown target":    "notify: {page: nobody}",
		"step channel":      "escalations: {e: [{notify: x}]}",
		"name clash":        "channels: {x: {type: webhook, url: u}}\nescalations: {x: [{notify: x}]}",
		"severity":          "notify: {loud: x}",
		"bad zone":          "timezone: Mars/Olympus",
		"notify not a list": "channels: {x: {type: webhook, url: u}}\nnotify: {page: {a: b}}",
	}
	for name, src := range bad {
		dir := t.TempDir()
		write(t, dir, "c.yaml", src)
		if _, err := LoadConfig(filepath.Join(dir, "c.yaml"), http.DefaultClient); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	t.Setenv("HOOK_TOKEN", "secret")
	dir := t.TempDir()
	write(t, dir, "c.yaml", "channels: {x: {type: webhook, url: u, token: ${HOOK_TOKEN}}}\nnotify: {page: x, warn: [x]}")
	c, err := LoadConfig(filepath.Join(dir, "c.yaml"), http.DefaultClient)
	if err != nil || len(c.Notify["page"]) != 1 || len(c.Notify["warn"]) != 1 || c.Repeat.Duration != 4*time.Hour {
		t.Fatalf("config = %+v, %v", c, err)
	}
}
