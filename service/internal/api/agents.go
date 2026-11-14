package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/models"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/skills"
	"heyrobots/service/internal/store"
)

var (
	agentColors = []string{"violet", "peach", "mint", "sky", "rose", "sun"}
	agentBadges = []string{"star", "book", "pen", "mail", "calendar", "code", "search", "bolt"}
)

// ListAgents lists the team with what each agent is doing. Owner devices
// only.
func (s *Server) ListAgents(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	agents, err := s.opts.Store.Agents()
	if err != nil {
		s.internal(w, "list agents", err)
		return
	}
	loads, err := s.opts.Store.AgentLoads()
	if err != nil {
		s.internal(w, "agent loads", err)
		return
	}
	pol, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	out := gen.AgentList{Agents: []gen.Agent{}}
	for _, a := range agents {
		out.Agents = append(out.Agents, s.agentView(a, loads[a.ID], pol))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) agentView(a store.Agent, l store.AgentLoad, pol policy.Policy) gen.Agent {
	v := gen.Agent{
		Id: a.ID, Name: a.Name, Purpose: a.Purpose, Instructions: a.Instructions, Toolsets: []string{},
		AnyToolset: a.AnyToolset, Skills: a.SkillList(), Capacity: a.Capacity, Color: gen.AgentColor(a.Color),
		Badge: gen.AgentBadge(a.Badge), Builtin: a.Builtin, Enabled: a.Enabled, Delegates: []string{}, Rules: []string{},
		Working: []gen.AgentTask{}, Waiting: l.Waiting,
	}
	if a.FollowUpsOff {
		off := true
		v.FollowUpsOff = &off
	}
	if v.Skills == nil {
		v.Skills = []string{}
	}
	for _, t := range a.ToolsetList() {
		v.Toolsets = append(v.Toolsets, string(t))
	}
	if a.Model != "" {
		v.Provider, v.Model = &a.Provider, &a.Model
		if a.BaseURL != "" {
			v.BaseUrl = &a.BaseURL
		}
	}
	for _, d := range pol.Delegates {
		if slices.Contains(agentsOrGeneral(d.Agents), a.ID) {
			v.Delegates = append(v.Delegates, d.Name)
		}
	}
	for _, r := range pol.Rules {
		if slices.Contains(agentsOrGeneral(r.Agents), a.ID) {
			v.Rules = append(v.Rules, r.Name)
		}
	}
	for _, t := range l.Running {
		m, _ := t.DecodeMandate()
		v.Working = append(v.Working, gen.AgentTask{TicketId: t.ID, ThreadId: t.OriginThreadID, Scope: m.Scope})
	}
	p, since := s.presence(a, len(l.Running))
	v.Presence, v.UnavailableSince = &p, since
	if a.Home != "" {
		home := a.Home
		if s.opts.Cluster != nil {
			home = s.opts.Cluster.MemberName(a.Home)
		}
		v.Home = &home
	}
	return v
}

// agentsOrGeneral is the agents a delegate or rule may use: those it
// names, or the General agent when it names none.
func agentsOrGeneral(ids []string) []string {
	if len(ids) == 0 {
		return []string{core.GeneralAgent}
	}
	return ids
}

// CreateAgent adds an agent. Owner devices only.
func (s *Server) CreateAgent(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.AgentRequest
	if !decode(w, r, &req) {
		return
	}
	now := s.opts.Now().UTC()
	s.saveAgent(w, r, store.Agent{ID: ids.New("ag"), CreatedAt: now}, req, http.StatusCreated)
}

// UpdateAgent changes an agent. Owner devices only.
func (s *Server) UpdateAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	cur, err := s.opts.Store.Agent(agentID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such agent")
		return
	}
	if err != nil {
		s.internal(w, "load agent", err)
		return
	}
	if cur.Home != "" {
		writeError(w, http.StatusConflict, "this agent runs on another machine; change it there")
		return
	}
	var req gen.AgentRequest
	if !decode(w, r, &req) {
		return
	}
	s.saveAgent(w, r, cur, req, http.StatusOK)
}

