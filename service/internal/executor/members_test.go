package executor

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// A group member can hand a piece of work to another by @mentioning it.
// The request carries the asking task's authority and no more: it can't
// change rules or settings.
func TestAGroupMemberAsksAnotherForAPiece(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	if err := e.store.SaveAgent(store.Agent{ID: "ag_coder", Name: "Coder", AnyToolset: true, Capacity: 3, Enabled: true, CreatedAt: e.clock, UpdatedAt: e.clock}); err != nil {
		t.Fatal(err)
	}
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, core.ToolsetResearch) })
	var th store.Thread
	if err := e.store.Tx(func(tx *store.Tx) error {
		if err := tx.SaveGroup(store.Group{ID: "gr_1", Name: "Trip", Members: `["ag_coder","ag_research"]`}); err != nil {
			return err
		}
		var err error
		th, err = tx.ChatThread("group:gr_1", "", "gr_1", owner.String(), e.clock)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.eval.Router = agentRouter{agent: "ag_coder"}
	if _, err := e.intake.Ingest(intake.Message{
		Trigger:   core.Trigger{DeliveryID: "g1", Source: core.SourceApp, Actor: owner, Authenticity: core.Verified, Addressed: true, Text: "plan Lisbon", At: e.clock},
		ThreadIDs: []string{th.ID}, CreatedBy: owner,
	}); err != nil {
		t.Fatal(err)
	}
	e.model.steps = [][]*genai.Part{reportCall("waiting", "Waiting on hotels.", "I'll do flights. @Researcher please find hotels near Alfama.", "")}
	e.evalAll()
	if !e.work() {
		t.Fatal("Coder didn't run")
	}

	// Researcher gets the piece, without going through the router.
	e.eval.Router = routeFunc(func(eval.Candidate) ([]eval.Route, error) {
		t.Fatal("a teammate's request went to the router")
		return nil, nil
	})
	e.evalAll()
	var researcher store.Ticket
	for _, tk := range e.view(th.ID).Tickets {
		if tk.AgentID == "ag_research" {
			researcher = tk
		}
	}
	m, _ := researcher.DecodeMandate()
	if researcher.ID == "" || m.Received.Source != core.SourceAgent || m.Role != core.RoleOwner || !strings.Contains(m.Scope, "hotels") {
		t.Fatalf("researcher's task = %+v / %+v", researcher, m)
	}
	e.model.steps = [][]*genai.Part{
		call("make_rule", map[string]any{"name": "hotels", "text": "book hotels when you like"}),
		reportCall("ready", "Found three.", "Three hotels in Alfama.", ""),
	}
	e.model.n = 0
	if !e.work() {
		t.Fatal("Researcher didn't run")
	}
	if u := e.model.users[len(e.model.users)-1]; !strings.Contains(u, "<chat>") || !strings.Contains(u, "Coder: ") || !strings.Contains(u, "Owner: plan Lisbon") {
		t.Fatalf("the researcher didn't see the group's conversation:\n%s", u)
	}
	if got := e.model.results; len(got) == 0 || !strings.Contains(got[0]["error"].(string), "teammate") {
		t.Fatalf("make_rule answered %v; a teammate's request can't change rules", got)
	}
	// The answer goes back to Coder, whose task carries on with it.
	var coder store.Ticket
	for _, tk := range e.view(th.ID).Tickets {
		if tk.AgentID == "ag_coder" {
			coder = tk
		}
	}
	if !coder.Open || coder.Stage != string(core.StageWorking) {
		t.Fatalf("coder's task = %+v; want it working again on the answer", coder)
	}
	e.model.steps = [][]*genai.Part{reportCall("ready", "Done.", "Flights and hotels are set.", "")}
	e.model.n = 0
	if !e.work() || !strings.Contains(e.model.users[len(e.model.users)-1], "Researcher answers: ") {
		t.Fatalf("coder didn't get the answer: %v", e.model.users[len(e.model.users)-1])
	}
	// Its answer is in the group, as a reply the app shows.
	if !slices.ContainsFunc(e.view(th.ID).Effects, func(ef store.Effect) bool {
		return ef.Source == string(core.SourceApp) && strings.Contains(ef.Body, "Three hotels")
	}) {
		t.Fatal("the researcher's answer isn't in the group")
	}
}

type routeFunc func(eval.Candidate) ([]eval.Route, error)

func (f routeFunc) Route(_ context.Context, c eval.Candidate) ([]eval.Route, error) { return f(c) }

// An agent keeps one folder per chat, so a file from an earlier message is
// still there for a later one.
func TestAChatKeepsItsFilesBetweenTasks(t *testing.T) {
	e := newEnv(t)
	var th store.Thread
	if err := e.store.Tx(func(tx *store.Tx) error {
		var err error
		th, err = tx.ChatThread("agent:ag_general", "ag_general", "", owner.String(), e.clock)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, core.ToolsetWriting) })
	e.eval.Router = acceptRouter{[]core.Toolset{core.ToolsetWriting}}
	say := func(id, text string) {
		if _, err := e.intake.Ingest(intake.Message{
			Trigger:   core.Trigger{DeliveryID: id, Source: core.SourceApp, Actor: owner, Authenticity: core.Verified, Addressed: true, Text: text, At: e.clock},
			ThreadIDs: []string{th.ID}, CreatedBy: owner,
		}); err != nil {
			t.Fatal(err)
		}
		e.clock = e.clock.Add(time.Minute)
	}
	e.model.steps = [][]*genai.Part{
		call("write_file", map[string]any{"path": "plan.md", "content": "Take the Eurostar."}),
		reportCall("ready", "Wrote it.", "plan.md", ""),
	}
	say("d1", "plan a trip to London")
	e.evalAll()
	e.work()
	e.model.steps = [][]*genai.Part{
		call("read_file", map[string]any{"path": "plan.md"}),
		reportCall("ready", "Here it is.", "Take the Eurostar.", ""),
	}
	e.model.n = 0
	say("d2", "print all of it here")
	e.evalAll()
	e.work()
	if got, _ := json.Marshal(e.model.results); !strings.Contains(string(got), "Take the Eurostar.") {
		t.Fatalf("the second task couldn't read the first one's file: %s", got)
	}
}
