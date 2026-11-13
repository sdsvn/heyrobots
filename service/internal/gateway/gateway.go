// Package gateway is the one way a task's worker reaches tools. It holds
// the tools the task's Mandate grants (the built-in toolsets and the
// owner's MCP servers) and checks every call the same way: an outward call
// never runs, it becomes an action in the outbox after the reach, taint and
// approval checks, and outside content is read in quarantine.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/adk/v2/tool"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/taint"
	"heyrobots/service/internal/telemetry"
	"heyrobots/service/internal/toolsets"
)

// Gateway builds each task's tools.
type Gateway struct {
	Store *store.Store
	// Outward names the built-in tools whose calls reach other people,
	// with how to describe each call. Such tools never run directly.
	Outward map[string]toolsets.ProposalFunc
	// MCP connects to the owner's MCP servers. Without it, MCP toolsets
	// are unavailable.
	MCP *MCP
	Now func() time.Time
}

// Task is what a Session needs to know about the task it serves.
type Task struct {
	TicketID string
	ThreadID string
	Mandate  core.Mandate
	Policy   policy.Policy
	// Trusted is what the owner vouches for, for the taint check.
	Trusted taint.Trusted
}

// Unavailable is returned by Open when a toolset can't be used now. Its
// message is for the person.
type Unavailable struct {
	Toolset core.Toolset
	Reason  string
	Blocked bool
}

func (u *Unavailable) Error() string { return u.Reason }

// Session is one Assignment's use of the gateway.
type Session struct {
	g       *Gateway
	task    Task
	tools   []tool.Tool
	closers []io.Closer
	missing []string
}

// Open gathers the tools of the task's toolsets. Close the Session when the
// Assignment ends.
func (g *Gateway) Open(ctx context.Context, task Task, env toolsets.Env) (*Session, error) {
	s := &Session{g: g, task: task}
	for _, ts := range task.Mandate.Toolsets {
		if server, ok := ts.MCPServer(); ok {
			if g.MCP == nil {
				s.Close()
				return nil, &Unavailable{Toolset: ts, Reason: fmt.Sprintf("The %s tools aren't set up on this server.", server)}
			}
			tools, closer, err := g.MCP.Tools(ctx, server)
			if err != nil {
				// One app that's down, or whose sign-in ended, doesn't stop
				// the task: it goes on without those tools, and is told why.
				s.missing = append(s.missing, fmt.Sprintf("The %s tools aren't working right now (%s). Do what you can without them. If the request needs them, tell the owner to reconnect %s under Settings, then Add-ons, and say that in your reply.", server, shorten(err.Error(), 200), server))
				continue
			}
			s.tools = append(s.tools, tools...)
			s.closers = append(s.closers, closer)
			continue
		}
		t, err := toolsets.For(ts, env)
		if err != nil {
			s.Close()
			return nil, &Unavailable{Toolset: ts, Reason: fmt.Sprintf("The %s toolset isn't available yet.", ts)}
		}
		s.tools = append(s.tools, t...)
	}
	// A stable order keeps the prompt prefix identical between calls, so
	// providers can cache it.
	slices.SortFunc(s.tools, func(a, b tool.Tool) int { return strings.Compare(a.Name(), b.Name()) })
	return s, nil
}

// Missing says which of the task's apps couldn't be reached, and why.
func (s *Session) Missing() []string { return s.missing }

// Tools are the task's tools.
func (s *Session) Tools() []tool.Tool { return s.tools }

// Close disconnects from the MCP servers the Session used.
func (s *Session) Close() {
	for _, c := range s.closers {
		_ = c.Close()
	}
	s.closers = nil
}

// Guard runs before every tool call. For a call that reaches other people
// it returns the result the worker sees instead of running the tool; for
// any other call it returns nil and the tool runs.
func (s *Session) Guard(tc context.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
	if m, ok := t.(*mcpTool); ok {
		if m.mode != store.MCPToolOutward {
			return nil, nil
		}
		return s.propose(tc, t.Name(), m.proposal(args))
	}
	describe, ok := s.g.Outward[t.Name()]
	if !ok {
		return nil, nil
	}
	p, err := describe(args)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}
	return s.propose(tc, t.Name(), p)
}

// propose queues an outward action. It decides in plain code whether the
// owner must approve it first.
func (s *Session) propose(ctx context.Context, toolName string, p toolsets.Proposal) (map[string]any, error) {
	pol, task := s.task.Policy, s.task
	_, span := telemetry.Tracer().Start(ctx, "heyrobots.propose "+toolName)
	defer span.End()
	// Mail or an invitation only to the owner's own proven addresses on
	// that Source reaches nobody else.
	if p.Reach == core.ReachOthers && len(p.Details.Recipients) > 0 && allOwner(pol, p.Source, p.Details.Recipients) {
		p.Reach = core.ReachOwner
	}
	findings := task.Trusted.Check(p.Details)
	covered := pol.Covers(p.Source, p.Action, p.Details.Recipients)
	need := p.Reach.NeedsApproval(covered, len(findings) > 0)
	// Only the owner can approve, and a delegate's request is never covered
	// by the owner's standing approvals when it acts as the owner.
	if task.Mandate.Role != core.RoleOwner && p.Reach != core.ReachInternal && p.Reach != core.ReachRequester {
		need = true
	}
	span.SetAttributes(telemetry.ObservationType.String("guardrail"), telemetry.GenAIToolName.String(toolName), telemetry.Source.String(string(p.Source)),
		telemetry.Action.String(p.Action), telemetry.Reach.String(string(p.Reach)),
		telemetry.NeedApproval.Bool(need), telemetry.Findings.Int(len(findings)), telemetry.TaskID.String(task.TicketID))
	details, _ := json.Marshal(p.Details)
	fj, _ := json.Marshal(findings)
	var payload []byte
	if p.Payload != nil {
		payload, _ = json.Marshal(p.Payload)
	}
	eff := store.Effect{
		TicketID: task.TicketID, ThreadID: task.ThreadID, Source: string(p.Source), Kind: "action",
		Reach: string(p.Reach), Body: p.Body, Action: p.Action, Recipients: strings.Join(p.Details.Recipients, ", "),
		Details: string(details), Findings: string(fj), CreatedAt: s.g.Now().UTC(), Status: store.EffectPending,
		Payload: string(payload),
	}
	if need {
		eff.Status, eff.Approval = store.EffectAwaitingApproval, store.ApprovalAwaiting
	}
	if err := s.g.Store.Tx(func(tx *store.Tx) error { return tx.CreateEffect(eff) }); err != nil {
		telemetry.Fail(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.String(string(telemetry.Outcome), "queued"))
	res := map[string]any{"queued": true, "needs_owner_approval": need}
	if len(findings) > 0 {
		res["why"] = findings
	}
	return res, nil
}

func allOwner(p policy.Policy, src core.SourceKind, people []string) bool {
	for _, x := range people {
		if role, _ := p.RoleOf(core.Identity{Source: src, ID: strings.ToLower(strings.TrimSpace(x))}); role != core.RoleOwner {
			return false
		}
	}
	return true
}

// IsUnavailable reports whether err says a toolset can't be used now.
func IsUnavailable(err error) (*Unavailable, bool) {
	var u *Unavailable
	ok := errors.As(err, &u)
	return u, ok
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
