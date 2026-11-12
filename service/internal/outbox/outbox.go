// Package outbox sends what the Assistant queued: replies, notices and
// outward actions. Every Effect was written in the same transaction as the
// decision behind it; this package delivers it later, in order per Thread,
// with retries, send caps and a last check of authority just before
// sending. It also puts together the daily digest and recovers Effects the
// server was sending when it stopped.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gate"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/taint"
	"heyrobots/service/internal/telemetry"
)

// Sender delivers Effects through one Source.
type Sender interface {
	// Send delivers the Effect and returns the Source's own id for it, if
	// it has one. Wrap an error with Permanent when retrying can't help.
	Send(ctx context.Context, e store.Effect) (ref string, err error)
	// Idempotent reports whether sending the same Effect twice is safe, for
	// example because the Source drops duplicates by the Effect's id. After
	// a crash mid-send, only an idempotent Sender is retried; otherwise the
	// owner is asked to check.
	Idempotent() bool
}

type permanentError struct{ error }

func (p permanentError) Unwrap() error { return p.error }

// Permanent marks a send error that retrying can't fix.
func Permanent(err error) error { return permanentError{err} }

type uncertainError struct{ error }

func (u uncertainError) Unwrap() error { return u.error }

// Uncertain marks a send error after which nobody can tell whether the
// action happened, such as a timeout once the request was sent. The Effect
// is never retried; the owner is asked to check.
func Uncertain(err error) error { return uncertainError{err} }

// IsUncertain reports whether err was marked Uncertain.
func IsUncertain(err error) bool {
	var u uncertainError
	return errors.As(err, &u)
}

// IsPermanent reports whether err was marked Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// AppSender delivers to the companion app. The message is already stored
// in its Thread, so delivering it only marks it sent; the app hears through
// the event stream.
type AppSender struct{}

func (AppSender) Send(context.Context, store.Effect) (string, error) { return "", nil }
func (AppSender) Idempotent() bool                                   { return true }

// Defaults.
const (
	MaxAttempts = 8
	Lease       = 2 * time.Minute
	SendTimeout = time.Minute
)

// Outbox sends queued Effects.
type Outbox struct {
	Store   *store.Store
	Senders map[string]Sender
	Log     *slog.Logger
	Now     func() time.Time
	// OnChanged is called after an Effect on a Thread changes.
	OnChanged func(threadID string)
	// OnDigest is called after a digest is put together.
	OnDigest func(store.Digest)

	once sync.Once
	kick chan struct{}

	mu      sync.Mutex
	touched []string
}

func (o *Outbox) init() {
	o.once.Do(func() {
		o.kick = make(chan struct{}, 1)
		if o.Now == nil {
			o.Now = time.Now
		}
		if o.Senders == nil {
			o.Senders = map[string]Sender{}
		}
	})
}

// Kick wakes the outbox after something was queued or approved.
func (o *Outbox) Kick() {
	o.init()
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// Run sends Effects until ctx ends.
func (o *Outbox) Run(ctx context.Context) {
	o.init()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		if err := o.Recover(); err != nil {
			o.Log.Error("outbox recovery", "err", err)
		}
		if _, err := o.Digest(); err != nil {
			o.Log.Error("digest", "err", err)
		}
		for ctx.Err() == nil {
			did, err := o.Step(ctx)
			if err != nil {
				o.Log.Error("outbox", "err", err)
				break
			}
			if !did {
				break
			}
		}
		select {
		case <-ctx.Done():
		case <-o.kick:
		case <-tick.C:
		}
	}
}

// Drain runs Step until nothing is due. It is for tests and the smoke run.
func (o *Outbox) Drain(ctx context.Context) error {
	for {
		did, err := o.Step(ctx)
		if err != nil || !did {
			return err
		}
	}
}

// claim is what Step decided inside its transaction.
type claim struct {
	effect  store.Effect
	sender  Sender
	changed bool
}

// Step handles the next due Effect: it sends it, or holds, defers, cancels
// or fails it. It reports whether there was one.
func (o *Outbox) Step(ctx context.Context) (bool, error) {
	o.init()
	now := o.Now().UTC()
	var c claim
	err := o.Store.Tx(func(tx *store.Tx) error {
		e, err := tx.NextEffect(now)
		if err != nil {
			return err
		}
		c.effect = e
		c.changed = true
		return o.prepare(tx, &c, now)
	})
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if c.sender != nil {
		o.send(ctx, c)
	}
	o.changed(c.effect.ThreadID)
	return true, nil
}

