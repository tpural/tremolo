package alerts

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/alerts/notify"
)

// ErrNoDigest means the alerts config has no digest.
var ErrNoDigest = errors.New("no digest is set up in the alerts config")

// RunDigest sends the weekly digest when it's due, until ctx ends.
func (e *Engine) RunDigest(ctx context.Context) {
	d := e.opts.Config.Digest
	if d == nil {
		return
	}
	for {
		next := d.Next(e.now(), e.opts.Config.Location())
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		if err := e.SendDigest(ctx, "tremolo"); err != nil && ctx.Err() == nil {
			e.opts.Log.Warn("weekly digest", "err", err)
		}
	}
}

// SendDigest sends the digest now to its channels.
func (e *Engine) SendDigest(ctx context.Context, by string) error {
	d := e.opts.Config.Digest
	if d == nil {
		return ErrNoDigest
	}
	n := e.Digest(ctx)
	var errs []error
	for _, ch := range d.Notify {
		now := e.now()
		err := e.opts.Config.providers[ch].Send(ctx, n)
		e.delivered(ch, "digest", now, err)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ch, err))
		}
	}
	e.opts.Log.Info("sent the weekly digest", "by", by, "channels", d.Notify, "failed", len(errs))
	return errors.Join(errs...)
}

// Digest is the last week in a few lines: alerts, what's firing, backups and
// heartbeat, disks filling, storage growth, and runbook fixes.
func (e *Engine) Digest(ctx context.Context) notify.Notification {
	now := e.now()
	since := now.Add(-7 * 24 * time.Hour)
	var lines []string
	lines = append(lines, e.weekOfAlerts(ctx, since))
	if snap := e.Snapshot(); len(snap.Alerts) > 0 {
		firing := 0
		for _, a := range snap.Alerts {
			if a.State == "firing" {
				firing++
			}
		}
		if firing > 0 {
			lines = append(lines, fmt.Sprintf("Firing now: %d.", firing))
		}
	}
	for _, l := range []string{e.health(ctx), e.disks(ctx), e.storageGrowth(ctx), e.fixes(ctx, since)} {
		if l != "" {
			lines = append(lines, l)
		}
	}
	n := notify.Notification{Event: "digest", Rule: "digest", Severity: "info", Since: now, Summary: strings.Join(lines, "\n")}
	if u := strings.TrimRight(e.opts.Config.URL, "/"); u != "" {
		n.URL = u + "/alerts"
	}
	return n
}

func (e *Engine) weekOfAlerts(ctx context.Context, since time.Time) string {
	if e.opts.DB == nil {
		return "No alert history is kept."
	}
	rows, err := e.opts.DB.SQL().QueryContext(ctx, `SELECT rule, severity, fired, acked FROM alert_history WHERE resolved >= ?`, since.UnixMilli())
	if err != nil {
		return "Couldn't read the alert history: " + err.Error()
	}
	defer rows.Close()
	bySev, byRule := map[string]int{}, map[string]int{}
	var acks []time.Duration
	total := 0
	for rows.Next() {
		var rule, sev string
		var fired int64
		var acked *int64
		if rows.Scan(&rule, &sev, &fired, &acked) != nil {
			continue
		}
		total++
		bySev[sev]++
		byRule[rule]++
		if acked != nil && fired > 0 {
			acks = append(acks, time.Duration(*acked-fired)*time.Millisecond)
		}
	}
	if total == 0 {
		return "No alerts in the last 7 days."
	}
	var parts []string
	for _, sev := range Severities {
		if n := bySev[sev]; n > 0 {
			parts = append(parts, plural(n, map[string]string{"page": "page", "warn": "warning", "info": "notice"}[sev]))
		}
	}
	rules := make([]string, 0, len(byRule))
	for r := range byRule {
		rules = append(rules, r)
	}
	slices.SortFunc(rules, func(a, b string) int { return cmp.Or(cmp.Compare(byRule[b], byRule[a]), cmp.Compare(a, b)) })
	s := fmt.Sprintf("Last 7 days: %s from %s; most from %s (%d).", strings.Join(parts, ", "), plural(len(byRule), "rule"), rules[0], byRule[rules[0]])
	if len(acks) > 0 {
		var sum time.Duration
		for _, a := range acks {
			sum += a
		}
		s += fmt.Sprintf(" Acknowledged in %s on average.", notifyHuman(sum/time.Duration(len(acks))))
	}
	return s
}

