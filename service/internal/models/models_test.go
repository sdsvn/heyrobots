package models

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// fakeAPI answers with the next canned body and records each request.
type fakeAPI struct {
	got     []map[string]any
	headers []http.Header
	answers []string
	status  int
}

func (f *fakeAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		f.got = append(f.got, m)
		f.headers = append(f.headers, r.Header.Clone())
		if f.status != 0 {
			w.WriteHeader(f.status)
			io.WriteString(w, `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		n := len(f.got) - 1
		if n >= len(f.answers) {
			n = len(f.answers) - 1
		}
		io.WriteString(w, f.answers[n])
	}))
	t.Cleanup(srv.Close)
	return srv
}

func one(t *testing.T, m model.LLM, req *model.LLMRequest) *model.LLMResponse {
	t.Helper()
	var out *model.LLMResponse
	for r, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		out = r
	}
	return out
}

// A worker turn after one tool call: thinking from the earlier turn is
// left out, the call and its result pair up, and tools keep their schema.
func conversation() *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("Book a table", genai.RoleUser),
			{Role: genai.RoleModel, Parts: []*genai.Part{
				{Text: "planning", Thought: true},
				{Text: "Let me look."},
				{FunctionCall: &genai.FunctionCall{ID: "toolu_1", Name: "web_fetch", Args: map[string]any{"url": "https://x.example"}}},
			}},
			{Role: genai.RoleUser, Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{ID: "toolu_1", Name: "web_fetch", Response: map[string]any{"answer": "open"}}},
			}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText("You are Bit.", genai.RoleUser),
			Temperature:       genai.Ptr[float32](0),
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name: "web_fetch", Description: "Read a page.",
				ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}},
			}}}},
		},
	}
}

func TestAnthropicRequestAndReply(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"content":[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"Booking."},{"type":"tool_use","id":"toolu_2","name":"report","input":{"state":"ready"}}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":5}}`}}
	srv := f.server(t)
	m := NewAnthropic("claude-haiku-5-5", "sk-test", srv.URL, nil)
	resp := one(t, m, conversation())

	req := f.got[0]
	if f.headers[0].Get("x-api-key") != "sk-test" || f.headers[0].Get("anthropic-version") != anthropicVersion {
		t.Fatalf("headers = %v", f.headers[0])
	}
	if req["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("no cache_control: %v", req["cache_control"])
	}
	if req["system"] != "You are Bit." || req["temperature"] != nil || req["model"] != "claude-haiku-5-5" {
		t.Fatalf("request = %v", req)
	}
	msgs := req["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	if len(asst) != 2 || asst[0].(map[string]any)["type"] != "text" || asst[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("assistant turn = %v (thinking must be left out)", asst)
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" || !strings.Contains(result["content"].(string), "open") {
		t.Fatalf("tool result = %v", result)
	}
	tool := req["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "web_fetch" || tool["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool = %v", tool)
	}

	parts := resp.Content.Parts
	if len(parts) != 2 || parts[0].Text != "Booking." || parts[1].FunctionCall == nil || parts[1].FunctionCall.ID != "toolu_2" || parts[1].FunctionCall.Args["state"] != "ready" {
		t.Fatalf("reply = %+v", parts)
	}
	if resp.UsageMetadata.CachedContentTokenCount != 5 {
		t.Fatalf("cached = %d", resp.UsageMetadata.CachedContentTokenCount)
	}
	if resp.UsageMetadata.PromptTokenCount != 105 || resp.UsageMetadata.CandidatesTokenCount != 20 {
		t.Fatalf("usage = %+v", resp.UsageMetadata)
	}
}

func TestAnthropicJSONAndSearch(t *testing.T) {
	f := &fakeAPI{answers: []string{
		`{"content":[{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"x"}}],"stop_reason":"pause_turn","usage":{"input_tokens":10,"output_tokens":1}}`,
		`{"content":[{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[{"type":"web_search_result","url":"https://news.example/a"}]},{"type":"text","text":"It rained.","citations":[{"type":"web_search_result_location","url":"https://news.example/b"}]}],"stop_reason":"end_turn","usage":{"input_tokens":30,"output_tokens":5}}`,
	}}
	srv := f.server(t)
	m := NewAnthropic("claude-sonnet-5-5", "k", srv.URL, nil)
	resp := one(t, m, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("weather", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}},
	})
	if len(f.got) != 2 {
		t.Fatalf("a paused turn wasn't resumed: %d calls", len(f.got))
	}
	if tool := f.got[0]["tools"].([]any)[0].(map[string]any); tool["type"] != "web_search_20260209" {
		t.Fatalf("search tool = %v", tool)
	}
	if n := len(resp.GroundingMetadata.GroundingChunks); n != 2 || resp.Content.Parts[0].Text != "It rained." || resp.UsageMetadata.PromptTokenCount != 40 {
		t.Fatalf("reply = %+v, %d sources", resp.Content.Parts, n)
	}

	f2 := &fakeAPI{answers: []string{`{"content":[{"type":"text","text":"{\"answer\":\"Paris\"}"}],"stop_reason":"end_turn","usage":{}}`}}
	m = NewAnthropic("claude-haiku-5-5", "k", f2.server(t).URL, nil)
	one(t, m, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("q", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{ResponseMIMEType: "application/json",
			ResponseSchema: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{"answer": {Type: genai.TypeString}}}},
	})
	format := f2.got[0]["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"].(map[string]any)["additionalProperties"] != false {
		t.Fatalf("format = %v", format)
	}
}

