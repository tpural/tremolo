package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/calendar"
	"github.com/tpural/tremolo/internal/users"
)

// maxCalendarRange bounds how much of the calendar one request lays out.
const maxCalendarRange = 100 * 24 * time.Hour

func calendarError(w http.ResponseWriter, err error) {
	if errors.Is(err, calendar.ErrNotFound) {
		apiError(w, http.StatusNotFound, err.Error())
		return
	}
	apiError(w, http.StatusBadRequest, err.Error())
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "body must be JSON: "+err.Error())
		return false
	}
	return true
}

// calendarRoutes adds maintenance periods and the calendar of them.
func calendarRoutes(mux *http.ServeMux, c *calendar.Calendar, opts Options) {
	// Every time maintenance is on in a range.
	mux.HandleFunc("GET /api/calendar", func(w http.ResponseWriter, r *http.Request) {
		from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
		to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
		if err := errors.Join(err1, err2); err != nil || !to.After(from) || to.Sub(from) > maxCalendarRange {
			apiError(w, http.StatusBadRequest, "from and to are RFC 3339 times, up to 100 days apart")
			return
		}
		occ, err := c.Occurrences(r.Context(), from, to)
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, struct {
			Maintenance []calendar.Occurrence `json:"maintenance"`
			Timezone    string                `json:"timezone"`
		}{append([]calendar.Occurrence{}, occ...), c.Location().String()})
	})

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
