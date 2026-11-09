package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/hoststate"
	"github.com/tpural/tremolo/internal/victoria"
)

const (
	// StateMetric is each host's state, written to the metrics store as it's
	// seen: one series per host and state, whose value is when the state began
	// (Unix seconds, 0 if unknown).
	StateMetric = "tremolo_host_state"
	// HistoryStream is the logs stream of host state changes.
	HistoryStream = "tremolo-hosts"
	// window is how far back a state sample counts at a moment: a few writes.
	window = 2 * time.Minute
)

// History records hosts' states in the metrics and logs stores, and reads
// them back for a past moment.
type History struct {
	lister  *Lister
	metrics *url.URL
	logs    *url.URL
	client  *http.Client
	log     *slog.Logger
	last    map[string]Host
}

// NewHistory returns a History for the hosts lister lists. logs may be nil.
func NewHistory(lister *Lister, metrics, logs *url.URL, client *http.Client, log *slog.Logger) *History {
	return &History{lister: lister, metrics: metrics, logs: logs, client: client, log: log}
}

// Run records the hosts' states every so often until ctx ends.
func (h *History) Run(ctx context.Context, every time.Duration) {
	for {
		if err := h.Record(ctx); err != nil && ctx.Err() == nil {
			h.log.Warn("recording host states", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Record writes every host's state now, and an event for each that changed.
func (h *History) Record(ctx context.Context) error {
	list, err := h.lister.List(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	if h.last == nil {
		// After a restart, compare with what was recorded before it.
		h.last = map[string]Host{}
		if before, ok, err := h.at(ctx, now); err == nil && ok {
			for _, p := range before {
				h.last[p.Name] = p
			}
		}
	}
	var samples []victoria.Sample
	var events []map[string]string
	for _, host := range list {
		since := 0.0
		if !host.Since.IsZero() {
			since = float64(host.Since.Unix())
		}
		samples = append(samples, victoria.Sample{Metric: StateMetric, Labels: labels(host), Value: since, Time: now})
		prev, seen := h.last[host.Name]
		if !seen || prev.State != host.State || prev.StateBy != host.StateBy || prev.Reason != host.Reason {
			ev := map[string]string{
				"_time": victoria.Time(now), "stream": HistoryStream, "host": host.Name,
				"state": string(host.State), "state_by": host.StateBy, "reason": host.Reason, "kind": string(host.Kind),
				"_msg": fmt.Sprintf("%s is %s", host.Name, host.State),
			}
			if seen {
				ev["from"] = string(prev.State)
				ev["_msg"] = fmt.Sprintf("%s went from %s to %s", host.Name, prev.State, host.State)
			}
			if host.Reason != "" {
				ev["_msg"] += ": " + host.Reason
			}
			events = append(events, ev)
		}
	}
	if err := victoria.Import(ctx, h.client, h.metrics, samples); err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	h.last = map[string]Host{}
	for _, host := range list {
		h.last[host.Name] = host
	}
	if h.logs == nil {
		return nil
	}
	if err := victoria.Insert(ctx, h.client, h.logs, "stream,host", events); err != nil {
		return fmt.Errorf("logs: %w", err)
	}
	return nil
}

func labels(h Host) [][2]string {
	return [][2]string{
		{"host_name", h.Name},
		{"state", string(h.State)},
		{"state_by", h.StateBy},
		{"reason", h.Reason},
		{"kind", string(h.Kind)},
		{"cluster", h.Cluster},
	}
}

// At returns the hosts as recorded at t, with what's known of them now (roles,
// details) filled in. ok is false when nothing was recorded then, e.g. before
// recording began.
func (h *History) At(ctx context.Context, t time.Time) ([]Host, bool, error) {
	past, ok, err := h.at(ctx, t)
	if err != nil || !ok {
		return nil, ok, err
	}
	current, err := h.lister.List(ctx)
	if err != nil {
		return nil, false, err
	}
	now := map[string]Host{}
	for _, c := range current {
		now[c.Name] = c
	}
	for i, p := range past {
		c, known := now[p.Name]
		if !known {
			continue
		}
		state, by, reason, since, seen := p.State, p.StateBy, p.Reason, p.Since, p.LastSeen
		p.Node, p.Source, p.Details = c.Node, c.Source, maps.Clone(c.Details)
		p.Name, p.State, p.StateBy, p.Reason, p.Since, p.LastSeen = c.Name, state, by, reason, since, seen
		p.Pods, p.Until = 0, time.Time{}
		past[i] = p
	}
	return past, true, nil
}

// at reads the recorded states at t: for each host, its newest sample in the
// window before t.
func (h *History) at(ctx context.Context, t time.Time) ([]Host, bool, error) {
	sel := fmt.Sprintf("last_over_time(%s[%s])", StateMetric, window)
	since, err := h.query(ctx, sel, t)
	if err != nil {
		return nil, false, err
	}
	stamps, err := h.query(ctx, "timestamp("+sel+")", t)
	if err != nil {
		return nil, false, err
	}
	when := map[string]float64{}
	for _, s := range stamps {
		when[key(s.labels)] = s.value
	}
	newest := map[string]Host{}
	at := map[string]float64{}
	for _, s := range since {
		name := s.labels["host_name"]
		ts := when[key(s.labels)]
		if name == "" || ts < at[name] {
			continue
		}
		host := Host{Kind: Kind(s.labels["kind"]), Cluster: s.labels["cluster"], StateBy: s.labels["state_by"], LastSeen: time.Unix(int64(ts), 0)}
		host.Name, host.State, host.Reason = name, hoststate.State(s.labels["state"]), s.labels["reason"]
		if s.value > 0 {
			host.Since = time.Unix(int64(s.value), 0)
		}
		newest[name], at[name] = host, ts
	}
	if len(newest) == 0 {
		return nil, false, nil
	}
	return sorted(pointers(newest)), true, nil
}

func pointers(m map[string]Host) map[string]*Host {
	out := make(map[string]*Host, len(m))
	for k, v := range m {
		out[k] = &v
	}
	return out
}

func key(labels map[string]string) string {
	keys := slices.Sorted(maps.Keys(labels))
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + labels[k] + "\x00")
	}
	return b.String()
}

type point struct {
	labels map[string]string
	value  float64
}

func (h *History) query(ctx context.Context, q string, t time.Time) ([]point, error) {
	u := *h.metrics
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"
	u.RawQuery = url.Values{"query": {q}, "time": {strconv.FormatInt(t.Unix(), 10)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query: %s", resp.Status)
	}
	var body struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]point, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		s, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		out = append(out, point{labels: r.Metric, value: v})
	}
	return out, nil
}
