package hosts

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/hoststate"
)

func TestPower(t *testing.T) {
	from := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	at := func(h int, m int) string {
		return from.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Format(time.RFC3339)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/query":
			// At the start of the window gpu01 was ready.
			fmt.Fprintf(w, `{"data":{"result":[{"metric":{"host_name":"gpu01","state":"ready","state_by":"kubernetes"},"value":[%d,"%d"]}]}}`, from.Unix(), from.Add(-time.Hour).Unix())
		case "/select/logsql/query":
			for _, row := range []struct{ t, state string }{
				{at(1, 0), "asleep"},
				{at(5, 0), "booting"},
				{at(5, 3), "ready"},
				{at(9, 0), "asleep"},
				{at(12, 0), "booting"},
				{at(12, 10), "ready"},
				{at(20, 0), "off"},
			} {
				fmt.Fprintf(w, `{"_time":%q,"state":%q,"state_by":"possum"}`+"\n", row.t, row.state)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := NewHistory(nil, u, u, srv.Client(), slog.New(slog.DiscardHandler))

	p, err := h.Power(context.Background(), "gpu01", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var got []hoststate.State
	for _, s := range p.Segments {
		got = append(got, s.State)
	}
	if fmt.Sprint(got) != "[ready asleep booting ready asleep booting ready off]" {
		t.Fatalf("segments %v", got)
	}
	if p.Wakes != 2 || p.WakeMedian != 10*time.Minute || p.WakeMax != 10*time.Minute {
		t.Fatalf("wakes %d, median %s, max %s", p.Wakes, p.WakeMedian, p.WakeMax)
	}
	if !p.Slept.Equal(from.Add(20*time.Hour)) || !p.Woke.Equal(from.Add(12*time.Hour+10*time.Minute)) {
		t.Fatalf("slept %s, woke %s", p.Slept, p.Woke)
	}
	if last := p.Segments[len(p.Segments)-1]; !last.To.Equal(from.Add(24 * time.Hour)) {
		t.Fatalf("last segment ends %s", last.To)
	}
}
