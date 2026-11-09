package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type request struct {
	path   string
	header http.Header
	body   string
}

func capture(t *testing.T) (string, <-chan request) {
	got := make(chan request, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- request{r.URL.Path, r.Header.Clone(), string(b)}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

func build(t *testing.T, config string) Provider {
	t.Helper()
	p, err := New(json.RawMessage(config), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var firing = Notification{
	Event: "firing", Rule: "host-down", Key: "nas", Summary: "nas is not ready", Severity: "page",
	Since: time.Now().Add(-time.Hour), Unacked: 15 * time.Minute,
	URL: "http://tremolo/alerts", AckURL: "http://tremolo/alerts?ack=abc",
}

func TestProviders(t *testing.T) {
	u, got := capture(t)
	cases := []struct {
		config string
		check  func(request) bool
	}{
		{`{"type":"homeassistant","url":"` + u + `","service":"mobile_app_phone","token":"t"}`, func(r request) bool {
			return r.path == "/api/services/notify/mobile_app_phone" && r.header.Get("Authorization") == "Bearer t" &&
				strings.Contains(r.body, `"title":"Firing: host-down"`) && strings.Contains(r.body, "time-sensitive") && strings.Contains(r.body, "ack=abc")
		}},
		{`{"type":"ntfy","url":"` + u + `/alerts"}`, func(r request) bool {
			return r.path == "/alerts" && r.header.Get("Priority") == "urgent" && strings.Contains(r.header.Get("Actions"), "ack=abc") &&
				strings.HasSuffix(r.body, "Unacknowledged for 15m")
		}},
		{`{"type":"slack","url":"` + u + `/slack"}`, func(r request) bool {
			var m struct{ Text string }
			_ = json.Unmarshal([]byte(r.body), &m)
			return r.path == "/slack" && strings.Contains(m.Text, "rotating_light") && strings.Contains(m.Text, "ack=abc|Acknowledge>")
		}},
		{`{"type":"webhook","url":"` + u + `/hook","token":"x"}`, func(r request) bool {
			var n Notification
			return json.Unmarshal([]byte(r.body), &n) == nil && n.Key == "nas" && r.header.Get("Authorization") == "Bearer x"
		}},
		{`{"type":"twilio","url":"` + u + `","accountSid":"AC1","authToken":"secret","from":"+1555","to":["+1666"]}`, func(r request) bool {
			form, _ := url.ParseQuery(r.body)
			user, pass, _ := (&http.Request{Header: r.header}).BasicAuth()
			return r.path == "/2010-04-01/Accounts/AC1/Messages.json" && user == "AC1" && pass == "secret" &&
				form.Get("To") == "+1666" && strings.Contains(form.Get("Body"), "Ack: http://tremolo/alerts?ack=abc")
		}},
	}
	for _, c := range cases {
		if err := build(t, c.config).Send(context.Background(), firing); err != nil {
			t.Fatalf("%s: %v", c.config, err)
		}
		if r := <-got; !c.check(r) {
			t.Errorf("%s sent %+v", c.config, r)
		}
	}
}

func TestNewRejects(t *testing.T) {
	for _, config := range []string{
		`{"type":"pager"}`,
		`{"type":"slack"}`,
		`{"type":"slack","url":"u","channel":"#x"}`,
	} {
		if _, err := New(json.RawMessage(config), http.DefaultClient); err == nil {
			t.Errorf("%s: want error", config)
		}
	}
}

func TestFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }))
	defer srv.Close()
	err := build(t, `{"type":"webhook","url":"`+srv.URL+`"}`).Send(context.Background(), firing)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

// fakeSMTP accepts one message without TLS or auth and returns it.
func fakeSMTP(t *testing.T) (host string, port int, msg <-chan string) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		say("220 fake")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "DATA"):
				say("354 go")
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				out <- data.String()
				say("250 ok")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	addr := lis.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, out
}

func TestEmail(t *testing.T) {
	host, port, msg := fakeSMTP(t)
	p := build(t, `{"type":"email","host":"`+host+`","port":`+strconv.Itoa(port)+`,"from":"tremolo@lab","to":["me@lab"]}`)
	if err := p.Send(context.Background(), firing); err != nil {
		t.Fatal(err)
	}
	m := <-msg
	for _, want := range []string{"To: me@lab", "Subject: [PAGE] Firing: host-down: nas is not ready", "Acknowledge: http://tremolo/alerts?ack=abc"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q:\n%s", want, m)
		}
	}
}

func TestPersonalAddress(t *testing.T) {
	u, got := capture(t)
	n := firing
	n.To, n.ForwardedBy, n.Note = "alex", "tim", "can you look?"
	cases := []struct {
		config string
		check  func(request) bool
	}{
		{`{"type":"homeassistant","url":"` + u + `","token":"t"}`, func(r request) bool {
			return r.path == "/api/services/notify/alex" && strings.Contains(r.body, "Forwarded to you") && strings.Contains(r.body, "From tim: can you look?")
		}},
		{`{"type":"ntfy","url":"` + u + `/team-topic"}`, func(r request) bool { return r.path == "/alex" }},
		{`{"type":"slack","url":"` + u + `/slack"}`, func(r request) bool {
			var m struct{ Text string }
			_ = json.Unmarshal([]byte(r.body), &m)
			return strings.HasPrefix(m.Text, "<@alex> ")
		}},
		{`{"type":"twilio","url":"` + u + `","accountSid":"AC1","authToken":"s","from":"+1555","to":["+1666"]}`, func(r request) bool {
			form, _ := url.ParseQuery(r.body)
			return form.Get("To") == "alex"
		}},
	}
	for _, c := range cases {
		if err := build(t, c.config).Send(context.Background(), n); err != nil {
			t.Fatalf("%s: %v", c.config, err)
		}
		if r := <-got; !c.check(r) {
			t.Errorf("%s sent %+v", c.config, r)
		}
	}
	// A channel without its own recipients needs a person's address.
	if err := build(t, `{"type":"twilio","url":"`+u+`","accountSid":"AC1","authToken":"s","from":"+1555"}`).Send(context.Background(), firing); err == nil || !strings.Contains(err.Error(), "no recipient") {
		t.Errorf("no recipient: %v", err)
	}
}
