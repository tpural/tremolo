package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/alerts/notify"
)

func TestDigestNext(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	d := &Digest{At: "Mon 08:00", Notify: Targets{"x"}}
	if err := d.parse(map[string]notify.Provider{"x": nil}); err != nil {
		t.Fatal(err)
	}
	// Tuesday: next Monday. Monday 07:59: today. Monday 08:00: next week.
	for at, want := range map[string]string{
		"2026-11-10T12:00:00-06:00": "2026-11-16T08:00:00-06:00",
		"2026-11-09T07:59:00-06:00": "2026-11-09T08:00:00-06:00",
		"2026-11-09T08:00:00-06:00": "2026-11-16T08:00:00-06:00",
	} {
		now, _ := time.Parse(time.RFC3339, at)
		if got := d.Next(now, loc).Format(time.RFC3339); got != want {
			t.Errorf("after %s: %s, want %s", at, got, want)
		}
	}
	for _, bad := range []string{"Monday", "Funday 08:00", "Mon 25:00", ""} {
		if err := (&Digest{At: bad, Notify: Targets{"x"}}).parse(map[string]notify.Provider{"x": nil}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := (&Digest{At: "Mon 08:00", Notify: Targets{"nope"}}).parse(map[string]notify.Provider{"x": nil}); err == nil {
		t.Error("accepted an unknown channel")
	}
}

func TestDigest(t *testing.T) {
	var mu sync.Mutex
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hook":
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			sent = append(sent, m)
			mu.Unlock()
		case "/api/v1/query":
			q := r.URL.Query().Get("query")
			switch {
			case strings.Contains(q, "tremolo_backup_age_seconds"):
				_, _ = io.WriteString(w, `{"data":{"result":[{"metric":{},"value":[0,"18000"]}]}}`)
			case strings.Contains(q, "offset 7d"):
				_, _ = io.WriteString(w, `{"data":{"result":[{"metric":{"store":"metrics"},"value":[0,"3000000"]}]}}`)
			case strings.Contains(q, "tremolo_storage_data_bytes"):
				_, _ = io.WriteString(w, `{"data":{"result":[{"metric":{"store":"metrics"},"value":[0,"91000000"]}]}}`)
			case strings.Contains(q, "system_filesystem_usage_bytes"):
				_, _ = io.WriteString(w, `{"data":{"result":[{"metric":{"host_name":"mm01","mountpoint":"/"},"value":[0,"0.74"]}]}}`)
			default:
				_, _ = io.WriteString(w, `{"data":{"result":[]}}`)
			}
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	e := engine(t, &Data{MetricsURL: u, Client: srv.Client()}, `
timezone: UTC
digest: {at: "Mon 08:00", notify: hook}
channels:
  hook: {type: webhook, url: "`+srv.URL+`/hook"}
`)
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	db := e.opts.DB.SQL()
	for i, row := range []struct {
		rule, sev string
		ack       bool
	}{{"crash-loop", "warn", true}, {"crash-loop", "warn", false}, {"host-down", "page", true}} {
		fired := now.Add(-time.Duration(i+1) * 24 * time.Hour)
		var acked any
		if row.ack {
			acked = fired.Add(10 * time.Minute).UnixMilli()
		}
		if _, err := db.Exec(`INSERT INTO alert_history (alert, rule, severity, summary, fired, resolved, acked, acked_by, assignee, data)
			VALUES ('a', ?, ?, 's', ?, ?, ?, '', '', '{}')`, row.rule, row.sev, fired.UnixMilli(), fired.Add(time.Hour).UnixMilli(), acked); err != nil {
			t.Fatal(err)
		}
	}
	text := e.Digest(context.Background()).Text()
	for _, want := range []string{
		"Last 7 days: 1 page, 2 warnings from 2 rules; most from crash-loop (2). Acknowledged in 10m on average.",
		"Newest backup 5h00m old.",
		"Disks over 70%: mm01 / 74%.",
		"Storage: metrics 91.0 MB (+3.0 MB this week).",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("digest lacks %q:\n%s", want, text)
		}
	}
	if err := e.SendDigest(context.Background(), "tim"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0]["event"] != "digest" {
		t.Fatalf("sent %v", sent)
	}
	if snap := e.Snapshot(); snap.Digest == nil || !snap.Digest.Next.Equal(now.AddDate(0, 0, 7)) {
		t.Fatalf("snapshot digest %+v", snap.Digest)
	}
}
