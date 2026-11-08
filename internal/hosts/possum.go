package hosts

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/cluster"
	"github.com/tpural/tremolo/internal/hoststate"
)

// Possum reads which Kubernetes nodes possum, a power autoscaler, has
// suspended, switched off or is waking.
type Possum struct {
	url    string
	client *http.Client
}

// NewPossum returns a provider for possum's status API at url.
func NewPossum(url string, client *http.Client) *Possum {
	return &Possum{url: strings.TrimRight(url, "/"), client: client}
}

// Name implements Provider.
func (*Possum) Name() string { return "possum" }

type possumMember struct {
	Group   string    `json:"group"`
	Name    string    `json:"name"`
	Phase   string    `json:"phase"`
	Since   time.Time `json:"since"`
	Message string    `json:"message"`
}

// Apply implements Provider. When possum can't be reached, hosts keep the
// state Kubernetes gave them.
func (p *Possum) Apply(ctx context.Context, hosts map[string]*Host) {
	members := p.members(ctx)
	// Switched-off nodes have no Node object; they're in the cluster the others are.
	local := ""
	for _, h := range hosts {
		if h.StateBy == "kubernetes" && h.Cluster != "" {
			local = h.Cluster
			break
		}
	}
	for _, m := range members {
		h, ok := hosts[m.Name]
		if !ok {
			h = &Host{Node: cluster.Node{Name: m.Name}, Kind: KindKubernetes, Cluster: local}
			hosts[m.Name] = h
		}
		if h.Details == nil {
			h.Details = map[string]string{}
		}
		h.Details["group"] = m.Group
		// A node that is ready is up, whatever possum last did.
		switch {
		case !ok && m.Phase == "Booting":
			h.State, h.Since, h.StateBy = hoststate.Booting, m.Since, p.Name()
		case !ok:
			h.State, h.Since, h.Reason, h.StateBy = hoststate.Off, m.Since, m.Message, p.Name()
		case m.Phase == "Off" && h.State != hoststate.Ready:
			h.State, h.Since, h.Reason, h.StateBy = hoststate.Asleep, m.Since, "", p.Name()
		case m.Phase == "Booting" && h.State != hoststate.Ready:
			h.State, h.Since, h.Reason, h.StateBy = hoststate.Booting, m.Since, "", p.Name()
		}
	}
}

func (p *Possum) members(ctx context.Context) []possumMember {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/api/status", http.NoBody)
	if err != nil {
		return nil
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Members []possumMember `json:"members"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	return body.Members
}
