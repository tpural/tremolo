// Package notify sends alert notifications through pluggable providers, such
// as Home Assistant, ntfy, Slack, email or SMS. A provider registers itself by
// type name; a channel in the config picks one by its type.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Notification is one message about an alert.
type Notification struct {
	// Event is firing, repeat, acknowledged or resolved.
	Event    string            `json:"event"`
	Rule     string            `json:"rule"`
	Key      string            `json:"key,omitempty"`
	Summary  string            `json:"summary"`
	Severity string            `json:"severity"`
	Labels   map[string]string `json:"labels,omitempty"`
	// Since is when the alert started firing.
	Since time.Time `json:"since"`
	// Unacked is how long it had been firing unacknowledged, for an escalation step.
	Unacked time.Duration `json:"unacked,omitempty"`
	// URL is the alerts page; AckURL opens it ready to acknowledge this alert.
	URL    string `json:"url,omitempty"`
	AckURL string `json:"ackUrl,omitempty"`
	// To is one person's address on this channel, e.g. their email or phone
	// number, in place of the channel's own recipients.
	To string `json:"to,omitempty"`
	// ForwardedBy is who passed the alert on to this recipient, with Note.
	ForwardedBy string `json:"forwardedBy,omitempty"`
	Note        string `json:"note,omitempty"`
}

// Title is a short heading, e.g. "Firing: host-down".
func (n Notification) Title() string {
	switch n.Event {
	case "resolved":
		return "Resolved: " + n.Rule
	case "acknowledged":
		return "Acknowledged: " + n.Rule
	case "repeat":
		return "Still firing: " + n.Rule
	}
	if n.ForwardedBy != "" {
		return "Forwarded to you: " + n.Rule
	}
	return "Firing: " + n.Rule
}

// Text is the message body in a sentence or two.
func (n Notification) Text() string {
	var b strings.Builder
	b.WriteString(n.Summary)
	if n.Key != "" && !strings.Contains(n.Summary, n.Key) {
		fmt.Fprintf(&b, " (%s)", n.Key)
	}
	switch {
	case n.Event == "resolved":
		fmt.Fprintf(&b, ", after %s", human(time.Since(n.Since)))
	case n.Unacked > 0 && n.Event != "acknowledged":
		fmt.Fprintf(&b, ". Unacknowledged for %s", human(n.Unacked))
	}
	if n.ForwardedBy != "" && n.Event != "resolved" {
		fmt.Fprintf(&b, ". From %s", n.ForwardedBy)
		if n.Note != "" {
			fmt.Fprintf(&b, ": %s", n.Note)
		}
	}
	return b.String()
}

// human writes a duration in minutes, e.g. "15m" or "2h5m".
func human(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "under a minute"
	}
	s := strings.TrimSuffix(d.String(), "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Provider delivers notifications to one place.
type Provider interface {
	Send(ctx context.Context, n Notification) error
}

// Factory builds a provider from a channel's config, as JSON. Client is
// shared by providers that use HTTP.
type Factory func(config json.RawMessage, client *http.Client) (Provider, error)

var factories = map[string]Factory{}

// Register makes a provider type available to channels. Providers call it
// from init.
func Register(typ string, f Factory) {
	if _, dup := factories[typ]; dup {
		panic("notify: provider registered twice: " + typ)
	}
	factories[typ] = f
}

// Types lists the registered provider types.
func Types() []string {
	out := make([]string, 0, len(factories))
	for t := range factories {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// New builds the provider a channel's config names with its type field.
func New(config json.RawMessage, client *http.Client) (Provider, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(config, &head); err != nil {
		return nil, err
	}
	f, ok := factories[head.Type]
	if !ok {
		return nil, fmt.Errorf("type %q is not one of %s", head.Type, strings.Join(Types(), ", "))
	}
	return f(config, client)
}

// decode reads a provider's own config, refusing fields it doesn't know so a
// typo doesn't go unnoticed.
func decode(config json.RawMessage, v any) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(config, &m); err != nil {
		return err
	}
	delete(m, "type")
	b, _ := json.Marshal(m)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// post sends body and fails on a non-2xx answer, with the start of its body.
func post(ctx context.Context, client *http.Client, url string, header http.Header, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func jsonHeader(token string) http.Header {
	h := http.Header{"Content-Type": {"application/json"}}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return h
}

// recipients is n.To when set, else the channel's own list.
func recipients(n Notification, own []string) ([]string, error) {
	if n.To != "" {
		return []string{n.To}, nil
	}
	if len(own) == 0 {
		return nil, errors.New("no recipient: set to on the channel, or use it from a person's contacts")
	}
	return own, nil
}

func required(fields map[string]string) error {
	var missing []string
	for name, v := range fields {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("needs %s", strings.Join(missing, ", "))
	}
	return nil
}
