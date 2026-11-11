package coverage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCheck(t *testing.T) {
	now := time.Now()
	stores := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "container_memory"):
			_, _ = w.Write([]byte(`{"data":{"result":[{"metric":{"namespace":"gridtie"}},{"metric":{"namespace":"angelorooms"}}]}}`))
		case strings.Contains(q, "tremolo_ingest"):
			_, _ = w.Write([]byte(`{"data":{"result":[{"metric":{"source":"nas","signal":"logs"}}]}}`))
		case strings.Contains(q, "by (host.name)"):
			_, _ = w.Write([]byte(`{"host.name":"mm01","n":"40"}` + "\n"))
		case strings.Contains(q, "by (k8s.namespace.name)"):
			_, _ = w.Write([]byte(`{"k8s.namespace.name":"gridtie","n":"12"}` + "\n"))
		default:
			http.Error(w, "unexpected "+q, http.StatusBadRequest)
		}
	}))
	defer stores.Close()
	u, _ := url.Parse(stores.URL)
	s := &Source{
		MetricsURL: u, LogsURL: u, Client: http.DefaultClient,
		Hosts: func(context.Context) ([]Host, error) {
			return []Host{{Name: "mm01", State: "ready", LastSeen: now}, {Name: "mm02", State: "ready"}, {Name: "pi-1", State: "asleep"}}, nil
		},
		Rules:      func() []string { return []string{"disk", "crash-loop"} },
		HasRunbook: func(r string) bool { return r == "disk" },
	}
	var got []string
	for _, g := range s.Check(context.Background(), now) {
		got = append(got, g.Kind+":"+g.Subject)
	}
	want := []string{"metrics:mm02", "logs:mm02", "logs:namespace angelorooms", "sender:nas/logs", "runbook:1 rules"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("gaps = %v, want %v", got, want)
	}
}
