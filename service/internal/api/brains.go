package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/config"
	"heyrobots/service/internal/models"
)

// probeTimeout bounds asking this machine what can think.
const probeTimeout = 2 * time.Second

// toolModels are local model families that call tools well enough.
var toolModels = []string{"qwen3", "qwen2.5", "llama3.1", "llama3.2", "mistral"}

// keyBrain is a provider that needs only a key, with its model per role.
type keyBrain struct {
	id, name, provider, baseURL, work, quick string
}

var keyBrains = []keyBrain{
	{"deepseek", "DeepSeek", models.OpenAI, "https://api.deepseek.com", "deepseek-chat", "deepseek-chat"},
	{"anthropic", "Claude (API key)", models.Anthropic, "", "claude-opus-5-5", "claude-haiku-5-5"},
	{"gemini", "Gemini", models.Gemini, "", config.DefaultWorkerModel, config.DefaultRouterModel},
	{"openai", "OpenAI", models.OpenAI, "https://api.openai.com/v1", "gpt-5", "gpt-5-mini"},
}

// ListBrains shows what can think for the assistant: what runs on this
// machine and the providers a key unlocks. Owner devices only.
func (s *Server) ListBrains(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.BrainList{Work: []gen.Brain{}, Quick: []gen.Brain{}}
	if b, ok := s.claudeCodeBrain(r.Context()); ok {
		// Your agent can do the work and route it, so no key is needed.
		out.Work, out.Quick = append(out.Work, b), append(out.Quick, b)
	}
	for _, b := range s.localBrains(r.Context()) {
		out.Work, out.Quick = append(out.Work, b), append(out.Quick, b)
	}
	for _, k := range keyBrains {
		_, hasKey := s.opts.Vault.Get(models.KeyName(k.provider, k.baseURL))
		detail := "Needs an API key"
		if hasKey {
			detail = "Key saved"
		}
		b := gen.Brain{Id: k.id, Name: k.name, Detail: detail, Available: true, NeedsKey: !hasKey, Provider: gen.BrainProvider(k.provider)}
		if k.baseURL != "" {
			b.BaseUrl = &k.baseURL
		}
		b.Model = k.work
		out.Work = append(out.Work, b)
		b.Model = k.quick
		out.Quick = append(out.Quick, b)
	}
	work, workErr := s.opts.Store.ModelRoleFor(models.RoleWorker)
	quick, quickErr := s.opts.Store.ModelRoleFor(models.RoleRouter)
	keyed := map[string]bool{}
	for _, k := range keyBrains {
		keyed[k.id] = true
	}
	// A key brain is the account, whatever model it was saved with; a local
	// brain is one model among several on the same server.
	same := func(b gen.Brain, provider, model, baseURL string) bool {
		u := ""
		if b.BaseUrl != nil {
			u = *b.BaseUrl
		}
		return string(b.Provider) == provider && (keyed[b.Id] || b.Model == model) && models.NormalizeURL(u) == models.NormalizeURL(baseURL)
	}
	for i, b := range out.Work {
		out.Work[i].Selected = workErr == nil && same(b, work.Provider, work.Model, work.BaseURL)
	}
	for i, b := range out.Quick {
		out.Quick[i].Selected = quickErr == nil && same(b, quick.Provider, quick.Model, quick.BaseURL)
	}
	// A saved role passed its test before it was saved.
	out.Chosen = workErr == nil && quickErr == nil
	writeJSON(w, http.StatusOK, out)
}

// claudeCodeBrain reports Claude Code when it is on this machine. It can
// be chosen once the owner has signed in for heyrobots.
func (s *Server) claudeCodeBrain(ctx context.Context) (gen.Brain, bool) {
	own := s.opts.ACP != nil && len(s.opts.ACP.Command) > 0
	if _, err := s.opts.Probe.LookPath("claude"); err != nil && !own {
		return gen.Brain{}, false
	}
	b := gen.Brain{Id: "claude_code", Name: "Your agent", Provider: gen.BrainProviderClaudeCode, Model: "claude"}
	switch ok, err := s.signedIn(ctx); {
	case s.opts.ACP == nil:
		b.Detail = "coming · connect any ACP agent"
	case err != nil:
		b.Detail = "Couldn't ask your agent: " + err.Error()
	case !ok:
		b.Detail = "Sign in first: run " + s.opts.ACP.LoginCommand() + " in a terminal"
	case own:
		b.Detail = "ACP · " + strings.Join(s.opts.ACP.Command, " ")
		b.Available = true
	default:
		b.Detail = "ACP · Claude Code on your subscription"
		b.Available = true
	}
	return b, true
}

func (s *Server) signedIn(ctx context.Context) (bool, error) {
	if s.opts.ACP == nil {
		return false, nil
	}
	if s.opts.Probe.SignedIn != nil {
		return s.opts.Probe.SignedIn(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return s.opts.ACP.SignedIn(ctx)
}

// localBrains lists tool-capable models in Ollama and LM Studio on their
// default ports, qwen3:8b first.
func (s *Server) localBrains(ctx context.Context) []gen.Brain {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	get := func(u string, v any) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(v) == nil
	}
	usable := func(name string) bool {
		return slices.ContainsFunc(toolModels, func(f string) bool { return strings.Contains(strings.ToLower(name), f) })
	}
	var out []gen.Brain
	add := func(id, app, base, model string) {
		if usable(model) {
			out = append(out, gen.Brain{Id: id + ":" + model, Name: model, Detail: "On this machine · " + app, Available: true,
				Provider: gen.BrainProviderOpenai, Model: model, BaseUrl: &base})
		}
	}
	var tags struct{ Models []struct{ Name string } }
	if get(s.opts.Probe.OllamaURL+"/api/tags", &tags) {
		for _, m := range tags.Models {
			add("ollama", "Ollama", s.opts.Probe.OllamaURL+"/v1", m.Name)
		}
	}
	var lm struct{ Data []struct{ ID string } }
	if get(s.opts.Probe.LMStudioURL+"/v1/models", &lm) {
		for _, m := range lm.Data {
			add("lmstudio", "LM Studio", s.opts.Probe.LMStudioURL+"/v1", m.ID)
		}
	}
	slices.SortStableFunc(out, func(a, b gen.Brain) int {
		rank := func(b gen.Brain) int {
			if b.Model == "qwen3:8b" {
				return 0
			}
			return 1
		}
		return rank(a) - rank(b)
	})
	return out
}
