package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type alert struct {
	ID          string            `json:"id"`
	Rule        string            `json:"rule"`
	Key         string            `json:"key"`
	Summary     string            `json:"summary"`
	Severity    string            `json:"severity"`
	State       string            `json:"state"`
	Since       time.Time         `json:"since"`
	FiringSince time.Time         `json:"firingSince"`
	Labels      map[string]string `json:"labels"`
	Silenced    bool              `json:"silenced"`
	Maintenance string            `json:"maintenance"`
	Timeline    []struct {
		Time time.Time `json:"time"`
		Kind string    `json:"kind"`
		By   string    `json:"by"`
		Note string    `json:"note"`
	} `json:"timeline"`
}

type snapshot struct {
	Alerts   []alert             `json:"alerts"`
	Runbooks map[string][]string `json:"runbooks"`
}

type host struct {
	Name     string    `json:"name"`
	State    string    `json:"state"`
	Reason   string    `json:"reason"`
	Since    time.Time `json:"since"`
	Kind     string    `json:"kind"`
	Roles    []string  `json:"roles"`
	Arch     string    `json:"arch"`
	CPU      int64     `json:"cpu"`
	Memory   int64     `json:"memoryBytes"`
	LastSeen time.Time `json:"lastSeen"`
	StateBy  string    `json:"stateBy"`
}

type investigation struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Alert     string    `json:"alert"`
	Rule      string    `json:"rule"`
	Title     string    `json:"title"`
	Severity  string    `json:"severity"`
	Status    string    `json:"status"`
	ClaimedBy string    `json:"claimedBy"`
	ClaimExp  time.Time `json:"claimExpires"`
	Opened    time.Time `json:"opened"`
	Closed    time.Time `json:"closed"`
	Summary   string    `json:"summary"`
	Fix       string    `json:"fix"`
	Runbook   string    `json:"runbook"`
	Outcome   string    `json:"outcome"`
	Previous  int64     `json:"previous"`
	Events    []struct {
		ID         int64     `json:"id"`
		Time       time.Time `json:"time"`
		By         string    `json:"by"`
		Kind       string    `json:"kind"`
		Text       string    `json:"text"`
		Action     string    `json:"action"`
		Target     string    `json:"target"`
		Disruptive bool      `json:"disruptive"`
		Result     string    `json:"result"`
	} `json:"events"`
}

func (i investigation) line() string {
	s := fmt.Sprintf("#%d [%s] %s (%s", i.ID, i.Status, i.Title, i.Kind)
	if i.Rule != "" {
		s += ", rule " + i.Rule
	}
	s += ", opened " + ago(i.Opened) + ")"
	if i.ClaimedBy != "" && i.ClaimExp.After(time.Now()) {
		s += " held by " + i.ClaimedBy
	}
	if i.Fix != "" {
		s += " fix: " + line(i.Fix, 160)
	}
	if i.Outcome != "" {
		s += " outcome: " + i.Outcome
	}
	return s
}

type runbook struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Rules     []string  `json:"rules"`
	Version   int       `json:"version"`
	Updated   time.Time `json:"updated"`
	UpdatedBy string    `json:"updatedBy"`
	Fixes     []struct {
		Key      string `json:"key"`
		Text     string `json:"text"`
		Held     int    `json:"held"`
		Recurred int    `json:"recurred"`
	} `json:"fixes"`
}

type pod struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	Owner      string `json:"owner"`
	Ready      int    `json:"ready"`
	Containers int    `json:"containers"`
	Restarts   int    `json:"restarts"`
	Reason     string `json:"reason"`
}

