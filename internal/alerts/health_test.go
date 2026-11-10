package alerts

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHealthy(t *testing.T) {
	e := engine(t, &Data{}, "timezone: UTC\n")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	if err := e.Healthy(); err == nil || !strings.Contains(err.Error(), "no rules") {
		t.Fatalf("no rules: %v", err)
	}
	good, err := ParseRule("good", "def check(now):\n    return []\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := ParseRule("bad", "def check(now):\n    fail(\"boom\")\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // rules are run by hand
	e.SetRules(ctx, []*Rule{good, bad}, nil)
	if err := e.Healthy(); err != nil {
		t.Fatalf("just loaded: %v", err)
	}
	now = now.Add(4 * time.Minute)
	if err := e.Healthy(); err == nil || !strings.Contains(err.Error(), "hasn't run") {
		t.Fatalf("never ran: %v", err)
	}
	e.Eval(context.Background(), good)
	e.Eval(context.Background(), bad)
	if err := e.Healthy(); err != nil {
		t.Fatalf("one rule failing: %v", err)
	}
	e.SetRules(ctx, []*Rule{bad}, nil)
	if err := e.Healthy(); err == nil || !strings.Contains(err.Error(), "every rule failed") {
		t.Fatalf("all failing: %v", err)
	}
}