func (e *Engine) health(ctx context.Context) string {
	var parts []string
	if v, ok := e.instant(ctx, "max(last_over_time(tremolo_backup_age_seconds[15m]))"); ok {
		parts = append(parts, fmt.Sprintf("newest backup %s old", notifyHuman(time.Duration(v)*time.Second)))
	}
	if v, ok := e.instant(ctx, "min(min_over_time(tremolo_heartbeat_ok[7d]))"); ok {
		if v == 1 {
			parts = append(parts, "heartbeat sent all week")
		} else {
			parts = append(parts, "heartbeat missed at times this week")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, ", ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func (e *Engine) disks(ctx context.Context) string {
	q := `sort_desc(max by (host_name, mountpoint) (sum by (host_name, mountpoint) (system_filesystem_usage_bytes{state="used", type!~"tmpfs|overlay|squashfs|nsfs|ramfs"}) / sum by (host_name, mountpoint) (system_filesystem_usage_bytes{type!~"tmpfs|overlay|squashfs|nsfs|ramfs"})) > 0.7)`
	rows, err := e.vector(ctx, q)
	if err != nil || len(rows) == 0 {
		return ""
	}
	var parts []string
	for _, r := range rows[:min(5, len(rows))] {
		parts = append(parts, fmt.Sprintf("%s %s %d%%", r.labels["host_name"], r.labels["mountpoint"], int(r.value*100)))
	}
	return "Disks over 70%: " + strings.Join(parts, ", ") + "."
}

func (e *Engine) storageGrowth(ctx context.Context) string {
	now, err := e.vector(ctx, "max by (store) (tremolo_storage_data_bytes)")
	if err != nil || len(now) == 0 {
		return ""
	}
	grew, _ := e.vector(ctx, "max by (store) (tremolo_storage_data_bytes) - max by (store) (tremolo_storage_data_bytes offset 7d)")
	growth := map[string]float64{}
	for _, g := range grew {
		growth[g.labels["store"]] = g.value
	}
	slices.SortFunc(now, func(a, b point) int { return cmp.Compare(a.labels["store"], b.labels["store"]) })
	var parts []string
	for _, s := range now {
		p := fmt.Sprintf("%s %s", s.labels["store"], bytes(s.value))
		if g, ok := growth[s.labels["store"]]; ok {
			p += fmt.Sprintf(" (%s%s this week)", map[bool]string{true: "+", false: "-"}[g >= 0], bytes(abs(g)))
		}
		parts = append(parts, p)
	}
	return "Storage: " + strings.Join(parts, ", ") + "."
}

// fixes counts the fixes added to runbooks this week, when tremolo keeps runbooks.
func (e *Engine) fixes(ctx context.Context, since time.Time) string {
	if e.opts.DB == nil {
		return ""
	}
	var n int
	err := e.opts.DB.SQL().QueryRowContext(ctx, `SELECT count(*) FROM alert_history WHERE resolved >= ? AND fix != ''`, since.UnixMilli()).Scan(&n)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("%s noted how they were fixed.", plural(n, "alert"))
}

type point struct {
	labels map[string]string
	value  float64
}

func (e *Engine) instant(ctx context.Context, q string) (float64, bool) {
	rows, err := e.vector(ctx, q)
	if err != nil || len(rows) == 0 {
		return 0, false
	}
	return rows[0].value, true
}

func (e *Engine) vector(ctx context.Context, q string) ([]point, error) {
	if e.opts.Data == nil || e.opts.Data.MetricsURL == nil || e.opts.Data.Client == nil {
		return nil, errNoStore
	}
	u := e.opts.Data.MetricsURL.JoinPath("/api/v1/query")
	u.RawQuery = url.Values{"query": {q}, "time": {strconv.FormatInt(e.now().Unix(), 10)}}.Encode()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.opts.Data.Client.Do(req)
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
	var out []point
	for _, r := range body.Data.Result {
		s, _ := r.Value[1].(string)
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			out = append(out, point{r.Metric, v})
		}
	}
	return out, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func bytes(b float64) string {
	for _, u := range []string{"B", "KB", "MB", "GB", "TB"} {
		if b < 1000 || u == "TB" {
			if u == "B" {
				return fmt.Sprintf("%.0f B", b)
			}
			return fmt.Sprintf("%.1f %s", b, u)
		}
		b /= 1000
	}
	return ""
}

func abs(f float64) float64 { return max(f, -f) }

// notifyHuman writes a duration in minutes, hours or days.
func notifyHuman(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}
