package users

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Scope is what an assistant token may do. Each signal lists the selectors
// whose data it may read, joined with OR: MetricsQL series selectors for
// metrics, LogsQL filters for logs and traces. "*" reads everything; an empty
// list reads nothing. Allow lists the other things it may do.
type Scope struct {
	Metrics []string `json:"metrics,omitempty"`
	Logs    []string `json:"logs,omitempty"`
	Traces  []string `json:"traces,omitempty"`
	Allow   []string `json:"allow,omitempty"`
}

// Capabilities an assistant token can be given besides reading signals.
const (
	ReadAlerts      = "alerts"
	ReadRunbooks    = "runbooks"
	ReadHosts       = "hosts"
	ReadDashboards  = "dashboards"
	ReadStorage     = "storage"
	WriteAlerts     = "write:alerts"
	WriteSilences   = "write:silences"
	WriteMaint      = "write:maintenance"
	WriteFixNotes   = "write:runbook-notes"
	WriteRunbooks   = "write:runbooks"
	maxSelectors    = 20
	maxSelectorSize = 500
)

// Capabilities lists every capability, reads first.
var Capabilities = []string{
	ReadAlerts, ReadRunbooks, ReadHosts, ReadDashboards, ReadStorage,
	WriteAlerts, WriteSilences, WriteMaint, WriteFixNotes, WriteRunbooks,
}

// Signals a scope reads by selector.
const (
	Metrics = "metrics"
	Logs    = "logs"
	Traces  = "traces"
)

// Allows reports whether s includes capability c.
func (s *Scope) Allows(c string) bool { return slices.Contains(s.Allow, c) }

// Selectors returns what s may read of a signal.
func (s *Scope) Selectors(signal string) []string {
	switch signal {
	case Metrics:
		return s.Metrics
	case Logs:
		return s.Logs
	case Traces:
		return s.Traces
	}
	return nil
}

// All reports whether s reads everything of a signal.
func (s *Scope) All(signal string) bool { return slices.Contains(s.Selectors(signal), "*") }

// Check reports what's wrong with s, if anything.
func (s *Scope) Check() error {
	for _, c := range s.Allow {
		if !slices.Contains(Capabilities, c) {
			return fmt.Errorf("unknown capability %q", c)
		}
	}
	for _, sig := range []string{Metrics, Logs, Traces} {
		list := s.Selectors(sig)
		if len(list) > maxSelectors {
			return fmt.Errorf("%s: at most %d selectors", sig, maxSelectors)
		}
		for _, sel := range list {
			if err := checkSelector(sig, sel); err != nil {
				return fmt.Errorf("%s selector %q: %w", sig, sel, err)
			}
		}
	}
	return nil
}

func checkSelector(signal, sel string) error {
	switch {
	case sel == "*":
		return nil
	case strings.TrimSpace(sel) != sel || sel == "":
		return errors.New("must not be empty or padded")
	case len(sel) > maxSelectorSize:
		return fmt.Errorf("longer than %d characters", maxSelectorSize)
	case strings.ContainsAny(sel, "\n\r"):
		return errors.New("must be one line")
	}
	if signal == Metrics && (!strings.HasPrefix(sel, "{") || !strings.HasSuffix(sel, "}")) {
		return errors.New(`must be a series selector like {k8s_namespace_name="x"}`)
	}
	return nil
}
