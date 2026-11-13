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
	// IgnoreNamespaces are namespaces no alert is raised for, e.g. ones evals
	// break on purpose; a trailing * matches any namespace starting with the rest.
	IgnoreNamespaces []string `json:"ignoreNamespaces,omitempty"`
	// Notify is where each severity goes when a rule doesn't say: channels
	// or escalations, by name.
	Notify map[string]Targets `json:"notify"`
	// Channels are where notifications go, by name. Each has a type, one of
	// notify.Types(), and that provider's settings.
	Channels map[string]json.RawMessage `json:"channels"`
	// Escalations are named chains of steps: who to tell, and how long after
	// an alert starts firing unacknowledged.
	Escalations map[string][]Step `json:"escalations"`
	// Digest is a weekly summary; nil sends none.
	Digest *Digest `json:"digest"`

	providers map[string]notify.Provider
}

// Digest is a summary of the week, sent at a set time.
type Digest struct {
	// At is a weekday and time in the alerts timezone, e.g. "Mon 08:00".
	At string `json:"at"`
	// Notify is the channels it goes to.
	Notify Targets `json:"notify"`

	day          time.Weekday
	hour, minute int
}

// Next is the first time the digest is due after t, in loc.
func (d *Digest) Next(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	at := time.Date(t.Year(), t.Month(), t.Day(), d.hour, d.minute, 0, 0, loc)
	at = at.AddDate(0, 0, (int(d.day)-int(at.Weekday())+7)%7)
	if !at.After(t) {
		at = at.AddDate(0, 0, 7)
	}
	return at
}

func (d *Digest) parse(channels map[string]notify.Provider) error {
	day, clock, ok := strings.Cut(strings.TrimSpace(d.At), " ")
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	i := slices.Index(days, strings.ToLower(strings.TrimSpace(day))[:min(3, len(day))])
	at, err := time.Parse("15:04", strings.TrimSpace(clock))
	if !ok || i < 0 || err != nil {
		return fmt.Errorf("digest: at %q should be a weekday and time, e.g. \"Mon 08:00\"", d.At)
	}
	d.day, d.hour, d.minute = time.Weekday(i), at.Hour(), at.Minute()
	if len(d.Notify) == 0 {
		return errors.New("digest: notify no channel")
	}
	for _, n := range d.Notify {
		if _, ok := channels[n]; !ok {
			return fmt.Errorf("digest: %q is not a channel", n)
		}
	}
	return nil
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
		}
		sort.SliceStable(steps, func(i, j int) bool { return steps[i].After.Duration < steps[j].After.Duration })
	}
	for sev := range c.Notify {
		if !slices.Contains(Severities, sev) {
			return c, fmt.Errorf("notify: %q is not a severity (%v)", sev, Severities)
		}
	}
	if c.Digest != nil {
		if err := c.Digest.parse(c.providers); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Ignored reports whether no alert is raised for namespace ns.
func (c Config) Ignored(ns string) bool {
	return ns != "" && slices.ContainsFunc(c.IgnoreNamespaces, func(p string) bool {
		if pre, ok := strings.CutSuffix(p, "*"); ok {
			return strings.HasPrefix(ns, pre)
		}
		return ns == p
	})
}

// Location is the configured zone.
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Planned is one recipient of an alert, and when it's due: a channel, or a
// person's address on one.
type Planned struct {
	// Key names the recipient: the channel, or "channel:address".
	Key     string        `json:"key"`
	Channel string        `json:"channel"`
	To      string        `json:"to,omitempty"`
	Person  string        `json:"person,omitempty"`
	After   time.Duration `json:"after"`
}

func recipientKey(channel, to string) string {
	if to == "" {
		return channel
	}
	return channel + ":" + to
}

// Contact is one way to reach a person: a channel and their address on it,
// such as an email address or phone number, used After an alert reaches them.
type Contact struct {
	Channel string   `json:"channel"`
	To      string   `json:"to"`
	After   Duration `json:"after"`
}

// Directory is the people and teams alerts can go to.
type Directory interface {
	// Person returns a user's contacts.
	Person(name string) ([]Contact, bool)
	// Team returns a team's members and, if it has one, where its alerts go.
	Team(name string) (members, notify []string, ok bool)
	// Names lists the people and teams.
	Names() (people, teams []string)
}

// planner resolves notify targets into recipients.
type planner struct {
	c   Config
	dir Directory
}

// plan flattens targets (channels, escalations, teams or people) into
// recipients and when each is due, earliest first. A recipient named twice is
// due at its earliest. It also returns problems, such as names it doesn't know.
func (p planner) plan(targets []string) ([]Planned, []string) {
	due := map[string]Planned{}
	var problems []string
	add := func(r Planned) {
		r.Key = recipientKey(r.Channel, r.To)
		if old, ok := due[r.Key]; !ok || r.After < old.After {
			due[r.Key] = r
		}
	}
	var walk func(targets []string, after time.Duration, depth int)
	person := func(name string, after time.Duration) bool {
		if p.dir == nil {
			return false
		}
		contacts, ok := p.dir.Person(name)
		if !ok {
			return false
		}
		if len(contacts) == 0 {
			problems = append(problems, fmt.Sprintf("%s has no contacts", name))
		}
		for _, ct := range contacts {
			if _, ok := p.c.providers[ct.Channel]; !ok {
				problems = append(problems, fmt.Sprintf("%s's contact uses no channel %q", name, ct.Channel))
				continue
			}
			add(Planned{Channel: ct.Channel, To: ct.To, Person: name, After: after + ct.After.Duration})
		}
		return true
	}
	walk = func(targets []string, after time.Duration, depth int) {
		if depth > 4 {
			problems = append(problems, fmt.Sprintf("%v: escalations and teams nest too deeply", targets))
			return
		}
		for _, t := range targets {
			if steps, ok := p.c.Escalations[t]; ok {
				for _, s := range steps {
					// Escalation steps may name people and teams as well as channels.
					walk(s.Notify, after+s.After.Duration, depth+1)
				}
				continue
			}
			if _, ok := p.c.providers[t]; ok {
				add(Planned{Channel: t, After: after})
				continue
			}
			if p.dir != nil {
				if members, notify, ok := p.dir.Team(t); ok {
					if len(notify) > 0 {
						walk(notify, after, depth+1)
					} else {
						for _, m := range members {
							person(m, after)
						}
						if len(members) == 0 {
							problems = append(problems, fmt.Sprintf("team %s has no members", t))
						}
					}
					continue
				}
			}
			if person(t, after) {
				continue
			}
			problems = append(problems, fmt.Sprintf("no channel, escalation, team or person %q", t))
		}
	}
	walk(targets, 0, 0)
	out := make([]Planned, 0, len(due))
	for _, r := range due {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Planned) int {
		if a.After != b.After {
			return int(a.After - b.After)
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out, problems
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
