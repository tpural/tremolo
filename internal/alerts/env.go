package alerts

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/tpural/tremolo/internal/hosts"
)

// HostLister lists hosts with their state.
type HostLister interface {
	List(ctx context.Context) ([]hosts.Host, error)
}

// Data is where rules read from.
type Data struct {
	MetricsURL *url.URL
	LogsURL    *url.URL
	Hosts      HostLister
	Client     *http.Client
}

// Env is one run of a rule: the data, the time it runs as, and where print goes.
type Env struct {
	Data  *Data
	Now   time.Time
	Print func(string)
	ctx   context.Context
}

func (e *Env) predeclared() starlark.StringDict {
	d := starlark.StringDict{
		"alert": starlark.NewBuiltin("alert", alertBuiltin),
		"ok": starlark.NewBuiltin("ok", func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
			return starlark.None, nil
		}),
		"metric":    starlark.NewBuiltin("metric", e.metric),
		"metrics":   starlark.NewBuiltin("metrics", e.metrics),
		"series":    starlark.NewBuiltin("series", e.series),
		"logs":      starlark.NewBuiltin("logs", e.logs),
		"log_count": starlark.NewBuiltin("log_count", e.logCount),
		"hosts":     starlark.NewBuiltin("hosts", e.hosts),
	}
	for k, v := range modules {
		d[k] = v
	}
	return d
}

func (e *Env) context(_ *starlark.Thread) context.Context {
	if e.ctx != nil {
		return e.ctx
	}
	return context.Background()
}

// errNoData is what data functions say while a rule file is only being checked.
var errNoData = fmt.Errorf("data is only available inside check(now)")

// metric returns one number, or None when the query matches nothing.
func (e *Env) metric(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var q string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "query", &q); err != nil {
		return nil, err
	}
	samples, err := e.instant(t, q)
	if err != nil {
		return nil, fmt.Errorf("metric: %w", err)
	}
	switch len(samples) {
	case 0:
		return starlark.None, nil
	case 1:
		return starlark.Float(samples[0].value), nil
	default:
		return nil, fmt.Errorf("metric: %q matched %d series; wrap it in sum(), max() or similar, or use metrics()", q, len(samples))
	}
}

// metrics returns every series, each with labels and value.
func (e *Env) metrics(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var q string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "query", &q); err != nil {
		return nil, err
	}
	samples, err := e.instant(t, q)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	out := make([]starlark.Value, len(samples))
	for i, s := range samples {
		out[i] = starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"labels": labelDict(s.labels), "value": starlark.Float(s.value),
		})
	}
	return starlark.NewList(out), nil
}

// series returns each series' values over a time range, oldest first.
func (e *Env) series(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var q string
	since, step := "1h", "1m"
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "query", &q, "since?", &since, "step?", &step); err != nil {
		return nil, err
	}
	if e.Data == nil {
		return nil, errNoData
	}
	back, err := time.ParseDuration(since)
	if err != nil {
		return nil, fmt.Errorf("series: since: %w", err)
	}
	every, err := time.ParseDuration(step)
	if err != nil || every <= 0 || back/every > 11000 {
		return nil, fmt.Errorf("series: step %q is not a duration or gives too many points", step)
	}
	var body struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	err = e.getJSON(t, e.Data.MetricsURL, "/api/v1/query_range", url.Values{
		"query": {q}, "start": {unix(e.Now.Add(-back))}, "end": {unix(e.Now)}, "step": {step},
	}, &body)
	if err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}
	out := make([]starlark.Value, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		values := make([]starlark.Value, 0, len(r.Values))
		times := make([]starlark.Value, 0, len(r.Values))
		for _, p := range r.Values {
			v, ok := number(p[1])
			ts, _ := p[0].(float64)
			if ok {
				values = append(values, starlark.Float(v))
				times = append(times, starlark.MakeInt64(int64(ts)))
			}
		}
		out = append(out, starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"labels": labelDict(r.Metric), "values": starlark.NewList(values), "times": starlark.NewList(times),
		}))
	}
	return starlark.NewList(out), nil
}

// logs returns matching log lines, newest first, as dicts of their fields.
func (e *Env) logs(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var q string
	since, limit := "15m", 100
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "query", &q, "since?", &since, "limit?", &limit); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("logs: limit must be 1 to 1000")
	}
	rows, err := e.logQuery(t, q+" | sort by (_time desc)", since, limit)
	if err != nil {
		return nil, fmt.Errorf("logs: %w", err)
	}
	out := make([]starlark.Value, len(rows))
	for i, r := range rows {
		out[i] = labelDict(r)
	}
	return starlark.NewList(out), nil
}

