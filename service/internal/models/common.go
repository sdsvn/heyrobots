// Package models connects heyrobots' three model roles (the router, the
// quarantine reader and the worker) to a provider of the owner's choice:
// Gemini, the Anthropic API, or any OpenAI-compatible endpoint such as
// DeepSeek, OpenRouter, Ollama, LM Studio or vLLM. Each adapter is an ADK
// model.LLM, so the agents built on it don't change.
package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// Providers.
const (
	Gemini    = "gemini"
	Anthropic = "anthropic"
	OpenAI    = "openai"
	// ClaudeCode is the owner's own Claude Code, which does the work itself
	// instead of answering model calls.
	ClaudeCode = "claude_code"
)

// Roles.
const (
	RoleRouter = "router"
	RoleReader = "reader"
	RoleWorker = "worker"
)

// Roles lists every role.
var Roles = []string{RoleRouter, RoleReader, RoleWorker}

// ErrNoWebSearch is returned for a web search request to a provider that
// has no search of its own.
var ErrNoWebSearch = errors.New("web search needs a Gemini or Anthropic reader model; choose one for the reader in settings")

// defaultMaxTokens caps a reply when the request sets no limit.
const defaultMaxTokens = 16000

// httpTimeout bounds one model call.
const httpTimeout = 5 * time.Minute

// systemText joins a request's system instruction.
func systemText(cfg *genai.GenerateContentConfig) string {
	if cfg == nil || cfg.SystemInstruction == nil {
		return ""
	}
	var parts []string
	for _, p := range cfg.SystemInstruction.Parts {
		if p != nil && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// toolDecl is a function tool in plain JSON schema.
type toolDecl struct {
	Name        string
	Description string
	Schema      map[string]any
}

// declarations returns the request's function tools, and whether it asks
// for web search.
func declarations(cfg *genai.GenerateContentConfig) ([]toolDecl, bool) {
	if cfg == nil {
		return nil, false
	}
	var out []toolDecl
	search := false
	for _, t := range cfg.Tools {
		if t == nil {
			continue
		}
		if t.GoogleSearch != nil || t.GoogleSearchRetrieval != nil {
			search = true
		}
		for _, d := range t.FunctionDeclarations {
			if d == nil {
				continue
			}
			out = append(out, toolDecl{Name: d.Name, Description: d.Description, Schema: declSchema(d)})
		}
	}
	return out, search
}

func declSchema(d *genai.FunctionDeclaration) map[string]any {
	var s map[string]any
	switch {
	case d.ParametersJsonSchema != nil:
		s = toMap(d.ParametersJsonSchema)
	case d.Parameters != nil:
		s = SchemaJSON(d.Parameters)
	}
	if s == nil {
		s = map[string]any{}
	}
	if s["type"] == nil {
		s["type"] = "object"
	}
	if s["type"] == "object" && s["properties"] == nil {
		s["properties"] = map[string]any{}
	}
	return s
}

func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// SchemaJSON turns a Gemini schema into JSON schema.
func SchemaJSON(s *genai.Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{}
	if s.Type != "" {
		m["type"] = strings.ToLower(string(s.Type))
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Format != "" {
		m["format"] = s.Format
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for k, v := range s.Properties {
			props[k] = SchemaJSON(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Items != nil {
		m["items"] = SchemaJSON(s.Items)
	}
	if len(s.AnyOf) > 0 {
		var any []any
		for _, a := range s.AnyOf {
			any = append(any, SchemaJSON(a))
		}
		m["anyOf"] = any
	}
	return m
}

// strictObjects closes every object in a schema, as structured output
// requires, and drops the constraints it doesn't support.
func strictObjects(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			switch k {
			case "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems", "multipleOf", "pattern":
				continue
			}
			out[k] = strictObjects(x)
		}
		if out["type"] == "object" {
			out["additionalProperties"] = false
			if props, ok := out["properties"].(map[string]any); ok {
				var req []any
				for k := range props {
					req = append(req, k)
				}
				out["required"] = req
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = strictObjects(x)
		}
		return out
	}
	return v
}

// wantsJSON reports whether the request asks for a JSON reply, with its
// schema if it gives one.
func wantsJSON(cfg *genai.GenerateContentConfig) (bool, map[string]any) {
	if cfg == nil || cfg.ResponseMIMEType != "application/json" {
		return false, nil
	}
	if cfg.ResponseJsonSchema != nil {
		return true, toMap(cfg.ResponseJsonSchema)
	}
	return true, SchemaJSON(cfg.ResponseSchema)
}

func maxTokens(cfg *genai.GenerateContentConfig) int {
	if cfg != nil && cfg.MaxOutputTokens > 0 {
		return int(cfg.MaxOutputTokens)
	}
	return defaultMaxTokens
}

// resultText is a function response as the text a tool result carries.
func resultText(r *genai.FunctionResponse) string {
	if r == nil {
		return ""
	}
	b, err := json.Marshal(r.Response)
	if err != nil {
		return fmt.Sprint(r.Response)
	}
	return string(b)
}

// APIError is an error answer from a provider.
type APIError struct {
	Provider string
	Status   int
	Message  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s answered %d: %s", e.Provider, e.Status, e.Message)
}

// post sends a JSON request and decodes the JSON answer. Error answers
// become APIError with the provider's own message, which never contains
// the key.
func post(ctx context.Context, c *http.Client, provider, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = e.Message
		}
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
			if len(msg) > 300 {
				msg = msg[:300]
			}
		}
		return &APIError{Provider: provider, Status: resp.StatusCode, Message: msg}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s sent an answer that isn't JSON: %w", provider, err)
	}
	return nil
}

func tokenUse(in, out int) *genai.GenerateContentResponseUsageMetadata {
	return &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: int32(in), CandidatesTokenCount: int32(out), TotalTokenCount: int32(in + out)}
}

func newID(prefix string, n int) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), n)
}
