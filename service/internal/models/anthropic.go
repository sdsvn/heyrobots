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

// AnthropicURL is the Messages API.
const AnthropicURL = "https://api.anthropic.com/v1/messages"

// anthropicVersion is the API version the adapter is written for. On it,
// requests carry no temperature.
const anthropicVersion = "2026-10-21"

// anthropicLLM calls the Anthropic Messages API.
type anthropicLLM struct {
	name string
	key  string
	url  string
	http *http.Client
}

// NewAnthropic returns an Anthropic model. url may be empty for the
// public API.
func NewAnthropic(name, key, url string, c *http.Client) model.LLM {
	if url == "" {
		url = AnthropicURL
	}
	if c == nil {
		c = &http.Client{Timeout: httpTimeout}
	}
	return &anthropicLLM{name: name, key: key, url: url, http: c}
}

func (m *anthropicLLM) Name() string { return m.name }

type aBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type aMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type aResponse struct {
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// anthropicMessages turns the conversation into Messages API turns.
// Thinking is never sent back: dropping every thinking block keeps the
// history valid however the system prompt or tools change between turns.
func anthropicMessages(contents []*genai.Content) []aMessage {
	var out []aMessage
	add := func(role string, blocks []json.RawMessage) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, aMessage{Role: role, Content: blocks})
	}
	for _, c := range contents {
		if c == nil {
			continue
		}
		role := "user"
		if c.Role == genai.RoleModel {
			role = "assistant"
		}
		var results, others []json.RawMessage
		for i, p := range c.Parts {
			if p == nil || p.Thought {
				continue
			}
			var b aBlock
			switch {
			case p.FunctionCall != nil:
				id := p.FunctionCall.ID
				if id == "" {
					id = newID("call", i)
				}
				args := p.FunctionCall.Args
				if args == nil {
					args = map[string]any{}
				}
				b = aBlock{Type: "tool_use", ID: id, Name: p.FunctionCall.Name, Input: args}
			case p.FunctionResponse != nil:
				b = aBlock{Type: "tool_result", ToolUseID: p.FunctionResponse.ID, Content: resultText(p.FunctionResponse)}
			case strings.TrimSpace(p.Text) != "":
				b = aBlock{Type: "text", Text: p.Text}
			default:
				continue
			}
			raw, _ := json.Marshal(b)
			// Tool results come first in a user turn.
			if b.Type == "tool_result" {
				results = append(results, raw)
			} else {
				others = append(others, raw)
			}
		}
		add(role, append(results, others...))
	}
	return out
}

func (m *anthropicLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(m.generate(ctx, req))
	}
}

func (m *anthropicLLM) generate(ctx context.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	decls, search := declarations(req.Config)
	body := map[string]any{
		"model":      m.name,
		"max_tokens": maxTokens(req.Config),
		"messages":   anthropicMessages(req.Contents),
		// Cache the stable prefix (tools, system, earlier turns) at the
		// last cacheable block; a read costs a tenth of the input price.
		"cache_control": map[string]any{"type": "ephemeral"},
	}
	system := systemText(req.Config)
	var tools []any
	for _, d := range decls {
		tools = append(tools, map[string]any{"name": d.Name, "description": d.Description, "input_schema": d.Schema})
	}
	if search {
		// Haiku has the basic search tool; the larger models filter results
		// as they search.
		kind := "web_search_20260209"
		if strings.Contains(m.name, "haiku") {
			kind = "web_search_20250305"
		}
		tools = append(tools, map[string]any{"type": kind, "name": "web_search", "max_uses": 3})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if ok, schema := wantsJSON(req.Config); ok {
		if schema != nil {
			body["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": strictObjects(schema)}}
		} else {
			system += "\n\nReply with one JSON object and nothing else."
		}
	}
	if system != "" {
		body["system"] = systemBlocks(system)
	}
	headers := map[string]string{"x-api-key": m.key, "anthropic-version": anthropicVersion}

	var all []json.RawMessage
	in, outTok, cached := 0, 0, 0
	var stop string
	// A server-side search can pause a long turn; sending the turn back
	// resumes it.
	for range 4 {
		var r aResponse
		if err := post(ctx, m.http, "Anthropic", m.url, headers, body, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Content...)
		in += r.Usage.InputTokens + r.Usage.CacheCreationInputTokens + r.Usage.CacheReadInputTokens
		cached += r.Usage.CacheReadInputTokens
		outTok += r.Usage.OutputTokens
		stop = r.StopReason
		if stop != "pause_turn" {
			break
		}
		msgs := body["messages"].([]aMessage)
		body["messages"] = append(msgs, aMessage{Role: "assistant", Content: r.Content})
	}
	resp := anthropicResponse(all, stop, in, outTok)
	resp.UsageMetadata.CachedContentTokenCount = int32(cached)
	return resp, nil
}

func anthropicResponse(blocks []json.RawMessage, stop string, in, out int) *model.LLMResponse {
	content := &genai.Content{Role: genai.RoleModel}
	var sources []string
	for _, raw := range blocks {
		var b struct {
			Type      string         `json:"type"`
			Text      string         `json:"text"`
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			Input     map[string]any `json:"input"`
			Citations []struct {
				URL string `json:"url"`
			} `json:"citations"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		switch b.Type {
		case "text":
			content.Parts = append(content.Parts, &genai.Part{Text: b.Text})
			for _, c := range b.Citations {
				sources = append(sources, c.URL)
			}
		case "tool_use":
			args := b.Input
			if args == nil {
				args = map[string]any{}
			}
			content.Parts = append(content.Parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: b.ID, Name: b.Name, Args: args}})
		case "web_search_tool_result":
			var results []struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(b.Content, &results) == nil {
				for _, r := range results {
					sources = append(sources, r.URL)
				}
			}
		}
	}
	resp := &model.LLMResponse{Content: content, UsageMetadata: tokenUse(in, out), TurnComplete: true}
	switch stop {
	case "max_tokens":
		resp.FinishReason = genai.FinishReasonMaxTokens
	case "refusal":
		resp.FinishReason = genai.FinishReasonSafety
		if len(content.Parts) == 0 {
			content.Parts = []*genai.Part{{Text: "The model declined this request."}}
		}
	default:
		resp.FinishReason = genai.FinishReasonStop
	}
	if len(sources) > 0 {
		g := &genai.GroundingMetadata{}
		seen := map[string]bool{}
		for _, s := range sources {
			if s != "" && !seen[s] {
				seen[s] = true
				g.GroundingChunks = append(g.GroundingChunks, &genai.GroundingChunk{Web: &genai.GroundingChunkWeb{URI: s}})
			}
		}
		resp.GroundingMetadata = g
	}
	if len(content.Parts) == 0 {
		content.Parts = []*genai.Part{{Text: ""}}
	}
	return resp
}

// systemBlocks splits the worker's system prompt at its section headings
// into blocks, with a cache breakpoint after the global prompt and after
// the agent's own section. The global block is the same for every agent
// and task, the agent block for every task of that agent, so both hit the
// cache across tasks; only the task block is new each time. Other prompts
// stay one block.
func systemBlocks(system string) any {
	var blocks []map[string]any
	rest := system
	for _, h := range []string{"\n# You are ", "\n# Your task\n"} {
		i := strings.Index(rest, h)
		if i <= 0 {
			continue
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": rest[:i], "cache_control": map[string]any{"type": "ephemeral"}})
		rest = rest[i+1:]
	}
	if len(blocks) == 0 {
		return system
	}
	return append(blocks, map[string]any{"type": "text", "text": rest})
}
