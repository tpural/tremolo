package cluster

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/tpural/tremolo/internal/hoststate"
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
	asleepAt := metav1.NewTime(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	cordoned := node("drained", true)
	cordoned.Spec.Unschedulable = true
	kube := fake.NewClientset(
		node("pi-up", true),
		node("broken", false),
		node("gpu", false, corev1.Taint{Key: "possum.dev/asleep", Effect: corev1.TaintEffectNoSchedule, TimeAdded: &asleepAt}),
		node("other-taint", true, corev1.Taint{Key: "example.com/busy", Effect: corev1.TaintEffectNoSchedule}),
		cordoned,
		pod("a", "pi-up"), pod("b", "pi-up"), pod("c", "broken"),
	)
	nodes, err := New(kube, []string{"possum.dev/asleep"}).Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Node{}
	for _, n := range nodes {
		got[n.Name] = n
	}
	want := map[string]hoststate.State{
		"pi-up": hoststate.Ready, "broken": hoststate.NotReady, "gpu": hoststate.Asleep,
		"other-taint": hoststate.Ready, "drained": hoststate.Maintenance,
	}
	if len(nodes) != len(want) {
		t.Fatalf("nodes = %+v", nodes)
	}
	for name, state := range want {
		if got[name].State != state {
			t.Errorf("%s state = %s, want %s", name, got[name].State, state)
		}
	}
	up := got["pi-up"]
	if up.Pods != 2 || up.CPU != 4 || up.Memory != 8<<30 || up.Arch != "arm64" || up.Roles[0] != "control-plane" {
		t.Errorf("pi-up = %+v", up)
	}
	if got["broken"].Reason == "" || !got["gpu"].Since.Equal(asleepAt.Time) || got["drained"].Reason != "cordoned" {
		t.Errorf("broken = %+v, gpu = %+v, drained = %+v", got["broken"], got["gpu"], got["drained"])
	}
}

func TestPods(t *testing.T) {
	yes := true
	web := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-7d9f-abcde", Namespace: "apps",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-7d9f", Controller: &yes}},
		},
		Spec: corev1.PodSpec{NodeName: "pi-1", Containers: []corev1.Container{
			{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("64Mi")}}},
			{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m")}}},
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Ready: true, RestartCount: 1},
			{RestartCount: 4, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
		}},
	}
	kube := fake.NewClientset(web, pod("elsewhere", "pi-2"))
	pods, err := New(kube, nil).Pods(context.Background(), "pi-1")
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods = %+v, %v", pods, err)
	}
	p := pods[0]
	if p.Owner != "Deployment/web" || p.Ready != 1 || p.Containers != 2 || p.Restarts != 5 || p.Reason != "CrashLoopBackOff" ||
		p.CPU != 300 || p.Memory != 64<<20 || p.Phase != "Running" {
		t.Fatalf("pod = %+v", p)
	}
}

func TestNodesWithoutAsleepTaints(t *testing.T) {
	kube := fake.NewClientset(node("gpu", false, corev1.Taint{Key: "possum.dev/asleep", Effect: corev1.TaintEffectNoSchedule}))
	nodes, err := New(kube, nil).Nodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].State != hoststate.NotReady {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
}