// Inputs. Descriptions are what a model sees.
type (
	none    struct{}
	idInput struct {
		ID int64 `json:"id" jsonschema:"the investigation's number"`
	}
	alertInput struct {
		ID string `json:"id" jsonschema:"the alert id, rule/key, as shown by alerts"`
	}
	hostInput struct {
		Name string `json:"name" jsonschema:"the host's name"`
	}
	metricsInput struct {
		Query string `json:"query" jsonschema:"a MetricsQL/PromQL query, e.g. max by (k8s_node_name) (system_filesystem_utilization)"`
		Start string `json:"start,omitempty" jsonschema:"for a range: how far back, e.g. 1h or 6h; leave empty for the current value"`
		Step  string `json:"step,omitempty" jsonschema:"for a range: resolution, e.g. 5m"`
	}
	namesInput struct {
		Contains string `json:"contains,omitempty" jsonschema:"only names containing this, e.g. memory"`
	}
	logsInput struct {
		Query string `json:"query" jsonschema:"a LogsQL query, e.g. k8s.namespace.name:=gridtie error"`
		Start string `json:"start,omitempty" jsonschema:"how far back, e.g. 15m or 1h (default 1h)"`
		Limit int    `json:"limit,omitempty" jsonschema:"most lines, up to 200 (default 50)"`
	}
	fieldsInput struct {
		Query string `json:"query" jsonschema:"a LogsQL query whose logs' fields to list, e.g. k8s.namespace.name:=gridtie"`
		Start string `json:"start,omitempty" jsonschema:"how far back (default 1h)"`
	}
	listInput struct {
		Status string `json:"status,omitempty" jsonschema:"open (default: open and escalated), done, or all"`
	}
	recordInput struct {
		ID   int64  `json:"id" jsonschema:"the investigation's number"`
		Kind string `json:"kind" jsonschema:"finding, query, note or change"`
		Text string `json:"text" jsonschema:"what you found or did, briefly; for a query include the query and what it showed"`
	}
	actionInput struct {
		ID         int64  `json:"id" jsonschema:"the investigation's number"`
		Action     string `json:"action" jsonschema:"what you'll do, e.g. rollout restart, delete pod, cordon"`
		Target     string `json:"target" jsonschema:"what it's on, e.g. deploy/gridtie or node/mm02"`
		Host       string `json:"host,omitempty" jsonschema:"the host it touches, if any"`
		Namespace  string `json:"namespace,omitempty" jsonschema:"the namespace it touches, if any"`
		Disruptive bool   `json:"disruptive" jsonschema:"true if it interrupts service: restarts, reboots, drains, deletes"`
		Why        string `json:"why" jsonschema:"why this should help"`
	}
	endInput struct {
		ID     int64  `json:"id" jsonschema:"the investigation's number"`
		Event  int64  `json:"event" jsonschema:"the action's number from begin_action"`
		Result string `json:"result" jsonschema:"how it went"`
	}
	finishInput struct {
		ID      int64  `json:"id" jsonschema:"the investigation's number"`
		Status  string `json:"status" jsonschema:"fixed or abandoned"`
		Fix     string `json:"fix,omitempty" jsonschema:"for fixed: what fixed it, in one line"`
		Summary string `json:"summary,omitempty" jsonschema:"the cause and anything the next agent should know"`
		Runbook string `json:"runbook,omitempty" jsonschema:"the runbook id this fix belongs in, if any"`
	}
	escalateInput struct {
		ID  int64  `json:"id" jsonschema:"the investigation's number"`
		Why string `json:"why" jsonschema:"what you tried, what you found, and why a person is needed"`
	}
	openInput struct {
		Kind  string `json:"kind" jsonschema:"gap (something tremolo doesn't observe), tuning, or runbook-review"`
		Title string `json:"title" jsonschema:"one line"`
		Text  string `json:"text,omitempty" jsonschema:"details and evidence"`
	}
	silenceInput struct {
		Rule  string  `json:"rule" jsonschema:"the alert's rule"`
		Key   string  `json:"key,omitempty" jsonschema:"one key of the rule, or empty for all"`
		Hours float64 `json:"hours" jsonschema:"how long, up to 24"`
		Note  string  `json:"note" jsonschema:"why"`
	}
	runbooksInput struct {
		Search string `json:"search,omitempty" jsonschema:"words in the title, body or rules"`
	}
	runbookInput struct {
		ID string `json:"id" jsonschema:"the runbook's id"`
	}
	ruleInput struct {
		Name string `json:"name" jsonschema:"the rule's name"`
	}
	saveRuleInput struct {
		Name   string `json:"name" jsonschema:"the rule's name, lowercase with dashes"`
		Source string `json:"source" jsonschema:"the whole rule in Starlark: a check(now) function returning alerts"`
		Note   string `json:"note" jsonschema:"why, for the rule's history"`
	}
	editInput struct {
		ID      string   `json:"id,omitempty" jsonschema:"the runbook's id; empty to create one"`
		Version int      `json:"version,omitempty" jsonschema:"the version you read, from runbook; required to edit"`
		Title   string   `json:"title" jsonschema:"the title"`
		Body    string   `json:"body" jsonschema:"the whole runbook in Markdown"`
		Rules   []string `json:"rules,omitempty" jsonschema:"alert rules it covers"`
	}
)

func tool(name, desc string, readOnly bool) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}}
}

