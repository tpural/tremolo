package storage

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tpural/tremolo/internal/ingest"
)

const vmMetrics = `# HELP vm_data_size_bytes x
vm_data_size_bytes{type="storage/small"} 1000
vm_data_size_bytes{type="indexdb/file"} 500
vm_free_disk_space_bytes{path="/storage"} 9000
vm_total_disk_space_bytes{path="/storage"} 10000
vm_rows_inserted_total{type="opentelemetry"} 70
vm_rows_inserted_total{type="prometheus"} 30
flag{name="retentionPeriod", value="90d", is_set="true"} 1
flag{name="search.latencyOffset", value="30s, or so", is_set="false"} 1
`

func TestSample(t *testing.T) {
	var mu sync.Mutex
	var imported string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vm/metrics":
			_, _ = io.WriteString(w, vmMetrics)
		case "/vm/api/v1/import/prometheus":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			imported = string(b)
			mu.Unlock()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	vm, _ := url.Parse(srv.URL + "/vm")
	logs, _ := url.Parse(srv.URL + "/missing")
	s := &Sampler{
		Stores:     Stores(vm, logs, nil),
		MetricsURL: vm,
		Client:     srv.Client(),
		Log:        slog.New(slog.DiscardHandler),
		Counts: func() map[ingest.Received]ingest.Count {
			return map[ingest.Received]ingest.Count{{Sender: "homelab", Signal: "logs"}: {Pushes: 3, Bytes: 4096}}
		},
	}
	if err := s.Sample(context.Background()); err != nil {
		t.Fatal(err)
	}
	u := s.Usage()
	if len(u) != 2 || u[0].DataBytes != 1500 || u[0].FreeBytes != 9000 || u[0].TotalBytes != 10000 || u[0].Rows != 100 || u[0].Retention != "90d" {
		t.Fatalf("metrics usage %+v", u[0])
	}
	if u[1].Error == "" {
		t.Fatalf("a store without /metrics: %+v", u[1])
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{`tremolo_storage_data_bytes{store="metrics"} 1500 `, `tremolo_ingest_bytes_total{source="homelab",signal="logs"} 4096 `} {
		if !strings.Contains(imported, want) {
			t.Errorf("imported lacks %q:\n%s", want, imported)
		}
	}
}

func TestParseBytes(t *testing.T) {
	for in, want := range map[string]float64{"15GiB": 15 << 30, "500MB": 500e6, "1048576": 1048576} {
		if got, ok := parseBytes(in); !ok || got != want {
			t.Errorf("%s = %v %v", in, got, ok)
		}
	}
	if _, ok := parseBytes("0"); ok {
		t.Error("0 is no limit")
	}
}
