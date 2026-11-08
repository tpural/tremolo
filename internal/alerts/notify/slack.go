package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Slack through an incoming webhook (api.slack.com/messaging/webhooks).
func init() {
	Register("slack", func(config json.RawMessage, client *http.Client) (Provider, error) {
		var c slack
		if err := decode(config, &c); err != nil {
			return nil, err
		}
		c.client = client
		return &c, required(map[string]string{"url": c.URL})
	})
}

type slack struct {
	// URL is the incoming webhook URL.
	URL    string `json:"url"`
	client *http.Client
}

func (s *slack) Send(ctx context.Context, n Notification) error {
	icon := map[string]string{"page": ":rotating_light:", "warn": ":warning:", "info": ":information_source:"}[n.Severity]
	switch n.Event {
	case "resolved":
		icon = ":white_check_mark:"
	case "acknowledged":
		icon = ":eyes:"
	}
	text := fmt.Sprintf("%s *%s*\n%s", icon, n.Title(), n.Text())
	if n.AckURL != "" && (n.Event == "firing" || n.Event == "repeat") {
		text += fmt.Sprintf("\n<%s|Acknowledge>", n.AckURL)
	} else if n.URL != "" {
		text += fmt.Sprintf("\n<%s|Open alerts>", n.URL)
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return post(ctx, s.client, s.URL, jsonHeader(""), body)
}
