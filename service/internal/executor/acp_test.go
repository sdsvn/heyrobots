package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"heyrobots/service/internal/acp"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/store"
)

// fakeAgentEnv makes this test binary act as the ACP agent when it is run
// as the adapter.
const fakeAgentEnv = "HEYROBOTS_FAKE_ACP"

func TestMain(m *testing.M) {
	if os.Getenv(fakeAgentEnv) == "1" {
		fakeAgent()
		return
	}
	os.Exit(m.Run())
}

// fakeAgent speaks ACP on stdio: it writes a file through the gateway's
// MCP tools, asks for an edit outside the folder (which must be refused),
// says a word, and reports ready.
func fakeAgent() {
	a := &fake{}
	conn := sdk.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	a.conn = conn
	<-conn.Done()
}

type fake struct {
	conn    *sdk.AgentSideConnection
	servers []sdk.McpServer
}

func (f *fake) Initialize(context.Context, sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{ProtocolVersion: sdk.ProtocolVersionNumber, AuthMethods: []sdk.AuthMethod{}}, nil
}
func (f *fake) Authenticate(context.Context, sdk.AuthenticateRequest) (sdk.AuthenticateResponse, error) {
	return sdk.AuthenticateResponse{}, nil
}
func (f *fake) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, nil
}
func (f *fake) Cancel(context.Context, sdk.CancelNotification) error { return nil }
func (f *fake) CloseSession(context.Context, sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	return sdk.CloseSessionResponse{}, nil
}
func (f *fake) ListSessions(context.Context, sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	return sdk.ListSessionsResponse{}, nil
}
func (f *fake) ResumeSession(context.Context, sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	return sdk.ResumeSessionResponse{}, nil
}
func (f *fake) SetSessionConfigOption(context.Context, sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	return sdk.SetSessionConfigOptionResponse{}, nil
}
func (f *fake) SetSessionMode(context.Context, sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	return sdk.SetSessionModeResponse{}, nil
}
func (f *fake) NewSession(_ context.Context, p sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	f.servers = p.McpServers
	return sdk.NewSessionResponse{SessionId: "fake-1"}, nil
}

func (f *fake) Prompt(ctx context.Context, p sdk.PromptRequest) (sdk.PromptResponse, error) {
	if len(f.servers) != 1 || f.servers[0].Http == nil {
		return sdk.PromptResponse{}, fmt.Errorf("expected one http MCP server, got %+v", f.servers)
	}
	srv := f.servers[0].Http
	hc := &http.Client{Transport: headers{srv.Headers}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "fake", Version: "0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: srv.Url, HTTPClient: hc}, nil)
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	defer cs.Close()
	call := func(name string, args map[string]any) (string, error) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		for _, c := range res.Content {
			if t, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(t.Text)
			}
		}
		return sb.String(), nil
	}
	if _, err := call("write_file", map[string]any{"path": "drafts/thanks.md", "content": "Dear Anna, thank you!"}); err != nil {
		return sdk.PromptResponse{}, err
	}
	// An edit outside the task folder must be refused.
	kind := sdk.ToolKindEdit
	perm, err := f.conn.RequestPermission(ctx, sdk.RequestPermissionRequest{
		ToolCall: sdk.ToolCallUpdate{ToolCallId: "t1", Kind: &kind, Locations: []sdk.ToolCallLocation{{Path: "/etc/hosts"}}},
		Options:  []sdk.PermissionOption{{OptionId: "yes", Kind: sdk.PermissionOptionKindAllowOnce, Name: "Allow"}, {OptionId: "no", Kind: sdk.PermissionOptionKindRejectOnce, Name: "Reject"}},
	})
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	refused := perm.Outcome.Selected != nil && perm.Outcome.Selected.OptionId == "no"
	_ = f.conn.SessionUpdate(ctx, sdk.SessionNotification{SessionId: p.SessionId, Update: sdk.SessionUpdate{
		AgentMessageChunk: &sdk.SessionUpdateAgentMessageChunk{SessionUpdate: "agent_message_chunk", Content: sdk.TextBlock("Writing the note.")}}})
	out, err := call("report", map[string]any{"state": "ready", "summary": "Wrote the thank-you note.",
		"result": fmt.Sprintf("Draft in drafts/thanks.md. Outside edit refused: %v", refused)})
	if err != nil || !strings.Contains(out, `"ok":true`) {
		return sdk.PromptResponse{}, fmt.Errorf("report: %q %v", out, err)
	}
	return sdk.PromptResponse{StopReason: sdk.StopReasonEndTurn}, nil
}

type headers struct{ h []sdk.HttpHeader }

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	for _, x := range h.h {
		r.Header.Set(x.Name, x.Value)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestAClaudeCodeTaskUsesTheGatewayOverMCP(t *testing.T) {
	e := newEnv(t)
	t.Setenv(fakeAgentEnv, "1")
	e.exec.ACP = &acp.Worker{Command: []string{os.Args[0], "-test.run=^$"}, ConfigDir: t.TempDir()}
	e.exec.UsesACP = func(store.Agent) bool { return true }
	res := e.say(owner, "c1", "draft a thank-you note to Anna")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	tk := e.ticket(res.ThreadIDs[0])
	if tk.Open || tk.Stage != string(core.StageReady) {
		t.Fatalf("ticket = %+v", tk)
	}
	ws := filepath.Join(e.exec.Workspaces, tk.ID)
	if b, err := os.ReadFile(filepath.Join(ws, "drafts", "thanks.md")); err != nil || string(b) != "Dear Anna, thank you!" {
		t.Fatalf("file = %q, %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md")); !strings.Contains(string(b), "# Your task") || !strings.Contains(string(b), "heyrobots") {
		t.Fatalf("CLAUDE.md = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, sessionFile+"-"+tk.ID)); string(b) != "fake-1" {
		t.Fatalf("session file = %q", b)
	}
	msgs := messages(e.view(res.ThreadIDs[0]))
	if len(msgs) != 1 || !strings.Contains(msgs[0], "Outside edit refused: true") {
		t.Fatalf("messages = %q", msgs)
	}
}

func TestInputSchemaUsesJSONSchemaTypes(t *testing.T) {
	s := inputSchema(nil)
	if b, _ := json.Marshal(s); string(b) != `{"type":"object"}` {
		t.Fatalf("schema = %s", b)
	}
}
