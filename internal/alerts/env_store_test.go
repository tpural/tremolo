package alerts

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A rule that queries a store tremolo wasn't given fails with an error, not a crash.
func TestRuleWithoutStore(t *testing.T) {
	r, err := ParseRule("needs-logs", "def check(now):\n    return [alert(e['_msg']) for e in logs('*')]\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), &Env{Data: &Data{}, Now: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "isn't set up") {
		t.Fatalf("err = %v", err)
	}
}
