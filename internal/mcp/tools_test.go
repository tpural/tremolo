package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestPrefixes(t *testing.T) {
	var names []string
	for i := range 200 {
		names = append(names, fmt.Sprintf("envoy_x%d", i))
	}
	names = append(names, "k8s_pod_phase", "k8s_hpa_min_replicas", "up")
	got := prefixes(names)
	if !strings.HasPrefix(got, "203 metric names. By prefix: envoy_ 200, k8s_ 2, up 1.") {
		t.Errorf("prefixes = %q", got)
	}
}
