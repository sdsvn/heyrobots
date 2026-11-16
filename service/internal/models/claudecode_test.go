package models

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"heyrobots/service/internal/store"
)

func generate(t *testing.T, m model.LLM, req *model.LLMRequest) (string, error) {
	t.Helper()
	var b strings.Builder
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			return "", err
		}
		for _, p := range resp.Content.Parts {
			b.WriteString(p.Text)
		}
	}
	return b.String(), nil
}

func TestClaudeCodeAnswersTheReadersCalls(t *testing.T) {
	var asked, searched string
	m := ClaudeCodeModel{
		Ask: func(_ context.Context, p string) (string, error) {
			asked = p
			return "Here it is:\n```json\n{\"answer\": \"42\"}\n```", nil
		},
		Search: func(_ context.Context, q string) (string, error) { searched = q; return "It rains in Oslo.", nil },
	}
	read := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("<document>the answer is 42</document>", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{SystemInstruction: genai.NewContentFromText("Extract.", genai.RoleUser),
			ResponseMIMEType: "application/json"},
	}
	got, err := generate(t, m, read)
	if err != nil || got != `{"answer": "42"}` {
		t.Fatalf("read = %q, %v; fenced JSON should come back bare", got, err)
	}
	if !strings.Contains(asked, "Extract.") || !strings.Contains(asked, "the answer is 42") || searched != "" {
		t.Fatalf("asked %q, searched %q", asked, searched)
	}

	asked = ""
	search := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("weather in Oslo", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}},
	}
	if got, err := generate(t, m, search); err != nil || got != "It rains in Oslo." || asked != "" || !strings.Contains(searched, "weather in Oslo") {
		t.Fatalf("search = %q, %v; asked %q, searched %q", got, err, asked, searched)
	}

	tools := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("send it", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "send_mail"}}}}},
	}
	if _, err := generate(t, m, tools); !errors.Is(err, ErrClaudeCodeTools) {
		t.Fatalf("a call with function tools: err = %v", err)
	}
}

func TestARoleOnClaudeCodeGetsItsModel(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SaveModelRole(store.ModelRole{Role: RoleReader, Provider: ClaudeCode, Model: "claude", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{Store: st, Vault: noKeys{}}
	if _, err := r.Model(RoleReader)(context.Background()); err == nil {
		t.Fatal("without Claude Code set up there is no model")
	}
	cc := ClaudeCodeModel{Ask: func(context.Context, string) (string, error) { return "ok", nil }}
	r.ClaudeCode = cc
	m, err := r.Model(RoleReader)(context.Background())
	if err != nil || m.Name() != "claude-code" {
		t.Fatalf("model = %v, %v", m, err)
	}
}

type noKeys struct{}

func (noKeys) Get(string) (string, bool) { return "", false }
