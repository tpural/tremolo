package api

import (
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/tpural/tremolo/internal/frontend"
	"github.com/tpural/tremolo/internal/users"
)

// frontendRoutes adds the browser apps: which there are, how they're doing,
// and each one's dashboard. What a token may read limits which apps it sees.
func frontendRoutes(mux *http.ServeMux, opts Options) {
	src := opts.Frontend
	apps := func(r *http.Request) ([]frontend.Summary, error) {
		var filter func(url.Values)
		if scope(r) != nil {
			filter = func(q url.Values) { restrict(r.Context(), users.Metrics, q) }
		}
		return src.Apps(r.Context(), time.Now(), filter)
	}
	mux.HandleFunc("GET /api/frontend", func(w http.ResponseWriter, r *http.Request) {
		list, err := apps(r)
		if err != nil {
			opts.Log.Warn("frontend apps", "err", err)
			apiError(w, http.StatusBadGateway, "can't read browser data: "+err.Error())
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/frontend/{app}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("app")
		list, err := apps(r)
		if err != nil {
			apiError(w, http.StatusBadGateway, "can't read browser data: "+err.Error())
			return
		}
		if !slices.ContainsFunc(list, func(s frontend.Summary) bool { return s.Name == name }) {
			apiError(w, http.StatusNotFound, "no browser data from "+name)
			return
		}
		writeJSON(w, frontend.Dashboard(name, src.Config[name]))
	})
}
