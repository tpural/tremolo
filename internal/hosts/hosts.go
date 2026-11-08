// Package hosts lists every machine tremolo knows about: hosts that send
// metrics, Kubernetes nodes and machines a power manager has switched off.
package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/cluster"
)

// Unreachable hosts sent metrics recently but have stopped.
const Unreachable cluster.State = "unreachable"

// staleAfter is how long without metrics before a host counts as unreachable.
const staleAfter = 3 * time.Minute

// lastSeenQuery returns, per host, the time of its last host-metrics sample
// within the lookback, with the labels collectors add to say where it came from.
func lastSeenQuery(lookback time.Duration) string {
	return fmt.Sprintf(`max by (host_name, tremolo_collector, k8s_cluster_name, source) (tlast_over_time(system_cpu_time_seconds_total[%ds]))`,
		int64(lookback.Seconds()))
}

// seenCacheFor keeps the lookback query, which can span weeks, from running on every page refresh.
const seenCacheFor = 15 * time.Second

// Kind says how a host is collected.
type Kind string

const (
	// KindKubernetes hosts are cluster nodes, from tremolo-kubernetes-collector or the Kubernetes API.
	KindKubernetes Kind = "kubernetes"
	// KindServer hosts push their own metrics, e.g. with the host collector config.
	KindServer Kind = "server"
)

// Host is a machine, from whichever sources know about it.
type Host struct {
	cluster.Node
	Kind Kind `json:"kind"`
	// Cluster is the Kubernetes cluster's name, when the collector sets one.
	Cluster string `json:"cluster,omitempty"`
	// Source is the name of the ingest token the host's metrics arrive with.
	Source string `json:"source,omitempty"`
	// LastSeen is the last metrics sample, if the host sends any.
	LastSeen time.Time `json:"lastSeen,omitzero"`
}

// sighting is the latest host-metrics sample from one host.
type sighting struct {
	at                       time.Time
	collector, cluster, from string
}

// Lister combines metrics and, when available, the cluster.
type Lister struct {
	metrics  *url.URL
	client   *http.Client
	cluster  *cluster.Cluster
	lookback time.Duration

	mu      sync.Mutex
	seen    map[string]sighting
	checked time.Time
}

// New returns a Lister. Hosts that sent metrics within lookback are listed; one
// that has gone quiet shows as unreachable until its last sample is older than
// that. cl may be nil.
func New(metrics *url.URL, client *http.Client, cl *cluster.Cluster, lookback time.Duration) *Lister {
	return &Lister{metrics: metrics, client: client, cluster: cl, lookback: lookback}
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

func merge(nodes []cluster.Node, seen map[string]sighting, now time.Time) []Host {
	byName := map[string]*Host{}
	var out []*Host
	// Nodes from the Kubernetes API are all in tremolo's own cluster, whose name
	// only the collector knows; nodes that send nothing (switched off) borrow it.
	local := map[string]int{}
	for _, n := range nodes {
		if c := seen[n.Name].cluster; c != "" {
			local[c]++
		}
	}
	localName, most := "", 0
	for c, n := range local {
		if n > most || (n == most && c < localName) {
			localName, most = c, n
		}
	}
	for _, n := range nodes {
		s := seen[n.Name]
		if s.cluster == "" {
			s.cluster = localName
		}
		h := &Host{Node: n, Kind: KindKubernetes, Cluster: s.cluster, Source: s.from, LastSeen: s.at}
		byName[n.Name] = h
		out = append(out, h)
	}
	for name, s := range seen {
		if _, ok := byName[name]; ok || name == "" {
			continue
		}
		h := &Host{Node: cluster.Node{Name: name, State: cluster.Ready}, Kind: KindServer, Source: s.from, LastSeen: s.at}
		// A node of a cluster tremolo can't query: known only from its collector.
		if s.collector == string(KindKubernetes) {
			h.Kind, h.Cluster = KindKubernetes, s.cluster
		}
		if now.Sub(s.at) > staleAfter {
			h.State, h.Since = Unreachable, s.at
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

// ErrNoCluster means tremolo has no Kubernetes access.
var ErrNoCluster = errors.New("tremolo has no Kubernetes access")

// Pods lists the pods on a Kubernetes node.
func (l *Lister) Pods(ctx context.Context, node string) ([]cluster.Pod, error) {
	if l.cluster == nil {
		return nil, ErrNoCluster
	}
	return l.cluster.Pods(ctx, node)
}

func (l *Lister) lastSeen(ctx context.Context) (map[string]sighting, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.checked) < seenCacheFor {
		return l.seen, nil
	}
	seen, err := l.fetchSeen(ctx)
	if err != nil {
		return nil, err
	}
	l.seen, l.checked = seen, time.Now()
	return seen, nil
}

func (l *Lister) fetchSeen(ctx context.Context) (map[string]sighting, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := *l.metrics
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"
	u.RawQuery = url.Values{"query": {lastSeenQuery(l.lookback)}}.Encode()
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
	out := map[string]sighting{}
	for _, r := range body.Data.Result {
		s, _ := r.Value[1].(string)
		secs, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		name, at := r.Metric["host_name"], time.Unix(int64(secs), 0)
		// A host reported through more than one path counts as its latest.
		if prev, ok := out[name]; ok && !at.After(prev.at) {
			continue
		}
		out[name] = sighting{at: at, collector: r.Metric["tremolo_collector"], cluster: r.Metric["k8s_cluster_name"], from: r.Metric["source"]}
	}
	return out, nil
}
