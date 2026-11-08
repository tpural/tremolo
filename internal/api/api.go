// Package api serves tremolo's read-only HTTP API and its web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/hosts"
)

// Options configures the API.
type Options struct {
	// MetricsURL is VictoriaMetrics (or any Prometheus-compatible query API).
	MetricsURL *url.URL
	// LogsURL is VictoriaLogs.
	LogsURL *url.URL
	// TracesURL is VictoriaTraces; nil when trace storage is off.
	TracesURL  *url.URL
	Hosts      *hosts.Lister
	Dashboards *dashboard.Store
	// UI is the built web app; index.html is served for unknown paths.
	UI        fs.FS
	Transport http.RoundTripper
	Version   string
	Log       *slog.Logger
}

// metricsPaths and logsPaths are the only upstream endpoints reachable, all read-only.
var (
	metricsPaths = map[string]string{
		"query":       "/api/v1/query",
		"query_range": "/api/v1/query_range",
		"labels":      "/api/v1/labels",
		"series":      "/api/v1/series",
	}
	logsPaths = map[string]string{
		"query":        "/select/logsql/query",
		"tail":         "/select/logsql/tail",
		"hits":         "/select/logsql/hits",
		"field_names":  "/select/logsql/field_names",
		"field_values": "/select/logsql/field_values",
	}
	// Spans are rows to VictoriaTraces' LogsQL endpoints, for counts and stats; whole
	// traces come from its Jaeger API.
	tracesPaths = map[string]string{
		"services":     "/select/jaeger/api/services",
		"search":       "/select/jaeger/api/traces",
		"query":        "/select/logsql/query",
		"hits":         "/select/logsql/hits",
		"field_names":  "/select/logsql/field_names",
		"field_values": "/select/logsql/field_values",
	}
	traceID = regexp.MustCompile(`^[0-9a-fA-F]{1,32}$`)
)

// New returns the HTTP handler.
func New(opts Options) http.Handler {
	mux := http.NewServeMux()
	for name, p := range metricsPaths {
		mux.Handle("GET /api/metrics/"+name, proxy(opts.MetricsURL, p, opts))
	}
	mux.HandleFunc("GET /api/metrics/label/{name}/values", func(w http.ResponseWriter, r *http.Request) {
		proxy(opts.MetricsURL, "/api/v1/label/"+url.PathEscape(r.PathValue("name"))+"/values", opts).ServeHTTP(w, r)
	})
	for name, p := range logsPaths {
		mux.Handle("GET /api/logs/"+name, proxy(opts.LogsURL, p, opts))
	}
	if opts.TracesURL != nil {
		for name, p := range tracesPaths {
			mux.Handle("GET /api/traces/"+name, proxy(opts.TracesURL, p, opts))
		}
		mux.HandleFunc("GET /api/traces/services/{name}/operations", func(w http.ResponseWriter, r *http.Request) {
			proxy(opts.TracesURL, "/select/jaeger/api/services/"+url.PathEscape(r.PathValue("name"))+"/operations", opts).ServeHTTP(w, r)
		})
		mux.HandleFunc("GET /api/traces/trace/{id}", func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			if !traceID.MatchString(id) {
				apiError(w, http.StatusBadRequest, "a trace id is up to 32 hex digits")
				return
			}
			proxy(opts.TracesURL, "/select/jaeger/api/traces/"+strings.ToLower(id), opts).ServeHTTP(w, r)
		})
	} else {
		mux.HandleFunc("GET /api/traces/", func(w http.ResponseWriter, _ *http.Request) {
			apiError(w, http.StatusNotFound, "trace storage is not enabled")
		})
	}
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		list, err := opts.Hosts.List(r.Context())
		if err != nil {
			opts.Log.Warn("list hosts", "err", err)
			apiError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/hosts/{name}/pods", func(w http.ResponseWriter, r *http.Request) {
		pods, err := opts.Hosts.Pods(r.Context(), r.PathValue("name"))
		switch {
		case errors.Is(err, hosts.ErrNoCluster):
			apiError(w, http.StatusServiceUnavailable, err.Error())
		case err != nil:
			opts.Log.Warn("list pods", "err", err)
			apiError(w, http.StatusBadGateway, err.Error())
		default:
			writeJSON(w, pods)
		}
	})
	mux.HandleFunc("GET /api/dashboards", func(w http.ResponseWriter, _ *http.Request) {
		type summary struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description,omitempty"`
			Group       string `json:"group,omitempty"`
		}
		list := []summary{}
		for _, d := range opts.Dashboards.List() {
			if d.Hidden {
				continue
			}
			list = append(list, summary{d.ID, d.Title, d.Description, d.Group})
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/dashboards/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, ok := opts.Dashboards.Get(r.PathValue("id"))
		if !ok {
			apiError(w, http.StatusNotFound, "no such dashboard")
			return
		}
		writeJSON(w, d)
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"version": opts.Version, "traces": opts.TracesURL != nil})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("GET /", spa(opts.UI))
	return mux
}

func proxy(base *url.URL, target string, opts Options) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// target may hold escaped segments, e.g. a label name with a slash.
			raw := strings.TrimRight(base.Path, "/") + target
			p, err := url.PathUnescape(raw)
			if err != nil {
				p = raw
			}
			pr.Out.URL = &url.URL{
				Scheme:   base.Scheme,
				Host:     base.Host,
				Path:     p,
				RawPath:  raw,
				RawQuery: pr.In.URL.RawQuery,
			}
			pr.Out.Host = ""
			// Only pass what a query needs: no cookies or credentials from the browser.
			pr.Out.Header = http.Header{}
			if a := pr.In.Header.Get("Accept"); a != "" {
				pr.Out.Header.Set("Accept", a)
			}
		},
		Transport:     opts.Transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			opts.Log.Warn("upstream", "path", r.URL.Path, "err", err)
			apiError(w, http.StatusBadGateway, "upstream unavailable")
		},
	}
}

// spa serves the web app's files, and index.html for any other path so the
// app's own router can handle it. Hashed assets are cached for a long time.
func spa(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(name, "api/") {
			apiError(w, http.StatusNotFound, "not found")
			return
		}
		if name != "" {
			if st, err := fs.Stat(ui, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, ui, "index.html")
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg, "status": code})
}
