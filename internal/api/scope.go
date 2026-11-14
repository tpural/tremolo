package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/agents"
	"github.com/tpural/tremolo/internal/users"
)

type tokenKey struct{}

// token returns the API token a request came with, if any.
func token(r *http.Request) (users.Token, bool) {
	t, ok := r.Context().Value(tokenKey{}).(users.Token)
	return t, ok
}

// scope returns an assistant token's scope, or nil when the request may do
// whatever its user's role allows.
func scope(r *http.Request) *users.Scope {
	if t, ok := token(r); ok {
		return t.Scope
	}
	return nil
}

// Signal reads, by route; their data is filtered by the token's selectors.
const (
	metricsRead = "read:" + users.Metrics
	logsRead    = "read:" + users.Logs
	tracesRead  = "read:" + users.Traces
	anyone      = "-"
)

// policy is what each route needs from an assistant token. A route missing
// from it is refused, so new routes stay closed to assistants until listed.
var policy = map[string]string{
	"GET /api/me":             anyone,
	"POST /mcp":               anyone, // each tool's call is checked on its own
	"GET /mcp":                anyone,
	"DELETE /mcp":             anyone,
	"GET /api/version":        anyone,
	"GET /healthz":            anyone,
	"GET /":                   anyone,
	"GET /api/teams":          anyone,
	"GET /api/frontend":       metricsRead,
	"GET /api/frontend/{app}": metricsRead,

	"GET /api/metrics/label/{name}/values":       metricsRead,
	"GET /api/traces/services/{name}/operations": tracesRead,
	"GET /api/traces/trace/{id}":                 tracesRead,

	"GET /api/hosts":                       users.ReadHosts,
	"GET /api/hosts/{name}/power":          users.ReadHosts,
	"GET /api/hosts/{name}/pods":           users.ReadHosts,
	"GET /api/calendar":                    users.ReadHosts,
	"GET /api/maintenance":                 users.ReadHosts,
	"PUT /api/hosts/{name}/maintenance":    users.WriteMaint,
	"DELETE /api/hosts/{name}/maintenance": users.WriteMaint,
	"POST /api/maintenance":                users.WriteMaint,
	"PUT /api/maintenance/{id}":            users.WriteMaint,
	"DELETE /api/maintenance/{id}":         users.WriteMaint,

	"GET /api/alerts":                                 users.ReadAlerts,
	"GET /api/alerts/history":                         users.ReadAlerts,
	"GET /api/alerts/digest":                          users.ReadAlerts,
	"GET /api/alerts/rules/{name}/versions":           users.ReadAlerts,
	"GET /api/alerts/rules/{name}/versions/{version}": users.ReadAlerts,
	"POST /api/alerts/ack":                            users.WriteAlerts,
	"POST /api/alerts/take":                           users.WriteAlerts,
	"POST /api/alerts/note":                           users.WriteAlerts,
	"POST /api/alerts/silences":                       users.WriteSilences,
	"DELETE /api/alerts/silences/{id}":                users.WriteSilences,

	"GET /api/runbooks":                    users.ReadRunbooks,
	"GET /api/runbooks/{id}":               users.ReadRunbooks,
	"GET /api/runbooks/{id}/markdown":      users.ReadRunbooks,
	"GET /api/runbooks/{id}/versions":      users.ReadRunbooks,
	"GET /api/runbooks/proposals":          users.ReadRunbooks,
	"POST /api/alerts/history/{entry}/fix": users.WriteFixNotes,
	"POST /api/runbooks":                   users.WriteRunbooks,
	"PUT /api/runbooks/{id}":               users.WriteRunbooks,

	"GET /api/dashboards":      users.ReadDashboards,
	"GET /api/dashboards/{id}": users.ReadDashboards,
	"GET /api/storage":         users.ReadStorage,

	"GET /api/agents":                               users.Investigate,
	"GET /api/investigations":                       users.Investigate,
	"GET /api/investigations/{id}":                  users.Investigate,
	"POST /api/investigations":                      users.Investigate,
	"POST /api/investigations/{id}/claim":           users.Investigate,
	"POST /api/investigations/{id}/events":          users.Investigate,
	"POST /api/investigations/{id}/actions":         users.Investigate,
	"POST /api/investigations/{id}/actions/{event}": users.Investigate,
	"POST /api/investigations/{id}/close":           users.Investigate,
	"POST /api/investigations/{id}/escalate":        users.Investigate,
	"GET /api/agents/report":                        users.Investigate,
	"GET /api/coverage":                             users.Investigate,
	"PUT /api/alerts/rules/{name}":                  users.WriteRules,
}

