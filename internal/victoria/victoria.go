// Package victoria writes tremolo's own telemetry to VictoriaMetrics and
// VictoriaLogs.
package victoria

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sample is one value of a series at a time.
type Sample struct {
	Metric string
	Labels [][2]string
	Value  float64
	Time   time.Time
}

// Import writes samples to VictoriaMetrics.
func Import(ctx context.Context, client *http.Client, base *url.URL, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	var b bytes.Buffer
	for _, s := range samples {
		fmt.Fprintf(&b, "%s{%s} %s %d\n", s.Metric, labels(s.Labels), strconv.FormatFloat(s.Value, 'f', -1, 64), s.Time.UnixMilli())
	}
	return post(ctx, client, base, "/api/v1/import/prometheus", "", &b)
}

// Insert writes log rows to VictoriaLogs. Each row needs _time and _msg;
// streamFields name the fields that pick its stream.
func Insert(ctx context.Context, client *http.Client, base *url.URL, streamFields string, rows []map[string]string) error {
	if len(rows) == 0 {
		return nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, r := range rows {
		_ = enc.Encode(r)
	}
	q := url.Values{"_stream_fields": {streamFields}, "_msg_field": {"_msg"}, "_time_field": {"_time"}}
	return post(ctx, client, base, "/insert/jsonline", q.Encode(), &b)
}

// Time formats t for a log row's _time.
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

var escape = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// maxLabel bounds a label's value, so a long reason can't make a huge series.
const maxLabel = 200

func labels(pairs [][2]string) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		v := p[1]
		if len(v) > maxLabel {
			v = v[:maxLabel]
		}
		b.WriteString(p[0] + `="` + escape.Replace(v) + `"`)
	}
	return b.String()
}

func post(ctx context.Context, client *http.Client, base *url.URL, path, query string, body *bytes.Buffer) error {
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return nil
}
