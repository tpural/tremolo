// Package hosts lists every machine tremolo knows about: hosts that send
// metrics, Kubernetes nodes and machines a power manager has switched off.
package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/cluster"
)

// Unreachable hosts sent metrics recently but have stopped.
const Unreachable cluster.State = "unreachable"

// staleAfter is how long without metrics before a host counts as unreachable.
const staleAfter = 3 * time.Minute

// lastSeenQuery returns, per host, the time of its last host-metrics sample in a day.
const lastSeenQuery = `max by (host_name) (tlast_over_time(system_cpu_time_seconds_total[1d]))`

// Host is a machine, from whichever sources know about it.
type Host struct {
	cluster.Node
	// LastSeen is the last metrics sample, if the host sends any.
	LastSeen time.Time `json:"lastSeen,omitzero"`
	// Kubernetes is true for cluster nodes, including ones that are off.
	Kubernetes bool `json:"kubernetes"`
}

// Lister combines metrics and, when available, the cluster.
type Lister struct {
	metrics *url.URL
	client  *http.Client
	cluster *cluster.Cluster
}

// New returns a Lister. cl may be nil.
func New(metrics *url.URL, client *http.Client, cl *cluster.Cluster) *Lister {
	return &Lister{metrics: metrics, client: client, cluster: cl}
}

// List returns every host by name. Either source failing is tolerated, so a
// storage outage still shows the cluster and the other way round.
func (l *Lister) List(ctx context.Context) ([]Host, error) {
	seen, mErr := l.lastSeen(ctx)
	var nodes []cluster.Node
	var cErr error
	if l.cluster != nil {
		nodes, cErr = l.cluster.Nodes(ctx)
	}
	if mErr != nil && (l.cluster == nil || cErr != nil) {
		return nil, fmt.Errorf("metrics: %w", mErr)
	}
	return merge(nodes, seen, time.Now()), nil
}

func merge(nodes []cluster.Node, seen map[string]time.Time, now time.Time) []Host {
	byName := map[string]*Host{}
	var out []*Host
	for _, n := range nodes {
		h := &Host{Node: n, Kubernetes: true, LastSeen: seen[n.Name]}
		byName[n.Name] = h
		out = append(out, h)
	}
	for name, t := range seen {
		if _, ok := byName[name]; ok || name == "" {
			continue
		}
		h := &Host{Node: cluster.Node{Name: name, State: cluster.Ready}, LastSeen: t}
		if now.Sub(t) > staleAfter {
			h.State, h.Since = Unreachable, t
		}
		out = append(out, h)
	}
	hosts := make([]Host, 0, len(out))
	for _, h := range out {
		hosts = append(hosts, *h)
	}
	slices.SortFunc(hosts, func(a, b Host) int { return strings.Compare(a.Name, b.Name) })
	return hosts
}

func (l *Lister) lastSeen(ctx context.Context) (map[string]time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := *l.metrics
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"
	u.RawQuery = url.Values{"query": {lastSeenQuery}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, r := range body.Data.Result {
		s, _ := r.Value[1].(string)
		secs, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		out[r.Metric["host_name"]] = time.Unix(int64(secs), 0)
	}
	return out, nil
}
