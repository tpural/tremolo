package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// prompts are the procedures agents follow, so even a small model works the same way.
func prompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "triage-alert",
		Description: "Work one open investigation from claim to fixed or escalated.",
		Arguments:   []*mcp.PromptArgument{{Name: "id", Description: "the investigation's number", Required: true}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		id := req.Params.Arguments["id"]
		return &mcp.GetPromptResult{
			Description: "Triage investigation " + id,
			Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: fmt.Sprintf(`Work investigation %s.
1. investigate %s to claim it. If someone else holds it, stop.
2. Read its alert with alert, and the runbook it names with runbook. Follow the runbook's steps, starting with the fixes that held most.
3. Gather evidence with metrics, logs and host, narrow to the namespace or host in the alert's labels. record each finding.
4. Before changing anything, begin_action. If it's refused, don't do it: record why and try another way or escalate.
5. After acting, end_action with what happened, and check the alert clears (alert, metrics).
6. When it's fixed: finish with status fixed and the fix in one line. If the runbook lacked this fix or was wrong, update it with edit_runbook.
7. If you can't fix it in a few tries, escalate with what you tried and found.`, id, id)}}},
		}, nil
	})
	s.AddPrompt(&mcp.Prompt{
		Name:        "daily-review",
		Description: "Look for problems before they alert, and gaps in what tremolo observes.",
	}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "Daily review",
			Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: `Review the lab:
1. overview. Work any open investigation nobody holds (triage-alert).
2. Look for trouble that hasn't alerted: hosts with high memory or disk (metrics), pods restarting (host), errors growing in logs.
3. Look for gaps: hosts or namespaces with no logs or metrics, services without traces, rules without runbooks (runbooks).
4. For each problem or gap, open_investigation (kind gap or tuning) with the evidence, then work it or leave it for a person.
Record what you checked even when it's fine.`}}},
		}, nil
	})
}
