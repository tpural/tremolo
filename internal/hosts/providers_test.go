package hosts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tpural/tremolo/internal/calendar"
	"github.com/tpural/tremolo/internal/cluster"
	"github.com/tpural/tremolo/internal/hoststate"

	"github.com/tpural/tremolo/internal/state"
)

func TestPossum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"members":[
			{"group":"pis","name":"pi-off","phase":"Off","since":"2026-01-01T00:00:00Z","message":"idle"},
			{"group":"pis","name":"pi-up","phase":"Off"},
			{"group":"gpu","name":"gpu","phase":"Off","since":"2026-01-02T00:00:00Z"},
			{"group":"pis","name":"pi-waking","phase":"Booting"}]}`))
	}))
	defer srv.Close()
	hosts := map[string]*Host{
		"pi-up": {Node: cluster.Node{Name: "pi-up", State: hoststate.Ready}, Kind: KindKubernetes, Cluster: "homelab", StateBy: "kubernetes"},
		"gpu":   {Node: cluster.Node{Name: "gpu", State: hoststate.NotReady}, Kind: KindKubernetes, Cluster: "homelab", StateBy: "kubernetes"},
	}
	NewPossum(srv.URL+"/", http.DefaultClient).Apply(context.Background(), hosts)
	want := map[string]hoststate.State{"pi-up": hoststate.Ready, "gpu": hoststate.Asleep, "pi-off": hoststate.Off, "pi-waking": hoststate.Booting}
	if len(hosts) != len(want) {
		t.Fatalf("hosts = %v", hosts)
	}
	for name, s := range want {
		if hosts[name].State != s {
			t.Errorf("%s = %s, want %s", name, hosts[name].State, s)
		}
	}
	off := hosts["pi-off"]
	if off.StateBy != "possum" || off.Details["group"] != "pis" || off.Reason != "idle" || off.Cluster != "homelab" || off.Kind != KindKubernetes {
		t.Errorf("pi-off = %+v", off)
	}
	if hosts["pi-up"].StateBy != "kubernetes" || hosts["pi-up"].Details["group"] != "pis" || hosts["gpu"].StateBy != "possum" {
		t.Errorf("pi-up = %+v, gpu = %+v", hosts["pi-up"], hosts["gpu"])
	}

	// possum being down leaves the hosts alone.
	NewPossum("http://127.0.0.1:1", http.DefaultClient).Apply(context.Background(), hosts)
	if hosts["gpu"].State != hoststate.Asleep {
		t.Errorf("gpu = %+v", hosts["gpu"])
	}
}

func TestMaintenance(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	open := func() *Maintenance {
		cal, err := calendar.Open(db, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		cal.SetClock(func() time.Time { return now })
		return NewMaintenance(cal)
	}
	m := open()
	if err := m.Set(ctx, "pi-1", now.Add(-time.Minute), "", "tim"); !errors.Is(err, ErrPast) {
		t.Fatalf("past window: %v", err)
	}
	if err := m.Set(ctx, "pi-1", now.Add(time.Hour), "new SD card", "tim"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(ctx, "nas", time.Time{}, "", "tim"); err != nil {
		t.Fatal(err)
	}
	hosts := func() map[string]*Host {
		return map[string]*Host{
			"pi-1": {Node: cluster.Node{Name: "pi-1", State: hoststate.Asleep}, StateBy: "possum"},
			"nas":  {Node: cluster.Node{Name: "nas", State: hoststate.Unreachable}, StateBy: "metrics"},
		}
	}

	// Saved, so a restart keeps the windows.
	_ = db.Close()
	if db, err = state.Open(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m = open()
	h := hosts()
	m.Apply(context.Background(), h)
	if h["pi-1"].State != hoststate.Maintenance || h["pi-1"].StateBy != "maintenance" || h["pi-1"].Reason != "new SD card" ||
		!h["pi-1"].Until.Equal(now.Add(time.Hour)) || !h["pi-1"].Since.Equal(now) {
		t.Errorf("pi-1 = %+v", h["pi-1"])
	}
	if h["nas"].State != hoststate.Maintenance || !h["nas"].Until.IsZero() {
		t.Errorf("nas = %+v", h["nas"])
	}

	// An ended window no longer applies; a cleared one is gone.
	now = now.Add(2 * time.Hour)
	if err := m.Clear(ctx, "nas"); err != nil {
		t.Fatal(err)
	}
	h = hosts()
	m.Apply(context.Background(), h)
	if h["pi-1"].State != hoststate.Asleep || h["nas"].State != hoststate.Unreachable {
		t.Errorf("after: pi-1 = %+v, nas = %+v", h["pi-1"], h["nas"])
	}
}
