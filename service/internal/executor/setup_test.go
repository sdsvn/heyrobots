package executor

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"heyrobots/service/internal/changes"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// The IT agent sees the setup and proposes a change; the change waits for
// the owner, and once approved the agent has what it was given.
func TestTheITAgentProposesSetupChangesForTheOwnersOK(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, core.ToolsetSetup) })
	e.eval.Router = agentRouter{agent: "ag_it", toolsets: []core.Toolset{core.ToolsetSetup}}
	e.model.steps = [][]*genai.Part{
		call("setup_status", map[string]any{}),
		call("give_to_agent", map[string]any{"agent": "researcher", "skill": "trip-planner"}),
		reportCall("ready", "Proposed it.", "Asked to give Researcher the trip planner.", ""),
	}
	res := e.say(owner, "c1", "give the researcher the trip planner skill")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	if tools := e.model.tools[0]; !slices.Contains(tools, "find_add_ons") || !slices.Contains(tools, "connect_add_on") {
		t.Fatalf("tools = %v", tools)
	}
	if got, _ := json.Marshal(e.model.results); !strings.Contains(string(got), "Researcher") || !strings.Contains(string(got), "trip-planner") {
		t.Fatalf("setup_status didn't show the team: %s", got)
	}
	acts := e.actions(res.ThreadIDs[0])
	if len(acts) != 1 || acts[0].Action != changes.ActionGive || acts[0].Status != store.EffectAwaitingApproval {
		t.Fatalf("actions = %+v", acts)
	}

	// Approved, the outbox makes the change.
	if _, err := (changes.Sender{Store: e.store, Now: time.Now}).Send(context.Background(), acts[0]); err != nil {
		t.Fatal(err)
	}
	ag, _ := e.store.Agent("ag_research")
	if !slices.Contains(ag.SkillList(), "trip-planner") {
		t.Fatalf("skills = %v", ag.SkillList())
	}
}
