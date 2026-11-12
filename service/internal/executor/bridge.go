package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"heyrobots/service/internal/ids"
)

// bridge serves each running Assignment's tools to its ACP agent as an
// MCP server on loopback. One listener serves every Assignment; a bearer
// token per session picks its tools.
type bridge struct {
	url  string
	mu   sync.Mutex
	runs map[string]*mcp.Server
}

func (e *Engine) bridge() (*bridge, error) {
	e.brOnce.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			e.brErr = err
			return
		}
		b := &bridge{url: "http://" + ln.Addr().String() + "/mcp", runs: map[string]*mcp.Server{}}
		h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			b.mu.Lock()
			defer b.mu.Unlock()
			return b.runs[tok]
		}, &mcp.StreamableHTTPOptions{Stateless: true})
		srv := &http.Server{Handler: h}
		go func() { _ = srv.Serve(ln) }()
		e.br = b
	})
	return e.br, e.brErr
}

// add serves s until remove is called. It returns the endpoint and its token.
func (b *bridge) add(s *mcp.Server) (url, token string) {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	token = hex.EncodeToString(raw[:])
	b.mu.Lock()
	b.runs[token] = s
	b.mu.Unlock()
	return b.url, token
}

func (b *bridge) remove(token string) {
	b.mu.Lock()
	delete(b.runs, token)
	b.mu.Unlock()
}

// runnable is what ADK's runner needs of a tool to call it.
type runnable interface {
	Declaration() *genai.FunctionDeclaration
	Run(ctx agent.Context, args any) (map[string]any, error)
}

// mcpServer offers the run's tools over MCP, each through the same guard
// and taint note as in the built-in worker.
func (r *run) mcpServer(tools []tool.Tool) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "heyrobots", Version: "0.1"}, nil)
	for _, t := range tools {
		rt, ok := t.(runnable)
		if !ok {
			continue
		}
		s.AddTool(&mcp.Tool{Name: t.Name(), Description: t.Description(), InputSchema: inputSchema(rt.Declaration())},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := map[string]any{}
				if b, err := json.Marshal(req.Params.Arguments); err == nil {
					_ = json.Unmarshal(b, &args)
				}
				res, err := r.callTool(ctx, t, rt, args)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				b, _ := json.Marshal(res)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
			})
	}
	return s
}

// invocation stands in for ADK's invocation context outside a runner: the
// tools only need a context and their actions.
type invocation struct{ agent.StrictContextMock }

func (invocation) Artifacts() agent.Artifacts { return nil }

func (r *run) callTool(ctx context.Context, t tool.Tool, rt runnable, args map[string]any) (map[string]any, error) {
	r.mu.Lock()
	r.calls++
	over := r.calls > r.e.MaxCalls
	r.mu.Unlock()
	if over {
		return nil, fmt.Errorf("tool call limit of %d reached; report now", r.e.MaxCalls)
	}
	tc := agent.NewToolContext(&invocation{agent.NewStrictContextMock(ctx)}, ids.New("tc"), nil, nil)
	if out, err := r.guardTools(tc, t, args); out != nil || err != nil {
		return out, err
	}
	res, err := rt.Run(tc, args)
	_, _ = r.noteTaint(tc, t, args, res, err)
	return res, err
}

// inputSchema gives a tool's parameters as JSON Schema.
func inputSchema(d *genai.FunctionDeclaration) map[string]any {
	out := map[string]any{"type": "object"}
	var src any
	switch {
	case d == nil:
		return out
	case d.ParametersJsonSchema != nil:
		src = d.ParametersJsonSchema
	case d.Parameters != nil:
		src = d.Parameters
	default:
		return out
	}
	b, err := json.Marshal(src)
	if err != nil || json.Unmarshal(b, &out) != nil {
		return map[string]any{"type": "object"}
	}
	lowerTypes(out)
	return out
}

// lowerTypes turns genai's "OBJECT" into JSON Schema's "object", all the
// way down.
func lowerTypes(v any) {
	switch x := v.(type) {
	case map[string]any:
		if t, ok := x["type"].(string); ok {
			x["type"] = strings.ToLower(t)
		}
		for _, c := range x {
			lowerTypes(c)
		}
	case []any:
		for _, c := range x {
			lowerTypes(c)
		}
	}
}