// prepare checks an Effect just before sending and claims it. If it must
// not go now, it records why and leaves c.sender nil.
func (o *Outbox) prepare(tx *store.Tx, c *claim, now time.Time) error {
	e := c.effect
	if e.Kind == "digest" {
		_, err := tx.UpdateEffect(e.ID, store.EffectPending, map[string]any{"status": store.EffectHeld})
		return err
	}
	p, err := tx.Policy()
	if err != nil {
		return err
	}
	if e.Kind == "action" {
		ok, err := o.stillAllowed(tx, p, e, now)
		if err != nil || !ok {
			return err
		}
	}
	sender := o.Senders[e.Source]
	if sender == nil {
		return o.fail(tx, e, store.EffectPending, fmt.Sprintf("no %s account is connected", e.Source), now)
	}
	if capped(e) {
		held, err := o.capHold(tx, p.Limits.Effective(), e, now)
		if err != nil || held {
			return err
		}
	}
	ok, err := tx.UpdateEffect(e.ID, store.EffectPending, map[string]any{
		"status": store.EffectSending, "lease_until": now.Add(Lease), "attempts": e.Attempts + 1,
	})
	if err != nil || !ok {
		return err
	}
	c.effect.Attempts++
	c.sender = sender
	return nil
}

// stillAllowed checks an outward action against the current Policy: its
// task must not be stopped, its Mandate must still hold, and an action that
// went ahead under a standing approval must still be covered.
func (o *Outbox) stillAllowed(tx *store.Tx, p policy.Policy, e store.Effect, now time.Time) (bool, error) {
	cancel := func(reason string) (bool, error) {
		if _, err := tx.UpdateEffect(e.ID, store.EffectPending, map[string]any{"status": store.EffectCancelled, "last_error": reason}); err != nil {
			return false, err
		}
		return false, o.notice(tx, e, fmt.Sprintf("I didn't send %s: %s.", describe(e), reason), now)
	}
	tk, err := tx.Ticket(e.TicketID)
	if err != nil {
		return false, err
	}
	if tk.StoppedBy != "" {
		_, err := tx.UpdateEffect(e.ID, store.EffectPending, map[string]any{"status": store.EffectCancelled, "last_error": "the task was stopped"})
		return false, err
	}
	m, err := tk.DecodeMandate()
	if err != nil {
		return cancel("the task's record is damaged")
	}
	if err := gate.Revalidate(p, m); err != nil {
		return cancel("the settings changed and the task is no longer allowed (" + err.Error() + ")")
	}
	if e.Approval == "" && core.Reach(e.Reach) == core.ReachOthers {
		var d taint.Details
		_ = json.Unmarshal([]byte(e.Details), &d)
		if !p.Covers(core.SourceKind(e.Source), e.Action, d.Recipients) {
			_, err := tx.UpdateEffect(e.ID, store.EffectPending, map[string]any{
				"status": store.EffectAwaitingApproval, "approval": store.ApprovalAwaiting,
				"last_error": "no standing approval covers it any more",
			})
			return false, err
		}
	}
	return true, nil
}

// capped reports whether an Effect counts toward send caps: it reaches
// someone other than the owner through a Source other than the app.
func capped(e store.Effect) bool {
	if e.Source == string(core.SourceApp) {
		return false
	}
	switch core.Reach(e.Reach) {
	case core.ReachRequester, core.ReachOthers, core.ReachIrreversible:
		return true
	}
	return false
}

// capHold defers an Effect while its Source is at its hourly or daily cap.
func (o *Outbox) capHold(tx *store.Tx, l policy.Limits, e store.Effect, now time.Time) (bool, error) {
	for _, w := range []struct {
		span  time.Duration
		limit int
		name  string
	}{{time.Hour, l.SendsPerHour, "hourly"}, {24 * time.Hour, l.SendsPerDay, "daily"}} {
		n, first, err := tx.SentSince(e.Source, now.Add(-w.span))
		if err != nil {
			return false, err
		}
		if n < int64(w.limit) {
			continue
		}
		next := first.Add(w.span)
		if !next.After(now) {
			next = now.Add(time.Minute)
		}
		reason := fmt.Sprintf("held by the %s send cap of %d", w.name, w.limit)
		_, err = tx.UpdateEffect(e.ID, store.EffectPending, map[string]any{"next_attempt_at": next, "last_error": reason})
		if err == nil && e.LastError != reason {
			o.Log.Warn("send cap reached", "source", e.Source, "cap", w.name, "limit", w.limit, "until", next)
		}
		return true, err
	}
	return false, nil
}

