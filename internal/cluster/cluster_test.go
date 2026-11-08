package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func node(name string, ready bool, taints ...corev1.Taint) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""}},
		Spec:       corev1.NodeSpec{Taints: taints},
		Status: corev1.NodeStatus{
			Capacity:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
			NodeInfo:   corev1.NodeSystemInfo{Architecture: "arm64"},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status, Message: "kubelet stopped posting"}},
		},
	}
}

func pod(name, nodeName string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Spec: corev1.PodSpec{NodeName: nodeName}}
}

func TestNodes(t *testing.T) {
	possum := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"members":[
			{"group":"pis","name":"pi-off","phase":"Off","since":"2026-01-01T00:00:00Z"},
			{"group":"pis","name":"pi-up","phase":"Running"},
			{"group":"gpu","name":"gpu","phase":"Off","since":"2026-01-02T00:00:00Z"},
			{"group":"pis","name":"pi-waking","phase":"Booting"}]}`))
	}))
	defer possum.Close()
	kube := fake.NewClientset(
		node("pi-up", true),
		node("broken", false),
		node("gpu", false, corev1.Taint{Key: TaintAsleep, Effect: corev1.TaintEffectNoSchedule}),
		pod("a", "pi-up"), pod("b", "pi-up"), pod("c", "broken"),
	)
	c := New(kube, possum.URL, http.DefaultClient)
	nodes, err := c.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Node{}
	for _, n := range nodes {
		got[n.Name] = n
	}
	want := map[string]State{"pi-up": Ready, "broken": NotReady, "gpu": Asleep, "pi-off": Off, "pi-waking": Booting}
	if len(nodes) != len(want) {
		t.Fatalf("nodes = %+v", nodes)
	}
	for name, state := range want {
		if got[name].State != state {
			t.Errorf("%s state = %s, want %s", name, got[name].State, state)
		}
	}
	up := got["pi-up"]
	if up.Pods != 2 || up.CPU != 4 || up.Memory != 8<<30 || up.Arch != "arm64" || up.Group != "pis" || up.Roles[0] != "control-plane" {
		t.Errorf("pi-up = %+v", up)
	}
	if got["broken"].Reason == "" || !got["pi-off"].Since.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("broken = %+v, pi-off = %+v", got["broken"], got["pi-off"])
	}
}

func TestNodesWithoutPossum(t *testing.T) {
	kube := fake.NewClientset(node("gpu", false, corev1.Taint{Key: TaintAsleep, Effect: corev1.TaintEffectNoSchedule}))
	nodes, err := New(kube, "http://127.0.0.1:1", http.DefaultClient).Nodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].State != Asleep {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
}
