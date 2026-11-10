package hosts

import (
	"bufio"
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

	"github.com/tpural/tremolo/internal/hoststate"
)

// Segment is a stretch of time a host spent in one state.
type Segment struct {
	State  hoststate.State `json:"state"`
	By     string          `json:"by,omitempty"`
	Reason string          `json:"reason,omitempty"`
	From   time.Time       `json:"from"`
	To     time.Time       `json:"to"`
}

// Power is a host's states over a window, and how its wakes went.
type Power struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Segments []Segment `json:"segments"`
	// Wakes counts the times it went from asleep or off to ready.
	Wakes int `json:"wakes"`
	// WakeMedian and WakeMax are how long those took, from leaving asleep or off to ready.
	WakeMedian time.Duration `json:"wakeMedian"`
	WakeMax    time.Duration `json:"wakeMax"`
	// Slept is when it last went to sleep or off; Woke when it last became ready after that.
	Slept time.Time `json:"slept,omitzero"`
	Woke  time.Time `json:"woke,omitzero"`
}

// maxChanges bounds how many state changes one window reads.
const maxChanges = 5000

func resting(s hoststate.State) bool { return s == hoststate.Asleep || s == hoststate.Off }

// Power returns host's states between from and to: its state at from, from the
// metrics store, then each recorded change.
func (h *History) Power(ctx context.Context, host string, from, to time.Time) (Power, error) {
	p := Power{From: from, To: to}
	if h.logs == nil {
		return p, errors.New("no logs store")
	}
	start, _, err := h.at(ctx, from)
	if err != nil {
		return p, err
	}
	cur := Segment{From: from} // an empty state: nothing recorded yet
	for _, s := range start {
		if s.Name == host {
			cur.State, cur.By, cur.Reason = s.State, s.StateBy, s.Reason
		}
	}
	changes, err := h.changes(ctx, host, from, to)
	if err != nil {
		return p, err
	}
	for _, c := range changes {
		if c.State == cur.State && c.By == cur.By && c.Reason == cur.Reason {
			continue
		}
		cur.To = c.From
		if cur.To.After(cur.From) {
			p.Segments = append(p.Segments, cur)
		}
		cur = c
	}
	cur.To = to
	p.Segments = append(p.Segments, cur)
	p.summarise()
	return p, nil
}

// summarise counts wakes: leaving asleep or off, then reaching ready.
func (p *Power) summarise() {
	var took []time.Duration
	var left time.Time
	for i, s := range p.Segments {
		if resting(s.State) {
			if i > 0 {
				p.Slept = s.From
			}
			left = time.Time{}
			if i+1 < len(p.Segments) {
				left = s.To
			}
			continue
		}
		if s.State == hoststate.Ready && !left.IsZero() {
			took = append(took, s.From.Sub(left))
			p.Woke, left = s.From, time.Time{}
		}
	}
	p.Wakes = len(took)
	if len(took) > 0 {
		slices.Sort(took)
		p.WakeMedian, p.WakeMax = took[len(took)/2], took[len(took)-1]
	}
}

// changes reads host's recorded state changes between from and to, oldest first.
func (h *History) changes(ctx context.Context, host string, from, to time.Time) ([]Segment, error) {
	u := *h.logs
	u.Path = strings.TrimRight(u.Path, "/") + "/select/logsql/query"
	q := fmt.Sprintf(`{stream=%q, host=%q} | sort by (_time) | fields _time, state, state_by, reason`, HistoryStream, host)
	u.RawQuery = url.Values{
		"query": {q}, "limit": {strconv.Itoa(maxChanges)},
		"start": {from.UTC().Format(time.RFC3339)}, "end": {to.UTC().Format(time.RFC3339)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("logs query: %s", resp.Status)
	}
	var out []Segment
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var row struct {
			Time   time.Time `json:"_time"`
			State  string    `json:"state"`
			By     string    `json:"state_by"`
			Reason string    `json:"reason"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		out = append(out, Segment{State: hoststate.State(row.State), By: row.By, Reason: row.Reason, From: row.Time})
	}
	slices.SortFunc(out, func(a, b Segment) int { return a.From.Compare(b.From) })
	return out, sc.Err()
}
