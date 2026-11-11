package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/users"
)

// Limits keep one query from tying up the stores: how far back it may look,
// how many points and log rows it may return, how long it may run, and how
// many may run at once for one user or token.
type Limits struct {
	MaxRange   time.Duration
	MaxPoints  int
	MaxLogRows int
	Timeout    time.Duration
	Concurrent int
}

// DefaultLimits suit a homelab's stores; assistants get tighter ones.
var (
	DefaultLimits   = Limits{MaxRange: 31 * 24 * time.Hour, MaxPoints: 11000, MaxLogRows: 2000, Timeout: 30 * time.Second, Concurrent: 8}
	AssistantLimits = Limits{MaxRange: 7 * 24 * time.Hour, MaxPoints: 11000, MaxLogRows: 500, Timeout: 30 * time.Second, Concurrent: 2}
)

// defaultWindow is how far back a query without a start looks.
const defaultWindow = time.Hour

// queueWait is how long a query waits for a free slot before it's refused.
const queueWait = 10 * time.Second

// lookbehind finds MetricsQL windows like [5m] or [1h:1m] and offsets.
var (
	lookbehind = regexp.MustCompile(`\[\s*([0-9][0-9a-zA-Z.]*)\s*(?::[^\]]*)?\]`)
	offset     = regexp.MustCompile(`(?i)\boffset\s+(-?[0-9][0-9a-zA-Z.]*)`)
)

// slots counts running queries by who's asking.
type slots struct {
	mu   sync.Mutex
	busy map[string]chan struct{}
}

func (s *slots) take(ctx context.Context, who string, n int) (func(), bool) {
	s.mu.Lock()
	if s.busy == nil {
		s.busy = map[string]chan struct{}{}
	}
	ch, ok := s.busy[who]
	if !ok || cap(ch) != n {
		ch = make(chan struct{}, n)
		s.busy[who] = ch
	}
	s.mu.Unlock()
	t := time.NewTimer(queueWait)
	defer t.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// limited applies the request's limits to a query of signal at target, then
// passes it on. It rewrites the query string in place: a missing start, too
// many points or rows. Anything it can't make fit is refused with why.
func limited(opts Options, signal, target string, running *slots, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lim := opts.Limits
		key := who(r)
		if t, ok := token(r); ok {
			key = "token:" + t.ID
			if t.Scope != nil {
				lim = opts.AssistantLimits
			}
		}
		q := r.URL.Query()
		before := q.Encode()
		if err := fit(lim, signal, target, q, time.Now()); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		if after := q.Encode(); after != before {
			r = r.Clone(r.Context())
			r.URL.RawQuery = after
		}
		if s := scope(r); s != nil {
			auditRead(opts, r, signal, q.Get("query"))
		}
		// A live tail runs until it's closed; it doesn't take a slot or time out.
		if strings.HasSuffix(target, "/tail") {
			h.ServeHTTP(w, r)
			return
		}
		if lim.Concurrent > 0 {
			release, ok := running.take(r.Context(), key, lim.Concurrent)
			if !ok {
				apiError(w, http.StatusTooManyRequests, fmt.Sprintf("too many queries at once (%d); wait for one to finish", lim.Concurrent))
				return
			}
			defer release()
		}
		if lim.Timeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), lim.Timeout)
			defer cancel()
			r = r.WithContext(context.WithValue(ctx, timeoutKey{}, lim.Timeout))
		}
		h.ServeHTTP(w, r)
	})
}

type timeoutKey struct{}

// timedOut is why a proxied query failed when it ran out of time, if it did.
func timedOut(r *http.Request, err error) (string, bool) {
	d, ok := r.Context().Value(timeoutKey{}).(time.Duration)
	if !ok || !errors.Is(err, context.DeadlineExceeded) {
		return "", false
	}
	return fmt.Sprintf("the query took longer than %s; narrow its time range or filters", d), true
}

// fit makes q fit lim, or says why it can't.
func fit(lim Limits, signal, target string, q url.Values, now time.Time) error {
	if signal == users.Metrics {
		return fitMetrics(lim, target, q, now)
	}
	if strings.HasSuffix(target, "/tail") || strings.HasPrefix(target, "/select/jaeger/") {
		return nil
	}
	start, end, err := window(q, now)
	if err != nil {
		return err
	}
	if lim.MaxRange > 0 && end.Sub(start) > lim.MaxRange {
		return fmt.Errorf("the time range is over %s; narrow it", human(lim.MaxRange))
	}
	if strings.HasSuffix(target, "/query") && lim.MaxLogRows > 0 {
		if n, err := strconv.Atoi(q.Get("limit")); err != nil || n <= 0 || n > lim.MaxLogRows {
			q.Set("limit", strconv.Itoa(lim.MaxLogRows))
		}
	}
	return nil
}

