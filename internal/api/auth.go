package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/users"
)

// SessionCookie holds a signed-in browser's session.
const SessionCookie = "tremolo_session"

type ctxKey struct{}

// current returns the signed-in user.
func current(r *http.Request) (users.User, bool) {
	u, ok := r.Context().Value(ctxKey{}).(users.User)
	return u, ok
}

// identify finds who a request is from: an API token or a session cookie,
// returning the token too when there's one.
func identify(dir *users.Directory, r *http.Request) (users.User, *users.Token, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		u, t, ok := dir.TokenAuth(strings.TrimPrefix(h, "Bearer "))
		return u, &t, ok
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		u, ok := dir.Session(c.Value)
		return u, nil, ok
	}
	return users.User{}, nil, false
}

// public lists what works without signing in: the app itself, signing in,
// health, and acknowledging from a notification's link.
func public(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case !strings.HasPrefix(p, "/api/"):
		return true
	case p == "/api/login", p == "/api/setup", p == "/api/version", strings.HasPrefix(p, "/api/ack/"):
		return true
	}
	return false
}

// authenticate requires a signed-in user for the API, and refuses requests
// that change things from another site.
func authenticate(dir *users.Directory, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			apiError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		u, t, ok := identify(dir, r)
		if ok {
			ctx := context.WithValue(r.Context(), ctxKey{}, u)
			if t != nil {
				ctx = context.WithValue(ctx, tokenKey{}, *t)
			}
			r = r.WithContext(ctx)
		} else if !public(r) {
			apiError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin is false when a browser says the request came from another site.
func sameOrigin(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return true
}

// need wraps h so only users with at least role may call it.
func need(role users.Role, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := current(r)
		if !ok {
			apiError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		if !u.Role.Can(role) {
			apiError(w, http.StatusForbidden, "this needs the "+string(role)+" role")
			return
		}
		h(w, r)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "body must be JSON: "+err.Error())
		return false
	}
	return true
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// client is who's signing in, for their list of sessions. Behind a proxy the
// address is the one it forwarded for.
func client(r *http.Request) users.Client {
	addr := r.RemoteAddr
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		addr, _, _ = strings.Cut(f, ",")
	}
	return users.Client{Address: strings.TrimSpace(addr), Agent: r.UserAgent()}
}

// setSession gives the browser its session cookie. It's Secure whenever the
// browser reached tremolo over HTTPS; plain HTTP on a LAN still works.
func setSession(w http.ResponseWriter, r *http.Request, sec string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request's scheme
		Name: SessionCookie, Value: sec, Path: "/", HttpOnly: true, Secure: secure(r),
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(users.SessionLength),
	})
}

// me is the signed-in user, with their teams.
type me struct {
	users.User
	Teams []string `json:"teams"`
	// Token is the API token the request came with: an assistant learns what
	// it may do from its scope.
	Token *users.Token `json:"token,omitempty"`
}

func userError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrNotFound):
		apiError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrExists):
		apiError(w, http.StatusConflict, err.Error())
	default:
		apiError(w, http.StatusBadRequest, err.Error())
	}
}

