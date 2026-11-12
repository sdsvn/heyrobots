package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
)

// ACP routes with the owner's own ACP agent, such as omp or Claude Code,
// when no model is set for the quick calls. An ACP agent can't call this
// server's routing tools, so it answers with the same calls as JSON, and
// the same checks as for tool calls apply.
type ACP struct {
	// Ask sends one prompt to a throwaway session and returns the answer.
	Ask func(ctx context.Context, prompt string) (string, error)
	// Prompt is the router's system prompt.
	Prompt string
}

var _ eval.Router = (*ACP)(nil)

const acpFormat = `
You can't call tools here. Instead, answer with only a JSON array of the
routing calls you would make, and nothing else. Each element is one call:
{"tool": "route_accept", "trigger_ids": [...], "scope": "...", "toolsets": [...], "matched_rules": [...], "agent": "...", "job": "...", "note": "..."}
{"tool": "route_attach", "trigger_ids": [...], "ticket_id": "...", "note": "..."}
{"tool": "route_stop", "trigger_ids": [...], "ticket_id": "...", "note": "..."}
{"tool": "route_ignore", "trigger_ids": [...], "reason": "...", "reply": "..."}
Leave out fields you don't need.`

type acpCall struct {
	Tool string `json:"tool"`
	AcceptArgs
	TicketID string `json:"ticket_id"`
	Reason   string `json:"reason"`
	Reply    string `json:"reply"`
}

// Route asks the ACP agent to route a Candidate.
func (a *ACP) Route(ctx context.Context, c eval.Candidate) ([]eval.Route, error) {
	msg, err := render(c)
	if err != nil {
		return nil, err
	}
	answer, err := a.Ask(ctx, a.Prompt+"\n"+acpFormat+"\n\n"+msg)
	if err != nil {
		return nil, fmt.Errorf("your agent: %w", err)
	}
	calls, err := parseCalls(answer)
	if err != nil {
		return nil, err
	}
	rec := newRecorder(c)
	for i, call := range calls {
		id := fmt.Sprintf("acp-%d", i)
		var r eval.Route
		switch call.Tool {
		case "route_accept":
			ts := make([]core.Toolset, len(call.Toolsets))
			for i, t := range call.Toolsets {
				ts[i] = core.Toolset(t)
			}
			r = eval.Route{Action: eval.ActionAccept, TriggerIDs: call.TriggerIDs, Scope: call.Scope, Toolsets: ts, MatchedRules: call.MatchedRules, Agent: call.Agent, Job: call.Job, Note: call.Note}
		case "route_attach":
			r = eval.Route{Action: eval.ActionAttach, TriggerIDs: call.TriggerIDs, TicketID: call.TicketID, Note: call.Note}
		case "route_stop":
			r = eval.Route{Action: eval.ActionStop, TriggerIDs: call.TriggerIDs, TicketID: call.TicketID, Note: call.Note}
		case "route_ignore":
			r = eval.Route{Action: eval.ActionIgnore, TriggerIDs: call.TriggerIDs, Reason: call.Reason, Note: call.Reply}
		default:
			continue
		}
		rec.add(id, r)
	}
	return rec.routes(), nil
}

// parseCalls reads the JSON array out of the agent's answer, which may
// wrap it in prose or a code fence.
func parseCalls(answer string) ([]acpCall, error) {
	start, end := strings.Index(answer, "["), strings.LastIndex(answer, "]")
	if start < 0 || end < start {
		return nil, errors.New("your agent didn't answer with routing calls")
	}
	var calls []acpCall
	if err := json.Unmarshal([]byte(answer[start:end+1]), &calls); err != nil {
		return nil, fmt.Errorf("your agent's routing calls aren't valid JSON: %w", err)
	}
	return calls, nil
}

// Either routes with Model's router, or with the ACP agent when UseACP says so.
type Either struct {
	Model  *Router
	ACP    *ACP
	UseACP func() bool
}

// Route picks the router for this Eval.
func (e Either) Route(ctx context.Context, c eval.Candidate) ([]eval.Route, error) {
	if e.ACP != nil && e.UseACP != nil && e.UseACP() {
		return e.ACP.Route(ctx, c)
	}
	return e.Model.Route(ctx, c)
}
