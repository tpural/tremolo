package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/calendar"
	"github.com/tpural/tremolo/internal/users"
)

// maxCalendarRange bounds how much of the calendar one request lays out.
const maxCalendarRange = 100 * 24 * time.Hour

func calendarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, calendar.ErrNotFound):
		apiError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, calendar.ErrExists):
		apiError(w, http.StatusConflict, err.Error())
	default:
		apiError(w, http.StatusBadRequest, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "body must be JSON: "+err.Error())
		return false
	}
	return true
}

// calendarRoutes adds on-call schedules, their overrides, and maintenance.
func calendarRoutes(mux *http.ServeMux, c *calendar.Calendar, opts Options) {
	// The calendar: each schedule's shifts and the maintenance in a range.
	mux.HandleFunc("GET /api/calendar", func(w http.ResponseWriter, r *http.Request) {
		from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
		to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
		if err := errors.Join(err1, err2); err != nil || !to.After(from) || to.Sub(from) > maxCalendarRange {
			apiError(w, http.StatusBadRequest, "from and to are RFC 3339 times, up to 100 days apart")
			return
		}
		type withShifts struct {
			calendar.Schedule
			Shifts []calendar.Shift `json:"shifts"`
		}
		out := struct {
			Schedules   []withShifts          `json:"schedules"`
			Maintenance []calendar.Occurrence `json:"maintenance"`
			Timezone    string                `json:"timezone"`
		}{Schedules: []withShifts{}, Maintenance: []calendar.Occurrence{}, Timezone: c.Location().String()}
		list, err := c.Schedules(r.Context())
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, s := range list {
			shifts, err := c.Shifts(r.Context(), s.Name, from, to)
			if err != nil {
				apiError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out.Schedules = append(out.Schedules, withShifts{s, append([]calendar.Shift{}, shifts...)})
		}
		occ, err := c.Occurrences(r.Context(), from, to)
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out.Maintenance = append(out.Maintenance, occ...)
		writeJSON(w, out)
	})

	mux.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		list, err := c.Schedules(r.Context())
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("PUT /api/schedules/{name}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		var s calendar.Schedule
		if !decode(w, r, &s) {
			return
		}
		s.Name = r.PathValue("name")
		if err := users.CheckName(s.Name); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := c.SaveSchedule(r.Context(), s, who(r)); err != nil {
			calendarError(w, err)
			return
		}
		audit(opts, r, "", "saved schedule", s.Name, fmt.Sprintf("%s, %d-day turns from %s", strings.Join(s.Members, ", "), s.Days, s.Start.Format(time.RFC3339)))
		saved, err := c.Schedule(r.Context(), s.Name)
		if err != nil {
			calendarError(w, err)
			return
		}
		writeJSON(w, saved)
	}))
	mux.HandleFunc("DELETE /api/schedules/{name}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		if err := c.DeleteSchedule(r.Context(), r.PathValue("name")); err != nil {
			calendarError(w, err)
			return
		}
		audit(opts, r, "", "deleted schedule", r.PathValue("name"), "")
		w.WriteHeader(http.StatusNoContent)
	}))
	// Anyone can take a shift themselves; editors can give one to anybody.
	mux.HandleFunc("POST /api/schedules/{name}/overrides", func(w http.ResponseWriter, r *http.Request) {
		var o calendar.Override
		if !decode(w, r, &o) {
			return
		}
		u, _ := current(r)
		if o.Person == "" {
			o.Person = u.Name
		}
		if o.Person != u.Name && !u.Role.Can(users.Editor) {
			apiError(w, http.StatusForbidden, "only editors can put someone else on call")
			return
		}
		o.Schedule, o.By = r.PathValue("name"), u.Name
		added, err := c.AddOverride(r.Context(), o)
		if err != nil {
			calendarError(w, err)
			return
		}
		audit(opts, r, "", "added on-call override", o.Schedule, fmt.Sprintf("%s from %s to %s", o.Person, o.Start.Format(time.RFC3339), o.End.Format(time.RFC3339)))
		writeJSON(w, added)
	})
	mux.HandleFunc("DELETE /api/schedules/{name}/overrides/{id}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		if err := c.DeleteOverride(r.Context(), r.PathValue("name"), r.PathValue("id")); err != nil {
			calendarError(w, err)
			return
		}
		audit(opts, r, "", "removed on-call override", r.PathValue("name"), r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		list, err := c.Periods(r.Context(), time.Now())
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, list)
	})
	save := func(w http.ResponseWriter, r *http.Request, id string) {
		var p calendar.Period
		if !decode(w, r, &p) {
			return
		}
		p.ID, p.By = id, who(r)
		saved, err := c.SavePeriod(r.Context(), p)
		if err != nil {
			calendarError(w, err)
			return
		}
		action := "planned maintenance"
		if id != "" {
			action = "changed maintenance"
		}
		audit(opts, r, "", action, saved.Title, periodDetail(saved))
		writeJSON(w, saved)
	}
	mux.HandleFunc("POST /api/maintenance", need(users.Editor, func(w http.ResponseWriter, r *http.Request) { save(w, r, "") }))
	mux.HandleFunc("PUT /api/maintenance/{id}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) { save(w, r, r.PathValue("id")) }))
	mux.HandleFunc("DELETE /api/maintenance/{id}", need(users.Editor, func(w http.ResponseWriter, r *http.Request) {
		p, err := c.Period(r.Context(), r.PathValue("id"))
		if err == nil {
			err = c.DeletePeriod(r.Context(), p.ID)
		}
		if err != nil {
			calendarError(w, err)
			return
		}
		audit(opts, r, "", "deleted maintenance", p.Title, periodDetail(p))
		w.WriteHeader(http.StatusNoContent)
	}))
}

func periodDetail(p calendar.Period) string {
	parts := []string{p.Start.Format(time.RFC3339)}
	if !p.End.IsZero() {
		parts[0] += " to " + p.End.Format(time.RFC3339)
	}
	if p.Every != "" {
		parts = append(parts, "every "+p.Every)
	}
	switch {
	case len(p.Rules) == 0 && len(p.Hosts) == 0:
		parts = append(parts, "all alerts")
	default:
		if len(p.Rules) > 0 {
			parts = append(parts, "rules "+strings.Join(p.Rules, ", "))
		}
		if len(p.Hosts) > 0 {
			parts = append(parts, "hosts "+strings.Join(p.Hosts, ", "))
		}
	}
	return strings.Join(parts, "; ")
}
