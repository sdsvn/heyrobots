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
)

// chatChoice is the model a chat uses: the agent's own when it has one,
// else the one doing the work.
func (s *Server) chatChoice(agentID string) (models.Choice, gen.ChatModelScope, store.Agent, error) {
	var a store.Agent
	if agentID != "" {
		var err error
		if a, err = s.opts.Store.Agent(agentID); err != nil {
			return models.Choice{}, "", a, err
		}
		if a.Home != "" {
			return models.Choice{}, "", a, errors.New("this agent runs on another machine; change its model there")
		}
		if a.Model != "" {
			return models.Choice{Provider: a.Provider, Model: a.Model, BaseURL: a.BaseURL}, gen.ChatModelScopeAgent, a, nil
		}
	}
	if s.opts.Models == nil {
		return models.Choice{}, "", a, errors.New("models aren't configured on this server")
	}
	c, err := s.opts.Models.Choice(models.RoleWorker)
	return c, gen.ChatModelScopeDefault, a, err
}

func (s *Server) chatModelView(ctx context.Context, c models.Choice, scope gen.ChatModelScope) gen.ChatModel {
	v := gen.ChatModel{Provider: c.Provider, Model: c.Model, Scope: scope, Models: []string{}}
	if c.BaseURL != "" {
		v.BaseUrl = &c.BaseURL
	}
	if c.Provider == models.ClaudeCode {
		note := "Your agent picks its own model"
		v.Note = &note
		return v
	}
	key, _ := s.opts.Vault.Get(models.KeyName(c.Provider, c.BaseURL))
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := models.List(lctx, c, key, nil)
	if err != nil {
		note := "Couldn't ask " + c.Provider + " for its models: " + shorten(err.Error(), 120)
		v.Note = &note
	}
	if !slices.Contains(list, c.Model) {
		list = append([]string{c.Model}, list...)
	}
	v.Models = list
	return v
}

// GetChatModel shows a chat's model and the others its provider offers.
// Owner devices only.
func (s *Server) GetChatModel(w http.ResponseWriter, r *http.Request, params gen.GetChatModelParams) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	id := ""
	if params.AgentId != nil {
		id = *params.AgentId
	}
	c, scope, _, err := s.chatChoice(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such agent")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.chatModelView(r.Context(), c, scope))
}

// SetChatModel switches a chat to another model of the same provider: the
// agent's own model in its chat, else the model doing the work. Owner
// devices only.
func (s *Server) SetChatModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.ChatModelUpdate
	if !decode(w, r, &req) {
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		writeError(w, http.StatusBadRequest, "pick a model")
		return
	}
	id := ""
	if req.AgentId != nil {
		id = *req.AgentId
	}
	c, scope, a, err := s.chatChoice(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such agent")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.Provider == models.ClaudeCode {
		writeError(w, http.StatusBadRequest, "your agent picks its own model")
		return
	}
	c.Model = model
	if id != "" {
		// Only this agent changes, on the provider it already uses.
		a.Provider, a.Model, a.BaseURL, a.UpdatedAt = c.Provider, c.Model, c.BaseURL, s.opts.Now().UTC()
		if err := s.opts.Store.SaveAgent(a); err != nil {
			s.internal(w, "save agent model", err)
			return
		}
		scope = gen.ChatModelScopeAgent
	} else if msg := s.saveModel(r.Context(), []string{models.RoleWorker}, c, ""); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.opts.Log.Info("chat model switched", "agent", id, "provider", c.Provider, "model", c.Model)
	writeJSON(w, http.StatusOK, s.chatModelView(r.Context(), c, scope))
}
