package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/taint"
)

// fakeSender records what it sends. errs are returned in turn, one per
// call; after them it succeeds.
type fakeSender struct {
	mu         sync.Mutex
	sent       []string
	errs       []error
	idempotent bool
}

func (f *fakeSender) Send(_ context.Context, e store.Effect) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return "", err
	}
	f.sent = append(f.sent, e.Body)
	return "ref-" + e.Body, nil
}

func (f *fakeSender) Idempotent() bool { return f.idempotent }

type env struct {
	t     *testing.T
	st    *store.Store
	ob    *Outbox
	mail  *fakeSender
	clock time.Time
	n     int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, st: st, mail: &fakeSender{}, clock: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	e.ob = &Outbox{
		Store:   st,
		Senders: map[string]Sender{"app": AppSender{}, "mail": e.mail},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return e.clock },
	}
	e.setPolicy(func(p *policy.Policy) { p.Limits.Timezone = "UTC" })
	return e
}

func (e *env) tx(fn func(tx *store.Tx) error) {
	e.t.Helper()
	if err := e.st.Tx(fn); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) setPolicy(change func(p *policy.Policy)) {
	e.tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		change(&p)
		_, err = tx.SavePolicy(p, e.clock)
		return err
	})
}

// ticket creates a Thread and an owner's Ticket on it.
func (e *env) ticket(source string, m core.Mandate) (threadID, ticketID string) {
	e.n++
	threadID, ticketID = "th"+string(rune('a'+e.n)), "tk"+string(rune('a'+e.n))
	if m.Role == "" {
		m = core.Mandate{Scope: "reply to Ann", Strength: core.Explicit, Role: core.RoleOwner, Requester: policy.AppOwner,
			Received: core.Received{Source: core.SourceKind(source)}, Toolsets: []core.Toolset{core.ToolsetMail}}
	}
	e.tx(func(tx *store.Tx) error {
		if err := tx.CreateThread(store.Thread{ID: threadID, Source: source, Key: threadID, CreatedBy: "app:owner", CreatedAt: e.clock, UpdatedAt: e.clock}); err != nil {
			return err
		}
		return tx.CreateTicket(store.Ticket{ID: ticketID, Open: true, Stage: string(core.StageWaiting), OriginThreadID: threadID}, m, threadID, e.clock)
	})
	return threadID, ticketID
}

func (e *env) effect(ef store.Effect) uint {
	if ef.Status == "" {
		ef.Status = store.EffectPending
	}
	if ef.Kind == "" {
		ef.Kind = "message"
	}
	if ef.Reach == "" {
		ef.Reach = string(core.ReachOwner)
	}
	ef.CreatedAt = e.clock
	e.tx(func(tx *store.Tx) error { return tx.CreateEffect(ef) })
	var id uint
	e.tx(func(tx *store.Tx) error {
		v, err := tx.ThreadView(ef.ThreadID)
		if err != nil {
			return err
		}
		id = v.Effects[len(v.Effects)-1].ID
		return nil
	})
	return id
}

// action queues an email to the given people.
func (e *env) action(threadID, ticketID, body string, approval string, to ...string) uint {
	d, _ := json.Marshal(taint.Details{Recipients: to})
	// Actions to other people only reach the outbox approved.
	if approval == "" {
		approval = store.ApprovalApproved
	}
	status := store.EffectPending
	if approval == store.ApprovalAwaiting {
		status = store.EffectAwaitingApproval
	}
	return e.effect(store.Effect{TicketID: ticketID, ThreadID: threadID, Source: "mail", Kind: "action", Action: "send_email",
		Reach: string(core.ReachOthers), Body: body, Recipients: strings.Join(to, ", "), Details: string(d), Status: status, Approval: approval})
}

func (e *env) get(id uint) store.Effect {
	var ef store.Effect
	e.tx(func(tx *store.Tx) error {
		var err error
		ef, err = tx.Effect(id)
		return err
	})
	return ef
}

