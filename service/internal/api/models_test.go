package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/models"
)

func newModelsHarness(t *testing.T, check func(models.Choice, string) error) *harness {
	t.Helper()
	return newHarnessWith(t, nil, func(h *harness, o *Options) {
		o.Models = &models.Resolver{Store: h.store, Vault: h.vault, Defaults: map[string]models.Choice{
			models.RoleRouter: {Provider: models.Gemini, Model: "gemini-small"},
			models.RoleReader: {Provider: models.Gemini, Model: "gemini-small"},
			models.RoleWorker: {Provider: models.Gemini, Model: "gemini-big"},
		}}
		o.CheckModel = func(_ context.Context, c models.Choice, key string) error { return check(c, key) }
	})
}

func TestChoosingAModelTriesItFirst(t *testing.T) {
	var tried []string
	h := newModelsHarness(t, func(c models.Choice, key string) error {
		tried = append(tried, c.Model+":"+key)
		if c.Model == "chatty" {
			return errors.New("the model didn't call a tool")
		}
		if key == "wrong" {
			return &models.APIError{Provider: "Anthropic", Status: 401, Message: "invalid x-api-key"}
		}
		return nil
	})
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	rec := h.do("GET", "/api/v1/models", owner, nil, "")
	var list gen.ModelList
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Roles) != 3 || list.Roles[2].Model != "gemini-big" || !list.Roles[2].IsDefault || list.Roles[2].HasKey {
		t.Fatalf("list = %+v", list)
	}
	if c := h.do("GET", "/api/v1/models", sam, nil, "").Code; c != http.StatusForbidden {
		t.Fatalf("delegate = %d", c)
	}

	put := func(role string, v gen.ModelRoleUpdate) (int, string) {
		rec := h.putJSON("/api/v1/models/"+role, owner, v)
		return rec.Code, rec.Body.String()
	}
	if c, body := put("worker", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderAnthropic, Model: "claude-opus-5-5"}); c != http.StatusBadRequest || !strings.Contains(body, "API key") {
		t.Fatalf("no key = %d %s", c, body)
	}
	if c, body := put("worker", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderAnthropic, Model: "claude-opus-5-5", Key: ptr("wrong")}); c != http.StatusBadRequest || !strings.Contains(body, "key was refused") {
		t.Fatalf("wrong key = %d %s", c, body)
	}
	if _, ok := h.vault.Get(models.KeyName(models.Anthropic, "")); ok {
		t.Fatal("a refused key was saved")
	}
	c, body := put("worker", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderAnthropic, Model: "claude-opus-5-5", Key: ptr("sk-good")})
	if c != http.StatusOK {
		t.Fatalf("set = %d %s", c, body)
	}
	if strings.Contains(body, "sk-good") {
		t.Fatal("the key was returned")
	}
	// The saved key is reused for the next role on the same provider.
	if c, body := put("router", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderAnthropic, Model: "claude-haiku-5-5"}); c != http.StatusOK {
		t.Fatalf("router = %d %s", c, body)
	}
	if tried[len(tried)-1] != "claude-haiku-5-5:sk-good" {
		t.Fatalf("tried = %v", tried)
	}
	// A local model needs no key and costs nothing.
	local := "http://localhost:11434/v1"
	if c, body := put("reader", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderOpenai, Model: "llama3.2", BaseUrl: &local}); c != http.StatusOK {
		t.Fatalf("local = %d %s", c, body)
	}
	if p, ok := h.srv.opts.Models.Price("llama3.2"); !ok || p.In != 0 {
		t.Fatalf("local price = %+v %v", p, ok)
	}
	// A model that can't call tools is refused.
	if c, body := put("reader", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderOpenai, Model: "chatty", BaseUrl: &local}); c != http.StatusBadRequest || !strings.Contains(body, "didn't pass") {
		t.Fatalf("chatty = %d %s", c, body)
	}
	// Plain http only reaches the owner's own network.
	far := "http://api.example.com/v1"
	if c, _ := put("reader", gen.ModelRoleUpdate{Provider: gen.ModelRoleUpdateProviderOpenai, Model: "x", BaseUrl: &far, Key: ptr("k")}); c != http.StatusBadRequest {
		t.Fatalf("plain http = %d", c)
	}

	json.Unmarshal(h.do("GET", "/api/v1/models", owner, nil, "").Body.Bytes(), &list)
	got := map[string]string{}
	for _, r := range list.Roles {
		got[string(r.Role)] = string(r.Provider) + "/" + r.Model
	}
	if got["worker"] != "anthropic/claude-opus-5-5" || got["router"] != "anthropic/claude-haiku-5-5" || got["reader"] != "openai/llama3.2" {
		t.Fatalf("roles = %v", got)
	}
}