func (o *Outbox) send(ctx context.Context, c claim) {
	e := c.effect
	tctx := ctx
	if e.TicketID != "" {
		tctx = telemetry.TaskContext(ctx, e.TicketID)
	}
	tctx, span := telemetry.Tracer().Start(tctx, "heyrobots.outbox.send", trace.WithAttributes(
		telemetry.SessionID.String(e.ThreadID), telemetry.EffectID.Int64(int64(e.ID)), telemetry.Source.String(e.Source), telemetry.Action.String(e.Action),
		telemetry.Reach.String(e.Reach), attribute.String("heyrobots.effect.kind", e.Kind), attribute.Int("heyrobots.attempt", e.Attempts),
		attribute.Bool("heyrobots.approved", e.Approval == store.ApprovalApproved),
	))
	defer span.End()
	sctx, cancel := context.WithTimeout(tctx, SendTimeout)
	ref, err := c.sender.Send(sctx, e)
	cancel()
	switch {
	case err == nil:
		span.SetAttributes(telemetry.Outcome.String("sent"))
	case IsUncertain(err):
		span.SetAttributes(telemetry.Outcome.String("uncertain"))
		telemetry.Fail(span, err)
	default:
		span.SetAttributes(telemetry.Outcome.String("failed"))
		telemetry.Fail(span, err)
	}
	now := o.Now().UTC()
	terr := o.Store.Tx(func(tx *store.Tx) error {
		if err == nil {
			_, err := tx.UpdateEffect(e.ID, store.EffectSending, map[string]any{
				"status": store.EffectSent, "sent_at": now, "ref": ref, "lease_until": nil, "last_error": "",
			})
			return err
		}
		if ctx.Err() != nil {
			// The server is stopping; the lease runs out and Recover decides.
			return nil
		}
		if IsUncertain(err) {
			ok, uerr := tx.UpdateEffect(e.ID, store.EffectSending, map[string]any{
				"status": store.EffectUncertain, "lease_until": nil, "last_error": shorten(err.Error(), 300),
			})
			if uerr != nil || !ok {
				return uerr
			}
			return o.notice(tx, e, fmt.Sprintf("Something went wrong while I was doing %s, and I can't tell whether it happened, so I won't try again. Please check.", describe(e)), now)
		}
		if IsPermanent(err) || e.Attempts >= MaxAttempts {
			return o.fail(tx, e, store.EffectSending, err.Error(), now)
		}
		_, uerr := tx.UpdateEffect(e.ID, store.EffectSending, map[string]any{
			"status": store.EffectPending, "lease_until": nil, "next_attempt_at": now.Add(backoff(e.Attempts)),
			"last_error": shorten(err.Error(), 300),
		})
		return uerr
	})
	if err != nil {
		o.Log.Warn("send failed", "effect", e.ID, "source", e.Source, "attempt", e.Attempts, "err", err)
	}
	if terr != nil {
		o.Log.Error("record send", "effect", e.ID, "err", terr)
	}
}

