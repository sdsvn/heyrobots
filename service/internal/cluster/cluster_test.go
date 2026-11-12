package cluster

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
)

const owner = "app:owner"

func newNode(t *testing.T, name string) *Node {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	n, err := New(st, intake.New(st, time.Now), slog.New(slog.NewTextHandler(io.Discard, nil)), name, "", owner)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func member(t *testing.T, n *Node, id string) store.Member {
	t.Helper()
	ms, _ := n.Store.Members()
	for _, m := range ms {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("%s doesn't know member %s", n.name, id)
	return store.Member{}
}

// Two machines join, see each other's agents, share a chat, and exactly one
// of them routes a message sent on the other.
func TestTwoMachinesShareOneTeam(t *testing.T) {
	ClaimWindow = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	home := newNode(t, "home")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	home.address = ln.Addr().String()
	go home.serve(ctx, ln)

	laptop := newNode(t, "laptop") // only dials out
	code, _ := home.NewCode()
	if _, err := laptop.Join(ctx, home.address, "wrong"); err == nil {
		t.Fatal("a wrong code joined")
	}
	// History from before the join must never be routed again.
	old, err := home.Intake.Ingest(intake.Message{
		Trigger:    core.Trigger{DeliveryID: "dev:0", Source: core.SourceApp, Actor: core.Identity{Source: core.SourceApp, ID: "owner"}, Authenticity: core.Verified, Addressed: true, Text: "done long ago"},
		ThreadKeys: []string{"chat:c0"}, CreatedBy: core.Identity{Source: core.SourceApp, ID: "owner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = home.Store.Tx(func(tx *store.Tx) error { return tx.SetTriggersState([]string{old.TriggerID}, store.EvalDone) })
	if _, err := laptop.Join(ctx, home.address, code); err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.Join(ctx, home.address, code); err == nil {
		t.Fatal("a code worked twice")
	}
	sync := func() {
		t.Helper()
		if err := laptop.exchange(ctx, member(t, laptop, home.id)); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	sync()
	if h, err := laptop.Store.TriggerByDelivery("app", "dev:0"); err != nil || h.EvalState != store.EvalDone {
		t.Fatalf("laptop's copy of old history: %+v, %v; want it done, never routed", h, err)
	}

	// Each sees the other's General as a card, and the other as online.
	card, err := laptop.Store.Agent(home.id + "." + core.GeneralAgent)
	if err != nil || card.Home != home.id {
		t.Fatalf("laptop's card of home's General: %+v, %v", card, err)
	}
	if !home.Online(member(t, home, laptop.id)) {
		t.Fatal("home doesn't see the laptop, which only dials out, as online")
	}

	// The owner writes on the laptop.
	res, err := laptop.Intake.Ingest(intake.Message{
		Trigger:    core.Trigger{DeliveryID: "dev:1", Source: core.SourceApp, Actor: core.Identity{Source: core.SourceApp, ID: "owner"}, Authenticity: core.Verified, Addressed: true, Text: "hello team"},
		ThreadKeys: []string{"chat:c1"}, CreatedBy: core.Identity{Source: core.SourceApp, ID: "owner"}, Hold: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	laptop.Publish(res.ThreadIDs[0])
	sync()
	copied, err := home.Store.TriggerByDelivery("app", "dev:1")
	if err != nil || copied.Origin != laptop.id || copied.EvalState != store.EvalHeld {
		t.Fatalf("home's copy: %+v, %v", copied, err)
	}

	// Both decide until one routes it; the other's copy stays held.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range []*Node{home, laptop} {
			if err := n.decide(); err != nil {
				t.Fatal(err)
			}
		}
		sync()
		a, _ := home.Store.TriggerByDelivery("app", "dev:1")
		b, _ := laptop.Store.TriggerByDelivery("app", "dev:1")
		if a.EvalState != store.EvalHeld || b.EvalState != store.EvalHeld {
			if a.EvalState != store.EvalHeld && b.EvalState != store.EvalHeld {
				t.Fatal("both machines routed the message")
			}
			// Keep going a little to be sure the other never follows.
			time.Sleep(3 * ClaimWindow)
			for _, n := range []*Node{home, laptop} {
				_ = n.decide()
			}
			a, _ = home.Store.TriggerByDelivery("app", "dev:1")
			b, _ = laptop.Store.TriggerByDelivery("app", "dev:1")
			if a.EvalState != store.EvalHeld && b.EvalState != store.EvalHeld {
				t.Fatal("both machines routed the message")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no machine routed the message")
}

// Work handed to another machine's agent is released there with that agent
// as its lead.
func TestHandOffReleasesOnTheAgentsMachine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home, laptop := newNode(t, "home"), newNode(t, "laptop")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	home.address = ln.Addr().String()
	go home.serve(ctx, ln)
	code, _ := home.NewCode()
	if _, err := laptop.Join(ctx, home.address, code); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		if err := laptop.exchange(ctx, member(t, laptop, home.id)); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	res, err := laptop.Intake.Ingest(intake.Message{
		Trigger:    core.Trigger{DeliveryID: "dev:2", Source: core.SourceApp, Actor: core.Identity{Source: core.SourceApp, ID: "owner"}, Authenticity: core.Verified, Addressed: true, Text: "@General write it"},
		ThreadKeys: []string{"chat:c2"}, CreatedBy: core.Identity{Source: core.SourceApp, ID: "owner"}, Hold: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	laptop.Publish(res.ThreadIDs[0])
	tr, _ := laptop.Store.TriggerByDelivery("app", "dev:2")
	card, _ := laptop.Store.Agent(home.id + "." + core.GeneralAgent)
	if err := laptop.Store.Tx(func(tx *store.Tx) error { return laptop.HandOff(tx, card, tr, res.ThreadIDs[0]) }); err != nil {
		t.Fatal(err)
	}
	sync()
	if err := home.decide(); err != nil {
		t.Fatal(err)
	}
	got, _ := home.Store.TriggerByDelivery("app", "dev:2")
	if got.EvalState != store.EvalPending || got.LeadAgent != core.GeneralAgent {
		t.Fatalf("home's copy after the hand-off: state %q lead %q; want pending, led by General", got.EvalState, got.LeadAgent)
	}
}

// A copied direct chat keeps its agent when messages arrive in it.
func TestCopiedChatKeepsItsAgent(t *testing.T) {
	n := newNode(t, "home")
	other := "mabc.ag_writer"
	if err := n.act(store.ChatEvent{Origin: "mabc", ThreadID: "th_x", Kind: kindChat, Body: `{"key":"agent:mabc.ag_writer","agent":"mabc.ag_writer","created_by":"app:owner"}`}); err != nil {
		t.Fatal(err)
	}
	if err := n.act(store.ChatEvent{Origin: "mabc", ThreadID: "th_x", Kind: kindMessage, Body: `{"delivery_id":"d:1","actor":"app:owner","text":"hi","held":true}`}); err != nil {
		t.Fatal(err)
	}
	var th store.Thread
	_ = n.Store.Tx(func(tx *store.Tx) error { var err error; th, err = tx.Thread("th_x"); return err })
	if th.AgentID != other {
		t.Fatalf("the copied chat's agent is %q; want %q", th.AgentID, other)
	}
}

// A message another machine handled itself, then handed to this machine's
// agent, is still picked up here.
func TestHandOffOfAHandledMessage(t *testing.T) {
	n := newNode(t, "home")
	for _, e := range []store.ChatEvent{
		{Origin: "mabc", Seq: 1, ThreadID: "th_y", Kind: kindMessage, Body: `{"delivery_id":"d:2","actor":"app:owner","text":"hi"}`},
		{Origin: "mabc", Seq: 2, ThreadID: "th_y", Kind: kindRouted, Body: `{"delivery_id":"d:2","agent":"` + n.id + `.ag_general"}`},
	} {
		if err := n.apply([]store.ChatEvent{e}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := n.Store.TriggerByDelivery("app", "d:2")
	if got.EvalState != store.EvalPending || got.LeadAgent != core.GeneralAgent {
		t.Fatalf("state %q lead %q; want pending, led by General", got.EvalState, got.LeadAgent)
	}
}