func TestOpenAIRequestAndReply(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_9","type":"function","function":{"name":"report","arguments":"{\"state\":\"ready\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":50,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":40}}}`}}
	srv := f.server(t)
	m := NewOpenAI("deepseek-chat", "dk", srv.URL+"/", nil)
	resp := one(t, m, conversation())
	req := f.got[0]
	if f.headers[0].Get("Authorization") != "Bearer dk" {
		t.Fatalf("auth = %q", f.headers[0].Get("Authorization"))
	}
	msgs := req["messages"].([]any)
	roles := []string{}
	for _, x := range msgs {
		roles = append(roles, x.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool" {
		t.Fatalf("roles = %v", roles)
	}
	asst := msgs[2].(map[string]any)
	if asst["content"] != "Let me look." || asst["tool_calls"].([]any)[0].(map[string]any)["id"] != "toolu_1" {
		t.Fatalf("assistant = %v", asst)
	}
	if msgs[3].(map[string]any)["tool_call_id"] != "toolu_1" {
		t.Fatalf("tool = %v", msgs[3])
	}
	call := resp.Content.Parts[0].FunctionCall
	if resp.UsageMetadata.CachedContentTokenCount != 40 {
		t.Fatalf("cached = %d", resp.UsageMetadata.CachedContentTokenCount)
	}
	if call == nil || call.ID != "call_9" || call.Args["state"] != "ready" || resp.UsageMetadata.PromptTokenCount != 50 {
		t.Fatalf("reply = %+v", resp)
	}
	// No search of its own.
	_, err := m.(*openaiLLM).generate(context.Background(), &model.LLMRequest{Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}}})
	if !errors.Is(err, ErrNoWebSearch) {
		t.Fatalf("search err = %v", err)
	}
}

func TestCheckPassesAndFails(t *testing.T) {
	good := &fakeAPI{answers: []string{
		`{"content":[{"type":"tool_use","id":"t","name":"record_number","input":{"number":42}}],"stop_reason":"tool_use","usage":{}}`,
		`{"content":[{"type":"text","text":"{\"answer\":\"Paris\"}"}],"stop_reason":"end_turn","usage":{}}`,
	}}
	if err := Check(context.Background(), NewAnthropic("claude-haiku-5-5", "k", good.server(t).URL, nil)); err != nil {
		t.Fatal(err)
	}
	chatty := &fakeAPI{answers: []string{`{"choices":[{"message":{"content":"42!"},"finish_reason":"stop"}],"usage":{}}`}}
	if err := Check(context.Background(), NewOpenAI("tiny", "", chatty.server(t).URL, nil)); err == nil || !strings.Contains(err.Error(), "function calling") {
		t.Fatalf("err = %v", err)
	}
	bad := &fakeAPI{status: 401}
	err := Check(context.Background(), NewAnthropic("claude-haiku-5-5", "wrong", bad.server(t).URL, nil))
	var api *APIError
	if !errors.As(err, &api) || api.Status != 401 || !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Fatalf("err = %v", err)
	}
}

func TestChoicesAreChecked(t *testing.T) {
	for _, c := range []Choice{
		{Provider: "x", Model: "m"},
		{Provider: Anthropic},
		{Provider: OpenAI, Model: "m", BaseURL: "http://api.example.com"},
		{Provider: Anthropic, Model: "m", BaseURL: "https://x"},
	} {
		if CheckChoice(c) == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	for _, c := range []Choice{
		{Provider: OpenAI, Model: "llama3.2", BaseURL: "http://localhost:11434/v1"},
		{Provider: OpenAI, Model: "deepseek-chat", BaseURL: "https://api.deepseek.com"},
		{Provider: OpenAI, Model: "qwen", BaseURL: "http://192.168.1.20:1234/v1"},
	} {
		if err := CheckChoice(c); err != nil {
			t.Errorf("refused %+v: %v", c, err)
		}
	}
	if KeyName(OpenAI, "https://api.deepseek.com/") == KeyName(OpenAI, "https://openrouter.ai/api/v1") {
		t.Fatal("two endpoints share a key")
	}
}
