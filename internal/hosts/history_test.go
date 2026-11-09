package hosts

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/hoststate"
)

// store is a tiny metrics and logs store: it keeps imported samples, answers
// the two queries History makes, and keeps log events.
type store struct {
	mu      sync.Mutex
	samples []sample
	events  []map[string]string
}

type sample struct {
	labels map[string]string
	value  float64
	ms     int64
}

var (
	line = regexp.MustCompile(`^(\w+)\{(.*)\} (\S+) (\d+)$`)
	pair = regexp.MustCompile(`(\w+)="((?:[^"\\]|\\.)*)"`)
)

func (s *store) serve(t *testing.T) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/import/prometheus":
			sc := bufio.NewScanner(r.Body)
			for sc.Scan() {
				m := line.FindStringSubmatch(sc.Text())
				if m == nil {
					t.Errorf("bad line %q", sc.Text())
					continue
				}
				labels := map[string]string{}
				for _, p := range pair.FindAllStringSubmatch(m[2], -1) {
					labels[p[1]], _ = strconv.Unquote(`"` + p[2] + `"`)
				}
				v, _ := strconv.ParseFloat(m[3], 64)
				ms, _ := strconv.ParseInt(m[4], 10, 64)
				s.samples = append(s.samples, sample{labels, v, ms})
			}
		case "/insert/jsonline":
			b, _ := io.ReadAll(r.Body)
			for l := range strings.Lines(string(b)) {
				var ev map[string]string
				_ = json.Unmarshal([]byte(l), &ev)
				s.events = append(s.events, ev)
			}
		case "/api/v1/query":
			q := r.URL.Query().Get("query")
			at, _ := strconv.ParseInt(r.URL.Query().Get("time"), 10, 64)
			var result []string
			if strings.Contains(q, StateMetric) {
				// The newest sample of each series in the window up to the time.
				newest := map[string]sample{}
				for _, sm := range s.samples {
					if sm.ms > (at+1)*1000 || sm.ms <= (at+1)*1000-window.Milliseconds() {
						continue
					}
					k := key(sm.labels)
					if sm.ms >= newest[k].ms {
						newest[k] = sm
					}
				}
				for _, sm := range newest {
					v := sm.value
					if strings.HasPrefix(q, "timestamp(") {
						v = float64(sm.ms) / 1000
					}
					m, _ := json.Marshal(sm.labels)
					result = append(result, fmt.Sprintf(`{"metric":%s,"value":[%d,"%v"]}`, m, at, v))
				}
			} else {
				// Every host sends metrics.
				for _, h := range []string{"nas", "pi-1"} {
					result = append(result, fmt.Sprintf(`{"metric":{"host_name":%q},"value":[%d,"%d"]}`, h, at, time.Now().Unix()))
				}
			}
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(result, ","))
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

// asleep puts pi-1 to sleep when set.
type asleep struct{ on *bool }

func (asleep) Name() string { return "possum" }
func (a asleep) Apply(_ context.Context, hosts map[string]*Host) {
	if *a.on {
		h := hosts["pi-1"]
		h.State, h.StateBy, h.Reason, h.Since = hoststate.Asleep, "possum", "idle", time.Unix(1_700_000_000, 0)
	}
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	s := &store{}
	u := s.serve(t)
	sleeping := false
	lister := New(u, http.DefaultClient, nil, time.Hour, asleep{&sleeping})
	h := NewHistory(lister, u, u, http.DefaultClient, slog.New(slog.DiscardHandler))

	if _, ok, _ := h.At(ctx, time.Now()); ok {
		t.Fatal("history before anything was recorded")
	}
	if err := h.Record(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.events) != 2 || s.events[0]["stream"] != HistoryStream {
		t.Fatalf("first record: events %v", s.events)
	}
	time.Sleep(20 * time.Millisecond)

	// pi-1 falls asleep: one event, from ready to asleep, and a new series.
	sleeping = true
	if err := h.Record(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.events) != 3 || s.events[2]["host"] != "pi-1" || s.events[2]["from"] != "ready" || s.events[2]["state"] != "asleep" ||
		s.events[2]["_msg"] != "pi-1 went from ready to asleep: idle" {
		t.Fatalf("change: events %v", s.events[2:])
	}
	// Recording again with nothing changed adds no events, even after a restart.
	h2 := NewHistory(lister, u, u, http.DefaultClient, slog.New(slog.DiscardHandler))
	if err := h2.Record(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.events) != 3 {
		t.Errorf("restart added events: %v", s.events[3:])
	}

	// Reading back: now pi-1 is asleep, with possum's reason and since.
	list, ok, err := h.At(ctx, time.Now())
	if err != nil || !ok || len(list) != 2 {
		t.Fatalf("at now = %+v %v %v", list, ok, err)
	}
	pi := list[1]
	if pi.Name != "pi-1" || pi.State != hoststate.Asleep || pi.StateBy != "possum" || pi.Reason != "idle" || pi.Since.Unix() != 1_700_000_000 {
		t.Errorf("pi-1 now = %+v", pi)
	}
}
