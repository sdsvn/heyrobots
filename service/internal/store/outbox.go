package store

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"heyrobots/service/internal/core"
)

// NoticesKey is the app Thread that holds the Assistant's own notices to
// the owner, such as a failed send.
const NoticesKey = "assistant:notices"

// Effect returns one Effect.
func (tx *Tx) Effect(id uint) (Effect, error) {
	var e Effect
	err := tx.db.First(&e, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Effect{}, ErrNotFound
	}
	return e, err
}

// NextEffect returns the oldest Effect that is due, keeping each Thread's
// order: an Effect waits while an older one on its Thread is still pending
// or being sent. Effects waiting for approval or the digest don't hold
// others up.
func (tx *Tx) NextEffect(now time.Time) (Effect, error) {
	var e Effect
	err := tx.db.Where("status = ? AND origin = '' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", EffectPending, now).
		Where("NOT EXISTS (SELECT 1 FROM effects o WHERE o.thread_id = effects.thread_id AND o.id < effects.id AND o.status IN ?)",
			[]string{EffectPending, EffectSending}).
		Order("id").First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Effect{}, ErrNotFound
	}
	return e, err
}

// UpdateEffect changes an Effect only if it still has the given status. It
// reports whether it did.
func (tx *Tx) UpdateEffect(id uint, from string, updates map[string]any) (bool, error) {
	res := tx.db.Model(&Effect{}).Where("id = ? AND status = ?", id, from).Updates(updates)
	return res.RowsAffected == 1, res.Error
}

// StaleSending lists Effects whose sending lease ran out, because the
// server stopped while sending them.
func (tx *Tx) StaleSending(now time.Time) ([]Effect, error) {
	var es []Effect
	err := tx.db.Where("status = ? AND lease_until < ?", EffectSending, now).Order("id").Find(&es).Error
	return es, err
}

// cappedReach are the reach levels that count toward send caps: those that
// reach someone other than the owner.
var cappedReach = []string{"requester", "others", "irreversible"}

// SentSince counts capped sends through a Source since a time, and returns
// the earliest of them.
func (tx *Tx) SentSince(source string, since time.Time) (int64, time.Time, error) {
	q := func() *gorm.DB {
		return tx.db.Model(&Effect{}).Where("status = ? AND source = ? AND sent_at >= ? AND reach IN ?", EffectSent, source, since, cappedReach)
	}
	var n int64
	if err := q().Count(&n).Error; err != nil || n == 0 {
		return n, time.Time{}, err
	}
	var first Effect
	if err := q().Order("sent_at").First(&first).Error; err != nil {
		return 0, time.Time{}, err
	}
	return n, *first.SentAt, nil
}

// AwaitingApproval lists the actions waiting for the owner, oldest first.
func (tx *Tx) AwaitingApproval() ([]Effect, error) {
	var es []Effect
	err := tx.db.Where("status = ?", EffectAwaitingApproval).Order("id").Find(&es).Error
	return es, err
}

// CancelTicketActions cancels a Ticket's outward actions that haven't been
// sent. Messages to the person stay, so a stop is still confirmed.
func (tx *Tx) CancelTicketActions(ticketID, reason string) (int64, error) {
	res := tx.db.Model(&Effect{}).
		Where("ticket_id = ? AND kind = ? AND status IN ?", ticketID, "action", []string{EffectPending, EffectAwaitingApproval}).
		Updates(map[string]any{"status": EffectCancelled, "last_error": reason})
	return res.RowsAffected, res.Error
}

// NoticesThread returns the owner's notices Thread on the app, creating it
// the first time.
func (tx *Tx) NoticesThread(id string, now time.Time) (Thread, error) {
	th, err := tx.ThreadByKey("app", NoticesKey)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return th, err
	}
	th = Thread{ID: id, Source: "app", Key: NoticesKey, CreatedBy: "app:owner", CreatedAt: now, UpdatedAt: now}
	return th, tx.CreateThread(th)
}

// Digest is one daily digest of proactive results.
type Digest struct {
	ID        string `gorm:"primaryKey"`
	Body      string
	Count     int
	CreatedAt time.Time
}

// HeldForDigest lists Effects waiting for the digest, oldest first.
func (tx *Tx) HeldForDigest() ([]Effect, error) {
	var es []Effect
	err := tx.db.Where("status = ?", EffectHeld).Order("id").Find(&es).Error
	return es, err
}

// CreateDigest stores a digest and marks its Effects sent in it.
func (tx *Tx) CreateDigest(d Digest, effectIDs []uint) error {
	if err := tx.db.Create(&d).Error; err != nil {
		return err
	}
	return tx.db.Model(&Effect{}).Where("id IN ? AND status = ?", effectIDs, EffectHeld).
		Updates(map[string]any{"status": EffectSent, "sent_at": d.CreatedAt, "digest_id": d.ID}).Error
}

// LastDigest returns the most recent digest.
func (tx *Tx) LastDigest() (Digest, error) {
	var d Digest
	err := tx.db.Order("created_at DESC").First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Digest{}, ErrNotFound
	}
	return d, err
}

// Digests lists digests, newest first.
func (tx *Tx) Digests(limit int) ([]Digest, error) {
	var ds []Digest
	err := tx.db.Order("created_at DESC").Limit(limit).Find(&ds).Error
	return ds, err
}

// Usage is one model call's token use and cost.
type Usage struct {
	ID           uint `gorm:"primaryKey"`
	At           time.Time
	TicketID     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	// CachedTokens of InputTokens came from the provider's prompt cache.
	CachedTokens int64
	CostMicros   int64
}

// TableName keeps the table name singular, as migration 4 created it.
func (Usage) TableName() string { return "usages" }

// RecordUsage stores one model call's use.
func (s *Store) RecordUsage(u Usage) error { return s.db.Create(&u).Error }

// SpendSince returns the cost of all model calls since a time, in
// millionths of a US dollar.
func (s *Store) SpendSince(since time.Time) (int64, error) {
	var n int64
	err := s.db.Model(&Usage{}).Select("COALESCE(SUM(cost_micros), 0)").Where("at >= ?", since).Scan(&n).Error
	return n, err
}

// TicketSpend returns the cost of model calls per Ticket, in millionths of
// a US dollar. With no ids it returns the Tickets that spent most since a
// time.
func (s *Store) TicketSpend(since time.Time, ticketIDs []string, limit int) (map[string]int64, error) {
	var rows []struct {
		TicketID string
		Cost     int64
	}
	q := s.db.Model(&Usage{}).Select("ticket_id, SUM(cost_micros) AS cost").Where("ticket_id <> ''").Group("ticket_id")
	if len(ticketIDs) > 0 {
		q = q.Where("ticket_id IN ?", ticketIDs)
	} else {
		q = q.Where("at >= ?", since).Order("cost DESC").Limit(limit)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.TicketID] = r.Cost
	}
	return out, nil
}

// AddressReply fills in who a message to the requester goes to.
//
// On the app, a reply goes to the requester's own conversation and is sent
// by the Assistant. On any other Source, such as mail, a reply goes out
// from the owner's own account. To the owner that is fine. To anyone else,
// a delegate included, it acts as the owner, so it reaches others and
// waits for the owner's approval.
func AddressReply(e Effect, requester core.Identity, owner bool) Effect {
	if e.Source == string(core.SourceApp) || e.Kind != "message" {
		return e
	}
	e.Recipients = requester.ID
	if !owner && e.Status == EffectPending {
		e.Reach = string(core.ReachOthers)
		e.Action = "reply"
		e.Status, e.Approval = EffectAwaitingApproval, ApprovalAwaiting
	}
	return e
}
