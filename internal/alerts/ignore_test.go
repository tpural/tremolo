package alerts

import "testing"

func TestIgnored(t *testing.T) {
	c := Config{IgnoreNamespaces: []string{"eval-*", "scratch"}}
	for ns, want := range map[string]bool{"eval-wedged": true, "scratch": true, "scratch2": false, "tremolo": false, "": false} {
		if got := c.Ignored(ns); got != want {
			t.Errorf("Ignored(%q) = %v", ns, got)
		}
	}
}
