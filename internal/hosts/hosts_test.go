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
	seen := map[string]sighting{
		"pi-1":      {at: now.Add(-10 * time.Second), collector: "kubernetes", cluster: "homelab"},
		"gpu":       {at: now.Add(-2 * time.Hour)},
		"laptop":    {at: now.Add(-30 * time.Second), collector: "host", from: "laptop"},
		"old-vm":    {at: now.Add(-time.Hour)},
		"remote-pi": {at: now.Add(-5 * time.Second), collector: "kubernetes", cluster: "garage"},
	}
	got := map[string]Host{}
	for _, h := range merge(nodes, seen, now) {
		got[h.Name] = h
	}
	want := map[string]cluster.State{"pi-1": cluster.Ready, "gpu": cluster.Asleep, "pi-off": cluster.Off, "laptop": cluster.Ready, "old-vm": Unreachable, "remote-pi": cluster.Ready}
	if len(got) != len(want) {
		t.Fatalf("hosts = %v", got)
	}
	for name, s := range want {
		if got[name].State != s {
			t.Errorf("%s = %s, want %s", name, got[name].State, s)
		}
	}
	kinds := map[string]Kind{"pi-1": KindKubernetes, "pi-off": KindKubernetes, "laptop": KindServer, "old-vm": KindServer, "remote-pi": KindKubernetes}
	for name, k := range kinds {
		if got[name].Kind != k {
			t.Errorf("%s kind = %s, want %s", name, got[name].Kind, k)
		}
	}
	if got["pi-off"].Cluster != "homelab" || got["gpu"].Cluster != "homelab" {
		t.Errorf("nodes without metrics should join the local cluster: %+v %+v", got["pi-off"], got["gpu"])
	}
	if got["pi-1"].Cluster != "homelab" || got["remote-pi"].Cluster != "garage" || got["laptop"].Source != "laptop" || got["laptop"].LastSeen.IsZero() {
		t.Errorf("details: %+v %+v %+v", got["pi-1"], got["remote-pi"], got["laptop"])
	}
}

func TestListFromMetricsOnly(t *testing.T) {
	ts := time.Now().Add(-5 * time.Second).Unix()
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != lastSeenQuery(30*24*time.Hour) {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"host_name":"laptop","tremolo_collector":"host","source":"old"},"value":[1,"%d"]},
			{"metric":{"host_name":"laptop","tremolo_collector":"host","source":"laptop"},"value":[1,"%d"]}]}}`, ts-60, ts)
	}))
	defer vm.Close()
	u, _ := url.Parse(vm.URL)
	hosts, err := New(u, http.DefaultClient, nil, 30*24*time.Hour).List(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].Name != "laptop" || hosts[0].State != cluster.Ready || hosts[0].LastSeen.Unix() != ts ||
		hosts[0].Source != "laptop" || hosts[0].Kind != KindServer {
		t.Fatalf("hosts = %+v, %v", hosts, err)
	}

	down, _ := url.Parse("http://127.0.0.1:1")
	if _, err := New(down, http.DefaultClient, nil, time.Hour).List(context.Background()); err == nil {
		t.Fatal("no sources should be an error")
	}
}
