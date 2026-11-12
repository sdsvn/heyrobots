package models

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// openaiLLM calls an OpenAI-compatible chat completions endpoint, as
// DeepSeek, OpenRouter, Ollama, LM Studio and vLLM offer.
type openaiLLM struct {
	name string
	key  string
	base string
	http *http.Client
}

// NewOpenAI returns a model on an OpenAI-compatible endpoint. base is the
// address the endpoint's paths start from, such as
// https://api.deepseek.com or http://localhost:11434/v1. key may be empty
// for a local server.
func NewOpenAI(name, key, base string, c *http.Client) model.LLM {
	if c == nil {
		c = &http.Client{Timeout: httpTimeout}
	}
	return &openaiLLM{name: name, key: key, base: strings.TrimRight(base, "/"), http: c}
}

func (m *openaiLLM) Name() string { return m.name }

type oCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oMessage struct {
	Role       string  `json:"role"`
	Content    *string `json:"content"`
	ToolCalls  []oCall `json:"tool_calls,omitempty"`
	ToolCallID string  `json:"tool_call_id,omitempty"`
}

func str(s string) *string { return &s }

func openaiMessages(system string, contents []*genai.Content) []oMessage {
	var out []oMessage
	if system != "" {
		out = append(out, oMessage{Role: "system", Content: str(system)})
	}
	for _, c := range contents {
		if c == nil {
			continue
		}
		if c.Role == genai.RoleModel {
			msg := oMessage{Role: "assistant"}
			var text []string
			for i, p := range c.Parts {
				switch {
				case p == nil || p.Thought:
				case p.FunctionCall != nil:
					id := p.FunctionCall.ID
					if id == "" {
						id = newID("call", i)
					}
					args, _ := json.Marshal(p.FunctionCall.Args)
					if p.FunctionCall.Args == nil {
						args = []byte("{}")
					}
					call := oCall{ID: id, Type: "function"}
					call.Function.Name, call.Function.Arguments = p.FunctionCall.Name, string(args)
					msg.ToolCalls = append(msg.ToolCalls, call)
				case p.Text != "":
					text = append(text, p.Text)
				}
			}
			if len(text) > 0 {
				msg.Content = str(strings.Join(text, "\n"))
			}
			if msg.Content != nil || len(msg.ToolCalls) > 0 {
				out = append(out, msg)
			}
			continue
		}
		var text []string
		for _, p := range c.Parts {
			switch {
			case p == nil || p.Thought:
			case p.FunctionResponse != nil:
				out = append(out, oMessage{Role: "tool", ToolCallID: p.FunctionResponse.ID, Content: str(resultText(p.FunctionResponse))})
			case p.Text != "":
				text = append(text, p.Text)
			}
		}
		if len(text) > 0 {
			out = append(out, oMessage{Role: "user", Content: str(strings.Join(text, "\n"))})
		}
	}
	return out
}

type oResponse struct {
	Choices []struct {
		Message struct {
			Content   *string `json:"content"`
			ToolCalls []oCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		// DeepSeek, OpenAI and vLLM report cache hits here.
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		// Older DeepSeek field.
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

func (m *openaiLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(m.generate(ctx, req))
	}
}

func (m *openaiLLM) generate(ctx context.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	decls, search := declarations(req.Config)
	if search {
		return nil, ErrNoWebSearch
	}
	system := systemText(req.Config)
	body := map[string]any{"model": m.name, "max_tokens": maxTokens(req.Config)}
	if ok, schema := wantsJSON(req.Config); ok {
		body["response_format"] = map[string]any{"type": "json_object"}
		hint := "\n\nReply with one JSON object and nothing else."
		if schema != nil {
			if b, err := json.Marshal(schema); err == nil {
				hint += " It must match this JSON schema: " + string(b)
			}
		}
		system += hint
	}
	if req.Config != nil && req.Config.Temperature != nil {
		body["temperature"] = *req.Config.Temperature
	}
	var tools []any
	for _, d := range decls {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": d.Name, "description": d.Description, "parameters": d.Schema}})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	body["messages"] = openaiMessages(strings.TrimSpace(system), req.Contents)
	headers := map[string]string{}
	if m.key != "" {
		headers["Authorization"] = "Bearer " + m.key
	}
	var r oResponse
	if err := post(ctx, m.http, "The model server", m.base+"/chat/completions", headers, body, &r); err != nil {
		return nil, err
	}
	content := &genai.Content{Role: genai.RoleModel}
	resp := &model.LLMResponse{Content: content, UsageMetadata: tokenUse(r.Usage.PromptTokens, r.Usage.CompletionTokens), TurnComplete: true, FinishReason: genai.FinishReasonStop}
	resp.UsageMetadata.CachedContentTokenCount = int32(max(r.Usage.PromptTokensDetails.CachedTokens, r.Usage.PromptCacheHitTokens))
	if len(r.Choices) == 0 {
		content.Parts = []*genai.Part{{Text: ""}}
		return resp, nil
	}
	ch := r.Choices[0]
	if ch.Message.Content != nil && *ch.Message.Content != "" {
		content.Parts = append(content.Parts, &genai.Part{Text: *ch.Message.Content})
	}
	for _, c := range ch.Message.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(c.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
				// A weak model's broken arguments go back to it as the
				// tool's error, through ADK's argument check.
				args = map[string]any{"_unparsed_arguments": c.Function.Arguments}
			}
		}
		content.Parts = append(content.Parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: c.ID, Name: c.Function.Name, Args: args}})
	}
	if ch.FinishReason == "length" {
		resp.FinishReason = genai.FinishReasonMaxTokens
	}
	if len(content.Parts) == 0 {
		content.Parts = []*genai.Part{{Text: ""}}
	}
	return resp, nil
}
