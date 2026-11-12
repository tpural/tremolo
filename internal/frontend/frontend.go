// Package frontend describes the browser apps that send tremolo Faro data:
// which there are, how their visitors are doing, and a dashboard for each.
package frontend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/ingest"
)

// App is what tremolo knows of an app beyond its data: the Kubernetes
// namespace its server runs in, and pages to leave out, such as an admin page
// whose speed says nothing about visitors.
type App struct {
	Namespace   string   `json:"namespace,omitempty"`
	IgnorePages []string `json:"ignorePages,omitempty"`
}

// Config is the apps by name, from --frontend.
type Config map[string]App

// ParseConfig reads --frontend: JSON, e.g. {"shop":{"namespace":"shop","ignorePages":["/admin"]}}.
func ParseConfig(s string) (Config, error) {
	c := Config{}
	if strings.TrimSpace(s) == "" {
		return c, nil
	}
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return nil, fmt.Errorf("--frontend: %w", err)
	}
	for name := range c {
		if !validName.MatchString(name) {
			return nil, fmt.Errorf("--frontend: app name %q: use letters, digits, '.', '_' and '-'", name)
		}
	}
	return c, nil
}

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// Summary is how an app's visitors are doing.
type Summary struct {
	Name string `json:"name"`
	App
	Views    int       `json:"views"`
	Errors   int       `json:"errors"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	// Vitals are the 75th percentiles over 7 days, by name (lcp, inp, cls,
	// fcp, ttfb), with how many measurements each is from.
	Vitals map[string]Vital `json:"vitals"`
}

// Vital is one Web Vital's 75th percentile.
type Vital struct {
	P75     float64 `json:"p75"`
	Samples int     `json:"samples"`
}

// Source reads browser data from VictoriaMetrics. Filter, when set, adds
// what a request may read to each query.
type Source struct {
	MetricsURL *url.URL
	Client     *http.Client
	Config     Config
}

// Apps lists the apps that sent data in the last 30 days, and those
// configured, with how they're doing. filter limits what's read.
func (s *Source) Apps(ctx context.Context, now time.Time, filter func(url.Values)) ([]Summary, error) {
	names, err := s.names(ctx, now, filter)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Summary{}
	for _, n := range names {
		byName[n] = &Summary{Name: n, App: s.Config[n], Vitals: map[string]Vital{}}
	}
	day, week := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)
	vitals := map[string]map[string][]float64{}
	err = s.export(ctx, ingest.EventsMetric, week, now, filter, func(m map[string]string, v []float64, ts []int64) {
		sum := byName[m["app"]]
		if sum == nil || ignored(sum.IgnorePages, m["page"]) {
			return
		}
		for i := range v {
			t := time.UnixMilli(ts[i])
			if t.After(sum.LastSeen) {
				sum.LastSeen = t
			}
			if t.Before(day) {
				continue
			}
			switch m["kind"] {
			case "page_view":
				sum.Views += int(v[i])
			case "exception":
				sum.Errors += int(v[i])
			}
		}
	})
	if err != nil {
		return nil, err
	}
	err = s.export(ctx, ingest.VitalsMetric, week, now, filter, func(m map[string]string, v []float64, _ []int64) {
		sum := byName[m["app"]]
		if sum == nil || ignored(sum.IgnorePages, m["page"]) {
			return
		}
		if vitals[sum.Name] == nil {
			vitals[sum.Name] = map[string][]float64{}
		}
		vitals[sum.Name][m["name"]] = append(vitals[sum.Name][m["name"]], v...)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(byName))
	for _, n := range names {
		sum := byName[n]
		for name, vs := range vitals[n] {
			sum.Vitals[name] = Vital{P75: quantile(vs, 0.75), Samples: len(vs)}
		}
		out = append(out, *sum)
	}
	return out, nil
}

// names are the apps with browser data in the last 30 days, and configured ones.
func (s *Source) names(ctx context.Context, now time.Time, filter func(url.Values)) ([]string, error) {
	q := url.Values{"match[]": {ingest.EventsMetric}, "start": {strconv.FormatInt(now.Add(-30*24*time.Hour).Unix(), 10)}}
	if filter != nil {
		filter(q)
	}
	var body struct{ Data []string }
	if err := s.get(ctx, "/api/v1/label/app/values", q, func(r *bufio.Reader) error { return json.NewDecoder(r).Decode(&body) }); err != nil {
		return nil, err
	}
	names := body.Data
	if filter == nil {
		for n := range s.Config {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Source) export(ctx context.Context, metric string, from, to time.Time, filter func(url.Values), each func(map[string]string, []float64, []int64)) error {
	q := url.Values{"match[]": {metric}, "start": {strconv.FormatInt(from.Unix(), 10)}, "end": {strconv.FormatInt(to.Unix(), 10)}}
	if filter != nil {
		filter(q)
	}
	return s.get(ctx, "/api/v1/export", q, func(r *bufio.Reader) error {
		dec := json.NewDecoder(r)
		for dec.More() {
			var row struct {
				Metric     map[string]string
				Values     []float64
				Timestamps []int64
			}
			if err := dec.Decode(&row); err != nil {
				return err
			}
			if len(row.Values) == len(row.Timestamps) {
				each(row.Metric, row.Values, row.Timestamps)
			}
		}
		return nil
	})
}

func (s *Source) get(ctx context.Context, path string, q url.Values, read func(*bufio.Reader) error) error {
	u := *s.MetricsURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, res.Status)
	}
	return read(bufio.NewReader(res.Body))
}

func ignored(pages []string, page string) bool { return slices.Contains(pages, page) }

// quantile is the q-th quantile of vs, interpolated.
func quantile(vs []float64, q float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	s := slices.Clone(vs)
	slices.Sort(s)
	pos := q * float64(len(s)-1)
	i := int(pos)
	if i+1 >= len(s) {
		return s[len(s)-1]
	}
	return s[i] + (s[i+1]-s[i])*(pos-float64(i))
}

// Dashboard is an app's dashboard: visitors, errors, Web Vitals, and its
// server when its namespace is known.
func Dashboard(name string, app App) dashboard.Dashboard {
	sel := fmt.Sprintf(`app=%q`, name)
	logSel := fmt.Sprintf(`{stream="browser", app=%q}`, name)
	if len(app.IgnorePages) > 0 {
		patterns := make([]string, len(app.IgnorePages))
		quoted := make([]string, len(app.IgnorePages))
		for i, p := range app.IgnorePages {
			patterns[i] = regexp.QuoteMeta(p)
			quoted[i] = strconv.Quote(p)
		}
		sel += fmt.Sprintf(`, page!~%q`, strings.Join(patterns, "|"))
		logSel += " -page:in(" + strings.Join(quoted, ", ") + ")"
	}
	f := func(v float64) *float64 { return &v }
	ev := func(kind string) string { return fmt.Sprintf(`browser_events{%s, kind=%q}`, sel, kind) }
	vital := func(name string) string { return fmt.Sprintf(`browser_web_vitals{%s, name=%q}`, sel, name) }
	panels := []dashboard.Panel{
		{Title: "Page views today", Type: "stat", Width: 3, Query: fmt.Sprintf(`sum(sum_over_time(%s[1d])) or vector(0)`, ev("page_view"))},
		{Title: "JavaScript errors today", Type: "stat", Width: 3, Query: fmt.Sprintf(`sum(sum_over_time(%s[1d])) or vector(0)`, ev("exception")), Warn: f(1), Bad: f(10)},
		{Title: "LCP p75, slowest page (good under 2.5s)", Type: "stat", Width: 3, Unit: "ms", Query: fmt.Sprintf(`max(quantile_over_time(0.75, %s[1d]))`, vital("lcp")), Warn: f(2500), Bad: f(4000)},
		{Title: "INP p75, slowest page (good under 200ms)", Type: "stat", Width: 3, Unit: "ms", Query: fmt.Sprintf(`max(quantile_over_time(0.75, %s[1d]))`, vital("inp")), Warn: f(200), Bad: f(500)},
		{Title: "Page views per hour", Type: "timeseries", Width: 6, Query: fmt.Sprintf(`sum(sum_over_time(%s[1h]))`, ev("page_view")), Legend: "views"},
		{Title: "JavaScript errors per hour", Type: "timeseries", Width: 6, Query: fmt.Sprintf(`sum by (page) (sum_over_time(%s[1h]))`, ev("exception")), Legend: "{{page}}"},
		{Title: "Top pages today", Type: "table", Width: 6, Legend: "{{page}}", Query: fmt.Sprintf(`topk(10, sum by (page) (sum_over_time(%s[1d])))`, ev("page_view"))},
		{Title: "Web Vitals p75 by page today (LCP, INP, FCP, TTFB in ms)", Type: "table", Width: 6, Legend: "{{name}} {{page}}", Query: fmt.Sprintf(`quantile_over_time(0.75, browser_web_vitals{%s}[1d])`, sel)},
		{Title: "LCP p75 by page", Type: "timeseries", Width: 6, Unit: "ms", Query: fmt.Sprintf(`quantile_over_time(0.75, %s[1h])`, vital("lcp")), Legend: "{{page}}"},
		{Title: "Layout shift (CLS) p75 by page (good under 0.1)", Type: "timeseries", Width: 6, Query: fmt.Sprintf(`quantile_over_time(0.75, %s[1h])`, vital("cls")), Legend: "{{page}}"},
		{Title: "Latest JavaScript errors", Type: "logs", Width: 12, Query: logSel + " kind:=exception"},
	}
	if ns := app.Namespace; ns != "" {
		panels = append(panels,
			dashboard.Panel{Title: "Server memory", Type: "timeseries", Width: 4, Unit: "bytes", Query: fmt.Sprintf(`sum by (container) (container_memory_working_set_bytes{namespace=%q, container!=""})`, ns), Legend: "{{container}}"},
			dashboard.Panel{Title: "Server CPU", Type: "timeseries", Width: 4, Query: fmt.Sprintf(`sum by (container) (rate(container_cpu_usage_seconds_total{namespace=%q, container!=""}[5m]))`, ns), Legend: "{{container}}"},
			dashboard.Panel{Title: "Restarts in the last hour", Type: "timeseries", Width: 4, Query: fmt.Sprintf(`sum by (k8s_pod_name) (max by (k8s_pod_name, k8s_container_name) (max_over_time(k8s_container_restarts{k8s_namespace_name=%[1]q}[1h])) - min by (k8s_pod_name, k8s_container_name) (min_over_time(k8s_container_restarts{k8s_namespace_name=%[1]q}[1h])))`, ns), Legend: "{{k8s_pod_name}}"},
			dashboard.Panel{Title: "Server errors", Type: "logs", Width: 12, Query: fmt.Sprintf(`{k8s.namespace.name=%q} k8s.container.name:* _msg:~"(?i)\\berror\\b|exception|traceback| 5[0-9][0-9] |\\bERR\\b"`, ns)},
		)
	}
	return dashboard.Dashboard{ID: "frontend-" + name, Title: name, Description: "Visitors, errors and Web Vitals from the browser", Hidden: true, Panels: panels}
}
