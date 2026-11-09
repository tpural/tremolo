package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// Home Assistant's notify service, e.g. the companion app on a phone.
func init() {
	Register("homeassistant", func(config json.RawMessage, client *http.Client) (Provider, error) {
		var c homeAssistant
		if err := decode(config, &c); err != nil {
			return nil, err
		}
		c.client = client
		return &c, required(map[string]string{"url": c.URL, "token": c.Token})
	})
}

type homeAssistant struct {
	URL string `json:"url"`
	// Service is the notify service, e.g. mobile_app_my_phone. A person's
	// contact on this channel names their own.
	Service string `json:"service"`
	// Token is a long-lived access token.
	Token  string `json:"token"`
	client *http.Client
}

func (h *homeAssistant) Send(ctx context.Context, n Notification) error {
	data := map[string]any{
		// One notification per alert on the phone, replaced as it changes.
		"tag":   "tremolo-" + n.Rule + "-" + n.Key,
		"group": "tremolo",
	}
	if u := firstNonEmpty(n.AckURL, n.URL); u != "" {
		data["url"], data["clickAction"] = u, u
	}
	if n.Severity == "page" && (n.Event == "firing" || n.Event == "repeat") {
		// Pages break through Focus and Do Not Disturb where the phone allows it.
		data["push"] = map[string]any{"interruption-level": "time-sensitive"}
		data["priority"] = "high"
	}
	services, err := recipients(n, nonEmpty(h.Service))
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"title": n.Title(), "message": n.Text(), "data": data})
	if err != nil {
		return err
	}
	return post(ctx, h.client, strings.TrimRight(h.URL, "/")+"/api/services/notify/"+url.PathEscape(services[0]), jsonHeader(h.Token), body)
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
