package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/v2/tool"

	"heyrobots/service/internal/acp"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/telemetry"
)

// sessionFile keeps the ACP session id in the task folder, so the next
// Assignment continues the same conversation.
const sessionFile = ".heyrobots-acp-session"

// acpNote tells Claude Code how heyrobots' instructions apply inside it.
const acpNote = `# How this works in Claude Code

You run inside Claude Code for the heyrobots server. Every tool these instructions name (report, ack, hand_back, remember, make_job and the task's own tools) is a tool of the "heyrobots" MCP server; call them there. The user turn comes from the server, not from a person typing; the people are inside the messages it carries. Read, search and edit files only in this folder. Don't fetch the web yourself and don't change your permission mode: those calls are refused; the task's research tools go through the server's quarantine instead.`

// runACP runs the Assignment in the owner's own Claude Code: one ACP
// session in the task folder, with the run's tools served over MCP.
func (r *run) runACP(ctx context.Context, tools []tool.Tool) (report, error) {
	e := r.e
	ws := filepath.Join(e.Workspaces, r.c.Ticket.ID)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		return report{State: "failed", Summary: "The task's folder couldn't be made."}, err
	}
	instructions := e.Prompt + "\n\n" + r.agentSection() + "\n\n" + r.taskSection() + "\n\n" + acpNote + "\n"
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte(instructions), 0o600); err != nil {
		return report{State: "failed", Summary: "The task's instructions couldn't be written."}, err
	}
	if err := r.copySkills(ws); err != nil {
		return report{State: "failed", Summary: "The agent's skills couldn't be copied."}, err
	}
	b, err := e.bridge()
	if err != nil {
		return report{State: "failed", Summary: "The task's tools couldn't be served."}, err
	}
	url, tok := b.add(r.mcpServer(tools))
	defer b.remove(tok)
	servers := []sdk.McpServer{{Http: &sdk.McpServerHttpInline{Name: "heyrobots", Type: "http", Url: url,
		Headers: []sdk.HttpHeader{{Name: "Authorization", Value: "Bearer " + tok}}}}}

	r.taintFromHistory(ctx, r.c.Ticket.ID)
	loadID, _ := os.ReadFile(filepath.Join(ws, sessionFile))
	client := &acpClient{r: r, ws: ws, ctx: ctx, spans: map[sdk.ToolCallId]trace.Span{}}
	rctx, cancel := context.WithTimeout(ctx, e.RunLimit)
	defer cancel()
	sess, err := e.ACP.Start(rctx, client, ws, servers, strings.TrimSpace(string(loadID)))
	if errors.Is(err, acp.ErrNotSignedIn) {
		return report{State: "blocked", Summary: "Claude Code isn't signed in for heyrobots.",
			HumanAction: "Run " + e.ACP.LoginCommand() + " in a terminal, then reply here and I'll carry on."}, err
	}
	if err != nil {
		return report{State: "failed", Summary: "Claude Code couldn't start: " + shorten(err.Error(), 200)}, err
	}
	defer sess.Close()
	_ = os.WriteFile(filepath.Join(ws, sessionFile), []byte(sess.ID), 0o600)

	msg := r.inputMessage()
	if len(loadID) > 0 && !sess.Loaded {
		msg = "Note from the host: your earlier history on this task couldn't be loaded. Check the folder for what you already did before repeating anything.\n\n" + msg
	}
	stop, err := sess.Prompt(rctx, msg)
	if err == nil && stop == sdk.StopReasonEndTurn && r.reported() == nil {
		// One nudge: the turn has to end with a report.
		stop, err = sess.Prompt(rctx, "End your turn with the report tool.")
	}
	final := r.reported()
	switch {
	case final != nil:
		return *final, nil
	case errors.Is(context.Cause(ctx), errStopped):
		return report{State: "dropped", Summary: "Stopped by request."}, nil
	case errors.Is(rctx.Err(), context.DeadlineExceeded):
		return report{State: "blocked", Summary: fmt.Sprintf("I ran out of time (%s) before finishing.", e.RunLimit)}, rctx.Err()
	case ctx.Err() != nil:
		return report{State: store.StateInterrupted}, ctx.Err()
	case err != nil:
		return report{State: "failed", Summary: "I couldn't finish because of an error: " + shorten(err.Error(), 200)}, err
	case stop == sdk.StopReasonRefusal:
		return report{State: "blocked", Summary: "Claude Code refused the task: " + shorten(client.text(), 500)}, nil
	default:
		s := client.text()
		if s == "" {
			s = stopWhy(stop)
		}
		return report{State: "blocked", Summary: shorten(s, 2000)}, nil
	}
}

