package models

import (
	"context"
	"errors"
	"iter"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ErrClaudeCodeTools says a call needs function tools, which a one-shot
// call to the owner's Claude Code can't take.
var ErrClaudeCodeTools = errors.New("Claude Code does the work itself and can't answer calls with tools; jobs that call tools need a model")

// ClaudeCodeModel answers one-shot calls, such as the quarantined reader's
// and the checks' judge, through the owner's own Claude Code: a fresh
// session with no tools, or with web search only for a search.
type ClaudeCodeModel struct {
	Ask    func(ctx context.Context, prompt string) (string, error)
	Search func(ctx context.Context, query string) (string, error)
}

// Name names the model for metering.
func (ClaudeCodeModel) Name() string { return "claude-code" }

// GenerateContent sends the request as one prompt and returns the answer.
func (m ClaudeCodeModel) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		tools, search := declarations(req.Config)
		if len(tools) > 0 || len(req.Tools) > 0 {
			yield(nil, ErrClaudeCodeTools)
			return
		}
		var b strings.Builder
		if req.Config != nil && req.Config.SystemInstruction != nil {
			for _, p := range req.Config.SystemInstruction.Parts {
				b.WriteString(p.Text + "\n\n")
			}
		}
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				b.WriteString(p.Text + "\n")
			}
		}
		json := req.Config != nil && req.Config.ResponseMIMEType == "application/json"
		ask, prompt := m.Ask, b.String()
		switch {
		case search:
			ask, prompt = m.Search, prompt+"\nSearch the web with your web search tool, then answer. Use no other tool."
		case json:
			prompt += "\nReply with the JSON only. Don't use any tools."
		default:
			prompt += "\nReply with the answer only. Don't use any tools."
		}
		if ask == nil {
			yield(nil, errors.New("Claude Code isn't set up on this server"))
			return
		}
		text, err := ask(ctx, prompt)
		if err != nil {
			yield(nil, err)
			return
		}
		if json {
			// Claude Code often fences its JSON; the callers parse it bare.
			if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
				text = text[i : j+1]
			}
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel), TurnComplete: true}, nil)
	}
}
