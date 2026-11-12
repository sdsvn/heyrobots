package executor

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/router"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/usage"
)

// costly reports token use on every response, like Gemini does.
type costly struct{ *script }

func (c costly) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for resp, err := range c.script.GenerateContent(ctx, req, stream) {
			if resp != nil {
				resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 1000, CandidatesTokenCount: 100, ThoughtsTokenCount: 100}
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}

func (e *env) withLedger(budget float64) *usage.Ledger {
	l := &usage.Ledger{Store: e.store, Prices: usage.Prices{"script": {In: 1, Out: 10}}, Now: func() time.Time { return e.clock }}
	e.setPolicy(func(p *policy.Policy) { p.Limits.MonthlyBudgetUSD = budget; p.Limits.Timezone = "UTC" })
	e.exec.Model = router.Metered(func(context.Context) (model.LLM, error) { return costly{e.model}, nil }, l.Record)
	e.exec.BudgetStop = l.BudgetStop
	return l
}

func TestModelUseIsRecordedAgainstTheTask(t *testing.T) {
	e := newEnv(t)
	l := e.withLedger(100)
	e.model.steps = [][]*genai.Part{call("save_note", map[string]any{"title": "a", "text": "b"}), reportCall("ready", "done", "result", "")}
	res := e.say(owner, "c1", "a task")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	spend, err := e.store.TicketSpend(time.Time{}, []string{tk.ID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Each call is 1000 in at $1/M and 200 out at $10/M, 3000 micro-dollars; there are two.
	if spend[tk.ID] != 6000 {
		t.Fatalf("ticket spend %d micro-dollars, want 6000", spend[tk.ID])
	}
	s, _ := l.Month()
	if s.SpentUSD != 0.006 {
		t.Fatalf("month spend %v", s.SpentUSD)
	}
}

func TestAUsedUpBudgetStopsTheTaskAndAsks(t *testing.T) {
	e := newEnv(t)
	e.withLedger(0.004) // one call costs $0.003
	e.model.steps = [][]*genai.Part{
		call("save_note", map[string]any{"title": "a", "text": "b"}),
		call("save_note", map[string]any{"title": "c", "text": "d"}),
		reportCall("ready", "done", "result", ""),
	}
	res := e.say(owner, "c1", "a task")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	if !tk.Open || tk.Stage != string(core.StageQuestion) {
		t.Fatalf("ticket %+v, want open and asking", tk)
	}
	msgs := messages(e.view(res.ThreadIDs[0]))
	if len(msgs) != 1 || !strings.Contains(msgs[0], "model budget") || !strings.Contains(msgs[0], "Raise the monthly budget") {
		t.Fatalf("messages %q", msgs)
	}
	if e.model.n != 2 {
		t.Fatalf("model calls %d, want the third refused", e.model.n)
	}
	// After a raise and a reply, the task carries on.
	e.setPolicy(func(p *policy.Policy) { p.Limits.MonthlyBudgetUSD = 10 })
	e.say(owner, "c1", "raised it, carry on")
	e.evalAll()
	e.work()
	if tk := e.ticket(res.ThreadIDs[0]); tk.Stage != string(core.StageReady) {
		t.Fatalf("ticket stage %s after the raise", tk.Stage)
	}
}

func TestStoppingARunningTaskCancelsIt(t *testing.T) {
	e := newEnv(t)
	res := e.say(owner, "c1", "long task")
	e.evalAll()
	tkID := e.ticket(res.ThreadIDs[0]).ID
	started := make(chan struct{})
	wait, _ := functiontool.New(functiontool.Config{Name: "wait_long", Description: "x"}, func(tc agent.Context, _ struct{}) (map[string]any, error) {
		close(started)
		<-tc.Done()
		return nil, tc.Err()
	})
	e.exec.Extra = func(store.Ticket) []tool.Tool { return []tool.Tool{wait} }
	e.model.steps = [][]*genai.Part{call("wait_long", map[string]any{})}
	done := make(chan struct{})
	go func() { defer close(done); e.work() }()
	<-started
	if err := e.store.Tx(func(tx *store.Tx) error { return tx.SetTicketState(tkID, false, core.StageEnded, "app:owner", e.clock) }); err != nil {
		t.Fatal(err)
	}
	e.exec.CancelStopped()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped task kept running")
	}
	var as []store.Assignment
	e.store.Tx(func(tx *store.Tx) error { var err error; as, err = tx.Assignments(tkID); return err })
	if len(as) != 1 || as[0].Status != store.AssignmentEnded || as[0].State != "dropped" {
		t.Fatalf("assignment %+v", as)
	}
	if tk := e.ticket(res.ThreadIDs[0]); tk.Open || len(messages(e.view(res.ThreadIDs[0]))) != 0 {
		t.Fatalf("ticket %+v messages %q", tk, messages(e.view(res.ThreadIDs[0])))
	}
}

func TestProactiveResultsWaitForTheDigest(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{reportCall("ready", "Prices fell.", "Prices fell 5%.", "")}
	res := e.say(owner, "c1", "task")
	e.evalAll()
	tk := e.ticket(res.ThreadIDs[0])
	// Make the Ticket proactive.
	m, _ := tk.DecodeMandate()
	m.Strength = core.Inferred
	e.store.Tx(func(tx *store.Tx) error {
		return tx.CreateTicket(store.Ticket{ID: "tk_inferred", Open: true, Stage: string(core.StageWaiting), OriginThreadID: res.ThreadIDs[0]}, m, res.ThreadIDs[0], e.clock)
	})
	e.store.Tx(func(tx *store.Tx) error { return tx.SetTicketState(tk.ID, false, core.StageEnded, "", e.clock) })
	e.store.Tx(func(tx *store.Tx) error { return tx.AddTicketInput("tk_inferred", res.TriggerID, e.clock) })
	e.work()
	var held []store.Effect
	for _, ef := range e.view(res.ThreadIDs[0]).Effects {
		if ef.TicketID == "tk_inferred" {
			held = append(held, ef)
		}
	}
	if len(held) != 1 || held[0].Kind != "digest" || held[0].Status != store.EffectHeld {
		t.Fatalf("proactive result %+v, want held for the digest", held)
	}
}
