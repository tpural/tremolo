package alerts

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/tpural/tremolo/internal/victoria"
)

const (
	// HistoryStream is the logs stream of everything that happens to alerts.
	HistoryStream = "tremolo-alerts"
	// AlertMetric is each pending and firing alert, written to the metrics
	// store as it's seen; its value is when it started (Unix seconds).
	AlertMetric = "tremolo_alert"
)

// journal collects what happened to alerts, for the logs store.
type journal []map[string]string

// add logs ev on the alert's timeline and in the journal.
func (j *journal) add(a *Alert, ev Event) {
	a.log(ev)
	j.only(a, ev)
}

// only puts ev in the journal alone, with extra fields.
func (j *journal) only(a *Alert, ev Event, extra ...[2]string) {
	row := map[string]string{
		"_time": victoria.Time(ev.Time), "_msg": what(ev, extra) + ": " + a.Summary, "stream": HistoryStream,
		"event": ev.Kind, "alert": a.ID, "rule": a.Rule, "key": a.Key, "severity": a.Severity,
	}
	for _, kv := range append([][2]string{{"by", ev.By}, {"to", ev.To}, {"note", ev.Note}, {"assignee", a.Assignee}}, extra...) {
		if kv[1] != "" && (kv[0] != "note" || kv[1] != a.Summary) {
			row[kv[0]] = kv[1]
		}
	}
	for k, v := range a.Labels {
		if _, taken := row[k]; !taken {
			row[k] = v
		}
	}
	*j = append(*j, row)
}

func what(ev Event, extra [][2]string) string {
	about := ""
	if i := slices.IndexFunc(extra, func(kv [2]string) bool { return kv[0] == "about" }); i >= 0 {
		about = " (" + extra[i][1] + ")"
	}
	switch ev.Kind {
	case "notified":
		return "notified " + ev.To + about
	case "failed":
		return "couldn't notify " + ev.To + about + ": " + ev.Note
	case "acknowledged":
		if ev.By != "" {
			return "acknowledged by " + ev.By
		}
	case "forwarded":
		return fmt.Sprintf("forwarded to %s by %s", ev.To, ev.By)
	case "taken":
		return "taken by " + ev.By
	case "note":
		return fmt.Sprintf("note from %s: %s", ev.By, ev.Note)
	case "held":
		return "held by maintenance " + ev.Note
	case "released":
		return "released after maintenance " + ev.Note
	case "silenced", "unsilenced":
		if ev.By != "" {
			return ev.Kind + " by " + ev.By
		}
	}
	return ev.Kind
}

// write sends the journal to the logs store.
func (e *Engine) write(ctx context.Context, j journal) {
	if len(j) == 0 || e.opts.Data == nil || e.opts.Data.LogsURL == nil {
		return
	}
	if err := victoria.Insert(ctx, e.storage(), e.opts.Data.LogsURL, "stream,rule", j); err != nil {
		e.opts.Log.Warn("writing alert history", "err", err)
	}
}

// Export writes every pending and firing alert to the metrics store every so
// often until ctx ends.
func (e *Engine) Export(ctx context.Context, every time.Duration) {
	for {
		if err := e.export(ctx); err != nil && ctx.Err() == nil {
			e.opts.Log.Warn("writing alert metrics", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (e *Engine) export(ctx context.Context) error {
	if e.opts.Data == nil || e.opts.Data.MetricsURL == nil {
		return nil
	}
	now := e.now()
	var samples []victoria.Sample
	e.store.Read(func(s *saved) {
		for _, a := range s.Alerts {
			since := a.Since
			if a.State == "firing" && !a.FiringSince.IsZero() {
				since = a.FiringSince
			}
			silenced := slices.ContainsFunc(s.Silences, func(si Silence) bool { return si.matches(a, now) })
			samples = append(samples, victoria.Sample{Metric: AlertMetric, Value: float64(since.Unix()), Time: now, Labels: [][2]string{
				{"alert", a.ID},
				{"rule", a.Rule},
				{"severity", a.Severity},
				{"state", a.State},
				{"acked", strconv.FormatBool(!a.Acked.IsZero())},
				{"silenced", strconv.FormatBool(silenced)},
				{"assignee", a.Assignee},
				{"maintenance", strconv.FormatBool(a.Maintenance != "")},
			}})
		}
	})
	return victoria.Import(ctx, e.storage(), e.opts.Data.MetricsURL, samples)
}

// storage is the client for the metrics and logs stores.
func (e *Engine) storage() *http.Client {
	if e.opts.Data != nil && e.opts.Data.Client != nil {
		return e.opts.Data.Client
	}
	return http.DefaultClient
}