func (t *tools) register(s *mcp.Server) {
	mcp.AddTool(s, tool("overview", "What's firing, which hosts aren't ready, what agents are working on, and what you may do. Start here.", true), t.overview)
	mcp.AddTool(s, tool("alerts", "Alerts firing and pending now.", true), t.alerts)
	mcp.AddTool(s, tool("alert", "One alert: labels, timeline, its runbooks, and how it was handled the last times.", true), t.alert)
	mcp.AddTool(s, tool("hosts", "Every host and its state.", true), t.hosts)
	mcp.AddTool(s, tool("host", "One host: state, size, and its pods with any not running first.", true), t.host)
	mcp.AddTool(s, tool("metrics", "Run a metrics query. With start, summarises each series over the range; without, gives current values.", true), t.metrics)
	mcp.AddTool(s, tool("metric_names", "Metric names, to find what to query.", true), t.metricNames)
	mcp.AddTool(s, tool("logs", "Search logs with LogsQL, newest first.", true), t.logs)
	mcp.AddTool(s, tool("log_fields", "Fields in logs matching a query, with how many logs have each.", true), t.logFields)
	mcp.AddTool(s, tool("investigations", "Agents' work: alerts to fix, gaps, tuning, runbook reviews.", true), t.investigations)
	mcp.AddTool(s, tool("investigate", "Claim an investigation and read its timeline. Claim before working on it.", false), t.investigate)
	mcp.AddTool(s, tool("record", "Add a finding, query, note or change to an investigation you hold.", false), t.record)
	mcp.AddTool(s, tool("begin_action", "Ask before any change outside tremolo. Go ahead only if this says so.", false), t.beginAction)
	mcp.AddTool(s, tool("end_action", "Say how an action went.", false), t.endAction)
	mcp.AddTool(s, tool("finish", "Close an investigation: fixed (say what fixed it) or abandoned.", false), t.finish)
	mcp.AddTool(s, tool("escalate", "Hand an investigation to a person, with what you tried.", false), t.escalate)
	mcp.AddTool(s, tool("open_investigation", "Start work that isn't an alert: a gap in what tremolo observes, tuning, or a runbook review.", false), t.open)
	mcp.AddTool(s, tool("silence", "Silence an alert's notifications for a while.", false), t.silence)
	mcp.AddTool(s, tool("runbooks", "List runbooks, optionally matching words.", true), t.runbooks)
	mcp.AddTool(s, tool("runbook", "Read a runbook, with which fixes have worked.", true), t.runbook)
	mcp.AddTool(s, tool("edit_runbook", "Create a runbook, or replace one's text (give the version you read).", false), t.editRunbook)
	mcp.AddTool(s, tool("coverage", "Gaps in what tremolo observes: hosts or namespaces without logs or metrics, quiet senders, rules without runbooks.", true), t.coverage)
	mcp.AddTool(s, tool("rule", "An alert rule's Starlark source and last run.", true), t.rule)
	mcp.AddTool(s, tool("save_rule", "Add or change an alert rule. tremolo replays it over the last week and refuses it if it fails or would fire too often.", false), t.saveRule)
}