// userRoutes adds signing in, the user's own profile and tokens, and the
// admin's management of users and teams.
func userRoutes(mux *http.ServeMux, opts Options) {
	dir := opts.Users
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name, Password string }
		if !decodeBody(w, r, &body) {
			return
		}
		sec, u, err := dir.Login(strings.ToLower(strings.TrimSpace(body.Name)), body.Password, client(r))
		switch {
		case errors.Is(err, users.ErrLocked):
			apiError(w, http.StatusTooManyRequests, err.Error())
			return
		case err != nil:
			opts.Log.Warn("sign-in failed", "user", body.Name, "remote", r.RemoteAddr)
			audit(opts, r, clip(body.Name, 64), "sign-in failed", "", r.RemoteAddr)
			apiError(w, http.StatusUnauthorized, err.Error())
			return
		}
		setSession(w, r, sec)
		opts.Log.Info("signed in", "user", u.Name)
		audit(opts, r, u.Name, "signed in", "", r.RemoteAddr)
		m := me{User: u, Teams: dir.TeamsOf(u.Name)}
		if t, ok := token(r); ok {
			m.Token = &t
		}
		writeJSON(w, m)
	})
	// Setup is open until the first user exists: whoever opens a new tremolo
	// first creates the admin and is signed in as them.
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]bool{"needed": dir.Empty()})
	})
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name, Password string }
		if !decodeBody(w, r, &body) {
			return
		}
		name := strings.ToLower(strings.TrimSpace(body.Name))
		if err := dir.Setup(name, body.Password); err != nil {
			if errors.Is(err, users.ErrSetUp) {
				apiError(w, http.StatusConflict, err.Error())
				return
			}
			userError(w, err)
			return
		}
		sec, u, err := dir.Login(name, body.Password, client(r))
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		setSession(w, r, sec)
		opts.Log.Info("first admin created", "user", u.Name, "remote", r.RemoteAddr)
		audit(opts, r, u.Name, "set up tremolo as the first admin", "", r.RemoteAddr)
		m := me{User: u, Teams: dir.TeamsOf(u.Name)}
		if t, ok := token(r); ok {
			m.Token = &t
		}
		writeJSON(w, m)
	})
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(SessionCookie); err == nil {
			dir.Logout(c.Value)
		}
		//nolint:gosec // Secure follows the request's scheme
		http.SetCookie(w, &http.Cookie{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		m := me{User: u, Teams: dir.TeamsOf(u.Name)}
		if t, ok := token(r); ok {
			m.Token = &t
		}
		writeJSON(w, m)
	})
	mux.HandleFunc("PUT /api/me", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		var body struct {
			DisplayName *string          `json:"displayName"`
			Contacts    []alerts.Contact `json:"contacts"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Contacts != nil && opts.Alerts != nil {
			channels, _ := opts.Alerts.Targets()
			for _, c := range body.Contacts {
				if !slices.Contains(channels, c.Channel) {
					apiError(w, http.StatusBadRequest, "no channel "+c.Channel)
					return
				}
			}
		}
		if err := dir.Update(u.Name, body.DisplayName, nil, body.Contacts); err != nil {
			userError(w, err)
			return
		}
		u, _ = dir.Get(u.Name)
		m := me{User: u, Teams: dir.TeamsOf(u.Name)}
		if t, ok := token(r); ok {
			m.Token = &t
		}
		writeJSON(w, m)
	})
	mux.HandleFunc("POST /api/me/password", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		var body struct{ Old, New string }
		if !decodeBody(w, r, &body) {
			return
		}
		if err := dir.ChangePassword(u.Name, body.Old, body.New); err != nil {
			if errors.Is(err, users.ErrBadLogin) {
				apiError(w, http.StatusForbidden, "the current password is wrong")
				return
			}
			userError(w, err)
			return
		}
		opts.Log.Info("password changed", "user", u.Name)
		audit(opts, r, "", "changed their password", u.Name, "")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/me/sessions", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		sec := ""
		if c, err := r.Cookie(SessionCookie); err == nil {
			sec = c.Value
		}
		writeJSON(w, dir.Sessions(u.Name, sec))
	})
	mux.HandleFunc("DELETE /api/me/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		if err := dir.EndSession(u.Name, r.PathValue("id")); err != nil {
			userError(w, err)
			return
		}
		audit(opts, r, "", "signed out a session", u.Name, "")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/me/tokens", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		writeJSON(w, dir.Tokens(u.Name))
	})
	mux.HandleFunc("POST /api/me/tokens", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		var body struct {
			Name    string
			Scope   *users.Scope
			Expires time.Time
		}
		if !decodeBody(w, r, &body) {
			return
		}
		sec, t, err := dir.CreateToken(u.Name, body.Name, body.Scope, body.Expires)
		if err != nil {
			userError(w, err)
			return
		}
		kind := "created a token"
		if t.Scope != nil {
			kind = "created an assistant token"
		}
		audit(opts, r, "", kind, u.Name, t.Name)
		writeJSON(w, map[string]any{"token": t, "secret": sec})
	})
	mux.HandleFunc("DELETE /api/me/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		if err := dir.DeleteToken(u.Name, r.PathValue("id")); err != nil {
			userError(w, err)
			return
		}
		audit(opts, r, "", "deleted a token", u.Name, "")
		w.WriteHeader(http.StatusNoContent)
	})

	// Everyone sees who's who, for forwarding; contacts are for admins.
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		u, _ := current(r)
		list := dir.List()
		if !u.Role.Can(users.Admin) {
			for i := range list {
				list[i].Contacts = nil
			}
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("POST /api/users", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name, DisplayName, Password string
			Role                        users.Role
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if clash(opts, body.Name) {
			apiError(w, http.StatusConflict, "a channel or escalation has that name")
			return
		}
		if err := dir.Create(body.Name, body.DisplayName, body.Role, body.Password); err != nil {
			userError(w, err)
			return
		}
		by, _ := current(r)
		opts.Log.Info("user created", "user", body.Name, "role", body.Role, "by", by.Name)
		audit(opts, r, "", "created a user", body.Name, string(body.Role))
		u, _ := dir.Get(body.Name)
		writeJSON(w, u)
	}))
	mux.HandleFunc("PUT /api/users/{name}", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			DisplayName *string          `json:"displayName"`
			Role        *users.Role      `json:"role"`
			Password    string           `json:"password"`
			Contacts    []alerts.Contact `json:"contacts"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if err := dir.Update(name, body.DisplayName, body.Role, body.Contacts); err != nil {
			userError(w, err)
			return
		}
		if body.Password != "" {
			if err := dir.SetPassword(name, body.Password); err != nil {
				userError(w, err)
				return
			}
		}
		by, _ := current(r)
		opts.Log.Info("user changed", "user", name, "by", by.Name, "password", body.Password != "")
		audit(opts, r, "", "changed a user", name, changes(body.Role, body.Contacts != nil, body.Password != ""))
		u, _ := dir.Get(name)
		writeJSON(w, u)
	}))
	mux.HandleFunc("DELETE /api/users/{name}", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		by, _ := current(r)
		name := r.PathValue("name")
		if name == by.Name {
			apiError(w, http.StatusBadRequest, "you can't delete yourself")
			return
		}
		if err := dir.Delete(name); err != nil {
			userError(w, err)
			return
		}
		opts.Log.Info("user deleted", "user", name, "by", by.Name)
		audit(opts, r, "", "deleted a user", name, "")
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/audit", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		log, err := opts.DB.AuditLog(r.Context(), state.AuditFilter{Before: before, Actor: q.Get("actor"), Via: q.Get("via"), Limit: limit})
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if log == nil {
			log = []state.Entry{}
		}
		writeJSON(w, log)
	}))
	mux.HandleFunc("DELETE /api/users/{name}/sessions", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		n, err := dir.EndSessions(name)
		if err != nil {
			userError(w, err)
			return
		}
		audit(opts, r, "", "signed a user out everywhere", name, strconv.FormatInt(n, 10)+" sessions")
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/teams", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, dir.Teams()) })
	mux.HandleFunc("PUT /api/teams/{name}", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Members []string `json:"members"`
			Notify  []string `json:"notify"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		name := r.PathValue("name")
		if clash(opts, name) {
			apiError(w, http.StatusConflict, "a channel or escalation has that name")
			return
		}
		if err := dir.SaveTeam(users.Team{Name: name, Members: body.Members, Notify: body.Notify}); err != nil {
			userError(w, err)
			return
		}
		audit(opts, r, "", "saved a team", name, "members: "+strings.Join(body.Members, ", "))
		writeJSON(w, dir.Teams())
	}))
	mux.HandleFunc("DELETE /api/teams/{name}", need(users.Admin, func(w http.ResponseWriter, r *http.Request) {
		if err := dir.DeleteTeam(r.PathValue("name")); err != nil {
			userError(w, err)
			return
		}
		audit(opts, r, "", "deleted a team", r.PathValue("name"), "")
		w.WriteHeader(http.StatusNoContent)
	}))
}

// audit records what someone did; actor defaults to the signed-in user.
// A failure is logged: it shouldn't undo what was done.
func audit(opts Options, r *http.Request, actor, action, target, detail string) {
	if actor == "" {
		actor = who(r)
	}
	e := state.Entry{Actor: actor, Action: action, Target: target, Detail: clip(detail, 500)}
	if t, ok := token(r); ok {
		e.Via = t.Name
	}
	err := opts.DB.Audit(r.Context(), e)
	if err != nil {
		opts.Log.Warn("audit log", "action", action, "err", err)
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// changes says what an admin changed about a user.
func changes(role *users.Role, contacts, password bool) string {
	var out []string
	if role != nil {
		out = append(out, "role "+string(*role))
	}
	if contacts {
		out = append(out, "contacts")
	}
	if password {
		out = append(out, "password reset")
	}
	return strings.Join(out, ", ")
}

// clash reports whether a user or team name is already a channel or escalation.
func clash(opts Options, name string) bool {
	if opts.Alerts == nil {
		return false
	}
	channels, escalations := opts.Alerts.Targets()
	return slices.Contains(channels, name) || slices.Contains(escalations, name)
}
