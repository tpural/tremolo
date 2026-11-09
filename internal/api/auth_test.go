package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

func TestAuth(t *testing.T) {
	dir, err := users.Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Bootstrap(""); err != nil {
		t.Fatal(err)
	}
	store, _ := dashboard.Load("")
	db, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(Options{
		Dashboards: store, Users: dir, DB: db, Log: slog.New(slog.DiscardHandler),
		UI: fstest.MapFS{"index.html": {Data: []byte("app")}},
	}))
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	do := func(method, path, body string, header ...string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// A new tremolo asks the first visitor to create the admin, once.
	if code, body := do("GET", "/api/setup", ""); code != 200 || body != `{"needed":true}`+"\n" {
		t.Fatalf("setup needed = %d %q", code, body)
	}
	if code, body := do("POST", "/api/setup", `{"name":"Admin","password":"admin-password"}`); code != 200 || !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("setup = %d %s", code, body)
	}
	if code, _ := do("POST", "/api/users", `{"name":"viewer","role":"viewer","password":"viewer-password"}`); code != 200 {
		t.Fatalf("setup didn't sign in: %d", code)
	}
	if code, _ := do("POST", "/api/setup", `{"name":"mallory","password":"password1"}`); code != 409 {
		t.Errorf("second setup = %d", code)
	}
	if _, body := do("GET", "/api/setup", ""); !strings.Contains(body, "false") {
		t.Errorf("setup still needed: %s", body)
	}
	do("POST", "/api/logout", "")

	// The app and health are open; the API needs signing in.
	for path, want := range map[string]int{"/": 200, "/hosts/x": 200, "/healthz": 200, "/api/version": 200, "/api/dashboards": 401, "/api/me": 401} {
		if code, _ := do("GET", path, ""); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}
	if code, _ := do("POST", "/api/login", `{"name":"viewer","password":"nope"}`); code != 401 {
		t.Fatalf("bad login = %d", code)
	}
	if code, body := do("POST", "/api/login", `{"name":"Viewer","password":"viewer-password"}`); code != 200 || !strings.Contains(body, `"role":"viewer"`) || strings.Contains(body, "passwordHash") {
		t.Fatalf("login = %d %s", code, body)
	}
	if code, _ := do("GET", "/api/dashboards", ""); code != 200 {
		t.Errorf("signed in: %d", code)
	}
	// Viewers can't change things, or read the audit log.
	if code, _ := do("GET", "/api/audit", ""); code != 403 {
		t.Errorf("viewer reading the audit log = %d", code)
	}
	if code, _ := do("POST", "/api/users", `{"name":"x","role":"viewer","password":"password1"}`); code != 403 {
		t.Errorf("viewer creating a user = %d", code)
	}
	// Another site can't use the cookie.
	if code, _ := do("POST", "/api/logout", "", "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Errorf("cross-site = %d", code)
	}
	if code, _ := do("POST", "/api/logout", ""); code != 204 {
		t.Errorf("logout = %d", code)
	}
	if code, _ := do("GET", "/api/me", ""); code != 401 {
		t.Errorf("after logout = %d", code)
	}

	// Admins manage users; API tokens work without cookies.
	do("POST", "/api/login", `{"name":"admin","password":"admin-password"}`)
	if code, body := do("POST", "/api/users", `{"name":"alex","role":"editor","password":"password1"}`); code != 200 {
		t.Fatalf("create = %d %s", code, body)
	}
	code, body := do("POST", "/api/me/tokens", `{"name":"script"}`)
	if code != 200 {
		t.Fatalf("token = %d", code)
	}
	var created struct{ Secret string }
	if err := json.Unmarshal([]byte(body), &created); err != nil || !strings.HasPrefix(created.Secret, users.TokenPrefix) {
		t.Fatalf("token body: %s", body)
	}
	secret := created.Secret
	// Everything above is in the audit log, newest first, without secrets.
	code, body = do("GET", "/api/audit", "")
	var log []state.Entry
	if err := json.Unmarshal([]byte(body), &log); code != 200 || err != nil {
		t.Fatalf("audit = %d %s", code, body)
	}
	var got []string
	for _, e := range log {
		got = append(got, e.Actor+" "+e.Action+" "+e.Target)
	}
	want := []string{"admin created a token admin", "admin created a user alex", "admin signed in ", "viewer signed in ", "viewer sign-in failed ", "admin created a user viewer", "admin set up tremolo as the first admin "}
	if strings.Join(got, "|") != strings.Join(want, "|") || strings.Contains(body, "password") || strings.Contains(body, secret) {
		t.Errorf("audit = %q\n%s", got, body)
	}
	c.Jar, _ = cookiejar.New(nil)
	if code, _ := do("GET", "/api/me", "", "Authorization", "Bearer "+secret); code != 200 {
		t.Errorf("token = %d", code)
	}
	if code, _ := do("GET", "/api/me", "", "Authorization", "Bearer tremolo_wrong"); code != 401 {
		t.Errorf("wrong token = %d", code)
	}
}
