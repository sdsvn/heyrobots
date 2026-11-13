package executor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/skills"
	"heyrobots/service/internal/store"
)

// agentRouter accepts every new message for one agent.
type agentRouter struct {
	agent    string
	toolsets []core.Toolset
}

func (a agentRouter) Route(_ context.Context, c eval.Candidate) ([]eval.Route, error) {
	var out []eval.Route
	for _, t := range c.Triggers {
		out = append(out, eval.Route{Action: eval.ActionAccept, TriggerIDs: []string{t.ID}, Scope: t.Text, Toolsets: a.toolsets, Agent: a.agent})
	}
	return out, nil
}

func (e *env) addResearcher() {
	e.t.Helper()
	err := e.store.SaveAgent(store.Agent{ID: "ag_research", Name: "Researcher", Purpose: "Finds things out.",
		Instructions: "Always cite two sources.", Toolsets: `["research"]`, Skills: `["trip-planner"]`,
		Capacity: 1, Enabled: true, CreatedAt: e.clock, UpdatedAt: e.clock})
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestATaskRunsAsItsAgent(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	lib := &skills.Library{Dir: t.TempDir()}
	if err := lib.Save(context.Background(), "trip-planner", "---\nname: trip-planner\ndescription: Plan trips the owner's way.\n---\nPrefer trains.\n"); err != nil {
		t.Fatal(err)
	}
	if err := lib.Save(context.Background(), "taxes", "---\nname: taxes\ndescription: Do the taxes.\n---\nx\n"); err != nil {
		t.Fatal(err)
	}
	e.exec.Skills = lib
	e.exec.Memory = filepath.Join(t.TempDir(), "memory")
	e.eval.Router = agentRouter{agent: "ag_research", toolsets: []core.Toolset{core.ToolsetResearch}}

	e.model.steps = [][]*genai.Part{
		call("remember", map[string]any{"note": "Prefers aisle seats"}),
		reportCall("ready", "Found it.", "Lisbon has trains.", ""),
	}
	res := e.say(owner, "c1", "how do I get around Lisbon?")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	tk := e.ticket(res.ThreadIDs[0])
	if tk.AgentID != "ag_research" || tk.Stage != string(core.StageReady) {
		t.Fatalf("ticket = %+v", tk)
	}
	sys, tools := e.model.systems[0], e.model.tools[0]
	for _, want := range []string{"You are Researcher", "Finds things out.", "Always cite two sources.", "trip-planner"} {
		if !strings.Contains(sys, want) {
			t.Errorf("the instruction lacks %q", want)
		}
	}
	if strings.Contains(sys, "taxes") {
		t.Error("the agent saw a skill it doesn't have")
	}
	if !slices.Contains(tools, "web_search") || !slices.Contains(tools, "load_skill") || slices.Contains(tools, "write_file") {
		t.Errorf("tools = %v", tools)
	}

	// Its next task for the owner remembers; a delegate's task doesn't.
	e.model.steps = [][]*genai.Part{reportCall("ready", "Done.", "ok", "")}
	e.model.n = 0
	e.say(owner, "c2", "book me a train")
	e.evalAll()
	e.work()
	// Memory rides in the user turn, so the cached system prompt stays put.
	if !strings.Contains(e.model.users[len(e.model.users)-1], "Prefers aisle seats") {
		t.Fatal("the agent forgot the owner's preference")
	}
	if strings.Contains(e.model.systems[len(e.model.systems)-1], "Prefers aisle seats") {
		t.Fatal("memory leaked into the system prompt")
	}
	e.setPolicy(func(p *policy.Policy) {
		p.Toolsets = append(p.Toolsets, core.ToolsetResearch)
		p.Delegates[0].Toolsets = []core.Toolset{core.ToolsetResearch}
		p.Delegates[0].Agents = []string{"ag_research"}
	})
	e.model.n = 0
	e.say(delegate, "c3", "find me a cafe")
	e.evalAll()
	e.work()
	if strings.Contains(e.model.users[len(e.model.users)-1], "Prefers aisle seats") {
		t.Fatal("the owner's memory reached a delegate's task")
	}
	if _, err := os.Stat(filepath.Join(e.exec.Memory, "ag_research", "owner.md")); err != nil {
		t.Fatal(err)
	}
}

func TestWorkGoesOnlyToAllowedAgentsThatCanDoIt(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	// A delegate whose agents don't include the Researcher.
	e.eval.Router = agentRouter{agent: "ag_research", toolsets: []core.Toolset{core.ToolsetWriting}}
	res := e.say(delegate, "c1", "write a poem")
	e.evalAll()
	if v := e.view(res.ThreadIDs[0]); len(v.Tickets) != 0 {
		t.Fatal("a delegate's work went to an agent they may not use")
	}
	// The owner may use any agent, but not for toolsets it lacks.
	res = e.say(owner, "c2", "write a poem")
	e.evalAll()
	v := e.view(res.ThreadIDs[0])
	if len(v.Tickets) != 0 || !strings.Contains(strings.Join(messages(v), " "), "can't use the writing toolset") {
		t.Fatalf("tickets %d, messages %q", len(v.Tickets), messages(v))
	}
}

func TestAnAgentWorksOnItsCapacityAtOnce(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	e.eval.Router = agentRouter{agent: "ag_research", toolsets: []core.Toolset{core.ToolsetResearch}}
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, core.ToolsetResearch) })
	e.say(owner, "c1", "one")
	e.say(owner, "c2", "two")
	e.evalAll()
	now := time.Now()
	var first store.Claimed
	if err := e.store.Tx(func(tx *store.Tx) error {
		var err error
		first, err = tx.ClaimAssignment("as_1", now, time.Hour, true, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := e.store.Tx(func(tx *store.Tx) error {
		_, err := tx.ClaimAssignment("as_2", now, time.Hour, true, true)
		return err
	})
	if err != store.ErrNotFound {
		t.Fatalf("a second task started while the Researcher was busy: %v", err)
	}
	loads, _ := e.store.AgentLoads()
	if l := loads["ag_research"]; len(l.Running) != 1 || l.Waiting != 1 || first.Assignment.AgentID != "ag_research" {
		t.Fatalf("load = %+v", l)
	}
}

// Background work leaves one of an agent's slots free, so a request from
// a person starts at once instead of waiting behind standing jobs.
func TestBackgroundWorkLeavesASlotForYou(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	ag, _ := e.store.Agent("ag_research")
	ag.Capacity = 2
	if err := e.store.SaveAgent(ag); err != nil {
		t.Fatal(err)
	}
	e.eval.Router = agentRouter{agent: "ag_research", toolsets: []core.Toolset{core.ToolsetResearch}}
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, core.ToolsetResearch) })
	res := e.say(owner, "c1", "find me a train")
	e.evalAll()
	mine := e.ticket(res.ThreadIDs[0])
	var input string
	_ = e.store.Tx(func(tx *store.Tx) error {
		in, err := tx.TicketInputs(mine.ID)
		if len(in) > 0 {
			input = in[0].TriggerID
		}
		return err
	})
	// Two standing-job tasks, older than the request.
	start := mine.UpdatedAt.Add(-time.Hour)
	for i, id := range []string{"tk_job1", "tk_job2"} {
		if err := e.store.Tx(func(tx *store.Tx) error {
			m := core.Mandate{Scope: "brief", Strength: core.Inferred, Role: core.RoleOwner, Agent: "ag_research", Job: "brief"}
			if err := tx.CreateTicket(store.Ticket{ID: id, Open: true, Stage: string(core.StageWorking), OriginThreadID: res.ThreadIDs[0]}, m, res.ThreadIDs[0], start.Add(time.Duration(i)*time.Minute)); err != nil {
				return err
			}
			return tx.AddTicketInput(id, input, start)
		}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(id string) (string, error) {
		var c store.Claimed
		err := e.store.Tx(func(tx *store.Tx) error {
			var err error
			c, err = tx.ClaimAssignment(id, time.Now(), time.Hour, true, true)
			return err
		})
		return c.Ticket.ID, err
	}
	if got, err := claim("as_1"); err != nil || got != "tk_job1" {
		t.Fatalf("first claim = %s %v; want the oldest job", got, err)
	}
	if got, err := claim("as_2"); err != nil || got != mine.ID {
		t.Fatalf("second claim = %s %v; want the person's request, not the second job", got, err)
	}
	if got, err := claim("as_3"); err != store.ErrNotFound {
		t.Fatalf("third claim = %s %v; the agent is full", got, err)
	}
}