func fitMetrics(lim Limits, target string, q url.Values, now time.Time) error {
	for _, m := range lookbehind.FindAllStringSubmatch(q.Get("query"), -1) {
		if d, err := parseDuration(m[1]); err == nil && lim.MaxRange > 0 && d > lim.MaxRange {
			return fmt.Errorf("the window [%s] is over %s; narrow it", m[1], human(lim.MaxRange))
		}
	}
	for _, m := range offset.FindAllStringSubmatch(q.Get("query"), -1) {
		if d, err := parseDuration(strings.TrimPrefix(m[1], "-")); err == nil && lim.MaxRange > 0 && d > lim.MaxRange {
			return fmt.Errorf("the offset %s is over %s; narrow it", m[1], human(lim.MaxRange))
		}
	}
	if !strings.HasSuffix(target, "/query_range") {
		return nil
	}
	start, end, err := window(q, now)
	if err != nil {
		return err
	}
	span := end.Sub(start)
	if lim.MaxRange > 0 && span > lim.MaxRange {
		return fmt.Errorf("the time range is over %s; narrow it", human(lim.MaxRange))
	}
	if lim.MaxPoints <= 0 {
		return nil
	}
	step, err := parseDuration(q.Get("step"))
	if err != nil || step <= 0 {
		step = time.Duration(math.Ceil(span.Seconds()/float64(lim.MaxPoints))) * time.Second
	}
	if min := time.Duration(math.Ceil(span.Seconds()/float64(lim.MaxPoints))) * time.Second; step < min {
		step = min
	}
	if step < time.Second {
		step = time.Second
	}
	q.Set("step", strconv.FormatInt(int64(step/time.Second), 10)+"s")
	return nil
}

// window reads a query's start and end, setting start when it's missing.
func window(q url.Values, now time.Time) (time.Time, time.Time, error) {
	end := now
	if v := q.Get("end"); v != "" {
		t, err := parseTime(v, now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("can't read end %q: %w", v, err)
		}
		end = t
	}
	if v := q.Get("start"); v != "" {
		t, err := parseTime(v, now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("can't read start %q: %w", v, err)
		}
		return t, end, nil
	}
	start := end.Add(-defaultWindow)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	return start, end, nil
}

// parseTime reads the times Victoria accepts: Unix seconds (or milli-, micro-
// or nanoseconds), RFC 3339, or a duration before now such as 1h or -1h.
func parseTime(v string, now time.Time) (time.Time, error) {
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		switch a := math.Abs(f); {
		case a >= 1e17:
			return time.Unix(0, int64(f)), nil
		case a >= 1e14:
			return time.UnixMicro(int64(f)), nil
		case a >= 1e11:
			return time.UnixMilli(int64(f)), nil
		}
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(frac*1e9)), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	if d, err := parseDuration(strings.TrimPrefix(v, "-")); err == nil {
		return now.Add(-d), nil
	}
	if v == "now" {
		return now, nil
	}
	return time.Time{}, errors.New("not a time")
}

// parseDuration reads Prometheus-style durations, which also have d, w and y.
func parseDuration(v string) (time.Duration, error) {
	if v == "" {
		return 0, errors.New("empty")
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(f * float64(time.Second)), nil
	}
	var total time.Duration
	rest := v
	for rest != "" {
		i := strings.IndexFunc(rest, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
		if i <= 0 {
			return 0, fmt.Errorf("bad duration %q", v)
		}
		n, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil {
			return 0, err
		}
		rest = rest[i:]
		j := strings.IndexFunc(rest, func(r rune) bool { return r >= '0' && r <= '9' })
		if j < 0 {
			j = len(rest)
		}
		unit := map[string]time.Duration{
			"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour,
			"d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "y": 365 * 24 * time.Hour,
		}[rest[:j]]
		if unit == 0 {
			return 0, fmt.Errorf("bad duration %q", v)
		}
		total += time.Duration(n * float64(unit))
		rest = rest[j:]
	}
	return total, nil
}

// human writes a duration in days when it's whole days.
func human(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return d.String()
}

// reads remembers recent assistant reads, so a page that polls is logged once a minute.
var reads = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

// auditRead records what an assistant token read.
func auditRead(opts Options, r *http.Request, signal, query string) {
	t, _ := token(r)
	key := t.ID + " " + r.URL.Path + " " + query
	now := time.Now()
	reads.Lock()
	if last, ok := reads.seen[key]; ok && now.Sub(last) < time.Minute {
		reads.Unlock()
		return
	}
	reads.seen[key] = now
	if len(reads.seen) > 10000 {
		for k, at := range reads.seen {
			if now.Sub(at) > time.Minute {
				delete(reads.seen, k)
			}
		}
	}
	reads.Unlock()
	audit(opts, r, "", "read "+signal, strings.TrimPrefix(r.URL.Path, "/api/"), query)
}
