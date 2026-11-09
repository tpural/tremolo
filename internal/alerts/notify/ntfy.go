package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// ntfy (ntfy.sh, or self-hosted): a topic URL to publish to.
func init() {
	Register("ntfy", func(config json.RawMessage, client *http.Client) (Provider, error) {
		var c ntfy
		if err := decode(config, &c); err != nil {
			return nil, err
		}
		c.client = client
		return &c, required(map[string]string{"url": c.URL})
	})
}

type ntfy struct {
	// URL is the topic, e.g. https://ntfy.sh/my-alerts. A person's contact on
	// this channel names their own topic on the same server.
	URL string `json:"url"`
	// Token is an access token, for protected topics.
	Token  string `json:"token"`
	client *http.Client
}

func (t *ntfy) Send(ctx context.Context, n Notification) error {
	h := http.Header{}
	h.Set("Title", n.Title())
	h.Set("Priority", map[string]string{"page": "urgent", "warn": "high", "info": "default"}[n.Severity])
	tag := map[string]string{"page": "rotating_light", "warn": "warning", "info": "information_source"}[n.Severity]
	switch n.Event {
	case "resolved":
		tag, h["Priority"] = "white_check_mark", []string{"default"}
	case "acknowledged":
		tag, h["Priority"] = "eyes", []string{"default"}
	}
	h.Set("Tags", tag)
	if u := firstNonEmpty(n.AckURL, n.URL); u != "" {
		h.Set("Click", u)
	}
	if n.AckURL != "" && (n.Event == "firing" || n.Event == "repeat") {
		h.Set("Actions", "view, Acknowledge, "+n.AckURL)
	}
	if t.Token != "" {
		h.Set("Authorization", "Bearer "+t.Token)
	}
	target := t.URL
	if n.To != "" {
		u, err := url.Parse(t.URL)
		if err != nil {
			return err
		}
		u.Path, u.RawQuery = "/"+url.PathEscape(n.To), ""
		target = u.String()
	}
	return post(ctx, t.client, target, h, []byte(n.Text()))
}
