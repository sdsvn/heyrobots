package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/gate"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

func (s *Server) agentRef(a store.Agent) *gen.AgentRef {
	p, _ := s.presence(a, 0)
	return &gen.AgentRef{Id: a.ID, Name: a.Name, Color: a.Color, Badge: a.Badge, Presence: &p}
}

// presence says whether an agent's machine is up and whether it is working.
func (s *Server) presence(a store.Agent, working int) (gen.Presence, *time.Time) {
	if s.opts.Cluster == nil {
		if working > 0 {
			return gen.Busy, nil
		}
		return gen.Available, nil
	}
	p, since := s.opts.Cluster.Presence(a, working)
	return gen.Presence(p), since
}

// teamByID returns every agent by id.
func (s *Server) teamByID() (map[string]store.Agent, error) {
	as, err := s.opts.Store.Agents()
	out := map[string]store.Agent{}
	for _, a := range as {
		out[a.ID] = a
	}
	return out, err
}

func (s *Server) groupView(g store.Group, team map[string]store.Agent) *gen.Group {
	v := &gen.Group{Id: g.ID, Name: g.Name, Members: []gen.AgentRef{}}
	for _, id := range g.MemberList() {
		if a, ok := team[id]; ok {
			v.Members = append(v.Members, *s.agentRef(a))
		}
	}
	if g.ThreadID != "" {
		tid := g.ThreadID
		v.ThreadId = &tid
	}
	return v
}

// mayUse reports whether a device's requests may go to every one of the
// agents: always for the owner, and for a delegate only to its own agents.
func mayUse(p policy.Policy, d store.Device, agents []string) bool {
	if d.Role == devices.RoleOwner {
		return true
	}
	allowed, any := gate.AllowedAgents(p, gate.Admission{Strength: core.Explicit, Role: core.RoleDelegate, Delegate: d.DelegateName}, nil)
	if any {
		return true
	}
	for _, a := range agents {
		if !slices.Contains(allowed, a) {
			return false
		}
	}
	return true
}

// groupFrom checks a group request; it returns the name and members, or
// why the request is bad.
func groupFrom(req gen.GroupRequest, team map[string]store.Agent) (string, string, string) {
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return "", "", "name must be 1 to 60 characters"
	}
	var members []string
	for _, id := range req.Members {
		if a, ok := team[id]; !ok || !a.Enabled {
			return "", "", "unknown or disabled agent " + id
		}
		if !slices.Contains(members, id) {
			members = append(members, id)
		}
	}
	if len(members) == 0 {
		return "", "", "a group needs at least one member"
	}
	b, _ := json.Marshal(members)
	return name, string(b), ""
}