func (t *tools) overview(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	var b strings.Builder
	var me struct {
		Name  string `json:"name"`
		Role  string `json:"role"`
		Token *struct {
			Name  string `json:"name"`
			Scope *struct {
				Metrics, Logs, Traces, Allow []string
				MaxRange                     int64 `json:"maxRange"`
			} `json:"scope"`
		} `json:"token"`
		Limits struct {
			MaxRange   int64 `json:"maxRange"`
			MaxLogRows int   `json:"maxLogRows"`
		} `json:"limits"`
	}
	if err := t.get(ctx, req, "/api/me", nil, &me); err != nil {
		return fail(err)
	}
	who := me.Name
	if me.Token != nil {
		who = me.Token.Name + " (for " + me.Name + ")"
	}
	fmt.Fprintf(&b, "You are %s.", who)
	if me.Token != nil && me.Token.Scope != nil {
		sc := me.Token.Scope
		fmt.Fprintf(&b, " You may read metrics %s, logs %s, traces %s; and: %s.", reach(sc.Metrics), reach(sc.Logs), reach(sc.Traces), strings.Join(sc.Allow, ", "))
	}
	if me.Limits.MaxRange > 0 {
		fmt.Fprintf(&b, " Queries may look back %s; logs return at most %d lines.", time.Duration(me.Limits.MaxRange)*time.Second, me.Limits.MaxLogRows)
	}
	b.WriteString("\n\n")
	var snap snapshot
	if err := t.get(ctx, req, "/api/alerts", nil, &snap); err == nil {
		firing := slices.DeleteFunc(slices.Clone(snap.Alerts), func(a alert) bool { return a.State != "firing" })
		fmt.Fprintf(&b, "Alerts: %d firing, %d pending.\n", len(firing), len(snap.Alerts)-len(firing))
		for _, a := range firing[:min(len(firing), 15)] {
			fmt.Fprintf(&b, "- %s [%s] %s, since %s\n", a.ID, a.Severity, line(a.Summary, 140), ago(a.FiringSince))
		}
	} else {
		fmt.Fprintf(&b, "Alerts: %v\n", err)
	}
	var hs []host
	if err := t.get(ctx, req, "/api/hosts", nil, &hs); err == nil {
		var odd []string
		for _, h := range hs {
			if h.State != "ready" {
				odd = append(odd, fmt.Sprintf("%s %s", h.Name, h.State))
			}
		}
		fmt.Fprintf(&b, "\nHosts: %d, %d not ready", len(hs), len(odd))
		if len(odd) > 0 {
			b.WriteString(": " + strings.Join(odd, ", "))
		}
		b.WriteString(" (asleep and off are deliberate: possum powers idle nodes down).\n")
	}
	var list []investigation
	if err := t.get(ctx, req, "/api/investigations", url.Values{"status": {"open,escalated"}, "limit": {"20"}}, &list); err == nil {
		fmt.Fprintf(&b, "\nOpen investigations: %d\n", len(list))
		for _, i := range list {
			b.WriteString("- " + i.line() + "\n")
		}
	}
	return ok(b.String())
}

func reach(sel []string) string {
	switch {
	case len(sel) == 0:
		return "none"
	case slices.Contains(sel, "*"):
		return "all"
	}
	return "matching " + strings.Join(sel, " or ")
}

func (t *tools) alerts(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	var snap snapshot
	if err := t.get(ctx, req, "/api/alerts", nil, &snap); err != nil {
		return fail(err)
	}
	if len(snap.Alerts) == 0 {
		return ok("Nothing firing or pending.")
	}
	var b strings.Builder
	for _, a := range snap.Alerts {
		fmt.Fprintf(&b, "%s [%s %s] %s, since %s", a.ID, a.State, a.Severity, line(a.Summary, 160), ago(cmpTime(a.FiringSince, a.Since)))
		if a.Silenced {
			b.WriteString(" (silenced)")
		}
		if a.Maintenance != "" {
			b.WriteString(" (held by maintenance " + a.Maintenance + ")")
		}
		if l := labels(a.Labels); l != "" {
			b.WriteString(" {" + l + "}")
		}
		b.WriteString("\n")
	}
	return ok(b.String())
}

func cmpTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	return a
}

func (t *tools) alert(ctx context.Context, req *mcp.CallToolRequest, in alertInput) (*mcp.CallToolResult, any, error) {
	var snap snapshot
	if err := t.get(ctx, req, "/api/alerts", nil, &snap); err != nil {
		return fail(err)
	}
	i := slices.IndexFunc(snap.Alerts, func(a alert) bool { return a.ID == in.ID })
	if i < 0 {
		return fail(fmt.Errorf("no alert %q is firing or pending; it may have resolved. Call alerts for the list", in.ID))
	}
	a := snap.Alerts[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s %s]\n%s\nSince %s. Labels: %s\n", a.ID, a.State, a.Severity, a.Summary, ago(cmpTime(a.FiringSince, a.Since)), labels(a.Labels))
	if ids := snap.Runbooks[a.Rule]; len(ids) > 0 {
		fmt.Fprintf(&b, "Runbooks: %s (read with runbook)\n", strings.Join(ids, ", "))
	} else {
		b.WriteString("No runbook covers this rule yet; write one with edit_runbook once it's fixed.\n")
	}
	tl := a.Timeline[max(0, len(a.Timeline)-12):]
	if len(tl) > 0 {
		b.WriteString("\nTimeline:\n")
		for _, e := range tl {
			fmt.Fprintf(&b, "- %s %s %s %s\n", ago(e.Time), e.Kind, e.By, line(e.Note, 160))
		}
	}
	var past []investigation
	if err := t.get(ctx, req, "/api/investigations", url.Values{"rule": {a.Rule}, "limit": {"5"}}, &past); err == nil && len(past) > 0 {
		b.WriteString("\nInvestigations of this rule, newest first:\n")
		for _, p := range past {
			b.WriteString("- " + p.line() + "\n")
		}
	}
	return ok(b.String())
}

