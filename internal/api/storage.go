package api

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/storage"
)

// storageRoutes adds how full the stores are and what fills them.
func storageRoutes(mux *http.ServeMux, s *storage.Sampler, opts Options) {
	client := &http.Client{Transport: opts.Transport, Timeout: 20 * time.Second}
	mux.HandleFunc("GET /api/storage", func(w http.ResponseWriter, r *http.Request) {
		type named struct {
			Name  string  `json:"name"`
			Count float64 `json:"count"`
		}
		out := struct {
			Stores      []storage.Usage `json:"stores"`
			TotalSeries float64         `json:"totalSeries"`
			TopSeries   []named         `json:"topSeries"`
			TopStreams  []named         `json:"topStreams"`
			Errors      []string        `json:"errors,omitempty"`
		}{Stores: s.Usage(), TopSeries: []named{}, TopStreams: []named{}}
		var tsdb struct {
			Data struct {
				TotalSeries float64 `json:"totalSeries"`
				Top         []struct {
					Name  string  `json:"name"`
					Value float64 `json:"value"`
				} `json:"seriesCountByMetricName"`
			} `json:"data"`
		}
		if err := getJSON(r.Context(), client, opts.MetricsURL, "/api/v1/status/tsdb", url.Values{"topN": {"15"}}, &tsdb); err != nil {
			out.Errors = append(out.Errors, "top series: "+err.Error())
		}
		out.TotalSeries = tsdb.Data.TotalSeries
		for _, t := range tsdb.Data.Top {
			out.TopSeries = append(out.TopSeries, named{t.Name, t.Value})
		}
		streams, err := topStreams(r.Context(), client, opts.LogsURL)
		if err != nil {
			out.Errors = append(out.Errors, "top streams: "+err.Error())
		}
		for _, st := range streams {
			out.TopStreams = append(out.TopStreams, named{st.name, st.count})
		}
		writeJSON(w, out)
	})
}

type streamCount struct {
	name  string
	count float64
}

// topStreams is the log streams with the most lines in the last day.
func topStreams(ctx context.Context, client *http.Client, logs *url.URL) ([]streamCount, error) {
	q := url.Values{"query": {"_time:1d | stats by (_stream) count() as n | sort by (n desc) | limit 15"}}
	u := logs.JoinPath("/select/logsql/query")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("logs answered %s", resp.Status)
	}
	var out []streamCount
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var row struct {
			Stream string `json:"_stream"`
			N      string `json:"n"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		n, _ := strconv.ParseFloat(row.N, 64)
		out = append(out, streamCount{strings.TrimSpace(row.Stream), n})
	}
	slices.SortFunc(out, func(a, b streamCount) int { return cmp.Compare(b.count, a.count) })
	return out, sc.Err()
}

func getJSON(ctx context.Context, client *http.Client, base *url.URL, path string, q url.Values, v any) error {
	u := base.JoinPath(path)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