func (e *env) drain() {
	e.t.Helper()
	if err := e.ob.Drain(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) notices() []string {
	var out []string
	e.tx(func(tx *store.Tx) error {
		th, err := tx.ThreadByKey("app", store.NoticesKey)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		v, err := tx.ThreadView(th.ID)
		for _, ef := range v.Effects {
			out = append(out, ef.Body)
		}
		return err
	})
	return out
}

func TestAppRepliesAreSentInOrder(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("app", core.Mandate{})
	a := e.effect(store.Effect{TicketID: tk, ThreadID: th, Source: "app", Body: "one"})
	b := e.effect(store.Effect{TicketID: tk, ThreadID: th, Source: "app", Body: "two"})
	var changed []string
	e.ob.OnChanged = func(id string) { changed = append(changed, id) }
	e.drain()
	ea, eb := e.get(a), e.get(b)
	if ea.Status != store.EffectSent || eb.Status != store.EffectSent {
		t.Fatalf("statuses %s %s, want both sent", ea.Status, eb.Status)
	}
	if len(changed) == 0 || changed[0] != th {
		t.Fatalf("the app wasn't told about the thread: %v", changed)
	}
}

func TestALaterEffectWaitsForAnEarlierOneOnItsThread(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	other, tk2 := e.ticket("mail", core.Mandate{})
	e.mail.errs = []error{errors.New("timeout")}
	first := e.action(th, tk, "first", "", "ann@example.com")
	second := e.action(th, tk, "second", "", "ann@example.com")
	elsewhere := e.action(other, tk2, "elsewhere", "", "ann@example.com")
	e.drain()
	if s := e.get(first); s.Status != store.EffectPending || s.NextAttemptAt == nil || s.Attempts != 1 {
		t.Fatalf("first should wait to retry: %+v", s)
	}
	if s := e.get(second).Status; s != store.EffectPending {
		t.Fatalf("second = %s, want pending behind first", s)
	}
	if s := e.get(elsewhere).Status; s != store.EffectSent {
		t.Fatalf("another thread's effect = %s, want sent", s)
	}
	e.clock = e.clock.Add(31 * time.Second)
	e.drain()
	if got := strings.Join(e.mail.sent, ","); got != "elsewhere,first,second" {
		t.Fatalf("sent %s, want elsewhere,first,second", got)
	}
}

func TestAPermanentErrorFailsAndTellsTheOwner(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	e.mail.errs = []error{Permanent(errors.New("mailbox full"))}
	id := e.action(th, tk, "hi", "", "ann@example.com")
	e.drain()
	if s := e.get(id); s.Status != store.EffectFailed || !strings.Contains(s.LastError, "mailbox full") {
		t.Fatalf("effect %+v, want failed", s)
	}
	n := e.notices()
	if len(n) != 1 || !strings.Contains(n[0], "couldn't send the send email to ann@example.com") {
		t.Fatalf("notices %q", n)
	}
	// The notice itself goes out through the app.
	e.drain()
	var v store.ThreadView
	e.tx(func(tx *store.Tx) error {
		th, _ := tx.ThreadByKey("app", store.NoticesKey)
		var err error
		v, err = tx.ThreadView(th.ID)
		return err
	})
	if v.Effects[0].Status != store.EffectSent {
		t.Fatalf("notice %s, want sent", v.Effects[0].Status)
	}
}

func TestRetriesStopAfterMaxAttempts(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	for range MaxAttempts {
		e.mail.errs = append(e.mail.errs, errors.New("down"))
	}
	id := e.action(th, tk, "hi", "", "ann@example.com")
	for range MaxAttempts {
		e.drain()
		e.clock = e.clock.Add(time.Hour)
	}
	if s := e.get(id); s.Status != store.EffectFailed || s.Attempts != MaxAttempts {
		t.Fatalf("effect %+v, want failed after %d attempts", s, MaxAttempts)
	}
	if len(e.notices()) != 1 {
		t.Fatal("the owner should hear once")
	}
}

func TestNoConnectedAccountFails(t *testing.T) {
	e := newEnv(t)
	delete(e.ob.Senders, "mail")
	th, tk := e.ticket("mail", core.Mandate{})
	id := e.action(th, tk, "hi", "", "ann@example.com")
	e.drain()
	if s := e.get(id); s.Status != store.EffectFailed || !strings.Contains(s.LastError, "no mail account") {
		t.Fatalf("effect %+v", s)
	}
}

func TestSendCapsHoldSendsUntilTheWindowPasses(t *testing.T) {
	e := newEnv(t)
	e.setPolicy(func(p *policy.Policy) { p.Limits.SendsPerHour = 2 })
	var idsOut []uint
	for _, body := range []string{"a", "b", "c"} {
		th, tk := e.ticket("mail", core.Mandate{})
		idsOut = append(idsOut, e.action(th, tk, body, "", "ann@example.com"))
	}
	// Messages to the owner in the app are never capped.
	th, tk := e.ticket("app", core.Mandate{})
	app := e.effect(store.Effect{TicketID: tk, ThreadID: th, Source: "app", Body: "note"})
	e.drain()
	if len(e.mail.sent) != 2 || e.get(app).Status != store.EffectSent {
		t.Fatalf("sent %v, app %s", e.mail.sent, e.get(app).Status)
	}
	held := e.get(idsOut[2])
	if held.Status != store.EffectPending || !strings.Contains(held.LastError, "hourly send cap") || held.NextAttemptAt == nil {
		t.Fatalf("third %+v, want held by the cap", held)
	}
	e.clock = e.clock.Add(30 * time.Minute)
	e.drain()
	if len(e.mail.sent) != 2 {
		t.Fatal("the cap was lifted too early")
	}
	e.clock = e.clock.Add(31 * time.Minute)
	e.drain()
	if len(e.mail.sent) != 3 {
		t.Fatalf("sent %v after the hour passed", e.mail.sent)
	}
}

func TestAStoppedTasksActionsAreCancelled(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	id := e.action(th, tk, "hi", "", "ann@example.com")
	e.tx(func(tx *store.Tx) error { return tx.SetTicketState(tk, false, core.StageEnded, "app:owner", e.clock) })
	e.drain()
	if s := e.get(id); s.Status != store.EffectCancelled {
		t.Fatalf("effect %s, want cancelled", s.Status)
	}
	if len(e.mail.sent) != 0 {
		t.Fatal("a stopped task's email was sent")
	}
}

// An action to other people that somehow reached the outbox without the
// owner's OK goes back for approval.
func TestAnUnapprovedActionGoesBackForApproval(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	id := e.effect(store.Effect{TicketID: tk, ThreadID: th, Source: "mail", Kind: "action", Action: "send_email",
		Reach: string(core.ReachOthers), Body: "hi", Recipients: "ann@example.com", Status: store.EffectPending})
	e.drain()
	if s := e.get(id); s.Status != store.EffectAwaitingApproval || s.Approval != store.ApprovalAwaiting {
		t.Fatalf("effect %+v, want awaiting approval", s)
	}
}

func TestARemovedDelegatesActionIsCancelled(t *testing.T) {
	e := newEnv(t)
	e.setPolicy(func(p *policy.Policy) {
		p.Delegates = []policy.Delegate{{Name: "sam", Identities: []core.Identity{policy.AppDelegate("sam")}, Toolsets: []core.Toolset{core.ToolsetMail}}}
	})
	th, tk := e.ticket("app", core.Mandate{Scope: "email Ann", Strength: core.Explicit, Role: core.RoleDelegate, Delegate: "sam",
		Requester: policy.AppDelegate("sam"), Received: core.Received{Source: core.SourceApp}})
	id := e.action(th, tk, "hi", store.ApprovalAwaiting, "ann@example.com")
	e.tx(func(tx *store.Tx) error {
		_, err := Decide(tx, Decision{EffectID: id, Approve: true, DeviceID: "dv1", At: e.clock})
		return err
	})
	e.setPolicy(func(p *policy.Policy) { p.Delegates = nil })
	e.drain()
	if s := e.get(id); s.Status != store.EffectCancelled || !strings.Contains(s.LastError, "no longer allowed") {
		t.Fatalf("effect %+v, want cancelled", s)
	}
	if n := e.notices(); len(n) != 1 || !strings.Contains(n[0], "I didn't send") {
		t.Fatalf("notices %q", n)
	}
}

func TestApprovalSendsTheAction(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	id := e.action(th, tk, "hi", store.ApprovalAwaiting, "bob@example.com")
	e.drain()
	if len(e.mail.sent) != 0 {
		t.Fatal("an action awaiting approval was sent")
	}
	e.tx(func(tx *store.Tx) error {
		ef, err := Decide(tx, Decision{EffectID: id, Approve: true, DeviceID: "dv1", At: e.clock})
		if err == nil && (ef.Approval != store.ApprovalApproved || ef.DecidedBy != "dv1") {
			t.Errorf("decided %+v", ef)
		}
		return err
	})
	e.drain()
	if s := e.get(id); s.Status != store.EffectSent || s.Ref != "ref-hi" {
		t.Fatalf("effect %+v, want sent", s)
	}
	e.tx(func(tx *store.Tx) error {
		_, err := Decide(tx, Decision{EffectID: id, Approve: true, DeviceID: "dv1", At: e.clock})
		if !errors.Is(err, ErrNotAwaiting) {
			t.Errorf("deciding twice: %v", err)
		}
		return nil
	})
}

func TestRejectionTellsTheTaskWithoutTrustingTheRecipient(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	id := e.action(th, tk, "hi", store.ApprovalAwaiting, "mallory@evil.example")
	e.tx(func(tx *store.Tx) error {
		_, err := Decide(tx, Decision{EffectID: id, Approve: false, DeviceID: "dv1", Note: "Ask Ann first.", At: e.clock})
		return err
	})
	e.drain()
	if s := e.get(id); s.Status != store.EffectRejected || len(e.mail.sent) != 0 {
		t.Fatalf("effect %s, sent %v", s.Status, e.mail.sent)
	}
	var inputs []store.TicketInput
	var v store.ThreadView
	e.tx(func(tx *store.Tx) error {
		var err error
		if inputs, err = tx.TicketInputs(tk); err != nil {
			return err
		}
		v, err = tx.ThreadView(th)
		return err
	})
	if len(inputs) != 1 || len(v.Triggers) != 1 {
		t.Fatalf("inputs %d, triggers %d, want the rejection delivered once", len(inputs), len(v.Triggers))
	}
	tr := v.Triggers[0]
	if strings.Contains(tr.Text, "mallory") || !strings.Contains(tr.Text, "Ask Ann first.") || tr.EvalState != store.EvalDone {
		t.Fatalf("rejection record %+v", tr)
	}
}

func TestRecoveryRetriesOnlyIdempotentSends(t *testing.T) {
	e := newEnv(t)
	th, tk := e.ticket("mail", core.Mandate{})
	id := e.action(th, tk, "hi", "", "ann@example.com")
	ath, atk := e.ticket("app", core.Mandate{})
	app := e.effect(store.Effect{TicketID: atk, ThreadID: ath, Source: "app", Body: "note"})
	// The server crashed while sending both.
	lease := e.clock.Add(-time.Minute)
	for _, x := range []uint{id, app} {
		e.tx(func(tx *store.Tx) error {
			_, err := tx.UpdateEffect(x, store.EffectPending, map[string]any{"status": store.EffectSending, "lease_until": lease, "attempts": 1})
			return err
		})
	}
	if err := e.ob.Recover(); err != nil {
		t.Fatal(err)
	}
	if s := e.get(id).Status; s != store.EffectUncertain {
		t.Fatalf("email %s, want uncertain", s)
	}
	if n := e.notices(); len(n) != 1 || !strings.Contains(n[0], "can't tell whether it went out") {
		t.Fatalf("notices %q", n)
	}
	e.drain()
	if s := e.get(app).Status; s != store.EffectSent {
		t.Fatalf("app note %s, want sent again", s)
	}
	if len(e.mail.sent) != 0 {
		t.Fatal("an uncertain email was sent again")
	}
}

func TestDigestCollectsHeldResultsOnceADay(t *testing.T) {
	e := newEnv(t)
	e.clock = time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)
	th, tk := e.ticket("app", core.Mandate{Scope: "watch prices", Strength: core.Inferred, Role: core.RoleOwner, Requester: policy.AppOwner})
	a := e.effect(store.Effect{TicketID: tk, ThreadID: th, Source: "app", Kind: "digest", Body: "Prices fell 5%."})
	e.drain()
	if s := e.get(a).Status; s != store.EffectHeld {
		t.Fatalf("digest effect %s, want held", s)
	}
	if d, err := e.ob.Digest(); err != nil || d != nil {
		t.Fatalf("digest before its hour: %v %v", d, err)
	}
	e.clock = time.Date(2026, 11, 11, 8, 5, 0, 0, time.UTC)
	var got []store.Digest
	e.ob.OnDigest = func(d store.Digest) { got = append(got, d) }
	d, err := e.ob.Digest()
	if err != nil || d == nil || !strings.Contains(d.Body, "## watch prices") || !strings.Contains(d.Body, "Prices fell 5%.") || d.Count != 1 {
		t.Fatalf("digest %+v %v", d, err)
	}
	if s := e.get(a); s.Status != store.EffectSent || s.DigestID != d.ID {
		t.Fatalf("effect %+v, want sent in the digest", s)
	}
	// Something new the same day waits for tomorrow.
	b := e.effect(store.Effect{TicketID: tk, ThreadID: th, Source: "app", Kind: "digest", Status: store.EffectHeld, Body: "Again."})
	e.clock = e.clock.Add(3 * time.Hour)
	if d, _ := e.ob.Digest(); d != nil {
		t.Fatal("a second digest on the same day")
	}
	e.clock = e.clock.Add(21 * time.Hour)
	if d, _ := e.ob.Digest(); d == nil || e.get(b).Status != store.EffectSent {
		t.Fatal("the next day's digest didn't come")
	}
	if len(got) != 2 {
		t.Fatalf("OnDigest called %d times", len(got))
	}
}

func TestBackoffGrowsToAnHour(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Fatalf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	if backoff(20) != time.Hour {
		t.Fatal("backoff should stop at an hour")
	}
}
