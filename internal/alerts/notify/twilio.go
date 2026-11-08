package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// SMS through Twilio's Messages API.
func init() {
	Register("twilio", func(config json.RawMessage, client *http.Client) (Provider, error) {
		var c twilio
		if err := decode(config, &c); err != nil {
			return nil, err
		}
		if c.URL == "" {
			c.URL = "https://api.twilio.com"
		}
		c.client = client
		return &c, required(map[string]string{"accountSid": c.AccountSID, "authToken": c.AuthToken, "from": c.From, "to": strings.Join(c.To, ",")})
	})
}

type twilio struct {
	AccountSID string `json:"accountSid"`
	AuthToken  string `json:"authToken"`
	// From is a Twilio number, e.g. +15551234567; To are the numbers to text.
	From string   `json:"from"`
	To   []string `json:"to"`
	// URL is the API base; tests point it elsewhere.
	URL    string `json:"url"`
	client *http.Client
}

func (t *twilio) Send(ctx context.Context, n Notification) error {
	body := n.Title() + ": " + n.Text()
	if n.AckURL != "" && (n.Event == "firing" || n.Event == "repeat") {
		body += " Ack: " + n.AckURL
	}
	endpoint := strings.TrimRight(t.URL, "/") + "/2010-04-01/Accounts/" + url.PathEscape(t.AccountSID) + "/Messages.json"
	for _, to := range t.To {
		form := url.Values{"From": {t.From}, "To": {to}, "Body": {body}}
		h := http.Header{
			"Content-Type":  {"application/x-www-form-urlencoded"},
			"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(t.AccountSID+":"+t.AuthToken))},
		}
		if err := post(ctx, t.client, endpoint, h, []byte(form.Encode())); err != nil {
			return err
		}
	}
	return nil
}
