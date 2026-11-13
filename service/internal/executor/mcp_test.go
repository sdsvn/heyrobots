package executor

import (
	"context"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/quarantine"
	"heyrobots/service/internal/store"
)

type answer struct{}

func (answer) Name() string { return "reader" }
func (answer) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{Content: genai.NewContentFromText(`{"answer":"Issue 7 is the login bug."}`, genai.RoleModel)}, nil)
	}
}

type query struct {
	Query string `json:"query"`
}

type issue struct {
	Title string `json:"title"`
}

// A task granted an MCP toolset sees only the tools the owner allowed:
// reads run through quarantine, actions wait in the outbox.
func TestATaskUsesItsMCPToolsThroughTheGateway(t *testing.T) {
	e := newEnv(t)
	var searched, created atomic.Int32
	srv := mcp.NewServer(&mcp.Implementation{Name: "tracker", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "search", Description: "Search issues."}, func(context.Context, *mcp.CallToolRequest, query) (*mcp.CallToolResult, any, error) {
		searched.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Issue 7: login fails"}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "create_issue", Description: "Create an issue."}, func(context.Context, *mcp.CallToolRequest, issue) (*mcp.CallToolResult, any, error) {
		created.Add(1)
		return &mcp.CallToolResult{}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "delete_all", Description: "Delete everything."}, func(context.Context, *mcp.CallToolRequest, query) (*mcp.CallToolResult, any, error) {
		t.Error("an unreviewed tool ran")
		return &mcp.CallToolResult{}, nil, nil
	})
	m := &gateway.MCP{Store: e.store, Reader: &quarantine.Reader{Model: func(context.Context) (model.LLM, error) { return answer{}, nil }},
		Connect: func(ctx context.Context, _ store.MCPServer) (*mcp.ClientSession, error) {
			ct, st := mcp.NewInMemoryTransports()
			if _, err := srv.Connect(ctx, st, nil); err != nil {
				return nil, err
			}
			return mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
		}}
	e.exec.MCP = m
	e.exec.Reader = m.Reader
	if err := e.store.Tx(func(tx *store.Tx) error {
		return tx.CreateMCPServer(store.MCPServer{Name: "tracker", Transport: store.MCPStdio, Command: `["x"]`, CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(context.Background(), "tracker"); err != nil {
		t.Fatal(err)
	}
	_ = e.store.Tx(func(tx *store.Tx) error {
		_ = tx.ReviewMCPTool("tracker", "search", store.MCPToolRead, "")
		return tx.ReviewMCPTool("tracker", "create_issue", store.MCPToolOutward, string(core.ReachOthers))
	})
	ts := core.MCPToolset("tracker")
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, ts) })
	e.eval.Router = acceptRouter{[]core.Toolset{ts}}

	e.model.steps = [][]*genai.Part{
		call("tracker__search", map[string]any{"query": "login", "question": "Which issue is the login bug?"}),
		call("tracker__delete_all", map[string]any{"query": "x"}),
		call("tracker__create_issue", map[string]any{"title": "Login still fails"}),
		reportCall("ready", "Filed it.", "Asked to file an issue.", ""),
	}
	res := e.say(owner, "c1", "file a follow-up to the login bug")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	if searched.Load() != 1 || created.Load() != 0 {
		t.Fatalf("searched %d, created %d", searched.Load(), created.Load())
	}
	acts := e.actions(res.ThreadIDs[0])
	// A tool nobody reviewed asks first too, like every tool that isn't read-only.
	if len(acts) != 2 || acts[1].Source != "mcp" || acts[1].Action != "tracker/create_issue" || acts[0].Status != store.EffectAwaitingApproval || acts[1].Status != store.EffectAwaitingApproval {
		t.Fatalf("actions = %+v", acts)
	}
	if tk := e.ticket(res.ThreadIDs[0]); tk.Stage != string(core.StageReady) {
		t.Fatalf("ticket = %+v", tk)
	}
}
