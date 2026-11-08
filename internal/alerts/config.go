package alerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/tpural/tremolo/internal/alerts/notify"
)

// Config is the alerts section of tremolo's configuration.
type Config struct {
	// Timezone is the zone now is in for rules, e.g. America/Chicago.
	Timezone string `json:"timezone"`
	// URL is tremolo's address, for links in notifications.
	URL string `json:"url"`
	// Repeat resends a firing, unacknowledged alert this often; zero sends it once.
	Repeat Duration `json:"repeat"`
	// Disable leaves out rules by name, e.g. built-ins you don't want.
	Disable []string `json:"disable"`
	// Notify is where each severity goes when a rule doesn't say: channels
	// or escalations, by name.
	Notify map[string]Targets `json:"notify"`
	// Channels are where notifications go, by name. Each has a type, one of
	// notify.Types(), and that provider's settings.
	Channels map[string]json.RawMessage `json:"channels"`
	// Escalations are named chains of steps: who to tell, and how long after
	// an alert starts firing unacknowledged.
	Escalations map[string][]Step `json:"escalations"`

	providers map[string]notify.Provider
}

// Step is one link of an escalation chain.
type Step struct {
	// After is how long the alert has fired unacknowledged; zero is at once.
	After  Duration `json:"after"`
	Notify Targets  `json:"notify"`
}

// Targets is one channel or escalation name, or a list of them.
type Targets []string

// UnmarshalJSON implements json.Unmarshaler.
func (t *Targets) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*t = Targets{one}
		if one == "" {
			*t = nil
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("must be a name or a list of names")
	}
	*t = many
	return nil
}

// Duration reads "4h" style durations from YAML.
type Duration struct{ time.Duration }

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	d.Duration = v
	return err
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// LoadConfig reads path, expanding ${VAR}s from the environment, and builds
// the channels. An empty path is the defaults: no channels, so alerts show
// only in the UI.
func LoadConfig(path string, client *http.Client) (Config, error) {
	c := Config{Repeat: Duration{4 * time.Hour}}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(b))), &c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return c, fmt.Errorf("timezone: %w", err)
	}
	c.providers = map[string]notify.Provider{}
	for name, raw := range c.Channels {
		p, err := notify.New(raw, client)
		if err != nil {
			return c, fmt.Errorf("channel %s: %w", name, err)
		}
		c.providers[name] = p
	}
	for name, steps := range c.Escalations {
		if _, clash := c.Channels[name]; clash {
			return c, fmt.Errorf("escalation %s: a channel has the same name", name)
		}
		if len(steps) == 0 {
			return c, fmt.Errorf("escalation %s: no steps", name)
		}
		for i, s := range steps {
			if len(s.Notify) == 0 {
				return c, fmt.Errorf("escalation %s step %d: notify nobody", name, i+1)
			}
			for _, ch := range s.Notify {
				if _, ok := c.providers[ch]; !ok {
					return c, fmt.Errorf("escalation %s step %d: no channel %q", name, i+1, ch)
				}
			}
		}
		sort.SliceStable(steps, func(i, j int) bool { return steps[i].After.Duration < steps[j].After.Duration })
	}
	for sev, targets := range c.Notify {
		if !slices.Contains(Severities, sev) {
			return c, fmt.Errorf("notify: %q is not a severity (%v)", sev, Severities)
		}
		if _, unknown := c.plan(targets); len(unknown) > 0 {
			return c, fmt.Errorf("notify: %s: no channel or escalation %q", sev, unknown[0])
		}
	}
	return c, nil
}

// Location is the configured zone.
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Planned is one channel an alert is sent to, and when.
type Planned struct {
	Channel string        `json:"channel"`
	After   time.Duration `json:"after"`
}

// plan flattens targets into channels and when each is due, earliest first.
// A channel named twice is due at its earliest. It also returns the names it
// doesn't know.
func (c Config) plan(targets []string) ([]Planned, []string) {
	due := map[string]time.Duration{}
	add := func(ch string, after time.Duration) {
		if old, ok := due[ch]; !ok || after < old {
			due[ch] = after
		}
	}
	var unknown []string
	for _, t := range targets {
		if steps, ok := c.Escalations[t]; ok {
			for _, s := range steps {
				for _, ch := range s.Notify {
					add(ch, s.After.Duration)
				}
			}
			continue
		}
		if _, ok := c.providers[t]; ok {
			add(t, 0)
			continue
		}
		unknown = append(unknown, t)
	}
	out := make([]Planned, 0, len(due))
	for ch, after := range due {
		out = append(out, Planned{ch, after})
	}
	slices.SortFunc(out, func(a, b Planned) int {
		if a.After != b.After {
			return int(a.After - b.After)
		}
		return strings.Compare(a.Channel, b.Channel)
	})
	return out, unknown
}

// targetNames lists the channels and escalations, for the UI.
func (c Config) targetNames() (channels, escalations []string) {
	for n := range c.Channels {
		channels = append(channels, n)
	}
	for n := range c.Escalations {
		escalations = append(escalations, n)
	}
	slices.Sort(channels)
	slices.Sort(escalations)
	return channels, escalations
}
