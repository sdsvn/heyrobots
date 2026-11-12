package models

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
)

type addArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

// An ADK agent runs a whole tool turn through an adapter: the model calls a
// function tool, ADK runs it, and the result goes back paired with the call.
func TestAnADKAgentRunsOnAnAdapter(t *testing.T) {
	f := &fakeAPI{answers: []string{
		`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"add","arguments":"{\"a\":2,\"b\":3}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":5}}`,
		`{"choices":[{"message":{"content":"It's 5."},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":3}}`,
	}}
	m := NewOpenAI("local", "", f.server(t).URL, nil)
	ran := 0
	add, err := functiontool.New(functiontool.Config{Name: "add", Description: "Adds two numbers."}, func(_ agent.Context, a addArgs) (map[string]int, error) {
		ran++
		return map[string]int{"sum": a.A + a.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "worker", Model: m, Instruction: "Add numbers.", Tools: []tool.Tool{add}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "t", Agent: ag, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	var last string
	for ev, err := range r.Run(context.Background(), "u", "s", genai.NewContentFromText("2+3?", genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					last = p.Text
				}
			}
		}
	}
	if ran != 1 || last != "It's 5." || len(f.got) != 2 {
		t.Fatalf("ran %d, last %q, calls %d", ran, last, len(f.got))
	}
	msgs := f.got[1]["messages"].([]any)
	tool := msgs[len(msgs)-1].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || !strings.Contains(tool["content"].(string), `"sum":5`) {
		t.Fatalf("tool result = %v", tool)
	}
	if !strings.Contains(msgs[0].(map[string]any)["content"].(string), "Add numbers.") {
		t.Fatalf("system = %v", msgs[0])
	}
}
