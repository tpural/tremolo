package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/agents"
	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/runbooks"
	"github.com/tpural/tremolo/internal/users"
)

// runbookRoutes adds the runbooks, and recording how a resolved alert was fixed.
func runbookRoutes(mux *http.ServeMux, s *runbooks.Store, e *alerts.Engine, opts Options) {
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10)).Decode(v); err != nil {
			apiError(w, http.StatusBadRequest, "body must be JSON: "+err.Error())
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/runbooks", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.List(r.Context(), r.URL.Query().Get("rule"))
		if err != nil {
			runbookError(w, err)
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/runbooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		rb, err := s.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			runbookError(w, err)
			return
		}
		out := struct {
			runbooks.Runbook
			// Fixes are what agents fixed with it and whether each held.
			Fixes []agents.FixStat `json:"fixes,omitempty"`
		}{Runbook: rb}
		if opts.Agents != nil {
			out.Fixes, _ = opts.Agents.FixStats(r.Context(), rb.ID)
		}
		writeJSON(w, out)
	})
	// The runbook as plain Markdown, for scripts and assistants.
	mux.HandleFunc("GET /api/runbooks/{id}/markdown", func(w http.ResponseWriter, r *http.Request) {
		rb, err := s.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			runbookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(rb.Body))
	})
	mux.HandleFunc("GET /api/runbooks/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		vs, err := s.Versions(r.Context(), r.PathValue("id"))
		if err != nil {
			runbookError(w, err)
			return
		}
		writeJSON(w, vs)
	})
	// An agent's edit waits for a person: it's kept as a proposal, and the
	// runbook stays as it was.
	propose := func(w http.ResponseWriter, r *http.Request, rb runbooks.Runbook, create bool) bool {
		by, agent := actor(r)
		if !agent {
			return false
		}
		p, err := s.Propose(r.Context(), rb, by, create)
		if err != nil {
			runbookError(w, err)
			return true
		}
		audit(opts, r, "", "proposed a runbook edit", p.Runbook.ID, fmt.Sprintf("proposal %d", p.ID))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(proposed(p))
		return true
	}
	mux.HandleFunc("GET /api/runbooks/proposals", func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		switch status {
		case "":
			status = "pending"
		case "all":
			status = ""
		}
		list, err := s.Proposals(r.Context(), status)
		if err != nil {
			runbookError(w, err)
			return
		}
		writeJSON(w, list)
	})
	// Only a person decides: assistant tokens can't reach these.
	mux.HandleFunc("POST /api/runbooks/proposals/{n}/{decision}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
		if err != nil {
			apiError(w, http.StatusBadRequest, "proposal must be a number")
			return
		}
		switch r.PathValue("decision") {
		case "approve":
			rb, err := s.Approve(r.Context(), n, who(r))
			if err != nil {
				runbookError(w, err)
				return
			}
			audit(opts, r, "", "approved a runbook edit", rb.ID, fmt.Sprintf("proposal %d, version %d", n, rb.Version))
			writeJSON(w, rb)
		case "reject":
			if err := s.Reject(r.Context(), n, who(r)); err != nil {
				runbookError(w, err)
				return
			}
			audit(opts, r, "", "rejected a runbook edit", "", fmt.Sprintf("proposal %d", n))
			w.WriteHeader(http.StatusNoContent)
		default:
			apiError(w, http.StatusNotFound, "approve or reject")
		}
	}))
	mux.HandleFunc("POST /api/runbooks", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var rb runbooks.Runbook
		if !decode(w, r, &rb) || propose(w, r, rb, true) {
			return
		}
		by, _ := actor(r)
		rb, err := s.Create(r.Context(), rb, by)
		if err != nil {
			runbookError(w, err)
			return
		}
		audit(opts, r, "", "created a runbook", rb.ID, rb.Title)
		writeJSON(w, rb)
	}))
	mux.HandleFunc("PUT /api/runbooks/{id}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var rb runbooks.Runbook
		if !decode(w, r, &rb) {
			return
		}
		rb.ID = r.PathValue("id")
		if propose(w, r, rb, false) {
			return
		}
		by, _ := actor(r)
		rb, err := s.Update(r.Context(), rb, by)
		if err != nil {
			runbookError(w, err)
			return
		}
		audit(opts, r, "", "edited a runbook", rb.ID, fmt.Sprintf("version %d", rb.Version))
		writeJSON(w, rb)
	}))
	mux.HandleFunc("DELETE /api/runbooks/{id}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := s.Delete(r.Context(), id); err != nil {
			runbookError(w, err)
			return
		}
		audit(opts, r, "", "deleted a runbook", id, "")
		w.WriteHeader(http.StatusNoContent)
	}))
	if e == nil {
		return
	}
	// How a resolved alert was fixed, and optionally a dated section added to a
	// runbook: an existing one by id, or "new" for one made for the alert's rule.
	mux.HandleFunc("POST /api/alerts/history/{entry}/fix", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		entry, err := strconv.ParseInt(r.PathValue("entry"), 10, 64)
		if err != nil {
			apiError(w, http.StatusBadRequest, "entry must be a number")
			return
		}
		var body struct {
			Fix     string `json:"fix"`
			Runbook string `json:"runbook"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		past, err := e.SetFix(r.Context(), entry, body.Fix)
		switch {
		case errors.Is(err, alerts.ErrNoEntry):
			apiError(w, http.StatusNotFound, err.Error())
			return
		case err != nil:
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		audit(opts, r, "", "noted how an alert was fixed", past.ID, clip(past.Fix, 200))
		out := struct {
			Past    alerts.Past       `json:"past"`
			Runbook *runbooks.Runbook `json:"runbook,omitempty"`
			// Proposal is the edit waiting for a person, when an agent noted the fix.
			Proposal *int64 `json:"proposal,omitempty"`
		}{Past: past}
		if body.Runbook != "" && past.Fix != "" {
			rb, p, err := addFix(r, s, past, body.Runbook)
			if err != nil {
				runbookError(w, err)
				return
			}
			if p != nil {
				audit(opts, r, "", "proposed a runbook edit", rb.ID, fmt.Sprintf("proposal %d", p.ID))
				out.Proposal = &p.ID
			} else {
				audit(opts, r, "", "added a fix to a runbook", rb.ID, past.ID)
				out.Runbook = &rb
			}
		}
		writeJSON(w, out)
	}))
}

// addFix appends a dated section about past to a runbook, making one for its
// rule when id is "new". An agent's addition is proposed instead.
func addFix(r *http.Request, s *runbooks.Store, past alerts.Past, id string) (runbooks.Runbook, *runbooks.Proposal, error) {
	_, agent := actor(r)
	section := fixSection(past, who(r))
	if id == "new" {
		id = runbooks.Slug(past.Rule)
		if _, err := s.Get(r.Context(), id); errors.Is(err, runbooks.ErrNotFound) {
			rb := runbooks.Runbook{
				ID: id, Title: past.Rule,
				Body:  fmt.Sprintf("# %s\n\nHow to handle `%s` alerts, and how they were fixed before.\n\n%s", past.Rule, past.Rule, section),
				Rules: []string{past.Rule},
			}
			if agent {
				p, err := s.Propose(r.Context(), rb, who(r), true)
				return p.Runbook, &p, err
			}
			rb, err := s.Create(r.Context(), rb, who(r))
			return rb, nil, err
		}
	}
	if agent {
		rb, err := s.Get(r.Context(), id)
		if err != nil {
			return rb, nil, err
		}
		rb.Body = strings.TrimRight(rb.Body, "\n") + "\n\n" + strings.TrimSpace(section) + "\n"
		p, err := s.Propose(r.Context(), rb, who(r), false)
		return p.Runbook, &p, err
	}
	rb, err := s.Append(r.Context(), id, section, who(r))
	return rb, nil, err
}

// proposed is how an agent's edit is answered: the runbook it would make,
// marked as waiting for a person.
func proposed(p runbooks.Proposal) any {
	return struct {
		runbooks.Runbook
		Proposal int64  `json:"proposal"`
		Pending  bool   `json:"pending"`
		Note     string `json:"note"`
	}{p.Runbook, p.ID, true, "Proposed: a person approves it before the runbook changes."}
}

// fixSection is a runbook entry for one fixed alert.
func fixSection(p alerts.Past, by string) string {
	fired := p.FiringSince
	if fired.IsZero() {
		fired = p.Since
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s: %s\n\n", fired.Format(time.DateOnly), p.Summary)
	fmt.Fprintf(&b, "- Alert `%s`, rule `%s`, %s\n", p.ID, p.Rule, p.Severity)
	fmt.Fprintf(&b, "- Fired %s, resolved after %s\n", fired.Format("2006-01-02 15:04 MST"), minutes(p.Resolved.Sub(fired)))
	if !p.Acked.IsZero() {
		fmt.Fprintf(&b, "- Acknowledged by %s after %s\n", p.AckedBy, minutes(p.Acked.Sub(fired)))
	}
	fmt.Fprintf(&b, "- Fix noted by %s\n\n%s\n", by, p.Fix)
	return b.String()
}

// minutes writes a duration to the minute, e.g. 30m or 2h5m.
func minutes(d time.Duration) string {
	s := strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
	return strings.TrimSuffix(strings.Replace(s, "h0m", "h", 1), "h0")
}

func runbookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runbooks.ErrNotFound):
		apiError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, runbooks.ErrExists), errors.Is(err, runbooks.ErrChanged), errors.Is(err, runbooks.ErrDecided):
		apiError(w, http.StatusConflict, err.Error())
	case errors.Is(err, runbooks.ErrInvalid):
		apiError(w, http.StatusBadRequest, err.Error())
	default:
		apiError(w, http.StatusInternalServerError, err.Error())
	}
}
