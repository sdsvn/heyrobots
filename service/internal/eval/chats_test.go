package eval

import (
	"slices"
	"testing"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
)

// chat stores the owner's message on the chat thread with key.
func (e *env) chat(key, agentID, groupID, text string) string {
	e.t.Helper()
	var th store.Thread
	if err := e.store.Tx(func(tx *store.Tx) error {
		var err error
		th, err = tx.ChatThread(key, agentID, groupID, owner.String(), e.clock.now())
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	e.n++
	if _, err := e.intake.Ingest(intake.Message{
		Trigger: core.Trigger{DeliveryID: key + "#" + text, Source: core.SourceApp, Actor: owner,
			Authenticity: core.Verified, Addressed: true, Text: text, At: e.clock.now()},
		ThreadIDs: []string{th.ID}, CreatedBy: owner,
	}); err != nil {
		e.t.Fatal(err)
	}
	return th.ID
}

func (e *env) agent(id, name string) {
	e.t.Helper()
	if err := e.store.SaveAgent(store.Agent{ID: id, Name: name, AnyToolset: true, Capacity: 1, Enabled: true}); err != nil {
		e.t.Fatal(err)
	}
}

func TestChatsFixWhoMayLead(t *testing.T) {
	e := newEnv(t)
	e.agent("ag_coder", "Coder")
	e.agent("ag_writer", "Writer")
	if err := e.store.Tx(func(tx *store.Tx) error {
		return tx.SaveGroup(store.Group{ID: "gr_1", Name: "Team", Members: `["ag_coder","ag_writer"]`})
	}); err != nil {
		t.Fatal(err)
	}
	// The router tries to hand every message to the General agent.
	var offered [][]string
	e.router.decide = func(c Candidate) ([]Route, error) {
		offered = append(offered, c.Triggers[0].Agents)
		return []Route{{Action: ActionAccept, TriggerIDs: []string{c.Triggers[0].ID}, Scope: "work", Agent: core.GeneralAgent}}, nil
	}
	// A direct chat and an @mention leave nothing to route: they go
	// straight to that agent.
	direct := e.chat("agent:ag_coder", "ag_coder", "", "fix the build")
	e.run()
	group := e.chat("group:gr_1", "", "gr_1", "write the notes")
	e.run()
	e.chat("group:gr_1", "", "gr_1", "@writer please review")
	e.run()
	if want := [][]string{{"ag_coder", "ag_writer"}}; len(offered) != 1 || !slices.Equal(offered[0], want[0]) {
		t.Fatalf("router offered %v, want only the unaddressed group message, with %v", offered, want[0])
	}
	if v := e.view(direct); len(v.Tickets) != 1 || v.Tickets[0].AgentID != "ag_coder" {
		t.Fatalf("direct chat tickets = %+v", v.Tickets)
	}
	// Accept still refuses a lead outside the chat.
	if got := kinds(e.view(group)); !slices.Contains(got, store.DecisionRefused) {
		t.Fatalf("group decisions = %v", got)
	}
	v := e.view(group)
	if len(v.Tickets) != 1 || v.Tickets[0].AgentID != "ag_writer" {
		t.Fatalf("group tickets = %+v; want the @mentioned Writer's", v.Tickets)
	}
}

func TestAChatOffersOnlyToolsetsItsLeadCanUse(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SaveAgent(store.Agent{ID: "ag_cos", Name: "Chief of staff", Toolsets: `["mail","calendar"]`, Capacity: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	id := e.chat("agent:ag_cos", "ag_cos", "", "every weekday at 8, brief me")
	e.run()
	v := e.view(id)
	if len(v.Tickets) != 1 {
		t.Fatalf("tickets = %+v", v.Tickets)
	}
	m, _ := v.Tickets[0].DecodeMandate()
	for _, ts := range m.Toolsets {
		if ts != core.ToolsetMail && ts != core.ToolsetCalendar {
			t.Fatalf("granted %v", m.Toolsets)
		}
	}
	if len(m.Toolsets) == 0 {
		t.Fatal("granted nothing")
	}
}

// A message to everyone in a group goes to each member once, and never to
// someone outside the group.
func TestAGroupMessageCanGoToEachMember(t *testing.T) {
	e := newEnv(t)
	e.agent("ag_coder", "Coder")
	e.agent("ag_writer", "Writer")
	if err := e.store.Tx(func(tx *store.Tx) error {
		return tx.SaveGroup(store.Group{ID: "gr_1", Name: "Team", Members: `["ag_coder","ag_writer"]`})
	}); err != nil {
		t.Fatal(err)
	}
	e.router.decide = func(c Candidate) ([]Route, error) {
		id := []string{c.Triggers[0].ID}
		return []Route{
			{Action: ActionAccept, TriggerIDs: id, Scope: "say what you can do", Agent: "ag_coder"},
			{Action: ActionAccept, TriggerIDs: id, Scope: "say what you can do", Agent: "ag_coder"},
			{Action: ActionAccept, TriggerIDs: id, Scope: "say what you can do", Agent: "ag_writer"},
			{Action: ActionAccept, TriggerIDs: id, Scope: "say what you can do", Agent: core.GeneralAgent},
		}, nil
	}
	group := e.chat("group:gr_1", "", "gr_1", "both of you, what can you do?")
	e.run()
	var leads []string
	for _, tk := range e.view(group).Tickets {
		leads = append(leads, tk.AgentID)
	}
	slices.Sort(leads)
	if !slices.Equal(leads, []string{"ag_coder", "ag_writer"}) {
		t.Fatalf("tickets led by %v; want one each for Coder and Writer", leads)
	}

	// In a direct chat a message still has one lead.
	e.router.decide = nil
	direct := e.chat("agent:ag_coder", "ag_coder", "", "and you?")
	e.run()
	if v := e.view(direct); len(v.Tickets) != 1 {
		t.Fatalf("direct chat tickets = %+v", v.Tickets)
	}
}
