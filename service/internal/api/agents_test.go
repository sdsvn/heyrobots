package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/skills"
)

func TestTheTeamCanBeBuiltAndShown(t *testing.T) {
	h := newHarnessWith(t, nil, func(h *harness, o *Options) {
		o.Skills = &skills.Library{Dir: t.TempDir()}
	})
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	// The library takes a one-file skill.
	rec := h.putJSON("/api/v1/skills/trip-planner", owner, gen.SkillRequest{Content: "---\nname: trip-planner\ndescription: Plan trips.\n---\nPrefer trains.\n"})
	if rec.Code != http.StatusOK {
		t.Fatalf("skill = %d %s", rec.Code, rec.Body)
	}
	if c := h.putJSON("/api/v1/skills/bad", owner, gen.SkillRequest{Content: "no frontmatter"}).Code; c != http.StatusBadRequest {
		t.Fatalf("bad skill = %d", c)
	}

	// The General agent is there from the start.
	var list gen.AgentList
	json.Unmarshal(h.do("GET", "/api/v1/agents", owner, nil, "").Body.Bytes(), &list)
	if len(list.Agents) != 1 || list.Agents[0].Id != core.GeneralAgent || !list.Agents[0].AnyToolset || !slices.Contains(list.Agents[0].Delegates, "sam") {
		t.Fatalf("team = %+v", list)
	}
	if c := h.do("GET", "/api/v1/agents", sam, nil, "").Code; c != http.StatusForbidden {
		t.Fatalf("delegate = %d", c)
	}

	req := gen.AgentRequest{Name: "Researcher", Purpose: "Finds things out.", Toolsets: []string{"research"}, Skills: []string{"trip-planner"},
		Capacity: 1, Color: gen.AgentRequestColorSky, Badge: gen.AgentRequestBadgeSearch, Enabled: true, Delegates: &[]string{"sam"}}
	rec = h.postJSON("/api/v1/agents", owner, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var a gen.Agent
	json.Unmarshal(rec.Body.Bytes(), &a)
	if a.Name != "Researcher" || a.Skills[0] != "trip-planner" || !slices.Contains(a.Delegates, "sam") {
		t.Fatalf("agent = %+v", a)
	}
	// Sam now may use both the General agent and the Researcher.
	pol, _ := h.store.Policy()
	if got := pol.Delegates[0].Agents; !slices.Contains(got, core.GeneralAgent) || !slices.Contains(got, a.Id) {
		t.Fatalf("sam's agents = %v", got)
	}
	// Saving the policy from settings keeps that.
	if c := h.do("PUT", "/api/v1/policy", owner, strings.NewReader(`{"mode":"observe","toolsets":["research","writing"],"delegates":[{"name":"sam","toolsets":["writing"]}],"rules":[]}`), "application/json").Code; c != http.StatusOK {
		t.Fatalf("policy = %d", c)
	}
	pol, _ = h.store.Policy()
	if !slices.Contains(pol.Delegates[0].Agents, a.Id) {
		t.Fatal("saving settings dropped the delegate's agents")
	}

	for name, bad := range map[string]gen.AgentRequest{
		"dup name":      {Name: "researcher", Toolsets: []string{}, Skills: []string{}, Capacity: 1, Color: "sky", Badge: "star", Enabled: true},
		"bad toolset":   {Name: "X", Toolsets: []string{"spaceflight"}, Skills: []string{}, Capacity: 1, Color: "sky", Badge: "star", Enabled: true},
		"unknown skill": {Name: "Y", Toolsets: []string{}, Skills: []string{"nope"}, Capacity: 1, Color: "sky", Badge: "star", Enabled: true},
		"capacity":      {Name: "Z", Toolsets: []string{}, Skills: []string{}, Capacity: 9, Color: "sky", Badge: "star", Enabled: true},
	} {
		if c := h.postJSON("/api/v1/agents", owner, bad).Code; c != http.StatusBadRequest && c != http.StatusConflict {
			t.Errorf("%s = %d", name, c)
		}
	}
	if c := h.do("DELETE", "/api/v1/agents/"+core.GeneralAgent, owner, nil, "").Code; c != http.StatusBadRequest {
		t.Fatalf("delete General = %d", c)
	}

	// Removing a skill takes it off the agent; removing the agent takes it
	// off the delegate.
	if c := h.do("DELETE", "/api/v1/skills/trip-planner", owner, nil, "").Code; c != http.StatusNoContent {
		t.Fatalf("delete skill = %d", c)
	}
	ag, _ := h.store.Agent(a.Id)
	if len(ag.SkillList()) != 0 {
		t.Fatal("the agent kept a removed skill")
	}
	if c := h.do("DELETE", "/api/v1/agents/"+a.Id, owner, nil, "").Code; c != http.StatusNoContent {
		t.Fatalf("delete = %d", c)
	}
	pol, _ = h.store.Policy()
	if slices.Contains(pol.Delegates[0].Agents, a.Id) {
		t.Fatal("the delegate still points at a removed agent")
	}
}
