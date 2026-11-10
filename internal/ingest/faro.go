package ingest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/victoria"
)

// BrowserStream is the logs stream browser telemetry goes to.
const BrowserStream = "browser"

// The metrics browser telemetry becomes, labelled by app and page.
const (
	// VitalsMetric is one Web Vitals measurement, e.g. name="lcp" in milliseconds (cls has no unit).
	VitalsMetric = "browser_web_vitals"
	// EventsMetric is 1 for each page view, error, log and event, by kind.
	EventsMetric = "browser_events"
)

// maxFaroBody bounds one browser batch.
const maxFaroBody = 256 << 10

// faroPayload is what the Grafana Faro web SDK sends.
type faroPayload struct {
	Meta struct {
		App struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Environment string `json:"environment"`
		} `json:"app"`
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Page struct {
			URL string `json:"url"`
		} `json:"page"`
		Browser struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			OS      string `json:"os"`
			Mobile  bool   `json:"mobile"`
		} `json:"browser"`
	} `json:"meta"`
	Logs []struct {
		Message   string `json:"message"`
		Level     string `json:"level"`
		Timestamp string `json:"timestamp"`
	} `json:"logs"`
	Exceptions []struct {
		Type       string `json:"type"`
		Value      string `json:"value"`
		Timestamp  string `json:"timestamp"`
		Stacktrace struct {
			Frames []struct {
				Filename string `json:"filename"`
				Function string `json:"function"`
				Lineno   int    `json:"lineno"`
				Colno    int    `json:"colno"`
			} `json:"frames"`
		} `json:"stacktrace"`
	} `json:"exceptions"`
	Measurements []struct {
		Type      string             `json:"type"`
		Values    map[string]float64 `json:"values"`
		Timestamp string             `json:"timestamp"`
	} `json:"measurements"`
	Events []struct {
		Name       string            `json:"name"`
		Domain     string            `json:"domain"`
		Attributes map[string]string `json:"attributes"`
		Timestamp  string            `json:"timestamp"`
	} `json:"events"`
}

// vitals are the Web Vitals kept; the SDK sends others (e.g. deltas) too.
var vitals = map[string]bool{"lcp": true, "inp": true, "cls": true, "fcp": true, "ttfb": true}

// faro takes Grafana Faro batches: errors, logs and events go to the logs store
// as the browser stream, Web Vitals and counts to the metrics store. Pages are
// kept as paths, without query strings, so no one's search or token is stored.
func (g *Gateway) faro(w http.ResponseWriter, r *http.Request) {
	sender, err := g.sender(r.Header.Get("Authorization"))
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="tremolo"`)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var p faroPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFaroBody)).Decode(&p); err != nil {
		http.Error(w, "body must be a Faro payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	rows, samples := faroRecords(p, sender, time.Now())
	ctx := r.Context()
	if err := victoria.Insert(ctx, g.client, g.opts.LogsURL, "stream,app", rows); err != nil {
		g.opts.Log.Warn("forward browser telemetry", "sender", sender, "err", err)
		http.Error(w, "storage unavailable", http.StatusBadGateway)
		return
	}
	if err := victoria.Import(ctx, g.client, g.opts.MetricsURL, samples); err != nil {
		g.opts.Log.Warn("forward browser metrics", "sender", sender, "err", err)
		http.Error(w, "storage unavailable", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// faroRecords turns a batch into log rows and samples.
func faroRecords(p faroPayload, sender string, now time.Time) ([]map[string]string, []victoria.Sample) {
	app := clip(p.Meta.App.Name, 64)
	if app == "" {
		app = "unknown"
	}
	page := pagePath(p.Meta.Page.URL)
	base := map[string]string{
		"stream": BrowserStream, "app": app, "release": clip(p.Meta.App.Version, 64), "environment": clip(p.Meta.App.Environment, 32),
		"page": page, "browser": clip(strings.TrimSpace(p.Meta.Browser.Name+" "+p.Meta.Browser.Version), 64), "os": clip(p.Meta.Browser.OS, 64),
		"mobile": strconv.FormatBool(p.Meta.Browser.Mobile), "session": clip(p.Meta.Session.ID, 64),
	}
	if sender != "" {
		base["source"] = sender
	}
	var rows []map[string]string
	var samples []victoria.Sample
	row := func(kind, msg, ts string, extra map[string]string) {
		t := at(ts, now)
		r := map[string]string{"_time": victoria.Time(t), "_msg": clip(msg, 4000), "kind": kind}
		for k, v := range base {
			if v != "" {
				r[k] = v
			}
		}
		for k, v := range extra {
			r[k] = v
		}
		rows = append(rows, r)
		samples = append(samples, victoria.Sample{Metric: EventsMetric, Labels: [][2]string{{"app", app}, {"kind", kind}, {"page", page}}, Value: 1, Time: t})
	}
	for _, e := range p.Exceptions {
		var stack strings.Builder
		for i := len(e.Stacktrace.Frames) - 1; i >= 0 && stack.Len() < 3000; i-- {
			f := e.Stacktrace.Frames[i]
			fmt.Fprintf(&stack, "at %s (%s:%d:%d)\n", cmp.Or(f.Function, "?"), pagePathOrFile(f.Filename), f.Lineno, f.Colno)
		}
		row("exception", strings.TrimSpace(e.Type+": "+e.Value), e.Timestamp, map[string]string{"error_type": clip(e.Type, 100), "stack": stack.String()})
	}
	for _, l := range p.Logs {
		row("log", l.Message, l.Timestamp, map[string]string{"level": clip(l.Level, 16)})
	}
	for _, e := range p.Events {
		kind := "event"
		if e.Name == "page_view" || e.Name == "view_changed" {
			kind = "page_view"
		}
		extra := map[string]string{"event": clip(e.Name, 100), "domain": clip(e.Domain, 64)}
		if path, ok := e.Attributes["path"]; ok && kind == "page_view" {
			extra["page"] = pagePath(path)
		}
		row(kind, e.Name, e.Timestamp, extra)
	}
	for _, m := range p.Measurements {
		if m.Type != "web-vitals" {
			continue
		}
		t := at(m.Timestamp, now)
		for name, v := range m.Values {
			if vitals[name] {
				samples = append(samples, victoria.Sample{Metric: VitalsMetric, Labels: [][2]string{{"app", app}, {"page", page}, {"name", name}}, Value: v, Time: t})
			}
		}
	}
	return rows, samples
}

// ids are path parts that name one thing (a number, uuid or hash), folded so pages don't multiply series.
var ids = regexp.MustCompile(`^([0-9]+|[0-9a-f]{8}-[0-9a-f-]{27,}|[0-9a-f]{16,})$`)

// pagePath is a URL's path only, with ids folded to :id, e.g. /posts/42?q=x is /posts/:id.
func pagePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "/"
	}
	parts := strings.Split(u.Path, "/")
	for i, p := range parts {
		if ids.MatchString(strings.ToLower(p)) {
			parts[i] = ":id"
		}
	}
	return clip(strings.Join(parts, "/"), 120)
}

// pagePathOrFile is a stack frame's script, without query strings.
func pagePathOrFile(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return clip(raw, 200)
}

func at(ts string, now time.Time) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	// A browser's clock can be anything; keep times within a day of ours.
	if err != nil || t.Before(now.Add(-24*time.Hour)) || t.After(now.Add(time.Hour)) {
		return now
	}
	return t
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
