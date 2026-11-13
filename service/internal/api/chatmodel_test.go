package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/models"
	"heyrobots/service/internal/store"
)

// A chat lists its provider's models and switches between them: an
// agent's chat changes only that agent, any other chat the model doing the
// work.
func TestAChatSwitchesModelWithinItsProvider(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
	}))
	defer provider.Close()
	h := newModelsHarness(t, func(models.Choice, string) error { return nil })
	owner := h.completeSetup()
	if c := h.putJSON("/api/v1/models/worker", owner, gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderOpenai, Model: "deepseek-chat", BaseUrl: &provider.URL, Key: ptr("dk")}).Code; c != http.StatusOK {
		t.Fatalf("set worker = %d", c)
	}
	var v gen.ChatModel
	json.Unmarshal(h.do("GET", "/api/v1/chat-model", owner, nil, "").Body.Bytes(), &v)
	if v.Model != "deepseek-chat" || v.Scope != gen.ChatModelScopeDefault || !slices.Equal(v.Models, []string{"deepseek-chat", "deepseek-reasoner"}) {
		t.Fatalf("default chat = %+v", v)
	}

	if err := h.store.SaveAgent(store.Agent{ID: "ag_writer", Name: "Writer", Capacity: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rec := h.putJSON("/api/v1/chat-model", owner, gen.ChatModelUpdate{AgentId: ptr("ag_writer"), Model: "deepseek-reasoner"})
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != http.StatusOK || v.Model != "deepseek-reasoner" || v.Scope != gen.ChatModelScopeAgent {
		t.Fatalf("agent switch = %d %+v", rec.Code, v)
	}
	a, _ := h.store.Agent("ag_writer")
	if a.Model != "deepseek-reasoner" || a.BaseURL != models.NormalizeURL(provider.URL) {
		t.Fatalf("agent = %+v", a)
	}
	json.Unmarshal(h.do("GET", "/api/v1/chat-model", owner, nil, "").Body.Bytes(), &v)
	if v.Model != "deepseek-chat" {
		t.Fatalf("the default changed too: %+v", v)
	}
}
