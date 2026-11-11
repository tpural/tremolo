// Package coverage finds gaps in what tremolo observes: hosts and
// namespaces that send no logs or metrics, senders that went quiet, rules
// without runbooks.
package coverage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// Gap is something tremolo doesn't see that it probably should.
type Gap struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Host is what coverage needs of a host.
type Host struct {
	Name     string
	State    string
	LastSeen time.Time
}

// Source checks coverage against the stores.
type Source struct {
	MetricsURL, LogsURL, TracesURL *url.URL
	Client                         *http.Client
	Hosts                          func(ctx context.Context) ([]Host, error)
	// Rules are the alert rules' names; HasRunbook reports whether one covers a rule.
	Rules      func() []string
	HasRunbook func(rule string) bool
}

// Check returns the gaps it finds; a check that fails is reported as a gap
// of kind "check".
func (s *Source) Check(ctx context.Context, now time.Time) []Gap {
	var gaps []Gap
	failed := func(what string, err error) {
		gaps = append(gaps, Gap{Kind: "check", Subject: what, Detail: err.Error()})
	}
	hostLogs, err := s.logCounts(ctx, s.LogsURL, "host.name", "1h")
	if err != nil {
		failed("logs by host", err)
	}
	if s.Hosts != nil {
		hosts, err := s.Hosts(ctx)
		if err != nil {
			failed("hosts", err)
		}
		for _, h := range hosts {
			if slices.Contains([]string{"asleep", "off", "waking"}, h.State) {
				continue
			}
			if h.LastSeen.IsZero() || now.Sub(h.LastSeen) > 15*time.Minute {
				gaps = append(gaps, Gap{"metrics", h.Name, "no host metrics in the last 15 minutes; is its collector running?"})
			}
			if hostLogs != nil && hostLogs[h.Name] == 0 {
				gaps = append(gaps, Gap{"logs", h.Name, "no logs with host.name in the last hour"})
			}
		}
	}
	running, err := s.labelValues(ctx, `count by (namespace) (container_memory_working_set_bytes{container!=""})`, "namespace")
	if err != nil {
		failed("namespaces running containers", err)
	}
	// A day: quiet services rightly log nothing for hours.
	if nsLogs, err := s.logCounts(ctx, s.LogsURL, "k8s.namespace.name", "24h"); err != nil {
		failed("logs by namespace", err)
	} else {
		for _, ns := range running {
			if nsLogs[ns] == 0 {
				gaps = append(gaps, Gap{"logs", "namespace " + ns, "containers are running but sent no logs in a day"})
			}
		}
	}
	if s.TracesURL != nil && len(running) > 0 {
		traced, err := s.logCounts(ctx, s.TracesURL, `"resource_attr:k8s.namespace.name"`, "24h")
		if err != nil {
			failed("traces by namespace", err)
		} else {
			var none []string
			for _, ns := range running {
				if traced[ns] == 0 {
					none = append(none, ns)
				}
			}
			if len(none) > 0 {
				gaps = append(gaps, Gap{"traces", fmt.Sprintf("%d namespaces", len(none)), "no traces in a day from: " + strings.Join(none, ", ") +
					". Worth instrumenting the services you debug most."})
			}
		}
	}
	quiet, err := s.labelValues(ctx, `sum by (source, signal) (increase(tremolo_ingest_pushes_total[1d])) > 0
		unless sum by (source, signal) (increase(tremolo_ingest_pushes_total[1h])) > 0`, "source", "signal")
	if err != nil {
		failed("quiet senders", err)
	}
	for _, q := range quiet {
		gaps = append(gaps, Gap{"sender", q, "sent data in the last day but nothing in the last hour"})
	}
	if s.Rules != nil && s.HasRunbook != nil {
		var missing []string
		for _, r := range s.Rules() {
			if !s.HasRunbook(r) {
				missing = append(missing, r)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			gaps = append(gaps, Gap{"runbook", fmt.Sprintf("%d rules", len(missing)), "no runbook for: " + strings.Join(missing, ", ")})
		}
	}
	return gaps
}

// labelValues runs an instant query and returns each series' labels, joined with "/".
func (s *Source) labelValues(ctx context.Context, query string, names ...string) ([]string, error) {
	u := *s.MetricsURL
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"
	u.RawQuery = url.Values{"query": {query}}.Encode()
	var res struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := s.get(ctx, u, func(r *bufio.Reader) error { return json.NewDecoder(r).Decode(&res) }); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Data.Result {
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = r.Metric[n]
		}
		if v := strings.Join(parts, "/"); strings.Trim(v, "/") != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// logCounts counts logs (or spans) by field over the last window.
func (s *Source) logCounts(ctx context.Context, base *url.URL, field, window string) (map[string]int64, error) {
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + "/select/logsql/query"
	u.RawQuery = url.Values{"query": {"_time:" + window + " | stats by (" + field + ") count() as n"}}.Encode()
	out := map[string]int64{}
	key := strings.Trim(field, `"`)
	err := s.get(ctx, u, func(r *bufio.Reader) error {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			var row map[string]any
			if json.Unmarshal(sc.Bytes(), &row) != nil {
				continue
			}
			name, _ := row[key].(string)
			var n int64
			switch v := row["n"].(type) {
			case string:
				_, _ = fmt.Sscan(v, &n)
			case float64:
				n = int64(v)
			}
			if name != "" {
				out[name] += n
			}
		}
		return sc.Err()
	})
	return out, err
}

func (s *Source) get(ctx context.Context, u url.URL, read func(*bufio.Reader) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
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
		return fmt.Errorf("%s: %s", u.Path, res.Status)
	}
	return read(bufio.NewReader(res.Body))
}
