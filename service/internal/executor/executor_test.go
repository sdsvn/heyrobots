package executor

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session/database"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/taint"
	"heyrobots/service/internal/toolsets"
	"heyrobots/service/prompts"
)

// script is a fake model. Each call returns the next step's parts; after
// the last step it ends the turn with text.
type script struct {
	mu    sync.Mutex
	steps [][]*genai.Part
	n     int
	users []string
	block bool
	// systems and tools record each call's instruction and tool names.
	systems []string
	tools   [][]string
	// results records the tools' answers the model was given.
	results []map[string]any
}

func (s *script) Name() string { return "script" }

func (s *script) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		s.mu.Lock()
		if s.block {
			s.mu.Unlock()
			<-ctx.Done()
			yield(nil, ctx.Err())
			return
		}
		if req.Config != nil && req.Config.SystemInstruction != nil {
			var sys strings.Builder
			for _, p := range req.Config.SystemInstruction.Parts {
				sys.WriteString(p.Text)
			}
			s.systems = append(s.systems, sys.String())
		}
		var names []string
		for name := range req.Tools {
			names = append(names, name)
		}
		s.tools = append(s.tools, names)
		for _, p := range req.Contents[len(req.Contents)-1].Parts {
			if p.FunctionResponse != nil {
				s.results = append(s.results, p.FunctionResponse.Response)
			}
		}
		if last := req.Contents[len(req.Contents)-1]; last.Role == genai.RoleUser && len(last.Parts) > 0 && last.Parts[0].Text != "" {
			s.users = append(s.users, last.Parts[0].Text)
		}
		var parts []*genai.Part
		if s.n < len(s.steps) {
			parts = s.steps[s.n]
		} else {
			parts = []*genai.Part{{Text: "done"}}
		}
		s.n++
		s.mu.Unlock()
		yield(&model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: parts}, TurnComplete: true}, nil)
	}
}

func call(name string, args map[string]any) []*genai.Part {
	return []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: name + "-" + strings.Repeat("x", len(args)), Name: name, Args: args}}}
}

func reportCall(state, summary, result, question string) []*genai.Part {
	return call("report", map[string]any{"state": state, "summary": summary, "result": result, "human_action": question})
}

type env struct {
	t      *testing.T
	store  *store.Store
	intake *intake.Intake
	eval   *eval.Engine
	exec   *Engine
	model  *script
	clock  time.Time
	n      int
}

type acceptRouter struct{ toolsets []core.Toolset }