// backoff is the wait before the next attempt: 30s, 1m, 2m … up to an hour.
func backoff(attempts int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

// fail gives up on an Effect and tells the owner.
func (o *Outbox) fail(tx *store.Tx, e store.Effect, from, reason string, now time.Time) error {
	ok, err := tx.UpdateEffect(e.ID, from, map[string]any{"status": store.EffectFailed, "lease_until": nil, "last_error": shorten(reason, 300)})
	if err != nil || !ok {
		return err
	}
	return o.notice(tx, e, fmt.Sprintf("I couldn't send %s: %s.", describe(e), shorten(reason, 200)), now)
}

// notice queues a message to the owner in the app's notices Thread. It
// never names the people involved beyond what the owner can already see in
// the task, and it is sent by the app, which never fails.
func (o *Outbox) notice(tx *store.Tx, about store.Effect, text string, now time.Time) error {
	if about.Source == string(core.SourceApp) && about.Kind == "notice" {
		return nil
	}
	th, err := tx.NoticesThread(ids.New("th"), now)
	if err != nil {
		return err
	}
	if err := tx.TouchThread(th.ID, now); err != nil {
		return err
	}
	o.mu.Lock()
	o.touched = append(o.touched, th.ID)
	o.mu.Unlock()
	return tx.CreateEffect(store.Effect{
		TicketID: about.TicketID, ThreadID: th.ID, Source: string(core.SourceApp), Kind: "notice",
		Reach: string(core.ReachOwner), Body: text, Status: store.EffectPending, CreatedAt: now,
	})
}

// Recover handles Effects the server was sending when it stopped. Through
// an idempotent Sender they are tried again. Otherwise nobody can tell
// whether they went out, so they are marked uncertain and the owner is
// asked to check rather than risk sending twice.
func (o *Outbox) Recover() error {
	o.init()
	now := o.Now().UTC()
	var threads []string
	err := o.Store.Tx(func(tx *store.Tx) error {
		stale, err := tx.StaleSending(now)
		if err != nil {
			return err
		}
		for _, e := range stale {
			threads = append(threads, e.ThreadID)
			if s := o.Senders[e.Source]; s != nil && s.Idempotent() {
				if _, err := tx.UpdateEffect(e.ID, store.EffectSending, map[string]any{
					"status": store.EffectPending, "lease_until": nil, "last_error": "the server stopped while sending; trying again",
				}); err != nil {
					return err
				}
				continue
			}
			ok, err := tx.UpdateEffect(e.ID, store.EffectSending, map[string]any{
				"status": store.EffectUncertain, "lease_until": nil, "last_error": "the server stopped while sending",
			})
			if err != nil {
				return err
			}
			if ok {
				if err := o.notice(tx, e, fmt.Sprintf("The server stopped while I was sending %s. I can't tell whether it went out, so I won't send it again. Please check, and ask me again if it didn't arrive.", describe(e)), now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if len(threads) > 0 {
		o.Log.Warn("recovered effects interrupted while sending", "count", len(threads))
	}
	for _, th := range threads {
		o.changed(th)
	}
	return err
}

// Digest puts together the daily digest once its hour has passed, if
// anything is waiting for it. It returns the digest, or nil.
func (o *Outbox) Digest() (*store.Digest, error) {
	o.init()
	now := o.Now().UTC()
	var made *store.Digest
	var threads []string
	err := o.Store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		l := p.Limits.Effective()
		local := now.In(l.Location())
		due := time.Date(local.Year(), local.Month(), local.Day(), l.DigestHour, 0, 0, 0, local.Location())
		if local.Before(due) {
			return nil
		}
		last, err := tx.LastDigest()
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil && !last.CreatedAt.Before(due) {
			return nil
		}
		held, err := tx.HeldForDigest()
		if err != nil || len(held) == 0 {
			return err
		}
		var b strings.Builder
		var idsOut []uint
		scopes := map[string]string{}
		for _, e := range held {
			scope, ok := scopes[e.TicketID]
			if !ok {
				if tk, err := tx.Ticket(e.TicketID); err == nil {
					if m, err := tk.DecodeMandate(); err == nil {
						scope = m.Scope
					}
				}
				scopes[e.TicketID] = scope
			}
			if scope == "" {
				scope = "Proactive work"
			}
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", scope, strings.TrimSpace(e.Body))
			idsOut = append(idsOut, e.ID)
			threads = append(threads, e.ThreadID)
		}
		d := store.Digest{ID: ids.New("dg"), Body: strings.TrimSpace(b.String()), Count: len(held), CreatedAt: now}
		if err := tx.CreateDigest(d, idsOut); err != nil {
			return err
		}
		made = &d
		return nil
	})
	if err != nil {
		return nil, err
	}
	if made != nil {
		for _, th := range threads {
			o.changed(th)
		}
		if o.OnDigest != nil {
			o.OnDigest(*made)
		}
	}
	return made, nil
}

// changed reports a Thread change, and any notices queued since the last
// call. It runs after the transaction committed.
func (o *Outbox) changed(threadID string) {
	o.mu.Lock()
	list := append(o.touched, threadID)
	o.touched = nil
	o.mu.Unlock()
	if o.OnChanged == nil {
		return
	}
	seen := map[string]bool{}
	for _, id := range list {
		if id != "" && !seen[id] {
			seen[id] = true
			o.OnChanged(id)
		}
	}
}

// describe names an Effect for a notice without quoting its body.
func describe(e store.Effect) string {
	if e.Kind == "action" {
		s := "the " + strings.ReplaceAll(e.Action, "_", " ")
		if e.Recipients != "" {
			s += " to " + e.Recipients
		}
		return s
	}
	return fmt.Sprintf("a reply on %s", e.Source)
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
