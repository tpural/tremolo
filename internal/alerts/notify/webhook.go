package notify

import (
	"context"
	"encoding/json"
	"net/http"
)

// webhook POSTs the notification as JSON, for anything not built in.
func init() {
	Register("webhook", func(config json.RawMessage, client *http.Client) (Provider, error) {
		var c webhook
		if err := decode(config, &c); err != nil {
			return nil, err
		}
		c.client = client
		return &c, required(map[string]string{"url": c.URL})
	})
}

type webhook struct {
	URL string `json:"url"`
	// Token is sent as a bearer token.
	Token  string `json:"token"`
	client *http.Client
}

func (w *webhook) Send(ctx context.Context, n Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return post(ctx, w.client, w.URL, jsonHeader(w.Token), body)
}