func (a acceptRouter) Route(_ context.Context, c eval.Candidate) ([]eval.Route, error) {
	var out []eval.Route
	for _, t := range c.Triggers {
		if t.Owned {
			out = append(out, eval.Route{Action: eval.ActionIgnore, TriggerIDs: []string{t.ID}, Reason: "test"})
			continue
		}
		out = append(out, eval.Route{Action: eval.ActionAccept, TriggerIDs: []string{t.ID}, Scope: t.Text, Toolsets: a.toolsets})
	}
	return out, nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sessions, err := database.NewSessionService(sqlite.Open(st.DSN()), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(sessions); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, store: st, model: &script{}, clock: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	now := func() time.Time { return e.clock }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.intake = intake.New(st, now)
	e.eval = &eval.Engine{Store: st, Router: acceptRouter{[]core.Toolset{core.ToolsetWriting}}, Log: log, Now: now}
	e.exec = &Engine{
		Store: st, Sessions: sessions, Prompt: prompts.Executor, Log: log, Now: now,
		Model:      func(context.Context) (model.LLM, error) { return e.model, nil },
		Workspaces: filepath.Join(dir, "workspaces"),
	}
	e.setPolicy(func(p *policy.Policy) {
		p.Toolsets = []core.Toolset{core.ToolsetResearch, core.ToolsetWriting}
		p.Delegates = []policy.Delegate{{Name: "partner", Identities: []core.Identity{policy.AppDelegate("partner")}, Toolsets: []core.Toolset{core.ToolsetWriting}}}
	})
	return e
}

func (e *env) setPolicy(change func(p *policy.Policy)) {
	e.t.Helper()
	if err := e.store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		change(&p)
		_, err = tx.SavePolicy(p, e.clock)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) say(who core.Identity, key, text string) intake.Result {
	e.t.Helper()
	e.n++
	res, err := e.intake.Ingest(intake.Message{
		Trigger:    core.Trigger{DeliveryID: key + "-" + strings.Repeat("m", e.n), Source: core.SourceApp, Actor: who, Authenticity: core.Verified, Addressed: true, Text: text, At: e.clock},
		ThreadKeys: []string{key}, CreatedBy: who,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.clock = e.clock.Add(time.Second)
	return res
}

func (e *env) evalAll() {
	e.t.Helper()
	for {
		did, err := e.eval.RunOnce(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if !did {
			return
		}
	}
}

func (e *env) work() bool {
	e.t.Helper()
	did, err := e.exec.RunOnce(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return did
}

func (e *env) view(threadID string) store.ThreadView {
	e.t.Helper()
	var v store.ThreadView
	if err := e.store.Tx(func(tx *store.Tx) error { var err error; v, err = tx.ThreadView(threadID); return err }); err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) ticket(threadID string) store.Ticket {
	e.t.Helper()
	v := e.view(threadID)
	if len(v.Tickets) != 1 {
		e.t.Fatalf("tickets = %d", len(v.Tickets))
	}
	return v.Tickets[0]
}

func messages(v store.ThreadView) []string {
	var out []string
	for _, ef := range v.Effects {
		out = append(out, ef.Body)
	}
	return out
}

var owner, delegate = policy.AppOwner, policy.AppDelegate("partner")

func TestReadyReportEndsTheTaskAndSendsTheResult(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{
		call("write_file", map[string]any{"path": "drafts/thanks.md", "content": "Dear Anna, thank you!"}),
		reportCall("ready", "Wrote the thank-you note.", "Draft in drafts/thanks.md: \"Dear Anna, thank you!\"", ""),
	}
	res := e.say(owner, "c1", "draft a thank-you note to Anna")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	tk := e.ticket(res.ThreadIDs[0])
	if tk.Open || tk.Stage != string(core.StageReady) {
		t.Fatalf("ticket = %+v", tk)
	}
	b, err := os.ReadFile(filepath.Join(e.exec.Workspaces, tk.ID, "drafts", "thanks.md"))
	if err != nil || string(b) != "Dear Anna, thank you!" {
		t.Fatalf("file = %q, %v", b, err)
	}
	msgs := messages(e.view(res.ThreadIDs[0]))
	if len(msgs) != 1 || !strings.Contains(msgs[0], "Dear Anna, thank you!") || !strings.Contains(msgs[0], "Wrote the thank-you note.") {
		t.Fatalf("messages = %q", msgs)
	}
	if !strings.Contains(e.model.users[0], "draft a thank-you note to Anna") {
		t.Fatalf("the worker didn't get the request: %q", e.model.users[0])
	}
	if e.work() {
		t.Fatal("a finished task ran again")
	}
}

func TestReadyNeedsAResult(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{
		reportCall("ready", "done", "", ""),
		reportCall("ready", "done", "The answer.", ""),
	}
	res := e.say(owner, "c1", "answer me")
	e.evalAll()
	e.work()
	if msgs := messages(e.view(res.ThreadIDs[0])); len(msgs) != 1 || !strings.Contains(msgs[0], "The answer.") {
		t.Fatalf("messages = %q", msgs)
	}
}

func TestWaitingThenAFollowUpStartsAFollowUpAssignment(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{reportCall("waiting", "waiting for the guest list", "", "")}
	res := e.say(owner, "c1", "plan seating for dinner")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	if !tk.Open || tk.Stage != string(core.StageWaiting) || len(messages(e.view(res.ThreadIDs[0]))) != 0 {
		t.Fatalf("waiting: ticket %+v messages %q", tk, messages(e.view(res.ThreadIDs[0])))
	}
	e.model.steps = append(e.model.steps, reportCall("ready", "Seated everyone.", "Anna next to Ben.", ""))
	if r := e.say(owner, "c1", "guests are Anna and Ben"); !r.Owned {
		t.Fatal("follow-up not delivered to the task")
	}
	e.evalAll()
	e.work()
	if len(e.model.users) != 2 || strings.Contains(e.model.users[1], "plan seating") || !strings.Contains(e.model.users[1], "guests are Anna and Ben") {
		t.Fatalf("second turn input = %q", e.model.users)
	}
	var as []store.Assignment
	e.store.Tx(func(tx *store.Tx) error { as, _ = tx.Assignments(tk.ID); return nil })
	if len(as) != 2 || as[0].Kind != store.AssignmentNew || as[1].Kind != store.AssignmentFollowUp {
		t.Fatalf("assignments = %+v", as)
	}
}

func TestBlockedWithAQuestionAsksThePerson(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{reportCall("blocked", "I need the date.", "", "Which date should the invitation give?")}
	res := e.say(owner, "c1", "write the party invitation")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	msgs := messages(e.view(res.ThreadIDs[0]))
	if !tk.Open || tk.Stage != string(core.StageQuestion) || len(msgs) != 1 || !strings.Contains(msgs[0], "Which date") {
		t.Fatalf("ticket %+v messages %q", tk, msgs)
	}
}

func TestAQuestionCanOfferAnswersToTap(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{call("report", map[string]any{"state": "waiting", "summary": "Found two flights.",
		"human_action": "Window or aisle?", "choices": []any{" Window ", "Aisle", "aisle", "", strings.Repeat("x", 61), "No\npreference"}})}
	res := e.say(owner, "c1", "book my Lisbon flight")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	if got := tk.ChoiceList(); tk.Stage != string(core.StageQuestion) || strings.Join(got, "|") != "Window|Aisle|No preference" {
		t.Fatalf("stage %s choices %q", tk.Stage, got)
	}

	// Answering moves the task on, and the choices go with the question.
	if r := e.say(owner, "c1", "Aisle"); !r.Owned {
		t.Fatal("the answer didn't go to the task")
	}
	if tk := e.ticket(res.ThreadIDs[0]); tk.Choices != "" || tk.Stage == string(core.StageQuestion) {
		t.Fatalf("after the answer: %+v", tk)
	}
}

func TestOneChoiceIsNoChoice(t *testing.T) {
	if got := cleanChoices([]string{"Yes", "yes", "  "}); got != nil {
		t.Fatalf("got %q", got)
	}
	if got := cleanChoices([]string{"a", "b", "c", "d", "e", "f"}); len(got) != 5 {
		t.Fatalf("got %q", got)
	}
}

func TestHandBackReturnsAMessageToEval(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{reportCall("waiting", "drafting", "", "")}
	res := e.say(owner, "c1", "draft a toast for the wedding")
	e.evalAll()
	e.work()
	follow := e.say(owner, "c1", "also book me a flight to Rome")
	e.evalAll() // the owned follow-up gets no new work
	e.model.steps = append(e.model.steps,
		call("hand_back", map[string]any{"message_ids": []any{follow.TriggerID}, "reason": "flights are not part of the toast"}),
		reportCall("waiting", "still drafting", "", ""))
	e.work()
	var tr store.Trigger
	e.store.Tx(func(tx *store.Tx) error {
		v, _ := tx.ThreadView(res.ThreadIDs[0])
		for _, x := range v.Triggers {
			if x.ID == follow.TriggerID {
				tr = x
			}
		}
		return nil
	})
	if tr.Owned || tr.EvalState != store.EvalPending || tr.HandedBackFrom != e.ticket(res.ThreadIDs[0]).ID || tr.HandbackReason == "" {
		t.Fatalf("handed-back trigger = %+v", tr)
	}
	// Eval sees it again with the reason, and accepts it as separate work.
	e.eval.Router = acceptRouter{[]core.Toolset{core.ToolsetResearch}}
	e.evalAll()
	if v := e.view(res.ThreadIDs[0]); len(v.Tickets) != 2 {
		t.Fatalf("tickets after hand-back = %d, want a new one", len(v.Tickets))
	}
}

func TestHandBackOnlyAcceptsThisTurnsMessages(t *testing.T) {
	e := newEnv(t)
	e.say(owner, "c1", "task")
	e.evalAll()
	// Another conversation's message that belongs to no task.
	other := e.say(owner, "c2", "unrelated")
	e.model.steps = [][]*genai.Part{
		call("hand_back", map[string]any{"message_ids": []any{other.TriggerID}, "reason": "x"}),
		reportCall("waiting", "", "", ""),
	}
	e.work()
	var tr store.Trigger
	e.store.Tx(func(tx *store.Tx) error { tr, _ = tx.TriggerByDelivery("app", "c2-mm"); return nil })
	if tr.ID != other.TriggerID {
		t.Fatalf("lookup found %q, want %q", tr.ID, other.TriggerID)
	}
	if tr.HandedBackFrom != "" {
		t.Fatal("a message from another conversation was handed back")
	}
}

func TestRunLimitEndsAStuckTurn(t *testing.T) {
	e := newEnv(t)
	e.model.block = true
	e.exec.RunLimit = 200 * time.Millisecond
	res := e.say(owner, "c1", "think forever")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	msgs := messages(e.view(res.ThreadIDs[0]))
	if tk.Stage != string(core.StageBlocked) || len(msgs) != 1 || !strings.Contains(msgs[0], "ran out of time") {
		t.Fatalf("ticket %+v messages %q", tk, msgs)
	}
}

func TestModelCallLimit(t *testing.T) {
	e := newEnv(t)
	e.exec.MaxCalls = 2
	for range 5 {
		e.model.steps = append(e.model.steps, call("list_files", map[string]any{}))
	}
	res := e.say(owner, "c1", "loop")
	e.evalAll()
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	if tk.Stage != string(core.StageFailed) || !strings.Contains(messages(e.view(res.ThreadIDs[0]))[0], "limit") {
		t.Fatalf("ticket %+v messages %q", tk, messages(e.view(res.ThreadIDs[0])))
	}
}

func TestEndingWithoutAReportIsReportedHonestly(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{{{Text: "I think I'm done."}}}
	res := e.say(owner, "c1", "do it")
	e.evalAll()
	e.work()
	if tk := e.ticket(res.ThreadIDs[0]); tk.Stage != string(core.StageBlocked) || !strings.Contains(messages(e.view(res.ThreadIDs[0]))[0], "didn't say why") {
		t.Fatalf("ticket %+v", tk)
	}
}

func TestInterruptedAssignmentIsRecoveredWithANote(t *testing.T) {
	e := newEnv(t)
	res := e.say(owner, "c1", "write a poem")
	e.evalAll()
	// A crash: the assignment is claimed and never finishes.
	e.store.Tx(func(tx *store.Tx) error {
		_, err := tx.ClaimAssignment("as_crashed", e.clock, time.Minute, true, true)
		return err
	})
	e.clock = e.clock.Add(2 * time.Minute)
	e.model.steps = [][]*genai.Part{reportCall("ready", "Wrote it.", "Roses are red.", "")}
	if !e.work() {
		t.Fatal("the interrupted task was not picked up again")
	}
	if !strings.Contains(e.model.users[0], "was interrupted") {
		t.Fatalf("no recovery note: %q", e.model.users[0])
	}
	var as []store.Assignment
	tkID := e.ticket(res.ThreadIDs[0]).ID
	e.store.Tx(func(tx *store.Tx) error { as, _ = tx.Assignments(tkID); return nil })
	if len(as) != 2 || as[0].State != store.StateInterrupted || !as[1].Recovered {
		t.Fatalf("assignments = %+v", as)
	}
}

func TestReservedSlotIsForFollowUpsOnly(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{reportCall("waiting", "", "", "")}
	first := e.say(owner, "c1", "task one")
	e.evalAll()
	e.work()
	e.say(owner, "c2", "task two") // new work
	e.say(owner, "c1", "more for task one")
	e.evalAll()
	_ = first
	var c store.Claimed
	err := e.store.Tx(func(tx *store.Tx) error {
		var err error
		c, err = tx.ClaimAssignment("as_x", e.clock, time.Minute, false, true)
		return err
	})
	if err != nil || c.Assignment.Kind != store.AssignmentFollowUp || c.Inputs[0].Text != "more for task one" {
		t.Fatalf("follow-up-only claim = %+v, %v", c.Assignment, err)
	}
	err = e.store.Tx(func(tx *store.Tx) error {
		_, err := tx.ClaimAssignment("as_y", e.clock, time.Minute, false, true)
		return err
	})
	if err != store.ErrNotFound {
		t.Fatalf("new work took the reserved slot: %v", err)
	}
}

func TestATaskStoppedWhileRunningStaysStopped(t *testing.T) {
	e := newEnv(t)
	res := e.say(owner, "c1", "long task")
	e.evalAll()
	tkID := e.ticket(res.ThreadIDs[0]).ID
	stopper, _ := functiontool.New(functiontool.Config{Name: "slow_step", Description: "x"}, func(agent.Context, struct{}) (map[string]any, error) {
		e.store.Tx(func(tx *store.Tx) error { return tx.SetTicketState(tkID, false, core.StageEnded, "app:owner", e.clock) })
		return map[string]any{"ok": true}, nil
	})
	e.exec.Extra = func(store.Ticket) []tool.Tool { return []tool.Tool{stopper} }
	e.model.steps = [][]*genai.Part{call("slow_step", map[string]any{}), reportCall("ready", "done", "result", "")}
	e.work()
	tk := e.ticket(res.ThreadIDs[0])
	if tk.Open || tk.StoppedBy != "app:owner" || len(messages(e.view(res.ThreadIDs[0]))) != 0 {
		t.Fatalf("ticket %+v messages %q", tk, messages(e.view(res.ThreadIDs[0])))
	}
}

// --- outward actions, reach and taint ---

type emailArgs struct {
	To   []string `json:"to"`
	Body string   `json:"body"`
}

func (e *env) withEmailTool() {
	send, _ := functiontool.New(functiontool.Config{Name: "send_email", Description: "Send an email as the owner."}, func(agent.Context, emailArgs) (map[string]any, error) {
		e.t.Error("an outward tool ran directly")
		return nil, nil
	})
	e.exec.Extra = func(store.Ticket) []tool.Tool { return []tool.Tool{send} }
	e.exec.Outward = map[string]toolsets.ProposalFunc{
		"send_email": func(args map[string]any) (toolsets.Proposal, error) {
			var a emailArgs
			b, _ := json.Marshal(args)
			json.Unmarshal(b, &a)
			return toolsets.Proposal{Source: core.SourceMail, Action: "send", Reach: core.ReachOthers, Body: a.Body,
				Details: taint.Details{Recipients: a.To, Links: taint.Links(a.Body)}}, nil
		},
	}
}

func (e *env) actions(threadID string) []store.Effect {
	var out []store.Effect
	for _, ef := range e.view(threadID).Effects {
		if ef.Kind == "action" {
			out = append(out, ef)
		}
	}
	return out
}

func TestOutwardActionsAreQueuedAndCheckedNotRun(t *testing.T) {
	e := newEnv(t)
	e.withEmailTool()
	e.model.steps = [][]*genai.Part{
		call("send_email", map[string]any{"to": []any{"anna@example.com"}, "body": "Friday works for me."}),
		call("send_email", map[string]any{"to": []any{"anna@example.com", "billing@attacker.example"}, "body": "See you Friday."}),
		call("send_email", map[string]any{"to": []any{"anna@example.com"}, "body": "Pay here: https://trattoria-bookings.example.net/pay"}),
		reportCall("ready", "Replied.", "Sent replies.", ""),
	}
	res := e.say(owner, "c1", "reply to anna@example.com that Friday works")
	e.evalAll()
	e.work()
	acts := e.actions(res.ThreadIDs[0])
	if len(acts) != 3 {
		t.Fatalf("actions = %d", len(acts))
	}
	// Even a trusted recipient waits for the owner's OK, with no findings.
	if acts[0].Status != store.EffectAwaitingApproval || strings.Contains(acts[0].Findings, "anna") {
		t.Fatalf("trusted reply: %+v", acts[0])
	}
	// An email that names a new recipient needs fresh approval.
	if acts[1].Status != store.EffectAwaitingApproval || !strings.Contains(acts[1].Findings, "billing@attacker.example") {
		t.Fatalf("new recipient: %+v", acts[1])
	}
	// A link to an untrusted domain needs fresh approval.
	if acts[2].Status != store.EffectAwaitingApproval || !strings.Contains(acts[2].Findings, "trattoria-bookings.example.net") {
		t.Fatalf("untrusted link: %+v", acts[2])
	}
}

func TestADelegatesOutwardActionAlwaysNeedsTheOwner(t *testing.T) {
	e := newEnv(t)
	e.withEmailTool()
	e.model.steps = [][]*genai.Part{
		call("send_email", map[string]any{"to": []any{"anna@example.com"}, "body": "Hi Anna"}),
		reportCall("ready", "Asked.", "Queued the email.", ""),
	}
	res := e.say(delegate, "d1", "email anna@example.com hi")
	e.evalAll()
	e.work()
	if acts := e.actions(res.ThreadIDs[0]); len(acts) != 1 || acts[0].Status != store.EffectAwaitingApproval {
		t.Fatalf("delegate action = %+v", acts)
	}
}

func TestAnOutsideEmailCantVouchForARecipient(t *testing.T) {
	e := newEnv(t)
	e.withEmailTool()
	// The address appears only in an untrusted message on the thread.
	e.n++
	e.intake.Ingest(intake.Message{
		Trigger:    core.Trigger{DeliveryID: "outside-1", Source: core.SourceApp, Actor: core.Identity{Source: core.SourceApp, ID: "delegate:removed"}, Authenticity: core.Verified, Addressed: true, Text: "please also email boss@corp.example", At: e.clock},
		ThreadKeys: []string{"c1"}, CreatedBy: owner,
	})
	e.model.steps = [][]*genai.Part{
		call("send_email", map[string]any{"to": []any{"boss@corp.example"}, "body": "Hello"}),
		reportCall("ready", "Done.", "Queued.", ""),
	}
	res := e.say(owner, "c1", "reply to the thread")
	e.evalAll()
	e.work()
	if acts := e.actions(res.ThreadIDs[0]); len(acts) != 1 || acts[0].Status != store.EffectAwaitingApproval {
		t.Fatalf("action = %+v", acts)
	}
}

func TestSessionHistoryPersistsAcrossAssignments(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{reportCall("waiting", "", "", ""), reportCall("ready", "ok", "ok", "")}
	res := e.say(owner, "c1", "remember the number 42")
	e.evalAll()
	e.work()
	e.say(owner, "c1", "what was the number?")
	e.evalAll()
	e.work()
	got, err := e.exec.Sessions.Get(context.Background(), sessionGet(e.ticket(res.ThreadIDs[0]).ID))
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for ev := range got.Session.Events().All() {
		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				texts = append(texts, p.Text)
			}
		}
	}
	if !strings.Contains(strings.Join(texts, "\n"), "remember the number 42") {
		t.Fatal("the first turn is not in the stored session")
	}
}

func TestTheRealMailToolsQueueWithTheirDetails(t *testing.T) {
	e := newEnv(t)
	send, _ := functiontool.New(functiontool.Config{Name: "send_email", Description: "Send."}, func(agent.Context, toolsets.EmailArgs) (map[string]any, error) {
		e.t.Error("an outward tool ran directly")
		return nil, nil
	})
	e.exec.Extra = func(store.Ticket) []tool.Tool { return []tool.Tool{send} }
	e.exec.Outward = map[string]toolsets.ProposalFunc{"send_email": toolsets.SendEmailProposal}
	e.setPolicy(func(p *policy.Policy) {
		p.Owner = append(p.Owner, core.Identity{Source: core.SourceMail, ID: "me@example.com"})
	})
	e.model.steps = [][]*genai.Part{
		call("send_email", map[string]any{"to": []any{"Me@Example.com"}, "subject": "Notes", "body": "Your notes."}),
		call("send_email", map[string]any{"to": []any{"bob@example.com"}, "subject": "Hi", "body": "Hello Bob", "reply_to_message_id": "m9"}),
		reportCall("ready", "Done.", "Queued.", ""),
	}
	res := e.say(owner, "c1", "mail my notes to me, and say hello to bob@example.com")
	e.evalAll()
	e.work()
	acts := e.actions(res.ThreadIDs[0])
	if len(acts) != 2 {
		t.Fatalf("actions %d", len(acts))
	}
	// Mail to the owner's own proven address reaches only the owner.
	if acts[0].Reach != "owner" || acts[0].Status != store.EffectPending {
		t.Fatalf("to self: %+v", acts[0])
	}
	// Mail to someone else waits for approval, and keeps what the sender needs.
	if acts[1].Reach != "others" || acts[1].Status != store.EffectAwaitingApproval ||
		!strings.Contains(acts[1].Payload, `"subject":"Hi"`) || !strings.Contains(acts[1].Payload, `"reply_to_message_id":"m9"`) {
		t.Fatalf("to bob: %+v", acts[1])
	}
}
