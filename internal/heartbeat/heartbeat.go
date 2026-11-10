// Package heartbeat pings a URL while alerting works end to end, so a watcher
// outside tremolo (e.g. Healthchecks.io) can page when the pings stop.
package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/victoria"
)

// Metric is 1 when the last round's checks passed and its ping was answered, else 0.
const Metric = "tremolo_heartbeat_ok"

// Check is one thing alerting needs.
type Check struct {
	Name string
	Run  func(context.Context) error
}

// Status is how the last round went.
type Status struct {
	Checked time.Time `json:"checked,omitzero"`
	// Pinged is the last time the watcher was told all is well.
	Pinged time.Time `json:"pinged,omitzero"`
	Error  string    `json:"error,omitempty"`
}

// Options configures a Heartbeat.
type Options struct {
	// URL is pinged with GET; it's a credential, so it's never logged.
	URL    string
	Checks []Check
	Client *http.Client
	// MetricsURL, when set, gets Metric after each round.
	MetricsURL *url.URL
	Log        *slog.Logger
}

// Heartbeat runs the checks and pings when they all pass.
type Heartbeat struct {
	opts   Options
	now    func() time.Time
	mu     sync.Mutex
	status Status
}

// New returns a Heartbeat.
func New(opts Options) *Heartbeat { return &Heartbeat{opts: opts, now: time.Now} }

// Status returns how the last round went.
func (h *Heartbeat) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

// Run beats every interval until ctx ends. The first beat waits an interval too,
// so rules have loaded and run before they're judged.
func (h *Heartbeat) Run(ctx context.Context, every time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		h.Beat(ctx)
	}
}

// Beat runs the checks once and pings if they pass.
func (h *Heartbeat) Beat(ctx context.Context) {
	err := h.check(ctx)
	if err == nil {
		err = h.ping(ctx)
	}
	if ctx.Err() != nil {
		return
	}
	now := h.now()
	h.mu.Lock()
	failing := h.status.Error != ""
	h.status.Checked, h.status.Error = now, ""
	if err == nil {
		h.status.Pinged = now
	} else {
		h.status.Error = err.Error()
	}
	h.mu.Unlock()
	switch {
	case err != nil:
		h.opts.Log.Warn("heartbeat not sent", "err", err)
	case failing:
		h.opts.Log.Info("heartbeat sent again")
	}
	if h.opts.MetricsURL != nil {
		ok := 0.0
		if err == nil {
			ok = 1
		}
		_ = victoria.Import(ctx, h.opts.Client, h.opts.MetricsURL, []victoria.Sample{{Metric: Metric, Value: ok, Time: now}})
	}
}

func (h *Heartbeat) check(ctx context.Context) error {
	var errs []string
	for _, c := range h.opts.Checks {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.Run(cctx)
		cancel()
		if err != nil {
			errs = append(errs, c.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (h *Heartbeat) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.opts.URL, nil)
	if err != nil {
		return errors.New("ping: bad URL")
	}
	resp, err := h.opts.Client.Do(req)
	if err != nil {
		// A url.Error repeats the URL; keep only what went wrong.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return fmt.Errorf("ping: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ping: answered %s", resp.Status)
	}
	return nil
}

// Health checks that a Victoria server's /health answers OK.
func Health(client *http.Client, base *url.URL) func(context.Context) error {
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.JoinPath("/health").String(), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ue, ok := errors.AsType[*url.Error](err); ok {
				err = ue.Err
			}
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("health answered %s", resp.Status)
		}
		return nil
	}
}
