package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/models"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/usage"
)

// checkTimeout bounds the test calls before a model is saved.
const checkTimeout = 90 * time.Second

// checkModel tries a choice with two small calls before it is saved.
func (s *Server) checkModel(ctx context.Context, c models.Choice, key string) error {
	if s.opts.CheckModel != nil {
		return s.opts.CheckModel(ctx, c, key)
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	if c.Provider == models.ClaudeCode {
		if s.opts.ACP == nil {
			return errors.New("Claude Code isn't set up on this server")
		}
		return s.opts.ACP.Check(ctx)
	}
	m, err := models.Build(ctx, c, key, nil)
	if err != nil {
		return err
	}
	return models.Check(ctx, m)
}

// ListModels shows the model of each role. Owner devices only.
func (s *Server) ListModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Models == nil {
		writeError(w, http.StatusServiceUnavailable, "models aren't configured on this server")
		return
	}
	out := gen.ModelList{Roles: []gen.ModelRole{}}
	for _, role := range models.Roles {
		v, err := s.modelView(role)
		if err != nil {
			s.internal(w, "load model choice", err)
			return
		}
		out.Roles = append(out.Roles, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) modelView(role string) (gen.ModelRole, error) {
	c, err := s.opts.Models.Choice(role)
	if err != nil {
		return gen.ModelRole{}, err
	}
	_, custom := s.opts.Store.ModelRoleFor(role)
	_, hasKey := s.opts.Vault.Get(models.KeyName(c.Provider, c.BaseURL))
	v := gen.ModelRole{
		Role: gen.ModelRoleRole(role), Provider: gen.ModelRoleProvider(c.Provider), Model: c.Model,
		HasKey: hasKey, Local: c.Provider == models.OpenAI && models.IsLocal(c.BaseURL), IsDefault: custom != nil,
	}
	if c.BaseURL != "" {
		v.BaseUrl = &c.BaseURL
	}
	if c.HasPrice {
		v.PriceIn, v.PriceOut = &c.Price.In, &c.Price.Out
	}
	return v, nil
}

// SetModel tries a model for a role and saves it if it works. Owner
// devices only.
func (s *Server) SetModel(w http.ResponseWriter, r *http.Request, role string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Models == nil {
		writeError(w, http.StatusServiceUnavailable, "models aren't configured on this server")
		return
	}
	if !slices.Contains(models.Roles, role) {
		writeError(w, http.StatusNotFound, "no such role")
		return
	}
	var req gen.ModelRoleUpdate
	if !decode(w, r, &req) {
		return
	}
	c := models.Choice{Provider: string(req.Provider), Model: strings.TrimSpace(req.Model)}
	if req.BaseUrl != nil {
		c.BaseURL = models.NormalizeURL(*req.BaseUrl)
	}
	if req.PriceIn != nil || req.PriceOut != nil {
		if req.PriceIn == nil || req.PriceOut == nil {
			writeError(w, http.StatusBadRequest, "give both prices, or neither")
			return
		}
		c.Price, c.HasPrice = usage.Price{In: *req.PriceIn, Out: *req.PriceOut}, true
	}
	key := ""
	if req.Key != nil {
		key = strings.TrimSpace(*req.Key)
	}
	if msg := s.saveModel(r.Context(), []string{role}, c, key); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	v, err := s.modelView(role)
	if err != nil {
		s.internal(w, "load model choice", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// saveModel checks a choice, tries it, and saves it for the roles, with
// its key. It returns a message for the person when it can't.
func (s *Server) saveModel(ctx context.Context, roles []string, c models.Choice, key string) string {
	if err := models.CheckChoice(c); err != nil {
		return err.Error()
	}
	keyName := models.KeyName(c.Provider, c.BaseURL)
	if key == "" {
		key, _ = s.opts.Vault.Get(keyName)
	}
	if key == "" && !keyless(c) {
		return "enter the API key"
	}
	if err := s.checkModel(ctx, c, key); err != nil {
		var api *models.APIError
		if errors.As(err, &api) && (api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden) {
			return "the key was refused: " + api.Message
		}
		return "the model didn't pass the test: " + err.Error()
	}
	if key != "" {
		if err := s.opts.Vault.Set(keyName, key); err != nil {
			s.opts.Log.Error("save model key", "err", err)
			return "the key couldn't be saved"
		}
	}
	now := s.opts.Now().UTC()
	for _, role := range roles {
		row := store.ModelRole{Role: role, Provider: c.Provider, Model: c.Model, BaseURL: c.BaseURL,
			PriceIn: c.Price.In, PriceOut: c.Price.Out, HasPrice: c.HasPrice, UpdatedAt: now}
		if err := s.opts.Store.SaveModelRole(row); err != nil {
			s.opts.Log.Error("save model choice", "err", err)
			return "the choice couldn't be saved"
		}
	}
	s.opts.Log.Info("model chosen", "roles", strings.Join(roles, ","), "provider", c.Provider, "model", c.Model)
	return ""
}

// hasModel reports whether the worker's model has what it needs to run.
func (s *Server) hasModel() bool {
	if s.opts.Models == nil {
		_, ok := s.opts.Vault.Get(models.GeminiKey)
		return ok
	}
	c, err := s.opts.Models.Choice(models.RoleWorker)
	if err != nil {
		return false
	}
	if c.Provider == models.ClaudeCode {
		return s.opts.ACP != nil
	}
	if c.Provider == models.OpenAI && models.IsLocal(c.BaseURL) {
		return true
	}
	_, ok := s.opts.Vault.Get(models.KeyName(c.Provider, c.BaseURL))
	return ok
}

// keyless says a choice needs no API key: a model on the owner's own
// machine or network, or their own Claude Code.
func keyless(c models.Choice) bool {
	return c.Provider == models.ClaudeCode || (c.Provider == models.OpenAI && models.IsLocal(c.BaseURL))
}
