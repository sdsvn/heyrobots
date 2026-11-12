package intake

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

func newIntake(t *testing.T) (*Intake, *store.Store) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, time.Now), st
}

func msg(delivery, key string) Message {
	return Message{
		Trigger:    core.Trigger{DeliveryID: delivery, Source: core.SourceApp, Actor: policy.AppOwner, Authenticity: core.Verified, Addressed: true, Text: "hi"},
		ThreadKeys: []string{key}, CreatedBy: policy.AppOwner,
	}
}

func TestSameDeliveryIsStoredOnce(t *testing.T) {
	in, _ := newIntake(t)
	a, err := in.Ingest(msg("dev1:m1", "chat:1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := in.Ingest(msg("dev1:m1", "chat:1"))
	if err != nil {
		t.Fatal(err)
	}
	if !b.Duplicate || b.TriggerID != a.TriggerID || b.ThreadIDs[0] != a.ThreadIDs[0] {
		t.Fatalf("replay: %+v vs %+v", b, a)
	}
}

func TestConcurrentReplaysStoreOneTrigger(t *testing.T) {
	in, _ := newIntake(t)
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := in.Ingest(msg("dev1:same", "chat:1"))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = r.TriggerID
		}()
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("replays created different Triggers: %v", ids)
		}
	}
}

func TestOneThreadPerKey(t *testing.T) {
	in, _ := newIntake(t)
	a, _ := in.Ingest(msg("d:1", "chat:1"))
	b, _ := in.Ingest(msg("d:2", "chat:1"))
	c, _ := in.Ingest(msg("d:3", "chat:2"))
	if a.ThreadIDs[0] != b.ThreadIDs[0] || a.ThreadIDs[0] == c.ThreadIDs[0] {
		t.Fatalf("threads: %s %s %s", a.ThreadIDs[0], b.ThreadIDs[0], c.ThreadIDs[0])
	}
}

func TestBotMessagesReachOpenTicketsButAreNotOwned(t *testing.T) {
	in, st := newIntake(t)
	first, _ := in.Ingest(msg("d:1", "chat:1"))
	now := time.Now().UTC()
	st.Tx(func(tx *store.Tx) error {
		return tx.CreateTicket(store.Ticket{ID: "tk_1", Open: true, Stage: "working"}, core.Mandate{Role: core.RoleOwner, Requester: policy.AppOwner, Strength: core.Explicit}, first.ThreadIDs[0], now)
	})
	bot := msg("d:2", "chat:1")
	bot.Trigger.ActorIsBot = true
	bot.Trigger.Actor = core.Identity{Source: core.SourceApp, ID: "ci[bot]"}
	r, _ := in.Ingest(bot)
	if r.Owned {
		t.Fatal("a bot message must not be owned by a Ticket")
	}
	var inputs []store.TicketInput
	st.Tx(func(tx *store.Tx) error { inputs, _ = tx.TicketInputs("tk_1"); return nil })
	if len(inputs) != 1 {
		t.Fatalf("bot message delivered %d times, want once", len(inputs))
	}
	person, _ := in.Ingest(msg("d:3", "chat:1"))
	if !person.Owned {
		t.Fatal("a person's follow-up must be owned by the open Ticket")
	}
}
