package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/users"
)

func alertError(w http.ResponseWriter, err error) {
	if errors.Is(err, alerts.ErrNoAlert) {
		apiError(w, http.StatusNotFound, err.Error())
		return
	}
	apiError(w, http.StatusBadRequest, err.Error())
}

// decodeRule reads a body holding a rule's source, which may be up to 64 KiB.
func decodeRule(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 80<<10)).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "body must be JSON: "+err.Error())
		return false
	}
	return true
}

// alertRoutes adds triage: acknowledging, taking, forwarding and notes, and
// acknowledging from a notification's link without signing in.
func alertRoutes(mux *http.ServeMux, e *alerts.Engine, opts Options) {
	type idBody struct {
		ID   string `json:"id"`
		To   string `json:"to"`
		Note string `json:"note"`
	}
	read := func(w http.ResponseWriter, r *http.Request) (idBody, bool) {
		var b idBody
		if !decodeBody(w, r, &b) {
			return b, false
		}
		if b.ID == "" || len(b.Note) > 2000 {
			apiError(w, http.StatusBadRequest, "needs an alert id, and a note under 2000 characters")
			return b, false
		}
		b.Note = strings.TrimSpace(b.Note)
		return b, true
	}
	mux.HandleFunc("POST /api/alerts/ack", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		b, ok := read(w, r)
		if !ok {
			return
		}
		a, err := e.Ack(r.Context(), b.ID, who(r))
		if err != nil {
			alertError(w, err)
			return
		}
		opts.Log.Info("acknowledged", "alert", a.ID, "by", who(r))
		audit(opts, r, "", "acknowledged", a.ID, "")
		writeJSON(w, a)
	}))
	mux.HandleFunc("POST /api/alerts/take", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		b, ok := read(w, r)
		if !ok {
			return
		}
		a, err := e.Take(r.Context(), b.ID, who(r))
		if err != nil {
			alertError(w, err)
			return
		}
		opts.Log.Info("taken", "alert", a.ID, "by", who(r))
		audit(opts, r, "", "took", a.ID, "")
		writeJSON(w, a)
	}))
	mux.HandleFunc("POST /api/alerts/forward", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		b, ok := read(w, r)
		if !ok {
			return
		}
		if b.To == "" {
			apiError(w, http.StatusBadRequest, "forward to whom?")
			return
		}
		a, err := e.Forward(r.Context(), b.ID, b.To, who(r), b.Note)
		if err != nil {
			alertError(w, err)
			return
		}
		opts.Log.Info("forwarded", "alert", a.ID, "to", b.To, "by", who(r))
		audit(opts, r, "", "forwarded", a.ID, strings.TrimSuffix("to "+b.To+": "+b.Note, ": "))
		writeJSON(w, a)
	}))
	mux.HandleFunc("POST /api/alerts/note", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		b, ok := read(w, r)
		if !ok {
			return
		}
		if b.Note == "" {
			apiError(w, http.StatusBadRequest, "the note is empty")
			return
		}
		a, err := e.Note(r.Context(), b.ID, who(r), b.Note)
		if err != nil {
			alertError(w, err)
			return
		}
		audit(opts, r, "", "added a note", a.ID, b.Note)
		writeJSON(w, a)
	}))

	// The rule editor: versions, saving, deleting, and replaying a draft.
	ruleError := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, alerts.ErrNoRule):
			apiError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, alerts.ErrReadOnly):
			apiError(w, http.StatusConflict, err.Error())
		case errors.Is(err, alerts.ErrBusy):
			apiError(w, http.StatusTooManyRequests, err.Error())
		default:
			apiError(w, http.StatusBadRequest, err.Error())
		}
	}
	mux.HandleFunc("GET /api/alerts/rules/{name}/versions", func(w http.ResponseWriter, r *http.Request) {
		v, err := e.Versions(r.Context(), r.PathValue("name"))
		if err != nil {
			ruleError(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("GET /api/alerts/rules/{name}/versions/{version}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("version"))
		if err != nil {
			apiError(w, http.StatusBadRequest, "version must be a number")
			return
		}
		v, err := e.VersionOf(r.Context(), r.PathValue("name"), n)
		if err != nil {
			ruleError(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("PUT /api/alerts/rules/{name}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Source string `json:"source"`
			Note   string `json:"note"`
		}
		if !decodeRule(w, r, &body) {
			return
		}
		name := r.PathValue("name")
		v, err := e.SaveRule(r.Context(), name, body.Source, body.Note, who(r))
		if err != nil {
			ruleError(w, err)
			return
		}
		opts.Log.Info("rule saved", "rule", name, "version", v.Version, "by", who(r))
		audit(opts, r, "", "saved a rule", name, strings.TrimSuffix("version "+strconv.Itoa(v.Version)+": "+v.Note, ": "))
		writeJSON(w, v)
	}))
	mux.HandleFunc("DELETE /api/alerts/rules/{name}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := e.DeleteRule(r.Context(), name); err != nil {
			ruleError(w, err)
			return
		}
		opts.Log.Info("rule deleted", "rule", name, "by", who(r))
		audit(opts, r, "", "deleted a rule", name, "")
		w.WriteHeader(http.StatusNoContent)
	}))
	// Send a test notification through a channel, to check it reaches people.
	mux.HandleFunc("POST /api/alerts/channels/{name}/test", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		err := e.TestChannel(r.Context(), name, who(r))
		switch {
		case errors.Is(err, alerts.ErrNoChannel):
			apiError(w, http.StatusNotFound, err.Error())
			return
		case err != nil:
			audit(opts, r, "", "sent a test notification", name, "failed: "+err.Error())
			apiError(w, http.StatusBadGateway, err.Error())
			return
		}
		audit(opts, r, "", "sent a test notification", name, "delivered")
		writeJSON(w, map[string]bool{"delivered": true})
	}))
	mux.HandleFunc("POST /api/alerts/replay", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name   string  `json:"name"`
			Source string  `json:"source"`
			Hours  float64 `json:"hours"`
		}
		if !decodeRule(w, r, &body) {
			return
		}
		to := time.Now()
		rep, err := e.Replay(r.Context(), body.Name, body.Source, to.Add(-time.Duration(body.Hours*float64(time.Hour))), to)
		if err != nil {
			ruleError(w, err)
			return
		}
		writeJSON(w, rep)
	}))

	mux.HandleFunc("GET /api/alerts/history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		past, err := e.History(r.Context(), alerts.HistoryFilter{Rule: q.Get("rule"), Text: q.Get("q"), Before: before, Limit: limit})
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, past)
	})

	// A notification's link: shows the alert, then acknowledges it. The
	// token is the credential; a signed-in user, or the token's owner, is who.
	type peek struct {
		Alert  alerts.Alert `json:"alert"`
		Person string       `json:"person,omitempty"`
	}
	mux.HandleFunc("GET /api/ack/{token}", func(w http.ResponseWriter, r *http.Request) {
		a, person, err := e.Peek(r.PathValue("token"))
		if err != nil {
			alertError(w, err)
			return
		}
		a.Timeline, a.Labels = nil, nil
		writeJSON(w, peek{a, person})
	})
	mux.HandleFunc("POST /api/ack/{token}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			By string `json:"by"`
		}
		if r.ContentLength > 0 && !decodeBody(w, r, &body) {
			return
		}
		tok := r.PathValue("token")
		_, person, err := e.Peek(tok)
		if err != nil {
			alertError(w, err)
			return
		}
		by := who(r)
		if by == "" {
			by = person
		}
		if by == "" {
			by = strings.TrimSpace(body.By)
			if len(by) > 100 {
				by = by[:100]
			}
		}
		a, err := e.Ack(r.Context(), tok, by)
		if err != nil {
			alertError(w, err)
			return
		}
		opts.Log.Info("acknowledged by link", "alert", a.ID, "by", a.AckedBy)
		audit(opts, r, a.AckedBy, "acknowledged by link", a.ID, "")
		a.Timeline, a.Labels = nil, nil
		writeJSON(w, peek{a, person})
	})
}
