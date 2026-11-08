package hosts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/cluster"
)

func TestMerge(t *testing.T) {
	now := time.Now()
	nodes := []cluster.Node{
		{Name: "pi-1", State: cluster.Ready},
		{Name: "gpu", State: cluster.Asleep},
		{Name: "pi-off", State: cluster.Off},
	}
	seen := map[string]time.Time{
		"pi-1":   now.Add(-10 * time.Second),
		"gpu":    now.Add(-2 * time.Hour),
		"laptop": now.Add(-30 * time.Second),
		"old-vm": now.Add(-time.Hour),
	}
	got := map[string]Host{}
	for _, h := range merge(nodes, seen, now) {
		got[h.Name] = h
	}
	want := map[string]cluster.State{"pi-1": cluster.Ready, "gpu": cluster.Asleep, "pi-off": cluster.Off, "laptop": cluster.Ready, "old-vm": Unreachable}
	if len(got) != len(want) {
		t.Fatalf("hosts = %v", got)
	}
	for name, s := range want {
		if got[name].State != s {
			t.Errorf("%s = %s, want %s", name, got[name].State, s)
		}
	}
	if !got["pi-1"].Kubernetes || got["laptop"].Kubernetes || got["laptop"].LastSeen.IsZero() {
		t.Errorf("sources: %+v %+v", got["pi-1"], got["laptop"])
	}
}

func TestListFromMetricsOnly(t *testing.T) {
	ts := time.Now().Add(-5 * time.Second).Unix()
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != lastSeenQuery {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"host_name":"laptop"},"value":[1,"%d"]}]}}`, ts)
	}))
	defer vm.Close()
	u, _ := url.Parse(vm.URL)
	hosts, err := New(u, http.DefaultClient, nil).List(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].Name != "laptop" || hosts[0].State != cluster.Ready || hosts[0].LastSeen.Unix() != ts {
		t.Fatalf("hosts = %+v, %v", hosts, err)
	}

	down, _ := url.Parse("http://127.0.0.1:1")
	if _, err := New(down, http.DefaultClient, nil).List(context.Background()); err == nil {
		t.Fatal("no sources should be an error")
	}
}