func (s *Server) saveAgent(w http.ResponseWriter, r *http.Request, a store.Agent, req gen.AgentRequest, status int) {
	bad := func(msg string) { writeError(w, http.StatusBadRequest, msg) }
	a.Name = strings.TrimSpace(req.Name)
	if a.Name == "" || utf8.RuneCountInString(a.Name) > 40 {
		bad("an agent needs a name of up to 40 characters")
		return
	}
	a.Purpose = strings.TrimSpace(req.Purpose)
	if utf8.RuneCountInString(a.Purpose) > 300 {
		bad("say what the agent is for in up to 300 characters")
		return
	}
	a.Instructions = ""
	if req.Instructions != nil {
		a.Instructions = strings.TrimSpace(*req.Instructions)
	}
	if utf8.RuneCountInString(a.Instructions) > 4000 {
		bad("keep the instructions under 4000 characters")
		return
	}
	if req.Capacity < 1 || req.Capacity > 5 {
		bad("capacity is 1 to 5 tasks at once")
		return
	}
	a.Capacity = req.Capacity
	if !slices.Contains(agentColors, string(req.Color)) || !slices.Contains(agentBadges, string(req.Badge)) {
		bad("pick one of the colors and badges")
		return
	}
	a.Color, a.Badge, a.Enabled = string(req.Color), string(req.Badge), req.Enabled
	if a.Builtin && !a.Enabled {
		bad("the General agent stays on")
		return
	}
	a.AnyToolset = req.AnyToolset != nil && *req.AnyToolset
	if req.FollowUpsOff != nil {
		a.FollowUpsOff = *req.FollowUpsOff
	}
	var ts []core.Toolset
	for _, t := range req.Toolsets {
		if !core.Toolset(t).IsToolset() {
			bad("unknown toolset " + t)
			return
		}
		if !slices.Contains(ts, core.Toolset(t)) {
			ts = append(ts, core.Toolset(t))
		}
	}
	tb, _ := json.Marshal(ts)
	a.Toolsets = string(tb)

	var sk []string
	if len(req.Skills) > 0 {
		if s.opts.Skills == nil {
			bad("there is no skills library on this server")
			return
		}
		lib, err := s.opts.Skills.List(r.Context())
		if err != nil {
			s.internal(w, "list skills", err)
			return
		}
		for _, name := range req.Skills {
			if !slices.ContainsFunc(lib, func(x skills.Skill) bool { return x.Name == name }) {
				bad("no skill named " + name)
				return
			}
			if !slices.Contains(sk, name) {
				sk = append(sk, name)
			}
		}
	}
	sb, _ := json.Marshal(sk)
	a.Skills = string(sb)

	// An agent with its own model must pass the same test as a role's.
	a.Provider, a.Model, a.BaseURL = "", "", ""
	if req.Model != nil && strings.TrimSpace(*req.Model) != "" {
		if req.Provider == nil {
			bad("say which provider the model is from")
			return
		}
		c := models.Choice{Provider: string(*req.Provider), Model: strings.TrimSpace(*req.Model)}
		if req.BaseUrl != nil {
			c.BaseURL = models.NormalizeURL(*req.BaseUrl)
		}
		key := ""
		if req.Key != nil {
			key = strings.TrimSpace(*req.Key)
		}
		if msg := s.tryModel(r, c, key); msg != "" {
			bad(msg)
			return
		}
		a.Provider, a.Model, a.BaseURL = c.Provider, c.Model, c.BaseURL
	}

	// The name must be unique on the team.
	all, err := s.opts.Store.Agents()
	if err != nil {
		s.internal(w, "list agents", err)
		return
	}
	for _, o := range all {
		if o.ID != a.ID && strings.EqualFold(o.Name, a.Name) {
			writeError(w, http.StatusConflict, "another agent has that name")
			return
		}
	}
	a.UpdatedAt = s.opts.Now().UTC()
	if err := s.opts.Store.SaveAgent(a); err != nil {
		s.internal(w, "save agent", err)
		return
	}
	// Left out, who may use the agent stays as it was.
	cur, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	now := s.agentView(a, store.AgentLoad{}, cur)
	delegates, rules := now.Delegates, now.Rules
	if req.Delegates != nil {
		delegates = *req.Delegates
	}
	if req.Rules != nil {
		rules = *req.Rules
	}
	pol, msg, err := s.setAgentUsers(a.ID, delegates, rules)
	if err != nil {
		s.internal(w, "save policy", err)
		return
	}
	if msg != "" {
		bad(msg)
		return
	}
	loads, _ := s.opts.Store.AgentLoads()
	s.opts.Log.Info("agent saved", "agent", a.ID, "name", a.Name)
	writeJSON(w, status, s.agentView(a, loads[a.ID], pol))
}

// tryModel checks and tries a model, saving its key if it works.
func (s *Server) tryModel(r *http.Request, c models.Choice, key string) string {
	if err := models.CheckChoice(c); err != nil {
		return err.Error()
	}
	keyName := models.KeyName(c.Provider, c.BaseURL)
	if key == "" {
		key, _ = s.opts.Vault.Get(keyName)
	}
	if key == "" && !keyless(c) {
		return "enter the API key for the agent's model"
	}
	if err := s.checkModel(r.Context(), c, key); err != nil {
		return "the agent's model didn't pass the test: " + err.Error()
	}
	if key != "" {
		if err := s.opts.Vault.Set(keyName, key); err != nil {
			return "the key couldn't be saved"
		}
	}
	return ""
}