func init() {
	for name := range metricsPaths {
		policy["GET /api/metrics/"+name] = metricsRead
	}
	for name := range logsPaths {
		policy["GET /api/logs/"+name] = logsRead
	}
	for name := range tracesPaths {
		policy["GET /api/traces/"+name] = tracesRead
	}
}

// allowed reports whether s may use the route with pattern.
func allowed(s *users.Scope, pattern string) bool {
	need, ok := policy[pattern]
	switch {
	case !ok:
		return false
	case need == anyone:
		return true
	case strings.HasPrefix(need, "read:"):
		return len(s.Selectors(strings.TrimPrefix(need, "read:"))) > 0
	}
	return s.Allows(need)
}

// guard refuses what an assistant token isn't allowed to do, and any change
// by one while agents are paused.
func guard(mux *http.ServeMux, next http.Handler, ag *agents.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := scope(r); s != nil {
			if _, pattern := mux.Handler(r); !allowed(s, pattern) {
				msg := "this token isn't allowed to do that"
				if need := policy[pattern]; need != "" && need != anyone {
					msg += "; it needs the " + strings.TrimPrefix(need, "read:") + " permission"
				}
				apiError(w, http.StatusForbidden, msg)
				return
			}
			if _, pattern := mux.Handler(r); ag != nil && !strings.HasSuffix(pattern, " /mcp") && r.Method != http.MethodGet && r.Method != http.MethodHead {
				if p := ag.Paused(); p.Paused {
					apiError(w, http.StatusLocked, "agents are paused by "+p.By+"; only reading is allowed until they're resumed")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// nothing matches no data, per signal.
var nothing = map[string]string{
	users.Metrics: `{__name__="tremolo:none"}`,
	users.Logs:    `_stream_id:=none`,
	users.Traces:  `_stream_id:=none`,
}

// restrict rewrites a query to Victoria so it sees only what the request's
// token may read of a signal. Filters a caller sent are dropped first, so they
// can't widen it.
func restrict(ctx context.Context, signal string, q url.Values) {
	q.Del("extra_filters")
	q.Del("extra_filters[]")
	s, _ := ctx.Value(tokenKey{}).(users.Token)
	if s.Scope == nil || s.Scope.All(signal) {
		return
	}
	sels := s.Scope.Selectors(signal)
	if len(sels) == 0 {
		// Nothing may be read; a filter no data matches keeps it that way.
		sels = []string{nothing[signal]}
	}
	if signal == users.Metrics {
		// VictoriaMetrics joins several extra_filters[] with OR.
		q["extra_filters[]"] = sels
		return
	}
	// VictoriaLogs joins several extra_filters with AND, so make one.
	if len(sels) == 1 {
		q.Set("extra_filters", sels[0])
		return
	}
	q.Set("extra_filters", "("+strings.Join(sels, ") or (")+")")
}

// restricted is r's query string, limited to what its token may read.
func restricted(r *http.Request, signal string) string {
	q := r.URL.Query()
	_, a := q["extra_filters"]
	_, b := q["extra_filters[]"]
	if !a && !b && scope(r) == nil {
		return r.URL.RawQuery
	}
	restrict(r.Context(), signal, q)
	return q.Encode()
}

// wholeTraces keeps Jaeger's API, which can't be filtered by span, to tokens
// that may read every trace.
func wholeTraces(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := scope(r); s != nil && !s.All(users.Traces) {
			apiError(w, http.StatusForbidden, "this token may read only some traces: query spans, or fetch a trace by id")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// traceInScope reports whether every span of trace id is one s may read.
func traceInScope(ctx context.Context, opts Options, s *users.Scope, id string) bool {
	count := func(scoped bool) (int, bool) {
		q := url.Values{"query": {"trace_id:=" + id + " | stats count() as n"}}
		if scoped {
			restrict(context.WithValue(ctx, tokenKey{}, users.Token{Scope: s}), users.Traces, q)
		}
		u := *opts.TracesURL
		u.Path = strings.TrimRight(u.Path, "/") + "/select/logsql/query"
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return 0, false
		}
		res, err := (&http.Client{Transport: opts.Transport, Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return 0, false
		}
		defer res.Body.Close()
		var row struct{ N string }
		if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&row) != nil {
			return 0, false
		}
		n, err := strconv.Atoi(row.N)
		return n, err == nil
	}
	all, ok := count(false)
	if !ok || all == 0 {
		return false
	}
	mine, ok := count(true)
	return ok && mine == all
}