// ListGroups lists the groups. Owner devices only.
func (s *Server) ListGroups(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	team, err := s.teamByID()
	if err != nil {
		s.internal(w, "load team", err)
		return
	}
	var gs []store.Group
	if err := s.opts.Store.Tx(func(tx *store.Tx) error {
		var err error
		gs, err = tx.Groups()
		return err
	}); err != nil {
		s.internal(w, "list groups", err)
		return
	}
	out := gen.GroupList{Groups: []gen.Group{}}
	for _, g := range gs {
		out.Groups = append(out.Groups, *s.groupView(g, team))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateGroup makes a group and its chat, so the chat opens empty. Owner
// devices only.
func (s *Server) CreateGroup(w http.ResponseWriter, r *http.Request) {
	s.saveGroup(w, r, "")
}

// UpdateGroup renames a group or changes its members. Owner devices only.
func (s *Server) UpdateGroup(w http.ResponseWriter, r *http.Request, groupID string) {
	s.saveGroup(w, r, groupID)
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request, groupID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.GroupRequest
	if !decode(w, r, &req) {
		return
	}
	team, err := s.teamByID()
	if err != nil {
		s.internal(w, "load team", err)
		return
	}
	name, members, bad := groupFrom(req, team)
	if bad != "" {
		writeError(w, http.StatusBadRequest, bad)
		return
	}
	now := s.opts.Now().UTC()
	var g store.Group
	err = s.opts.Store.Tx(func(tx *store.Tx) error {
		if groupID == "" {
			g = store.Group{ID: ids.New("gr"), CreatedAt: now}
			th, err := tx.ChatThread("group:"+g.ID, "", g.ID, policy.AppOwner.String(), now)
			if err != nil {
				return err
			}
			g.ThreadID = th.ID
		} else {
			var err error
			if g, err = tx.Group(groupID); err != nil {
				return err
			}
			if g.RemovedAt != nil {
				return store.ErrNotFound
			}
		}
		g.Name, g.Members = name, members
		return tx.SaveGroup(g)
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such group")
		return
	}
	if err != nil {
		s.internal(w, "save group", err)
		return
	}
	status := http.StatusOK
	if groupID == "" {
		status = http.StatusCreated
	}
	if s.opts.Cluster != nil {
		s.opts.Cluster.Publish(g.ThreadID)
	}
	writeJSON(w, status, s.groupView(g, team))
}

// DeleteGroup removes a group; its chat stays readable. Owner devices only.
func (s *Server) DeleteGroup(w http.ResponseWriter, r *http.Request, groupID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	now := s.opts.Now().UTC()
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		g, err := tx.Group(groupID)
		if err != nil {
			return err
		}
		if g.RemovedAt != nil {
			return store.ErrNotFound
		}
		g.RemovedAt = &now
		return tx.SaveGroup(g)
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such group")
		return
	}
	if err != nil {
		s.internal(w, "delete group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MarkThreadRead records that the calling device read a conversation.
func (s *Server) MarkThreadRead(w http.ResponseWriter, r *http.Request, threadID string) {
	d := deviceFrom(r)
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		th, err := tx.Thread(threadID)
		if err != nil {
			return err
		}
		if !canSee(d, th) {
			return store.ErrNotFound
		}
		return tx.MarkRead(d.ID, th.ID, s.opts.Now().UTC())
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	if err != nil {
		s.internal(w, "mark read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// chatThread resolves the direct chat or group a message is for: the
// conversation it replies to, or the one its agent_id or group_id names,
// created on first use. It returns an HTTP status and message when the
// device may not post there. A conversation that is no chat comes back as
// is; with no thread, agent or group, th is empty.
func (s *Server) chatThread(tx *store.Tx, d store.Device, req gen.SendMessageRequest) (th store.Thread, status int, msg string, err error) {
	var agentID, groupID string
	switch {
	case req.ThreadId != nil && *req.ThreadId != "":
		th, err = tx.Thread(*req.ThreadId)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !canSee(d, th)) {
			return th, http.StatusNotFound, "no such conversation", nil
		}
		if err != nil {
			return th, 0, "", err
		}
		agentID, groupID = th.AgentID, th.GroupID
	case req.AgentId != nil && *req.AgentId != "":
		agentID = *req.AgentId
	case req.GroupId != nil && *req.GroupId != "":
		groupID = *req.GroupId
	}
	var members []string
	switch {
	case agentID != "":
		ag, err := tx.Agent(agentID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !ag.Enabled) {
			return th, http.StatusNotFound, "that agent isn't available", nil
		}
		if err != nil {
			return th, 0, "", err
		}
		members = []string{agentID}
	case groupID != "":
		g, err := tx.Group(groupID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && g.RemovedAt != nil) {
			return th, http.StatusNotFound, "no such group", nil
		}
		if err != nil {
			return th, 0, "", err
		}
		members = g.MemberList()
	default:
		return th, 0, "", nil
	}
	p, err := tx.Policy()
	if err != nil {
		return th, 0, "", err
	}
	if !mayUse(p, d, members) {
		return th, http.StatusForbidden, "your requests can't go to every agent in this chat", nil
	}
	if th.ID != "" {
		return th, 0, "", nil
	}
	// Each delegate has their own chat with an agent or group; the owner's
	// devices share one.
	key := "agent:" + agentID
	if groupID != "" {
		key = "group:" + groupID
	}
	who := appIdentity(d)
	if d.Role != devices.RoleOwner {
		key += ":" + who.String()
	}
	th, err = tx.ChatThread(key, agentID, groupID, who.String(), s.opts.Now().UTC())
	return th, 0, "", err
}
