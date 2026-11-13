package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestListReadsEachProvidersModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") == "ak":
			w.Write([]byte(`{"data":[{"id":"claude-sonnet-5-5"},{"id":"claude-haiku-5-5"}]}`))
		case r.URL.Path == "/models" && r.Header.Get("Authorization") == "Bearer dk":
			w.Write([]byte(`{"data":[{"id":"deepseek-reasoner"},{"id":"deepseek-chat"}]}`))
		case r.URL.Path == "/v1beta/models" && r.URL.Query().Get("key") == "gk":
			w.Write([]byte(`{"models":[{"name":"models/gemini-3.5-flash","supportedGenerationMethods":["generateContent"]},{"name":"models/text-embedding","supportedGenerationMethods":["embedContent"]}]}`))
		default:
			http.Error(w, "no", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	AnthropicModelsURL, GeminiModelsURL = srv.URL+"/v1/models?limit=1000", srv.URL+"/v1beta/models?pageSize=1000"
	ctx := context.Background()
	for _, tc := range []struct {
		c    Choice
		key  string
		want []string
	}{
		{Choice{Provider: Anthropic}, "ak", []string{"claude-haiku-5-5", "claude-sonnet-5-5"}},
		{Choice{Provider: OpenAI, BaseURL: srv.URL}, "dk", []string{"deepseek-chat", "deepseek-reasoner"}},
		{Choice{Provider: Gemini}, "gk", []string{"gemini-3.5-flash"}},
		{Choice{Provider: ClaudeCode}, "", []string{}},
	} {
		got, err := List(ctx, tc.c, tc.key, srv.Client())
		if err != nil || !slices.Equal(got, tc.want) {
			t.Fatalf("%s: %v, %v; want %v", tc.c.Provider, got, err, tc.want)
		}
	}
	if _, err := List(ctx, Choice{Provider: OpenAI, BaseURL: srv.URL}, "wrong", srv.Client()); err == nil {
		t.Fatal("a refused key listed models")
	}
}
