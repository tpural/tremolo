// Package storage watches tremolo's stores, VictoriaMetrics, VictoriaLogs and
// VictoriaTraces: how much they hold, how much room is left, and what each sender
// pushes, written back as metrics so it can be charted and alerted on.
package storage

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/ingest"
	"github.com/tpural/tremolo/internal/victoria"
)

// The metrics the Sampler writes, labelled by store (metrics, logs or traces), or by source and signal.
const (
	DataMetric    = "tremolo_storage_data_bytes"
	FreeMetric    = "tremolo_storage_free_bytes"
	TotalMetric   = "tremolo_storage_total_bytes"
	MaxMetric     = "tremolo_storage_max_bytes"
	RowsMetric    = "tremolo_storage_rows_total"
	IngestBytes   = "tremolo_ingest_bytes_total"
	IngestPushes  = "tremolo_ingest_pushes_total"
	sampleTimeout = 10 * time.Second
)

// Store is one of tremolo's stores.
type Store struct {
	// Name is metrics, logs or traces.
	Name string
	URL  *url.URL
	// prefix is the store's own metric prefix: vm_, vl_ or vt_.
	prefix string
}

// Stores names tremolo's stores; traces may be nil.
func Stores(metrics, logs, traces *url.URL) []Store {
	out := []Store{{Name: "metrics", URL: metrics, prefix: "vm_"}, {Name: "logs", URL: logs, prefix: "vl_"}}
	if traces != nil {
		out = append(out, Store{Name: "traces", URL: traces, prefix: "vt_"})
	}
	return out
}

// Usage is a store as last sampled.
type Usage struct {
	Store      string    `json:"store"`
	DataBytes  float64   `json:"dataBytes"`
	FreeBytes  float64   `json:"freeBytes"`
	TotalBytes float64   `json:"totalBytes,omitempty"`
	Rows       float64   `json:"rows"`
	Retention  string    `json:"retention,omitempty"`
	MaxDisk    string    `json:"maxDisk,omitempty"`
	Sampled    time.Time `json:"sampled"`
	Error      string    `json:"error,omitempty"`
}

// Sampler reads the stores' own metrics every so often.
type Sampler struct {
	Stores []Store
	// Counts is what the ingest gateway has received; nil when ingest is off.
	Counts func() map[ingest.Received]ingest.Count
	// MetricsURL gets the samples.
	MetricsURL *url.URL
	Client     *http.Client
	Log        *slog.Logger

	mu   sync.Mutex
	last []Usage
}

// Usage returns each store as last sampled.
func (s *Sampler) Usage() []Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.last)
}

// Run samples every interval until ctx ends.
func (s *Sampler) Run(ctx context.Context, every time.Duration) {
	for {
		if err := s.Sample(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("writing storage metrics", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Sample reads every store once and writes what it found.
func (s *Sampler) Sample(ctx context.Context) error {
	now := time.Now()
	var usage []Usage
	var samples []victoria.Sample
	for _, st := range s.Stores {
		u := s.read(ctx, st)
		u.Sampled = now
		usage = append(usage, u)
		if u.Error != "" {
			continue
		}
		label := [][2]string{{"store", st.Name}}
		samples = append(samples,
			victoria.Sample{Metric: DataMetric, Labels: label, Value: u.DataBytes, Time: now},
			victoria.Sample{Metric: FreeMetric, Labels: label, Value: u.FreeBytes, Time: now},
			victoria.Sample{Metric: RowsMetric, Labels: label, Value: u.Rows, Time: now},
		)
		if u.TotalBytes > 0 {
			samples = append(samples, victoria.Sample{Metric: TotalMetric, Labels: label, Value: u.TotalBytes, Time: now})
		}
		if n, ok := parseBytes(u.MaxDisk); ok {
			samples = append(samples, victoria.Sample{Metric: MaxMetric, Labels: label, Value: n, Time: now})
		}
	}
	if s.Counts != nil {
		for k, c := range s.Counts() {
			labels := [][2]string{{"source", k.Sender}, {"signal", k.Signal}}
			samples = append(samples,
				victoria.Sample{Metric: IngestBytes, Labels: labels, Value: float64(c.Bytes), Time: now},
				victoria.Sample{Metric: IngestPushes, Labels: labels, Value: float64(c.Pushes), Time: now},
			)
		}
	}
	s.mu.Lock()
	s.last = usage
	s.mu.Unlock()
	return victoria.Import(ctx, s.Client, s.MetricsURL, samples)
}

// read parses the parts of a store's /metrics that say how full it is.
func (s *Sampler) read(ctx context.Context, st Store) Usage {
	u := Usage{Store: st.Name}
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, st.URL.JoinPath("/metrics").String(), nil)
	if err != nil {
		u.Error = err.Error()
		return u
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		u.Error = "unreachable"
		return u
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		u.Error = "metrics answered " + resp.Status
		return u
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		name, labels, value, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		switch name {
		case st.prefix + "data_size_bytes":
			u.DataBytes += value
		case st.prefix + "free_disk_space_bytes":
			u.FreeBytes = value
		case st.prefix + "total_disk_space_bytes":
			u.TotalBytes = value
		case st.prefix + "rows_inserted_total", st.prefix + "rows_ingested_total":
			u.Rows += value
		case "flag":
			switch labels["name"] {
			case "retentionPeriod":
				u.Retention = labels["value"]
			case "retention.maxDiskSpaceUsageBytes":
				if labels["value"] != "0" {
					u.MaxDisk = labels["value"]
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		u.Error = err.Error()
	}
	return u
}

// parseBytes reads a size flag such as 15GiB, 500MB or 1048576.
func parseBytes(s string) (float64, bool) {
	units := []struct {
		suffix string
		mult   float64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(n, 64)
			return v * u.mult, err == nil && v > 0
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && v > 0
}

// parseLine reads one line of the Prometheus text format: name{labels} value.
func parseLine(line string) (string, map[string]string, float64, bool) {
	if line == "" || line[0] == '#' {
		return "", nil, 0, false
	}
	i := strings.IndexAny(line, "{ ")
	if i < 0 {
		return "", nil, 0, false
	}
	name, rest := line[:i], line[i:]
	labels := map[string]string{}
	if strings.HasPrefix(rest, "{") {
		end := strings.LastIndex(rest, "}")
		if end < 0 {
			return "", nil, 0, false
		}
		for _, pair := range splitLabels(rest[1:end]) {
			k, v, found := strings.Cut(pair, "=")
			if !found {
				continue
			}
			if uq, err := strconv.Unquote(strings.TrimSpace(v)); err == nil {
				labels[strings.TrimSpace(k)] = uq
			}
		}
		rest = rest[end+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	return name, labels, v, err == nil
}

// splitLabels splits a=“x”, b=“y” on the commas outside quotes.
func splitLabels(s string) []string {
	var out []string
	var b strings.Builder
	quoted, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
