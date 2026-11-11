// Package mcp is tremolo's Model Context Protocol server, at /mcp: tools for
// agents to see what's happening, work investigations and keep runbooks.
//
// Every tool calls tremolo's own API in-process with the caller's token, so
// label scopes, query limits, the audit log and the pause switch apply as
// they do to any other request. Answers are short text, not raw JSON, sized
// for small local models.
package mcp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// instructions are what every agent is told when it connects.
const instructions = `tremolo watches this homelab: metrics, logs, traces, hosts and alerts.
You help run it. Work like this:
1. Call overview to see what's firing and what's open.
2. Pick an open investigation and call investigate on it; this claims it so no other agent works on it.
3. Read its alert and runbook (alert, runbook), then gather evidence (metrics, logs, host). Record each finding with record.
4. Before any change outside tremolo, call begin_action. Act only if it says go ahead; then end_action with the result.
5. When fixed, call finish with status fixed and the fix in one line. If the runbook was wrong or missing, fix it with edit_runbook.
6. If you can't fix it, call escalate with what you tried. A person takes over.
Keep queries narrow: a namespace, host or service, and the last hour unless you need more.
Metric labels use underscores: host_name, k8s_namespace_name, k8s_pod_name; log fields use dots: host.name, k8s.namespace.name. Find metric names with metric_names.`

// New returns the /mcp handler. api is tremolo's API handler, which tools
// call with the request's own credentials.
func New(api http.Handler, version string) http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "tremolo", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	t := &tools{api: api}
	t.register(s)
	prompts(s)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

type tools struct{ api http.Handler }

// call makes an API request as the tool's caller.
func (t *tools) call(ctx context.Context, req *mcp.CallToolRequest, method, path string, q url.Values, body any) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return http.StatusBadRequest, []byte(err.Error())
	}
	if req.Extra != nil {
		for _, h := range []string{"Authorization", "Cookie"} {
			if v := req.Extra.Header.Get(h); v != "" {
				r.Header.Set(h, v)
			}
		}
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := &recorder{header: http.Header{}, code: http.StatusOK}
	t.api.ServeHTTP(w, r)
	return w.code, w.body.Bytes()
}

type recorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

// get decodes a successful JSON answer into v, or returns why it failed.
func (t *tools) get(ctx context.Context, req *mcp.CallToolRequest, path string, q url.Values, v any) error {
	code, b := t.call(ctx, req, http.MethodGet, path, q, nil)
	if code != http.StatusOK {
		return apiErr(code, b)
	}
	return json.Unmarshal(b, v)
}

func (t *tools) send(ctx context.Context, req *mcp.CallToolRequest, method, path string, body, v any) error {
	code, b := t.call(ctx, req, method, path, nil, body)
	if code >= 300 {
		return apiErr(code, b)
	}
	if v == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// apiErr turns an API failure into what to do next.
func apiErr(code int, b []byte) error {
	var e struct{ Error string }
	_ = json.Unmarshal(b, &e)
	msg := cmp.Or(e.Error, strings.TrimSpace(string(b)))
	switch code {
	case http.StatusUnauthorized:
		return fmt.Errorf("not signed in: connect with an assistant token as a Bearer header")
	case http.StatusForbidden:
		return fmt.Errorf("%s. Ask a person to add it, or work with what you have", msg)
	case http.StatusLocked:
		return fmt.Errorf("%s. Stop making changes; you may still read", msg)
	case http.StatusConflict:
		return fmt.Errorf("%s", msg)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s; wait a little and retry", msg)
	case http.StatusGatewayTimeout:
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%d: %s", code, clip(msg, 500))
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: clip(s, maxAnswer)}}}
}

func fail(err error) (*mcp.CallToolResult, any, error) {
	r := text(err.Error())
	r.IsError = true
	return r, nil, nil
}

func ok(s string) (*mcp.CallToolResult, any, error) { return text(s), nil, nil }

// maxAnswer keeps answers inside a small model's context.
const maxAnswer = 6000

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n… (%d more characters cut; narrow the request)", len(s)-n)
}

func line(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1fh ago", d.Hours())
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func labels(m map[string]string, skip ...string) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if !slices.Contains(skip, k) {
			parts = append(parts, k+"="+m[k])
		}
	}
	return strings.Join(parts, " ")
}

func num(v float64) string {
	switch a := math.Abs(v); {
	case a == 0:
		return "0"
	case a >= 1e9:
		return strconv.FormatFloat(v/1e9, 'f', 2, 64) + "G"
	case a >= 1e6:
		return strconv.FormatFloat(v/1e6, 'f', 2, 64) + "M"
	case a >= 1e4:
		return strconv.FormatFloat(v/1e3, 'f', 1, 64) + "k"
	case a < 0.01:
		return strconv.FormatFloat(v, 'g', 3, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