func (t *tools) hosts(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	var hs []host
	if err := t.get(ctx, req, "/api/hosts", nil, &hs); err != nil {
		return fail(err)
	}
	var b strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&b, "%s %s (%s", h.Name, h.State, h.Kind)
		if len(h.Roles) > 0 {
			b.WriteString(", " + strings.Join(h.Roles, "/"))
		}
		fmt.Fprintf(&b, ", %s, %d cores, %s memory)", h.Arch, h.CPU, num(float64(h.Memory)))
		if h.Reason != "" {
			b.WriteString(" " + line(h.Reason, 100))
		}
		b.WriteString("\n")
	}
	return ok(b.String())
}

func (t *tools) host(ctx context.Context, req *mcp.CallToolRequest, in hostInput) (*mcp.CallToolResult, any, error) {
	var hs []host
	if err := t.get(ctx, req, "/api/hosts", nil, &hs); err != nil {
		return fail(err)
	}
	i := slices.IndexFunc(hs, func(h host) bool { return h.Name == in.Name })
	if i < 0 {
		return fail(fmt.Errorf("no host %q; call hosts for the names", in.Name))
	}
	h := hs[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s is %s since %s (decided by %s)", h.Name, h.State, ago(h.Since), h.StateBy)
	if h.Reason != "" {
		b.WriteString(": " + h.Reason)
	}
	fmt.Fprintf(&b, "\n%s %s, roles %v, %s, %d cores, %s memory, metrics last seen %s\n", h.Kind, h.Name, h.Roles, h.Arch, h.CPU, num(float64(h.Memory)), ago(h.LastSeen))
	var pods []pod
	if err := t.get(ctx, req, "/api/hosts/"+url.PathEscape(h.Name)+"/pods", nil, &pods); err != nil {
		fmt.Fprintf(&b, "\nPods: %v\n", err)
	} else {
		slices.SortStableFunc(pods, func(a, b pod) int {
			return btoi(a.Phase == "Running" && a.Reason == "") - btoi(b.Phase == "Running" && b.Reason == "")
		})
		fmt.Fprintf(&b, "\nPods (%d):\n", len(pods))
		for _, p := range pods[:min(len(pods), 40)] {
			fmt.Fprintf(&b, "- %s/%s (%s) %s ready %d/%d restarts %d %s\n", p.Namespace, p.Name, p.Owner, p.Phase, p.Ready, p.Containers, p.Restarts, p.Reason)
		}
	}
	return ok(b.String())
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (t *tools) metrics(ctx context.Context, req *mcp.CallToolRequest, in metricsInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return fail(fmt.Errorf("give a query"))
	}
	q := url.Values{"query": {in.Query}}
	path := "/api/metrics/query"
	if in.Start != "" {
		path = "/api/metrics/query_range"
		q.Set("start", "-"+strings.TrimPrefix(in.Start, "-"))
		q.Set("step", or(in.Step, "5m"))
	}
	var res struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
				Values [][]any           `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := t.get(ctx, req, path, q, &res); err != nil {
		return fail(err)
	}
	rs := res.Data.Result
	if len(rs) == 0 {
		return ok("No data. Check the metric name with metric_names, and the labels; the series may not exist in that range.")
	}
	var b strings.Builder
	n := min(len(rs), 30)
	if len(rs) > n {
		fmt.Fprintf(&b, "%d series; showing %d. Aggregate (sum by, topk) to see fewer.\n", len(rs), n)
	}
	for _, r := range rs[:n] {
		name := labels(r.Metric)
		if r.Values == nil {
			fmt.Fprintf(&b, "%s = %s\n", or(name, "{}"), num(value(r.Value)))
			continue
		}
		vs := make([]float64, 0, len(r.Values))
		for _, p := range r.Values {
			vs = append(vs, value(p))
		}
		lo, hi := slices.Min(vs), slices.Max(vs)
		var sum float64
		for _, v := range vs {
			sum += v
		}
		first, last := vs[0], vs[len(vs)-1]
		trend := "steady"
		if d := last - first; hi != lo && d > (hi-lo)/3 {
			trend = "rising"
		} else if hi != lo && -d > (hi-lo)/3 {
			trend = "falling"
		}
		fmt.Fprintf(&b, "%s: last %s, min %s, max %s, avg %s, %s (%d points)\n", or(name, "{}"), num(last), num(lo), num(hi), num(sum/float64(len(vs))), trend, len(vs))
	}
	return ok(b.String())
}

func or(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func value(p []any) float64 {
	if len(p) < 2 {
		return 0
	}
	s, _ := p[1].(string)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func (t *tools) metricNames(ctx context.Context, req *mcp.CallToolRequest, in namesInput) (*mcp.CallToolResult, any, error) {
	var res struct{ Data []string }
	if err := t.get(ctx, req, "/api/metrics/label/__name__/values", url.Values{"start": {"-1h"}}, &res); err != nil {
		return fail(err)
	}
	names := slices.DeleteFunc(res.Data, func(n string) bool { return in.Contains != "" && !strings.Contains(n, in.Contains) })
	if len(names) == 0 {
		return ok("No metric names match.")
	}
	more := ""
	if len(names) > 150 {
		more = fmt.Sprintf("\n… and %d more; pass contains to narrow.", len(names)-150)
		names = names[:150]
	}
	return ok(strings.Join(names, "\n") + more)
}

func (t *tools) logs(ctx context.Context, req *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return fail(fmt.Errorf("give a LogsQL query, e.g. k8s.namespace.name:=gridtie error"))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	q := url.Values{"query": {in.Query}, "start": {"-" + strings.TrimPrefix(or(in.Start, "1h"), "-")}, "limit": {strconv.Itoa(limit)}}
	code, body := t.call(ctx, req, http.MethodGet, "/api/logs/query", q, nil)
	if code != http.StatusOK {
		return fail(apiErr(code, body))
	}
	type entry struct {
		at   time.Time
		text string
	}
	var rows []entry
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		raw, _ := row["_time"].(string)
		at, _ := time.Parse(time.RFC3339Nano, raw)
		ts := at.Format("15:04:05")
		where := ""
		for _, k := range []string{"k8s.namespace.name", "k8s.pod.name", "host.name", "service.name", "app"} {
			if v, ok := row[k].(string); ok && v != "" {
				where += v + " "
			}
		}
		msg, _ := row["_msg"].(string)
		rows = append(rows, entry{at, fmt.Sprintf("%s %s| %s", ts, where, line(msg, 300))})
	}
	n := len(rows)
	slices.SortStableFunc(rows, func(a, b entry) int { return b.at.Compare(a.at) })
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.text + "\n")
	}
	if n == 0 {
		return ok("No logs match. Widen start, or check field names with log_fields.")
	}
	head := fmt.Sprintf("%d lines, newest first", n)
	if n == limit {
		head += " (the limit; there may be more: narrow the query or raise limit up to 200)"
	}
	return ok(head + ":\n" + b.String())
}

func (t *tools) logFields(ctx context.Context, req *mcp.CallToolRequest, in fieldsInput) (*mcp.CallToolResult, any, error) {
	q := url.Values{"query": {or(in.Query, "*")}, "start": {"-" + strings.TrimPrefix(or(in.Start, "1h"), "-")}}
	var res struct {
		Values []struct {
			Value string `json:"value"`
			Hits  int64  `json:"hits"`
		} `json:"values"`
	}
	if err := t.get(ctx, req, "/api/logs/field_names", q, &res); err != nil {
		return fail(err)
	}
	var b strings.Builder
	for _, v := range res.Values[:min(len(res.Values), 80)] {
		fmt.Fprintf(&b, "%s (%d)\n", v.Value, v.Hits)
	}
	return ok(or(b.String(), "No logs match."))
}

func (t *tools) investigations(ctx context.Context, req *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, any, error) {
	status := map[string]string{"": "open,escalated", "open": "open,escalated", "done": "fixed,resolved,abandoned", "all": ""}[in.Status]
	var list []investigation
	if err := t.get(ctx, req, "/api/investigations", url.Values{"status": {status}, "limit": {"30"}}, &list); err != nil {
		return fail(err)
	}
	if len(list) == 0 {
		return ok("None.")
	}
	var b strings.Builder
	for _, i := range list {
		b.WriteString(i.line() + "\n")
	}
	return ok(b.String())
}

func (t *tools) investigate(ctx context.Context, req *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
	var i investigation
	if err := t.send(ctx, req, http.MethodPost, fmt.Sprintf("/api/investigations/%d/claim", in.ID), struct{}{}, &i); err != nil {
		return fail(err)
	}
	var b strings.Builder
	b.WriteString("You hold this for 15 minutes; each record renews it.\n" + i.line() + "\n")
	if i.Alert != "" {
		fmt.Fprintf(&b, "Alert: %s (details with alert)\n", i.Alert)
	}
	if i.Previous > 0 {
		fmt.Fprintf(&b, "The last time this alert fired: investigation #%d\n", i.Previous)
	}
	b.WriteString("\nTimeline:\n")
	for _, e := range i.Events[max(0, len(i.Events)-25):] {
		fmt.Fprintf(&b, "- [%d] %s %s %s", e.ID, ago(e.Time), e.By, e.Kind)
		if e.Action != "" {
			fmt.Fprintf(&b, " %s %s", e.Action, e.Target)
			if e.Kind == "action" {
				b.WriteString(" → " + or(e.Result, "no result yet"))
			}
		}
		if e.Text != "" {
			b.WriteString(": " + line(e.Text, 300))
		}
		b.WriteString("\n")
	}
	return ok(b.String())
}

func (t *tools) record(ctx context.Context, req *mcp.CallToolRequest, in recordInput) (*mcp.CallToolResult, any, error) {
	if err := t.send(ctx, req, http.MethodPost, fmt.Sprintf("/api/investigations/%d/events", in.ID), map[string]string{"kind": in.Kind, "text": in.Text}, nil); err != nil {
		return fail(err)
	}
	return ok("Recorded.")
}

func (t *tools) beginAction(ctx context.Context, req *mcp.CallToolRequest, in actionInput) (*mcp.CallToolResult, any, error) {
	var e struct {
		ID int64 `json:"id"`
	}
	err := t.send(ctx, req, http.MethodPost, fmt.Sprintf("/api/investigations/%d/actions", in.ID), map[string]any{
		"action": in.Action, "target": in.Target, "host": in.Host, "namespace": in.Namespace, "disruptive": in.Disruptive, "why": in.Why,
	}, &e)
	if err != nil {
		return fail(fmt.Errorf("%w. Do not make this change", err))
	}
	return ok(fmt.Sprintf("Go ahead. This is action %d; call end_action with its result when done.", e.ID))
}

func (t *tools) endAction(ctx context.Context, req *mcp.CallToolRequest, in endInput) (*mcp.CallToolResult, any, error) {
	if err := t.send(ctx, req, http.MethodPost, fmt.Sprintf("/api/investigations/%d/actions/%d", in.ID, in.Event), map[string]string{"result": in.Result}, nil); err != nil {
		return fail(err)
	}
	return ok("Recorded.")
}

func (t *tools) finish(ctx context.Context, req *mcp.CallToolRequest, in finishInput) (*mcp.CallToolResult, any, error) {
	var i investigation
	if err := t.send(ctx, req, http.MethodPost, fmt.Sprintf("/api/investigations/%d/close", in.ID), map[string]string{
		"status": in.Status, "fix": in.Fix, "summary": in.Summary, "runbook": in.Runbook,
	}, &i); err != nil {
		return fail(err)
	}
	msg := "Closed as " + i.Status + "."
	if i.Status == "fixed" {
		msg += " tremolo will check whether the fix holds."
		if in.Runbook == "" {
			msg += " If no runbook covers this, write one with edit_runbook."
		}
	}
	return ok(msg)
}

func (t *tools) escalate(ctx context.Context, req *mcp.CallToolRequest, in escalateInput) (*mcp.CallToolResult, any, error) {
	if err := t.send(ctx, req, http.MethodPost, fmt.Sprintf("/api/investigations/%d/escalate", in.ID), map[string]string{"why": in.Why}, nil); err != nil {
		return fail(err)
	}
	return ok("Escalated: people are being told. You may keep recording findings.")
}

func (t *tools) open(ctx context.Context, req *mcp.CallToolRequest, in openInput) (*mcp.CallToolResult, any, error) {
	var i investigation
	if err := t.send(ctx, req, http.MethodPost, "/api/investigations", map[string]string{"kind": in.Kind, "title": in.Title, "text": in.Text}, &i); err != nil {
		return fail(err)
	}
	return ok(fmt.Sprintf("Opened investigation #%d; you hold it.", i.ID))
}

func (t *tools) silence(ctx context.Context, req *mcp.CallToolRequest, in silenceInput) (*mcp.CallToolResult, any, error) {
	if in.Hours <= 0 || in.Hours > 24 {
		return fail(fmt.Errorf("hours must be more than 0 and at most 24"))
	}
	until := time.Now().Add(time.Duration(in.Hours * float64(time.Hour)))
	if err := t.send(ctx, req, http.MethodPost, "/api/alerts/silences", map[string]any{"rule": in.Rule, "key": in.Key, "until": until, "note": in.Note}, nil); err != nil {
		return fail(err)
	}
	return ok("Silenced until " + until.Format(time.RFC3339) + ".")
}

func (t *tools) runbooks(ctx context.Context, req *mcp.CallToolRequest, in runbooksInput) (*mcp.CallToolResult, any, error) {
	var list []runbook
	if err := t.get(ctx, req, "/api/runbooks", nil, &list); err != nil {
		return fail(err)
	}
	words := strings.Fields(strings.ToLower(in.Search))
	var b strings.Builder
	for _, r := range list {
		hay := strings.ToLower(r.ID + " " + r.Title + " " + r.Body + " " + strings.Join(r.Rules, " "))
		if slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			continue
		}
		fmt.Fprintf(&b, "%s: %s (rules %s, version %d, by %s %s)\n", r.ID, r.Title, strings.Join(r.Rules, ", "), r.Version, r.UpdatedBy, ago(r.Updated))
	}
	return ok(or(b.String(), "No runbooks match."))
}

func (t *tools) runbook(ctx context.Context, req *mcp.CallToolRequest, in runbookInput) (*mcp.CallToolResult, any, error) {
	var r runbook
	if err := t.get(ctx, req, "/api/runbooks/"+url.PathEscape(in.ID), nil, &r); err != nil {
		return fail(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, version %d (rules %s; last edited by %s %s)\n", r.Title, r.Version, strings.Join(r.Rules, ", "), r.UpdatedBy, ago(r.Updated))
	if len(r.Fixes) > 0 {
		b.WriteString("\nWhat has worked (held / came back):\n")
		for _, f := range r.Fixes {
			fmt.Fprintf(&b, "- %s: %d / %d\n", line(f.Text, 200), f.Held, f.Recurred)
		}
	}
	b.WriteString("\n" + r.Body)
	return ok(b.String())
}

func (t *tools) editRunbook(ctx context.Context, req *mcp.CallToolRequest, in editInput) (*mcp.CallToolResult, any, error) {
	body := map[string]any{"title": in.Title, "body": in.Body, "rules": in.Rules, "version": in.Version}
	var r runbook
	var err error
	if in.ID == "" {
		err = t.send(ctx, req, http.MethodPost, "/api/runbooks", body, &r)
	} else {
		if in.Version == 0 {
			return fail(fmt.Errorf("give the version you read with runbook, so a newer edit isn't overwritten"))
		}
		err = t.send(ctx, req, http.MethodPut, "/api/runbooks/"+url.PathEscape(in.ID), body, &r)
	}
	if err != nil {
		return fail(err)
	}
	return ok(fmt.Sprintf("Saved %s, version %d.", r.ID, r.Version))
}

func (t *tools) coverage(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	var gaps []struct{ Kind, Subject, Detail string }
	if err := t.get(ctx, req, "/api/coverage", nil, &gaps); err != nil {
		return fail(err)
	}
	if len(gaps) == 0 {
		return ok("No gaps found.")
	}
	var b strings.Builder
	for _, g := range gaps {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", g.Kind, g.Subject, g.Detail)
	}
	b.WriteString("\nOpen a gap investigation for each worth closing, with open_investigation.")
	return ok(b.String())
}

func (t *tools) rule(ctx context.Context, req *mcp.CallToolRequest, in ruleInput) (*mcp.CallToolResult, any, error) {
	var snap struct {
		Rules []struct {
			Name    string    `json:"name"`
			Origin  string    `json:"origin"`
			Source  string    `json:"source"`
			LastRun time.Time `json:"lastRun"`
			Error   string    `json:"error"`
		} `json:"rules"`
	}
	if err := t.get(ctx, req, "/api/alerts", nil, &snap); err != nil {
		return fail(err)
	}
	for _, r := range snap.Rules {
		if r.Name == in.Name {
			status := ""
			if r.Error != "" {
				status = ", failing: " + r.Error
			}
			return ok(fmt.Sprintf("%s (%s), last ran %s%s\n\n%s", r.Name, r.Origin, ago(r.LastRun), status, r.Source))
		}
	}
	return fail(fmt.Errorf("no rule %q", in.Name))
}

func (t *tools) saveRule(ctx context.Context, req *mcp.CallToolRequest, in saveRuleInput) (*mcp.CallToolResult, any, error) {
	var v struct {
		Version int `json:"version"`
	}
	if err := t.send(ctx, req, http.MethodPut, "/api/alerts/rules/"+url.PathEscape(in.Name), map[string]string{"source": in.Source, "note": in.Note}, &v); err != nil {
		return fail(err)
	}
	return ok(fmt.Sprintf("Saved %s, version %d. It replayed cleanly over the last week.", in.Name, v.Version))
}
