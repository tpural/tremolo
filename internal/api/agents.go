package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/agents"
	"github.com/tpural/tremolo/internal/coverage"
	"github.com/tpural/tremolo/internal/users"
)

// actor is who a request acts as in investigations: an assistant token's
// name, or the person.
func actor(r *http.Request) (name string, agent bool) {
	if t, ok := token(r); ok && t.Scope != nil {
		return t.Name, true
	}
	return who(r), false
}

func agentsError(w http.ResponseWriter, err error) {
	var claimed agents.ClaimedError
	var refused agents.Refusal
	switch {
	case errors.Is(err, agents.ErrNotFound):
		apiError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, agents.ErrClosed), errors.As(err, &claimed), errors.As(err, &refused):
		apiError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agents.ErrInvalid):
		apiError(w, http.StatusBadRequest, err.Error())
	default:
		apiError(w, http.StatusInternalServerError, err.Error())
	}
}

func idOf(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		apiError(w, http.StatusBadRequest, name+" must be a number")
		return 0, false
	}
	return id, true
}

// agentRoutes are investigations and the agents' rails.
func agentRoutes(mux *http.ServeMux, opts Options) {
	svc := opts.Agents
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.State(r.Context())
		if err != nil {
			agentsError(w, err)
			return
		}
		writeJSON(w, st)
	})
	mux.HandleFunc("POST /api/agents/pause", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Note string }
		if !decodeBody(w, r, &body) {
			return
		}
		if err := svc.Pause(r.Context(), who(r), strings.TrimSpace(body.Note)); err != nil {
			agentsError(w, err)
			return
		}
		audit(opts, r, "", "paused the agents", "", body.Note)
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /api/agents/resume", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Resume(r.Context(), who(r)); err != nil {
			agentsError(w, err)
			return
		}
		audit(opts, r, "", "resumed the agents", "", "")
		w.WriteHeader(http.StatusNoContent)
	}))

	// The daily report: a preview of the last day, or sent now.
	mux.HandleFunc("GET /api/agents/report", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(svc.Report(r.Context(), time.Now().Add(-24*time.Hour))))
	})
	mux.HandleFunc("POST /api/agents/report", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		if len(svc.Config().Notify) == 0 {
			apiError(w, http.StatusConflict, "no channels to send to: set agents.notify in the chart")
			return
		}
		svc.SendReport(r.Context())
		audit(opts, r, "", "sent the agents' report", "", "")
		w.WriteHeader(http.StatusNoContent)
	}))
	if c := opts.Coverage; c != nil {
		mux.HandleFunc("GET /api/coverage", func(w http.ResponseWriter, r *http.Request) {
			gaps := c.Check(r.Context(), time.Now())
			if gaps == nil {
				gaps = []coverage.Gap{}
			}
			writeJSON(w, gaps)
		})
	}

	mux.HandleFunc("GET /api/investigations", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := agents.Filter{Kind: q.Get("kind"), Alert: q.Get("alert"), Rule: q.Get("rule")}
		if st := q.Get("status"); st != "" {
			f.Status = strings.Split(st, ",")
		}
		f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
		f.Limit, _ = strconv.Atoi(q.Get("limit"))
		list, err := svc.List(r.Context(), f)
		if err != nil {
			agentsError(w, err)
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/investigations/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		i, err := svc.Get(r.Context(), id)
		if err != nil {
			agentsError(w, err)
			return
		}
		writeJSON(w, i)
	})
	mux.HandleFunc("POST /api/investigations", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Kind, Title, Text string }
		if !decodeBody(w, r, &body) {
			return
		}
		by, agent := actor(r)
		i, err := svc.Open(r.Context(), body.Kind, body.Title, body.Text, by, agent)
		if err != nil {
			agentsError(w, err)
			return
		}
		audit(opts, r, "", "opened an investigation", strconv.FormatInt(i.ID, 10), i.Title)
		writeJSON(w, i)
	}))
	mux.HandleFunc("POST /api/investigations/{id}/claim", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		by, agent := actor(r)
		i, err := svc.Claim(r.Context(), id, by, agent)
		if err != nil {
			agentsError(w, err)
			return
		}
		writeJSON(w, i)
	}))
	mux.HandleFunc("POST /api/investigations/{id}/events", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		var body struct{ Kind, Text string }
		if !decodeBody(w, r, &body) {
			return
		}
		by, agent := actor(r)
		e, err := svc.Record(r.Context(), id, by, agent, body.Kind, body.Text)
		if err != nil {
			agentsError(w, err)
			return
		}
		writeJSON(w, e)
	}))
	mux.HandleFunc("POST /api/investigations/{id}/actions", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		var body agents.Action
		if !decodeBody(w, r, &body) {
			return
		}
		by, agent := actor(r)
		e, err := svc.BeginAction(r.Context(), id, by, agent, body)
		if err != nil {
			agentsError(w, err)
			return
		}
		audit(opts, r, "", "began "+body.Action, body.Target, body.Why)
		writeJSON(w, e)
	}))
	mux.HandleFunc("POST /api/investigations/{id}/actions/{event}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		event, ok := idOf(w, r, "event")
		if !ok {
			return
		}
		var body struct{ Result string }
		if !decodeBody(w, r, &body) {
			return
		}
		by, agent := actor(r)
		if err := svc.EndAction(r.Context(), id, event, by, agent, body.Result); err != nil {
			agentsError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /api/investigations/{id}/close", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		var body struct{ Status, Summary, Fix, Runbook string }
		if !decodeBody(w, r, &body) {
			return
		}
		by, agent := actor(r)
		i, err := svc.Close(r.Context(), id, by, agent, body.Status, body.Summary, body.Fix, body.Runbook)
		if err != nil {
			agentsError(w, err)
			return
		}
		audit(opts, r, "", "closed an investigation as "+body.Status, strconv.FormatInt(id, 10), body.Fix)
		writeJSON(w, i)
	}))
	mux.HandleFunc("POST /api/investigations/{id}/escalate", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id, ok := idOf(w, r, "id")
		if !ok {
			return
		}
		var body struct{ Why string }
		if !decodeBody(w, r, &body) {
			return
		}
		by, agent := actor(r)
		i, err := svc.Escalate(r.Context(), id, by, agent, body.Why)
		if err != nil {
			agentsError(w, err)
			return
		}
		if i.Alert != "" && opts.Alerts != nil {
			_, _ = opts.Alerts.Note(r.Context(), i.Alert, by, "escalated: "+body.Why)
		}
		audit(opts, r, "", "escalated an investigation", strconv.FormatInt(id, 10), body.Why)
		writeJSON(w, i)
	}))
}
