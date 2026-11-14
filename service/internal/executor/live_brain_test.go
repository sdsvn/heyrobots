//go:build live

// The brain with a real model: Claude Code over ACP, signed in as the
// owner, against the fake mailbox and a clock the test moves. Run with
//
//	go test -tags live -run TestLiveBrain -v -timeout 60m ./internal/executor
package executor

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"iter"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"heyrobots/service/internal/acp"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/quarantine"
	"heyrobots/service/internal/router"
	"heyrobots/service/prompts"
	"heyrobots/service/internal/store"
)

// askLLM answers one-shot model calls, such as the quarantined reader's,
// through Claude Code.
type askLLM struct{ w *acp.Worker }

func (askLLM) Name() string { return "claude-code" }

func (a askLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var b strings.Builder
		if req.Config != nil && req.Config.SystemInstruction != nil {
			for _, p := range req.Config.SystemInstruction.Parts {
				b.WriteString(p.Text + "\n\n")
			}
		}
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				b.WriteString(p.Text + "\n")
			}
		}
		text, err := a.w.Ask(ctx, b.String()+"\nReply with the answer only, no tools.")
		if err != nil {
			yield(nil, err)
			return
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel), TurnComplete: true}, nil)
	}
}

func newLiveWorld(t *testing.T) *mailWorld {
	w := newMailWorld(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("LIVE_DEBUG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	w.exec.ACP = &acp.Worker{Log: log, Model: os.Getenv("LIVE_MODEL")}
	// The real router, through Claude Code too.
	w.eval.Router = &router.ACP{Prompt: prompts.Router, Ask: w.exec.ACP.Ask}
	w.exec.Reader = &quarantine.Reader{Model: func(context.Context) (model.LLM, error) { return askLLM{w.exec.ACP}, nil }}
	w.exec.UsesACP = func(store.Agent) bool { return true }
	w.exec.RunLimit = 10 * time.Minute
	// The real clock drives Claude Code; the test's clock drives the
	// server. Start it at today's date so the agent's sense of "Friday"
	// matches.
	now := time.Now().UTC().Truncate(time.Minute)
	w.clock = now
	return w
}

// turn sends a message in a chat and runs everything it starts, then
// returns what the agent said there since.
func (w *mailWorld) turn(t *testing.T, chat, text string) string {
	t.Helper()
	before := map[uint]bool{}
	var thread string
	_ = w.store.Tx(func(tx *store.Tx) error {
		ths, _ := tx.Threads("", 100)
		for _, th := range ths {
			v, _ := tx.ThreadView(th.ID)
			for _, e := range v.Effects {
				before[e.ID] = true
			}
		}
		return nil
	})
	// Every chat here is a direct chat with the mail agent, as in the app.
	if err := w.store.Tx(func(tx *store.Tx) error {
		_, err := tx.ChatThread(chat, "ag_mail", "", owner.String(), w.clock)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	res := w.say(owner, chat, text)
	thread = res.ThreadIDs[0]
	for {
		w.evalAll()
		if !w.workLive(t) {
			break
		}
	}
	return w.newSince(thread, before)
}

func (w *mailWorld) workLive(t *testing.T) bool {
	t.Helper()
	did, err := w.exec.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return did
}

func (w *mailWorld) newSince(thread string, before map[uint]bool) string {
	var b strings.Builder
	for _, e := range w.view(thread).Effects {
		if before[e.ID] {
			continue
		}
		b.WriteString("[" + e.Kind + "/" + e.Status + "] ")
		if e.Kind == "action" {
			b.WriteString(e.Action + " to " + e.Recipients + ": ")
		}
		b.WriteString(e.Body + "\n")
	}
	return b.String()
}

func (w *mailWorld) dumpBrain(t *testing.T) {
	t.Helper()
	_ = w.store.Tx(func(tx *store.Tx) error {
		facts, _ := tx.Facts("ag_mail", "", true, 100)
		for _, f := range facts {
			t.Logf("  fact f%d [%s/%s] %s: %s", f.ID, f.Kind, f.Source, f.Subject, f.Text)
		}
		ds, _ := tx.Diary("ag_mail", "", "", time.Time{}, 100)
		for _, d := range ds {
			t.Logf("  diary: %s | %s", d.Text, d.Outcome)
		}
		fs, _ := tx.FollowUps("ag_mail", "", []string{store.FollowUpOpen, store.FollowUpFired, store.FollowUpDone, store.FollowUpCancelled}, 100)
		for _, f := range fs {
			t.Logf("  follow-up %s [%s/%s] due %s: %s (outcome %q)", f.ID, f.Kind, f.State, f.DueAt.Format("Mon 2 Jan 15:04"), f.What, f.Outcome)
		}
		ins, _ := tx.Incidents("ag_mail", "", time.Time{}, 100)
		for _, in := range ins {
			t.Logf("  incident [%s/%s] %s note=%q", in.Cause, in.Key, in.Detail, in.Note)
		}
		return nil
	})
}

func TestLiveBrain(t *testing.T) {
	w := newLiveWorld(t)
	step := func(name, chat, text string) string {
		t.Logf("\n=== %s\n> %s", name, text)
		out := w.turn(t, chat, text)
		t.Logf("%s", out)
		return out
	}

	// 1. The owner teaches it.
	step("teach", "chat-mail", "Some things to remember: Lena Ortiz (lena@juniper.example) is the buyer at Juniper Bikes, and she signs contracts with DocuSign. And from now on, keep any email you draft for me to two or three short lines.")
	w.dumpBrain(t)
	var facts []store.BrainFact
	_ = w.store.Tx(func(tx *store.Tx) error { facts, _ = tx.Facts("ag_mail", "owner", false, 50); return nil })
	if len(facts) == 0 {
		t.Error("it remembered nothing")
	}

	// 2. A long run of unrelated messages in another chat.
	for i, m := range []string{
		"what's a good 2-day plan for Porto?",
		"which is better for a rainy day there, the Livraria Lello or the Serralves museum?",
		"give me a simple recipe for pastel de nata",
		"how long does the train from Porto to Lisbon take?",
		"write a two-line haiku about trains",
		"thanks, that's all for the trip",
	} {
		out := step("unrelated "+string(rune('1'+i)), "chat-trip", m)
		if strings.Contains(out, "Lena") || strings.Contains(out, "Juniper") || strings.Contains(out, "DocuSign") {
			t.Errorf("an unrelated answer brought up the Juniper memory: %s", out)
		}
	}

	// 3. It uses what it knows: a short email to Lena, for approval.
	out := step("email lena", "chat-mail", "Email Lena asking her to send the signed Juniper contract.")
	if !strings.Contains(out, "send_email to lena@juniper.example") {
		t.Fatalf("no email to Lena was proposed: %s", out)
	}
	w.approveAll()
	w.sendAll()
	w.dumpBrain(t)
	fs := w.followUps(store.FollowUpOpen)
	if len(fs) != 1 || fs[0].Kind != store.FollowUpReply {
		t.Fatalf("no reply check after the send: %+v", fs)
	}

	// 4. It recalls on request.
	out = step("recall", "chat-mail", "What do you know about Lena, and what are you waiting on?")
	if !strings.Contains(out, "DocuSign") || !(strings.Contains(strings.ToLower(out), "reply") || strings.Contains(strings.ToLower(out), "contract")) {
		t.Errorf("it didn't recall Lena or the reply it waits on: %s", out)
	}

	// 5. A reminder the owner asks for.
	out = step("remind", "chat-mail", "Remind me next Friday to renew the Juniper insurance.")
	w.dumpBrain(t)
	var task []store.BrainFollowUp
	for _, f := range w.followUps(store.FollowUpOpen) {
		if f.Kind == store.FollowUpTask {
			task = append(task, f)
		}
	}
	if len(task) != 1 || task[0].DueAt.Weekday() != time.Friday || !strings.Contains(out, "I'll come back to this on Fri") {
		t.Errorf("reminder = %+v; reply %s", task, out)
	}

	// 6. Three working days pass with no reply: it drafts a nudge.
	w.clock = w.followUps(store.FollowUpOpen)[0].DueAt.Add(time.Minute)
	for _, f := range w.followUps(store.FollowUpOpen) {
		if f.Kind == store.FollowUpReply {
			w.clock = f.DueAt.Add(time.Minute)
		}
	}
	if n := w.fire(); n != 1 {
		t.Fatalf("the reply check started %d tasks", n)
	}
	thread := w.followUps(store.FollowUpFired)[0].ThreadID
	before := map[uint]bool{}
	for _, e := range w.view(thread).Effects {
		before[e.ID] = true
	}
	for w.workLive(t) {
	}
	out = w.newSince(thread, before)
	t.Logf("\n=== nudge\n%s", out)
	w.dumpBrain(t)
	if !strings.Contains(out, "send_email to lena@juniper.example") {
		t.Errorf("no nudge drafted: %s", out)
	}

	// 7. A declined draft teaches it: the next draft follows the reason.
	if err := w.store.Tx(func(tx *store.Tx) error {
		es, _ := tx.AwaitingApproval()
		for _, e := range es {
			if _, err := outbox.Decide(tx, outbox.Decision{EffectID: e.ID, Approve: false, Note: "too pushy; just ask if she needs anything from us", DeviceID: "dv_owner", At: w.clock}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for w.workLive(t) {
	}
	out = step("after decline", "chat-mail", "Try the nudge to Lena again.")
	w.dumpBrain(t)
	_ = core.SourceApp
}