// setAgentUsers makes the agent usable by exactly the named delegates and
// rules, keeping each one's other agents. It saves a new policy version
// only if something changed.
func (s *Server) setAgentUsers(agentID string, delegates, rules []string) (policy.Policy, string, error) {
	var out policy.Policy
	var msg string
	saved := false
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		pol, err := tx.Policy()
		if err != nil {
			return err
		}
		out = pol
		for _, name := range delegates {
			if _, ok := pol.DelegateNamed(name); !ok {
				msg = "no delegate named " + name
				return nil
			}
		}
		for _, name := range rules {
			if !slices.ContainsFunc(pol.Rules, func(r policy.Rule) bool { return r.Name == name }) {
				msg = "no rule named " + name
				return nil
			}
		}
		set := func(cur []string, want bool) ([]string, bool) {
			list := slices.Clone(agentsOrGeneral(cur))
			has := slices.Contains(list, agentID)
			switch {
			case want && !has:
				return append(list, agentID), true
			case !want && has:
				return slices.DeleteFunc(list, func(x string) bool { return x == agentID }), true
			}
			return cur, false
		}
		next, changed := pol, false
		next.Delegates = slices.Clone(pol.Delegates)
		for i, d := range next.Delegates {
			if list, ch := set(d.Agents, slices.Contains(delegates, d.Name)); ch {
				next.Delegates[i].Agents, changed = list, true
			}
		}
		next.Rules = slices.Clone(pol.Rules)
		for i, r := range next.Rules {
			if list, ch := set(r.Agents, slices.Contains(rules, r.Name)); ch {
				next.Rules[i].Agents, changed = list, true
			}
		}
		if !changed {
			return nil
		}
		out, err = tx.SavePolicy(next, s.opts.Now().UTC())
		saved = err == nil
		return err
	})
	if saved {
		s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": out.Version}})
	}
	return out, msg, err
}

// DeleteAgent removes an agent with no open tasks. Owner devices only.
func (s *Server) DeleteAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if store.IsRemoteAgent(agentID) {
		writeError(w, http.StatusConflict, "this agent runs on another machine; remove it there")
		return
	}
	err := s.opts.Store.DeleteAgent(agentID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such agent")
		return
	case errors.Is(err, store.ErrAgentBusy):
		writeError(w, http.StatusConflict, "the agent still has open tasks; let them finish or stop them first")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Delegates and rules no longer point at it.
	if _, _, err := s.setAgentUsers(agentID, nil, nil); err != nil {
		s.opts.Log.Warn("clean policy after removing agent", "err", err)
	}
	s.opts.Log.Info("agent removed", "agent", agentID)
	w.WriteHeader(http.StatusNoContent)
}

// ListSkills lists the skills library. Owner devices only.
func (s *Server) ListSkills(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.SkillList{Skills: []gen.Skill{}}
	if s.opts.Skills == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	lib, err := s.opts.Skills.List(r.Context())
	if err != nil {
		s.internal(w, "list skills", err)
		return
	}
	agents, err := s.opts.Store.Agents()
	if err != nil {
		s.internal(w, "list agents", err)
		return
	}
	for _, sk := range lib {
		v := gen.Skill{Name: sk.Name, Description: sk.Description, HasScripts: sk.HasScripts, Files: sk.Files, UsedBy: []string{}}
		for _, a := range agents {
			if slices.Contains(a.SkillList(), sk.Name) {
				v.UsedBy = append(v.UsedBy, a.Name)
			}
		}
		out.Skills = append(out.Skills, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// SaveSkill adds or replaces a one-file skill. Owner devices only.
func (s *Server) SaveSkill(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Skills == nil {
		writeError(w, http.StatusBadRequest, "there is no skills library on this server")
		return
	}
	var req gen.SkillRequest
	if !decode(w, r, &req) {
		return
	}
	if err := s.opts.Skills.Save(r.Context(), name, req.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lib, err := s.opts.Skills.List(r.Context())
	if err != nil {
		s.internal(w, "list skills", err)
		return
	}
	for _, sk := range lib {
		if sk.Name == name {
			s.opts.Log.Info("skill saved", "skill", name)
			writeJSON(w, http.StatusOK, gen.Skill{Name: sk.Name, Description: sk.Description, HasScripts: sk.HasScripts, Files: sk.Files, UsedBy: []string{}})
			return
		}
	}
	s.internal(w, "find saved skill", errors.New("missing after save"))
}

// DeleteSkill removes a skill and takes it off every agent. Owner devices
// only.
func (s *Server) DeleteSkill(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotFound, "no such skill")
		return
	}
	if err := s.opts.Skills.Delete(name); errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "no such skill")
		return
	} else if err != nil {
		s.internal(w, "delete skill", err)
		return
	}
	agents, err := s.opts.Store.Agents()
	if err != nil {
		s.internal(w, "list agents", err)
		return
	}
	for _, a := range agents {
		if list := a.SkillList(); slices.Contains(list, name) {
			b, _ := json.Marshal(slices.DeleteFunc(list, func(x string) bool { return x == name }))
			a.Skills = string(b)
			if err := s.opts.Store.SaveAgent(a); err != nil {
				s.internal(w, "update agent", err)
				return
			}
		}
	}
	s.opts.Log.Info("skill removed", "skill", name)
	w.WriteHeader(http.StatusNoContent)
}
