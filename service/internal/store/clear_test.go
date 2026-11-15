package store

import (
	"testing"
	"time"
)

func TestClearingHistoryKeepsTheTeamAndTheLedger(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC()
	exec := func(q string, args ...any) {
		t.Helper()
		if err := s.db.Exec(q, args...).Error; err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec("INSERT INTO threads (id, source, key, created_by, created_at, updated_at, agent_id, group_id) VALUES ('th_mail', 'mail', 'k1', 'x', ?, ?, '', '')", now, now)
	exec("INSERT INTO threads (id, source, key, created_by, created_at, updated_at, agent_id, group_id) VALUES ('th_dm', 'app', 'k2', 'x', ?, ?, 'ag_general', '')", now, now)
	exec("INSERT INTO tickets (id, origin_thread_id, open, created_at, updated_at) VALUES ('tk_1', 'th_mail', true, ?, ?)", now, now)
	if err := s.Tx(func(tx *Tx) error {
		if err := tx.CreateEffect(Effect{TicketID: "tk_1", ThreadID: "th_mail", Source: "mail", Kind: "action", Action: "send", Reach: "others",
			Status: EffectAwaitingApproval, Approval: ApprovalAwaiting, CreatedAt: now}); err != nil {
			return err
		}
		if _, err := tx.AppendEvent("m_a", "th_dm", "message", "d1", map[string]string{"text": "hi"}, now); err != nil {
			return err
		}
		return tx.AddOutcome(AgentOutcome{TicketID: "tk_1", AgentID: "ag_it", Checked: true, CreatedAt: now}, now)
	}); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO usages (at, ticket_id, model, input_tokens, output_tokens, cached_tokens, cost_micros) VALUES (?, 'tk_1', 'm', 10, 5, 0, 7)", now)
	exec("INSERT INTO brain_facts (agent_id, subject, text, created_at, updated_at) VALUES ('ag_general', 'owner', 'Prefers aisle seats', ?, ?)", now, now)

	c, err := s.ClearHistory(false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tasks != 1 || c.Chats != 1 || c.Actions != 1 || c.ForgotBrain {
		t.Fatalf("cleared = %+v", c)
	}
	count := func(table string) (n int64) {
		t.Helper()
		if err := s.db.Table(table).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	for table, want := range map[string]int64{"tickets": 0, "effects": 0, "agent_outcomes": 0, "threads": 1, "usages": 1, "brain_facts": 1, "agents": 4,
		// The log shared with the team's other machines stays, so their
		// numbering goes on and nothing cleared comes back from them.
		"chat_events": 1} {
		if got := count(table); got != want {
			t.Errorf("%s: %d rows, want %d", table, got, want)
		}
	}
	if _, err := s.ClearHistory(true); err != nil {
		t.Fatal(err)
	}
	if got := count("brain_facts"); got != 0 {
		t.Errorf("forgetting clears what agents learned: %d facts left", got)
	}
}