// stopWhy says, in plain words, why a turn ended without a report.
func stopWhy(stop sdk.StopReason) string {
	switch stop {
	case sdk.StopReasonMaxTokens:
		return "My answer got too long and was cut off before I finished. Ask me to carry on, or to do it in smaller parts."
	case sdk.StopReasonMaxTurnRequests:
		return "I used up my steps for this turn before finishing. Reply and I'll carry on from where I stopped."
	case sdk.StopReasonCancelled:
		return "My turn was cancelled before I finished. Reply and I'll carry on."
	}
	return "I stopped before finishing without saying why. Reply and I'll carry on."
}

func (r *run) reported() *report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.final
}

// copySkills puts the agent's skills where Claude Code loads them.
func (r *run) copySkills(ws string) error {
	names := r.agent.SkillList()
	if len(names) == 0 || r.e.Skills == nil {
		return nil
	}
	dir := filepath.Join(ws, ".claude", "skills")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, name := range names {
		src := filepath.Join(r.e.Skills.Dir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.CopyFS(filepath.Join(dir, name), os.DirFS(src)); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) hasToolset(ts core.Toolset) bool {
	for _, t := range r.toolsets {
		if t == ts {
			return true
		}
	}
	return false
}

// acpClient answers Claude Code: its own tools may touch only the task
// folder, and what it does shows in the trace and as progress.
type acpClient struct {
	acp.BaseClient
	r   *run
	ws  string
	ctx context.Context

	mu    sync.Mutex
	buf   strings.Builder
	spans map[sdk.ToolCallId]trace.Span
}

func (c *acpClient) RequestPermission(_ context.Context, p sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	return acp.Answer(p, c.allowed(p.ToolCall)), nil
}

// allowed keeps Claude Code's own tools inside the task: reads, searches
// and edits in the folder; commands only with the code or data toolset;
// never the web or a mode switch, which would skip the quarantine.
func (c *acpClient) allowed(t sdk.ToolCallUpdate) bool {
	for _, l := range t.Locations {
		if !inside(c.ws, l.Path) {
			return false
		}
	}
	kind := sdk.ToolKindOther
	if t.Kind != nil {
		kind = *t.Kind
	}
	switch kind {
	case sdk.ToolKindFetch, sdk.ToolKindSwitchMode:
		return false
	case sdk.ToolKindExecute:
		return c.r.hasToolset(core.ToolsetCode) || c.r.hasToolset(core.ToolsetData)
	}
	return true
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (c *acpClient) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	u := n.Update
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		if c.buf.Len() < 4000 {
			c.buf.WriteString(u.AgentMessageChunk.Content.Text.Text)
		}
	case u.ToolCall != nil:
		tc := u.ToolCall
		_, span := telemetry.Tracer().Start(c.ctx, "execute_tool "+tc.Title, trace.WithAttributes(
			telemetry.GenAIOperation.String("execute_tool"), telemetry.GenAIToolName.String(tc.Title),
			attribute.String("acp.tool.kind", string(tc.Kind))))
		c.spans[tc.ToolCallId] = span
		_ = c.r.e.Store.Tx(func(tx *store.Tx) error { return tx.NoteProgress(c.r.c.Assignment.ID, shorten(tc.Title, 200)) })
	case u.ToolCallUpdate != nil && u.ToolCallUpdate.Status != nil:
		st := *u.ToolCallUpdate.Status
		if st != sdk.ToolCallStatusCompleted && st != sdk.ToolCallStatusFailed {
			return nil
		}
		if span, ok := c.spans[u.ToolCallUpdate.ToolCallId]; ok {
			span.SetAttributes(telemetry.Outcome.String(string(st)))
			span.End()
			delete(c.spans, u.ToolCallUpdate.ToolCallId)
		}
	}
	return nil
}

func (c *acpClient) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.buf.String())
}
