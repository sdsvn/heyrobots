package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"

	"heyrobots/service/internal/store"
	"heyrobots/service/internal/usage"
)

// Choice is a provider, model and endpoint for one role.
type Choice struct {
	Provider string
	Model    string
	// BaseURL is an OpenAI-compatible endpoint's address.
	BaseURL string
	// Price is what the owner says the model costs, if they said.
	Price    usage.Price
	HasPrice bool
}

// Vault is where model keys live.
type Vault interface {
	Get(name string) (string, bool)
}

// GeminiKey is the vault entry of the Gemini key, the one setup always
// asked for.
const GeminiKey = "model_api_key"

// KeyName is the vault entry of a provider's key. An OpenAI-compatible
// endpoint's key belongs to its address, so DeepSeek's and OpenRouter's
// keys stay apart.
func KeyName(provider, baseURL string) string {
	switch provider {
	case Gemini:
		return GeminiKey
	case OpenAI:
		return "model_key:openai:" + NormalizeURL(baseURL)
	}
	return "model_key:" + provider
}

// NormalizeURL trims an endpoint address to compare it.
func NormalizeURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// IsLocal reports whether an endpoint is on this machine or the owner's own
// network, where calls cost nothing and may use plain http.
func IsLocal(baseURL string) bool {
	u, err := url.Parse(NormalizeURL(baseURL))
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// CheckChoice checks a choice's fields before it is tried.
func CheckChoice(c Choice) error {
	if strings.TrimSpace(c.Model) == "" || len(c.Model) > 200 {
		return errors.New("name the model")
	}
	switch c.Provider {
	case Gemini, Anthropic, ClaudeCode:
		if c.BaseURL != "" {
			return errors.New("only an OpenAI-compatible endpoint takes an address")
		}
	case OpenAI:
		u, err := url.Parse(NormalizeURL(c.BaseURL))
		if err != nil || u.Host == "" {
			return errors.New("give the endpoint's address, such as https://api.deepseek.com")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && IsLocal(c.BaseURL)) {
			return errors.New("use https, or http only for a model on your own machine or network")
		}
	default:
		return errors.New("provider must be gemini, anthropic, openai or claude_code")
	}
	if c.HasPrice && (c.Price.In < 0 || c.Price.Out < 0) {
		return errors.New("prices can't be negative")
	}
	return nil
}

// Build makes a model for a choice with its key.
func Build(ctx context.Context, c Choice, key string, hc *http.Client) (model.LLM, error) {
	switch c.Provider {
	case Gemini:
		if key == "" {
			return nil, errors.New("no Gemini API key is saved")
		}
		return gemini.NewModel(ctx, c.Model, &genai.ClientConfig{APIKey: key, Backend: genai.BackendGeminiAPI})
	case Anthropic:
		if key == "" {
			return nil, errors.New("no Anthropic API key is saved")
		}
		return NewAnthropic(c.Model, key, "", hc), nil
	case OpenAI:
		if key == "" && !IsLocal(c.BaseURL) {
			return nil, errors.New("no key is saved for " + NormalizeURL(c.BaseURL))
		}
		return NewOpenAI(c.Model, key, NormalizeURL(c.BaseURL), hc), nil
	case ClaudeCode:
		return nil, errors.New("Claude Code does the work itself and can't answer model calls; jobs and quick calls need a model")
	}
	return nil, fmt.Errorf("unknown provider %q", c.Provider)
}

// Resolver gives each role its model, as the owner set it, or the
// configured Gemini model when they haven't. It reads the choice on every
// call, so a change applies to the next task without a restart.
type Resolver struct {
	Store *store.Store
	Vault Vault
	// Defaults are used for roles the owner hasn't set.
	Defaults map[string]Choice
	HTTP     *http.Client

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	sig string
	llm model.LLM
}

// Choice returns a role's choice.
func (r *Resolver) Choice(role string) (Choice, error) {
	row, err := r.Store.ModelRoleFor(role)
	if errors.Is(err, store.ErrNotFound) {
		return r.Defaults[role], nil
	}
	if err != nil {
		return Choice{}, err
	}
	return Choice{Provider: row.Provider, Model: row.Model, BaseURL: row.BaseURL,
		Price: usage.Price{In: row.PriceIn, Out: row.PriceOut}, HasPrice: row.HasPrice}, nil
}

// Model returns the function that gives a role its model.
func (r *Resolver) Model(role string) func(ctx context.Context) (model.LLM, error) {
	return func(ctx context.Context) (model.LLM, error) {
		c, err := r.Choice(role)
		if err != nil {
			return nil, err
		}
		key, _ := r.Vault.Get(KeyName(c.Provider, c.BaseURL))
		if key == "" && c.Provider != OpenAI && c.Provider != ClaudeCode {
			return nil, fmt.Errorf("no model API key is saved; finish setup first")
		}
		sig := c.Provider + "\x00" + c.Model + "\x00" + c.BaseURL + "\x00" + key
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.cache == nil {
			r.cache = map[string]cached{}
		}
		if hit, ok := r.cache[role]; ok && hit.sig == sig {
			return hit.llm, nil
		}
		m, err := Build(ctx, c, key, r.HTTP)
		if err != nil {
			return nil, err
		}
		r.cache[role] = cached{sig: sig, llm: m}
		return m, nil
	}
}

// Price is the ledger's override: a price the owner set for a model in
// use, or nothing for a model on their own machine or network.
func (r *Resolver) Price(name string) (usage.Price, bool) {
	for _, role := range Roles {
		c, err := r.Choice(role)
		if err != nil || c.Model != name {
			continue
		}
		if c.HasPrice {
			return c.Price, true
		}
		if c.Provider == OpenAI && IsLocal(c.BaseURL) {
			return usage.Price{}, true
		}
	}
	return usage.Price{}, false
}

// Check makes two small calls, checking that the model can call a tool
// and can answer in JSON. Every role needs one or the other, so a model
// that fails is refused before it is saved.
func Check(ctx context.Context, m model.LLM) error {
	// A tool call.
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("Call record_number with the number 42.", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText("You are being tested. Use the record_number tool when asked. Don't reply with text.", genai.RoleUser),
			MaxOutputTokens:   2000,
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "record_number",
				Description: "Records a number.",
				Parameters: &genai.Schema{Type: genai.TypeObject, Required: []string{"number"},
					Properties: map[string]*genai.Schema{"number": {Type: genai.TypeInteger}}},
			}}}},
		},
	}
	called := false
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil {
			return fmt.Errorf("the test call failed: %w", err)
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			if p.FunctionCall != nil && p.FunctionCall.Name == "record_number" {
				if n, ok := p.FunctionCall.Args["number"].(float64); ok && n == 42 {
					called = true
				}
			}
		}
	}
	if !called {
		return errors.New("the model didn't call a tool when asked to, so it can't do heyrobots' work; choose a model that supports function calling")
	}
	// A JSON answer.
	req = &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("What is the capital of France? Put it in answer.", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{
			MaxOutputTokens:  2000,
			ResponseMIMEType: "application/json",
			ResponseSchema:   &genai.Schema{Type: genai.TypeObject, Required: []string{"answer"}, Properties: map[string]*genai.Schema{"answer": {Type: genai.TypeString}}},
		},
	}
	var text strings.Builder
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil {
			return fmt.Errorf("the test call failed: %w", err)
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if !p.Thought {
					text.WriteString(p.Text)
				}
			}
		}
	}
	var out struct {
		Answer string `json:"answer"`
	}
	raw := strings.TrimSpace(text.String())
	raw = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```"), "```")
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &out) != nil || out.Answer == "" {
		return errors.New("the model didn't answer in JSON when asked to, so it can't read for the quarantine; choose a model that supports JSON output")
	}
	return nil
}

// ForChoice builds a model for a choice outside the roles, such as an
// agent's own model, with the key saved for its provider.
func (r *Resolver) ForChoice(ctx context.Context, c Choice) (model.LLM, error) {
	key, _ := r.Vault.Get(KeyName(c.Provider, c.BaseURL))
	return Build(ctx, c, key, r.HTTP)
}
