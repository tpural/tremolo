package heartbeat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBeat(t *testing.T) {
	var pings, imports atomic.Int32
	var lastOK atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ping/secret":
			pings.Add(1)
		case "/api/v1/import/prometheus":
			imports.Add(1)
			b, _ := io.ReadAll(r.Body)
			lastOK.Store(string(b))
		case "/health":
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	var broken error
	h := New(Options{
		URL:        srv.URL + "/ping/secret",
		Client:     srv.Client(),
		MetricsURL: base,
		Log:        slog.New(slog.DiscardHandler),
		Checks: []Check{
			{Name: "metrics", Run: Health(srv.Client(), base)},
			{Name: "rules", Run: func(context.Context) error { return broken }},
		},
	})
	ctx := context.Background()

	h.Beat(ctx)
	if st := h.Status(); pings.Load() != 1 || st.Error != "" || st.Pinged.IsZero() {
		t.Fatalf("healthy: pings %d, status %+v", pings.Load(), st)
	}
	if m := lastOK.Load().(string); !strings.HasPrefix(m, Metric+"{} 1 ") {
		t.Fatalf("metric %q", m)
	}

	broken = errors.New("stuck")
	h.Beat(ctx)
	if st := h.Status(); pings.Load() != 1 || st.Error != "rules: stuck" {
		t.Fatalf("broken: pings %d, status %+v", pings.Load(), st)
	}
	if m := lastOK.Load().(string); !strings.HasPrefix(m, Metric+"{} 0 ") {
		t.Fatalf("metric %q", m)
	}

	// A watcher that refuses the ping counts as a failure; its URL stays out of the error.
	broken = nil
	h.opts.URL = srv.URL + "/wrong/secret"
	h.Beat(ctx)
	if st := h.Status(); !strings.Contains(st.Error, "404") || strings.Contains(st.Error, "secret") {
		t.Fatalf("refused: %+v", st)
	}
	h.opts.URL = "http://127.0.0.1:1/secret"
	h.Beat(ctx)
	if st := h.Status(); st.Error == "" || strings.Contains(st.Error, "secret") {
		t.Fatalf("unreachable: %+v", st)
	}
}
