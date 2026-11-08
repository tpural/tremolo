// Package cluster reports the state of every node, including the ones a power
// manager such as possum has put to sleep or switched off.
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// State is a node's condition as a person would describe it.
type State string

const (
	Ready    State = "ready"
	NotReady State = "notready"
	// Asleep is suspended by a power manager; its Node object is kept.
	Asleep State = "asleep"
	// Off is powered off by a power manager; it has no Node object.
	Off State = "off"
	// Booting is being powered on.
	Booting State = "booting"
)

// TaintAsleep marks nodes possum has suspended.
const TaintAsleep = "possum.dev/asleep"

const cacheFor = 5 * time.Second

// Node is one machine in the cluster.
type Node struct {
	Name    string    `json:"name"`
	State   State     `json:"state"`
	Reason  string    `json:"reason,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	Group   string    `json:"group,omitempty"`
	Roles   []string  `json:"roles,omitempty"`
	Arch    string    `json:"arch,omitempty"`
	CPU     int64     `json:"cpu,omitempty"`
	Memory  int64     `json:"memoryBytes,omitempty"`
	Pods    int       `json:"pods"`
	Kubelet string    `json:"kubelet,omitempty"`
	Address string    `json:"address,omitempty"`
}

// Cluster reads node state from Kubernetes and, optionally, possum.
type Cluster struct {
	kube      kubernetes.Interface
	possumURL string
	client    *http.Client

	mu      sync.Mutex
	nodes   []Node
	checked time.Time
}

// New returns a Cluster. possumURL may be empty.
func New(kube kubernetes.Interface, possumURL string, client *http.Client) *Cluster {
	return &Cluster{kube: kube, possumURL: strings.TrimRight(possumURL, "/"), client: client}
}

// Nodes lists every node by name, cached for a few seconds.
func (c *Cluster) Nodes(ctx context.Context) ([]Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checked) < cacheFor {
		return c.nodes, nil
	}
	nodes, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	c.nodes, c.checked = nodes, time.Now()
	return nodes, nil
}

func (c *Cluster) load(ctx context.Context) ([]Node, error) {
	list, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	pods, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "status.phase!=Succeeded,status.phase!=Failed"})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	perNode := map[string]int{}
	for i := range pods.Items {
		perNode[pods.Items[i].Spec.NodeName]++
	}
	byName := map[string]*Node{}
	var out []*Node
	for i := range list.Items {
		n := fromNode(&list.Items[i])
		n.Pods = perNode[n.Name]
		byName[n.Name] = &n
		out = append(out, &n)
	}
	// possum is optional: without it, sleeping nodes are still recognised by their taint.
	for _, m := range c.possumMembers(ctx) {
		n, ok := byName[m.Name]
		if !ok {
			n = &Node{Name: m.Name}
			out = append(out, n)
		}
		n.Group = m.Group
		switch {
		case !ok && m.Phase == "Booting":
			n.State, n.Since = Booting, m.Since
		case !ok:
			n.State, n.Since, n.Reason = Off, m.Since, m.Message
		case m.Phase == "Off" && n.State != Ready:
			n.State, n.Since = Asleep, m.Since
		case m.Phase == "Booting" && n.State != Ready:
			n.State, n.Since = Booting, m.Since
		}
	}
	nodes := make([]Node, 0, len(out))
	for _, n := range out {
		nodes = append(nodes, *n)
	}
	slices.SortFunc(nodes, func(a, b Node) int { return strings.Compare(a.Name, b.Name) })
	return nodes, nil
}

func fromNode(n *corev1.Node) Node {
	out := Node{
		Name:    n.Name,
		State:   NotReady,
		Arch:    n.Status.NodeInfo.Architecture,
		CPU:     n.Status.Capacity.Cpu().Value(),
		Memory:  n.Status.Capacity.Memory().Value(),
		Kubelet: n.Status.NodeInfo.KubeletVersion,
	}
	for k := range n.Labels {
		if role, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && role != "" {
			out.Roles = append(out.Roles, role)
		}
	}
	slices.Sort(out.Roles)
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			out.Address = a.Address
		}
	}
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			out.Since = cond.LastTransitionTime.Time
			if cond.Status == corev1.ConditionTrue {
				out.State = Ready
			} else {
				out.Reason = cond.Message
			}
		}
	}
	for _, t := range n.Spec.Taints {
		if t.Key == TaintAsleep {
			out.State, out.Reason = Asleep, ""
			if t.TimeAdded != nil {
				out.Since = t.TimeAdded.Time
			}
		}
	}
	return out
}

type possumMember struct {
	Group   string    `json:"group"`
	Name    string    `json:"name"`
	Phase   string    `json:"phase"`
	Since   time.Time `json:"since"`
	Message string    `json:"message"`
}

func (c *Cluster) possumMembers(ctx context.Context) []possumMember {
	if c.possumURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.possumURL+"/api/status", http.NoBody)
	if err != nil {
		return nil
	}
	resp, err := c.client.Do(req)
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

// Pod is one pod on a node.
type Pod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Owner is the workload that runs it, e.g. "Deployment/gridtie".
	Owner string `json:"owner,omitempty"`
	Phase string `json:"phase"`
	// Reason explains a pod that isn't running well, e.g. CrashLoopBackOff.
	Reason     string    `json:"reason,omitempty"`
	Ready      int       `json:"ready"`
	Containers int       `json:"containers"`
	Restarts   int32     `json:"restarts"`
	Started    time.Time `json:"started,omitzero"`
	// CPU (millicores) and Memory (bytes) are the containers' requests.
	CPU    int64 `json:"cpuRequestMillis,omitempty"`
	Memory int64 `json:"memoryRequestBytes,omitempty"`
}

// Pods lists the pods scheduled on a node, by namespace and name.
func (c *Cluster) Pods(ctx context.Context, node string) ([]Pod, error) {
	list, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	out := make([]Pod, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Spec.NodeName == node { // also when a client ignores the selector
			out = append(out, fromPod(&list.Items[i]))
		}
	}
	slices.SortFunc(out, func(a, b Pod) int {
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func fromPod(p *corev1.Pod) Pod {
	out := Pod{
		Namespace:  p.Namespace,
		Name:       p.Name,
		Phase:      string(p.Status.Phase),
		Reason:     p.Status.Reason,
		Containers: len(p.Spec.Containers),
	}
	if p.Status.StartTime != nil {
		out.Started = p.Status.StartTime.Time
	}
	for _, ref := range p.OwnerReferences {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		kind, name := ref.Kind, ref.Name
		// Deployments own pods through a ReplicaSet named <deployment>-<hash>.
		if kind == "ReplicaSet" {
			if i := strings.LastIndex(name, "-"); i > 0 {
				kind, name = "Deployment", name[:i]
			}
		}
		out.Owner = kind + "/" + name
	}
	for _, c := range p.Spec.Containers {
		out.CPU += c.Resources.Requests.Cpu().MilliValue()
		out.Memory += c.Resources.Requests.Memory().Value()
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			out.Ready++
		}
		out.Restarts += cs.RestartCount
		if w := cs.State.Waiting; w != nil && w.Reason != "" && out.Reason == "" {
			out.Reason = w.Reason
		}
	}
	return out
}
