package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Email over SMTP, with STARTTLS when the server offers it.
func init() {
	Register("email", func(config json.RawMessage, _ *http.Client) (Provider, error) {
		var c email
		if err := decode(config, &c); err != nil {
			return nil, err
		}
		if err := required(map[string]string{"host": c.Host, "from": c.From, "to": strings.Join(c.To, ",")}); err != nil {
			return nil, err
		}
		if c.Port == 0 {
			c.Port = 587
		}
		return &c, nil
	})
}

type email struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	// insecure skips certificate checks; only tests set it.
	insecure bool
}

func (e *email) Send(ctx context.Context, n Notification) error {
	addr := net.JoinHostPort(e.Host, fmt.Sprint(e.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: e.Host, InsecureSkipVerify: e.insecure, MinVersion: tls.VersionTLS12}); err != nil { //nolint:gosec // only tests set insecure
			return err
		}
	}
	if e.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", e.Username, e.Password, e.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(e.From); err != nil {
		return err
	}
	for _, to := range e.To {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(e.message(n))); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (e *email) message(n Notification) string {
	var b strings.Builder
	header := func(k, v string) {
		fmt.Fprintf(&b, "%s: %s\r\n", k, strings.NewReplacer("\r", "", "\n", "").Replace(v))
	}
	header("From", e.From)
	header("To", strings.Join(e.To, ", "))
	header("Subject", "["+strings.ToUpper(n.Severity)+"] "+n.Title()+": "+n.Summary)
	header("Date", time.Now().Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	b.WriteString("\r\n")
	b.WriteString(n.Text() + "\r\n")
	for k, v := range n.Labels {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	if n.AckURL != "" && (n.Event == "firing" || n.Event == "repeat") {
		fmt.Fprintf(&b, "\r\nAcknowledge: %s\r\n", n.AckURL)
	}
	if n.URL != "" {
		fmt.Fprintf(&b, "Alerts: %s\r\n", n.URL)
	}
	return b.String()
}