// log_count counts matching log lines.
func (e *Env) logCount(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var q string
	since := "15m"
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "query", &q, "since?", &since); err != nil {
		return nil, err
	}
	rows, err := e.logQuery(t, q+" | stats count() as n", since, 0)
	if err != nil {
		return nil, fmt.Errorf("log_count: %w", err)
	}
	if len(rows) == 0 {
		return starlark.MakeInt(0), nil
	}
	n, err := strconv.ParseInt(rows[0]["n"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("log_count: %w", err)
	}
	return starlark.MakeInt64(n), nil
}

// hosts returns every host by name, with its state.
func (e *Env) hosts(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	if e.Data == nil || e.Data.Hosts == nil {
		return nil, errNoData
	}
	list, err := e.Data.Hosts.List(e.context(t))
	if err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}
	out := starlark.NewDict(len(list))
	for _, h := range list {
		_ = out.SetKey(starlark.String(h.Name), starlarkstruct.FromStringDict(starlark.String("host"), starlark.StringDict{
			"name": starlark.String(h.Name), "state": starlark.String(h.State), "state_by": starlark.String(h.StateBy),
			"reason": starlark.String(h.Reason), "kind": starlark.String(h.Kind), "cluster": starlark.String(h.Cluster),
			"details": labelDict(h.Details), "roles": stringList(h.Roles),
			"since": unixOrNone(h.Since), "last_seen": unixOrNone(h.LastSeen),
		}))
	}
	return out, nil
}

type sample struct {
	labels map[string]string
	value  float64
}

func (e *Env) instant(t *starlark.Thread, q string) ([]sample, error) {
	if e.Data == nil {
		return nil, errNoData
	}
	var body struct {
		Data struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := e.getJSON(t, e.Data.MetricsURL, "/api/v1/query", url.Values{"query": {q}, "time": {unix(e.Now)}}, &body); err != nil {
		return nil, err
	}
	if body.Data.ResultType == "scalar" {
		var p [2]any
		if err := json.Unmarshal(body.Data.Result, &p); err != nil {
			return nil, err
		}
		v, _ := number(p[1])
		return []sample{{value: v}}, nil
	}
	var result []struct {
		Metric map[string]string `json:"metric"`
		Value  [2]any            `json:"value"`
	}
	if err := json.Unmarshal(body.Data.Result, &result); err != nil {
		return nil, err
	}
	out := make([]sample, 0, len(result))
	for _, r := range result {
		if v, ok := number(r.Value[1]); ok {
			out = append(out, sample{labels: r.Metric, value: v})
		}
	}
	return out, nil
}

func (e *Env) logQuery(t *starlark.Thread, q, since string, limit int) ([]map[string]string, error) {
	if e.Data == nil {
		return nil, errNoData
	}
	back, err := time.ParseDuration(since)
	if err != nil {
		return nil, fmt.Errorf("since: %w", err)
	}
	params := url.Values{"query": {q}, "start": {unix(e.Now.Add(-back))}, "end": {unix(e.Now)}}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	resp, err := e.get(t, e.Data.LogsURL, "/select/logsql/query", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rows []map[string]string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var row map[string]string
		if err := json.Unmarshal(sc.Bytes(), &row); err == nil {
			rows = append(rows, row)
		}
	}
	return rows, sc.Err()
}

func (e *Env) getJSON(t *starlark.Thread, base *url.URL, p string, params url.Values, v any) error {
	resp, err := e.get(t, base, p, params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (e *Env) get(t *starlark.Thread, base *url.URL, p string, params url.Values) (*http.Response, error) {
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + p
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(e.context(t), http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := e.Data.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		var msg strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for i := 0; i < 5 && sc.Scan(); i++ {
			msg.WriteString(sc.Text())
		}
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(msg.String()))
	}
	return resp, nil
}

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func number(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

func labelDict(m map[string]string) *starlark.Dict {
	d := starlark.NewDict(len(m))
	for k, v := range m {
		_ = d.SetKey(starlark.String(k), starlark.String(v))
	}
	return d
}

func stringList(ss []string) *starlark.List {
	out := make([]starlark.Value, len(ss))
	for i, s := range ss {
		out[i] = starlark.String(s)
	}
	return starlark.NewList(out)
}

func unixOrNone(t time.Time) starlark.Value {
	if t.IsZero() {
		return starlark.None
	}
	return starlark.MakeInt64(t.Unix())
}
