package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
)

// Eval states a Trigger moves through.
const (
	// EvalPending waits for an Eval that may start new work.
	EvalPending = "pending"
	// EvalNotice reached a Ticket that owns it; Eval sees it only as a
	// follow-up notice and may only stop that Ticket.
	EvalNotice = "notice"
	// EvalRunning is part of a running Eval's batch.
	EvalRunning = "evaluating"
	// EvalHeld waits for the machines to agree who routes it; it isn't
	// evaluated here unless this machine wins.
	EvalHeld = "held"
	// EvalDone has a Decision, or needed none.
	EvalDone = "done"
)

// Decision kinds.
const (
	DecisionAccept      = "accept"
	DecisionAttach      = "attach"
	DecisionIgnore      = "ignore"
	DecisionStop        = "stop"
	DecisionWouldAccept = "would_accept"
	DecisionRefused     = "refused"
	DecisionUnavailable = "unavailable"
)

// Effect statuses and approvals.
const (
	// EffectPending is ready to send when its turn comes.
	EffectPending = "pending"
	// EffectAwaitingApproval waits for the owner.
	EffectAwaitingApproval = "awaiting_approval"
	// EffectSending is being sent; its lease says until when.
	EffectSending = "sending"
	EffectSent    = "sent"
	// EffectFailed could not be sent; the owner was told.
	EffectFailed = "failed"
	// EffectRejected was declined by the owner.
	EffectRejected = "rejected"
	// EffectCancelled was dropped before sending, for example because its
	// task was stopped or its Mandate no longer holds.
	EffectCancelled = "cancelled"
	// EffectHeld waits for the daily digest.
	EffectHeld = "held"
	// EffectUncertain was being sent when the server stopped, through a
	// Source where sending twice isn't safe. The owner is asked to check.
	EffectUncertain = "uncertain"

	ApprovalAwaiting = "awaiting"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
)

// PolicyVersion is one stored version of the Policy.
type PolicyVersion struct {
	Version   int `gorm:"primaryKey;autoIncrement:false"`
	Body      string
	CreatedAt time.Time
}

// Thread is a conversation as its Source defines it.
type Thread struct {
	ID             string `gorm:"primaryKey"`
	Source         string
	Key            string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	EvalID         string
	EvalLeaseUntil *time.Time
	EvalFailures   int
	// AgentID is set on a direct chat with one agent, GroupID on a group's
	// chat.
	AgentID string
	GroupID string
}

// Trigger is one stored event.
type Trigger struct {
	ID           string `gorm:"primaryKey"`
	Source       string
	DeliveryID   string
	ActorSource  string
	ActorID      string
	Authenticity int
	IsBot        bool
	Addressed    bool
	ReceivedAll  bool
	Text         string
	At           time.Time
	CreatedAt    time.Time
	EvalState    string
	EvalID       string
	Owned        bool
	// HandbackReason and HandedBackFrom are set when a Ticket's Executor
	// returned this message as beyond that Ticket.
	HandbackReason string
	HandedBackFrom string
	// Origin is the machine a copied message came from; empty for this one.
	Origin string
	// LeadAgent fixes which agent leads the work, when another machine's
	// lead already chose one here.
	LeadAgent string
}

// Core returns the domain form of the Trigger.
func (t Trigger) Core(threadKeys []string) core.Trigger {
	return core.Trigger{
		DeliveryID:   t.DeliveryID,
		Source:       core.SourceKind(t.Source),
		Actor:        core.Identity{Source: core.SourceKind(t.ActorSource), ID: t.ActorID},
		Authenticity: core.Authenticity(t.Authenticity),
		ActorIsBot:   t.IsBot,
		Addressed:    t.Addressed,
		ReceivedAll:  t.ReceivedAll,
		ThreadKeys:   threadKeys,
		Text:         t.Text,
		At:           t.At,
	}
}

// TriggerThread links a Trigger to one of its Threads.
type TriggerThread struct {
	TriggerID string `gorm:"primaryKey"`
	ThreadID  string `gorm:"primaryKey"`
}

// Ticket is a unit of authorized work. Its Mandate is stored as JSON and
// never changes.
type Ticket struct {
	ID             string `gorm:"primaryKey"`
	Mandate        string
	Open           bool
	Stage          string
	StoppedBy      string
	OriginThreadID string
	// Choices are answers the person can tap while the Ticket asks a
	// question, as a JSON array. Any change of stage clears them.
	Choices string
	// AgentID is the agent that does the work, copied from the Mandate so
	// the dispatcher can keep each agent to its capacity.
	AgentID string
	// Tainted is set once the Ticket's work read outside content, so a
	// later turn starts as limited even when its history isn't the
	// server's to read, as in Claude Code.
	Tainted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MarkTainted records that a Ticket's work read outside content.
func (tx *Tx) MarkTainted(id string) error {
	return tx.db.Model(&Ticket{}).Where("id = ?", id).Update("tainted", true).Error
}

// ChoiceList returns the answers the person can tap, if any.
func (t Ticket) ChoiceList() []string {
	var cs []string
	if t.Choices != "" {
		_ = json.Unmarshal([]byte(t.Choices), &cs)
	}
	return cs
}

// DecodeMandate returns the Ticket's Mandate.
func (t Ticket) DecodeMandate() (core.Mandate, error) {
	var m core.Mandate
	err := json.Unmarshal([]byte(t.Mandate), &m)
	return m, err
}

// Subscription links a Ticket to a Thread whose messages it receives.
type Subscription struct {
	TicketID    string `gorm:"primaryKey"`
	ThreadID    string `gorm:"primaryKey"`
	ContextOnly bool
	CreatedAt   time.Time
}

// TicketInput is a Trigger delivered to a Ticket for its Executor.
type TicketInput struct {
	ID          uint `gorm:"primaryKey"`
	TicketID    string
	TriggerID   string
	CreatedAt   time.Time
	DeliveredAt *time.Time
}

// Decision records what an Eval decided about some Triggers.
type Decision struct {
	ID            string `gorm:"primaryKey"`
	ThreadID      string
	EvalID        string
	Kind          string
	TicketID      string
	TriggerIDs    string
	PolicyVersion int
	Note          string
	Reason        string
	CreatedAt     time.Time
}

// Effect is an outgoing action waiting in the outbox. It is written in the
// same transaction as the decision behind it.
type Effect struct {
	ID        uint `gorm:"primaryKey"`
	TicketID  string
	ThreadID  string
	Source    string
	Kind      string
	Reach     string
	Body      string
	Status    string
	Attempts  int
	CreatedAt time.Time
	SentAt    *time.Time
	// Approval is "" for messages that need none, or awaiting, approved or
	// rejected for an outward action.
	Approval   string
	Action     string
	Recipients string
	Details    string
	Findings   string
	// Delivery bookkeeping.
	NextAttemptAt *time.Time
	LeaseUntil    *time.Time
	LastError     string
	// DecidedBy is the device that approved or rejected the action.
	DecidedBy string
	DecidedAt *time.Time
	DigestID  string
	// Ref is the Source's own id for what was sent, such as a message id.
	Ref string
	// Origin is the machine a copied reply came from, and RemoteID its id
	// there; a copy is only shown here, never sent or decided here.
	Origin   string
	RemoteID uint
	// AgentID is who wrote a copied reply.
	AgentID string
	// AckedAt is when the owner dismissed a failed or uncertain Effect from
	// the attention feed.
	AckedAt *time.Time
	// Payload is what the sender needs beyond the body, as JSON, such as an
	// email's subject and thread.
	Payload string
}

// Tx is a database transaction with the domain operations.
type Tx struct{ db *gorm.DB }

// Tx runs fn in one transaction. If fn returns an error, nothing is kept.
func (s *Store) Tx(fn func(tx *Tx) error) error {
	return s.db.Transaction(func(db *gorm.DB) error { return fn(&Tx{db: db}) })
}

// Policy returns the current Policy.
func (tx *Tx) Policy() (policy.Policy, error) {
	var row PolicyVersion
	if err := tx.db.Order("version DESC").First(&row).Error; err != nil {
		return policy.Policy{}, fmt.Errorf("load policy: %w", err)
	}
	var p policy.Policy
	if err := json.Unmarshal([]byte(row.Body), &p); err != nil {
		return policy.Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	p.Version = row.Version
	return p, nil
}

// SavePolicy stores p as the next version and returns it with its version.
func (tx *Tx) SavePolicy(p policy.Policy, now time.Time) (policy.Policy, error) {
	cur, err := tx.Policy()
	if err != nil {
		return policy.Policy{}, err
	}
	p.Version = cur.Version + 1
	body, err := json.Marshal(p)
	if err != nil {
		return policy.Policy{}, err
	}
	return p, tx.db.Create(&PolicyVersion{Version: p.Version, Body: string(body), CreatedAt: now}).Error
}

// Policy returns the current Policy outside a transaction.
func (s *Store) Policy() (policy.Policy, error) {
	var p policy.Policy
	err := s.Tx(func(tx *Tx) error {
		var err error
		p, err = tx.Policy()
		return err
	})
	return p, err
}

// TriggerByDelivery finds a Trigger by its Source's delivery id.
func (tx *Tx) TriggerByDelivery(source, deliveryID string) (Trigger, error) {
	var t Trigger
	err := tx.db.Where("source = ? AND delivery_id = ?", source, deliveryID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Trigger{}, ErrNotFound
	}
	return t, err
}

// ThreadByKey finds a Thread by its Source and key.
func (tx *Tx) ThreadByKey(source, key string) (Thread, error) {
	var th Thread
	err := tx.db.Where("source = ? AND key = ?", source, key).First(&th).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Thread{}, ErrNotFound
	}
	return th, err
}

// Thread finds a Thread by id.
func (tx *Tx) Thread(id string) (Thread, error) {
	var th Thread
	err := tx.db.First(&th, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Thread{}, ErrNotFound
	}
	return th, err
}

// CreateThread stores a new Thread.
func (tx *Tx) CreateThread(th Thread) error { return tx.db.Create(&th).Error }

// TouchThread records activity on a Thread.
func (tx *Tx) TouchThread(id string, now time.Time) error {
	return tx.db.Model(&Thread{}).Where("id = ?", id).Update("updated_at", now).Error
}

// CreateTrigger stores a Trigger and links it to its Threads.
func (tx *Tx) CreateTrigger(t Trigger, threadIDs []string) error {
	if err := tx.db.Create(&t).Error; err != nil {
		return err
	}
	for _, id := range threadIDs {
		if err := tx.db.Create(&TriggerThread{TriggerID: t.ID, ThreadID: id}).Error; err != nil {
			return err
		}
	}
	return nil
}

// ThreadIDsOf returns the Threads a Trigger belongs to.
func (tx *Tx) ThreadIDsOf(triggerID string) ([]string, error) {
	var ids []string
	err := tx.db.Model(&TriggerThread{}).Where("trigger_id = ?", triggerID).Pluck("thread_id", &ids).Error
	return ids, err
}

// SubscribedTickets returns the Tickets subscribed to a Thread, including
// ended ones.
func (tx *Tx) SubscribedTickets(threadID string) ([]Ticket, []Subscription, error) {
	var subs []Subscription
	if err := tx.db.Where("thread_id = ?", threadID).Find(&subs).Error; err != nil {
		return nil, nil, err
	}
	tickets := make([]Ticket, 0, len(subs))
	for _, sub := range subs {
		var t Ticket
		if err := tx.db.First(&t, "id = ?", sub.TicketID).Error; err != nil {
			return nil, nil, err
		}
		tickets = append(tickets, t)
	}
	return tickets, subs, nil
}

// Ticket finds a Ticket by id.
func (tx *Tx) Ticket(id string) (Ticket, error) {
	var t Ticket
	err := tx.db.First(&t, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Ticket{}, ErrNotFound
	}
	return t, err
}

// CreateTicket stores a Ticket with its Mandate and subscribes it to a Thread.
func (tx *Tx) CreateTicket(t Ticket, m core.Mandate, threadID string, now time.Time) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	t.Mandate, t.CreatedAt, t.UpdatedAt, t.AgentID = string(body), now, now, m.LeadAgent()
	if err := tx.db.Create(&t).Error; err != nil {
		return err
	}
	return tx.Subscribe(t.ID, threadID, false, now)
}

// Subscribe links a Ticket to a Thread. It does nothing if they are linked.
func (tx *Tx) Subscribe(ticketID, threadID string, contextOnly bool, now time.Time) error {
	return tx.db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&Subscription{TicketID: ticketID, ThreadID: threadID, ContextOnly: contextOnly, CreatedAt: now}).Error
}

// SetTicketState updates a Ticket's open flag, stage and who stopped it,
// and clears any answer choices.
func (tx *Tx) SetTicketState(id string, open bool, stage core.TicketStage, stoppedBy string, now time.Time) error {
	return tx.db.Model(&Ticket{}).Where("id = ?", id).Updates(map[string]any{
		"open": open, "stage": string(stage), "stopped_by": stoppedBy, "choices": "", "updated_at": now,
	}).Error
}

// SetTicketChoices stores the answers the person can tap for the Ticket's
// question.
func (tx *Tx) SetTicketChoices(id string, choices []string) error {
	v := ""
	if len(choices) > 0 {
		b, err := json.Marshal(choices)
		if err != nil {
			return err
		}
		v = string(b)
	}
	return tx.db.Model(&Ticket{}).Where("id = ?", id).Update("choices", v).Error
}

// AddTicketInput delivers a Trigger to a Ticket.
func (tx *Tx) AddTicketInput(ticketID, triggerID string, now time.Time) error {
	return tx.db.Create(&TicketInput{TicketID: ticketID, TriggerID: triggerID, CreatedAt: now}).Error
}

// TicketInputs returns the Triggers delivered to a Ticket, oldest first.
func (tx *Tx) TicketInputs(ticketID string) ([]TicketInput, error) {
	var in []TicketInput
	err := tx.db.Where("ticket_id = ?", ticketID).Order("id").Find(&in).Error
	return in, err
}

// ClaimEval claims one Thread that has Triggers waiting for Eval and no
// running Eval, and moves those Triggers into the Eval's batch. If the
// Thread's last Eval ran out its lease, that counts as a failure and its
// batch is put back first.
func (tx *Tx) ClaimEval(evalID string, now time.Time, lease time.Duration) (Thread, []Trigger, error) {
	var th Thread
	err := tx.db.Where("(eval_lease_until IS NULL OR eval_lease_until < ?) AND EXISTS ("+
		"SELECT 1 FROM trigger_threads tt JOIN triggers t ON t.id = tt.trigger_id "+
		"WHERE tt.thread_id = threads.id AND t.eval_state IN (?, ?, ?))",
		now, EvalPending, EvalNotice, EvalRunning).
		Order("updated_at").First(&th).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Thread{}, nil, ErrNotFound
	}
	if err != nil {
		return Thread{}, nil, err
	}
	failures := th.EvalFailures
	if th.EvalID != "" {
		// The previous Eval's lease expired: it crashed or ran out of time.
		if err := tx.requeue(th.ID, th.EvalID); err != nil {
			return Thread{}, nil, err
		}
		failures++
	}
	until := now.Add(lease)
	res := tx.db.Model(&Thread{}).Where("id = ? AND (eval_lease_until IS NULL OR eval_lease_until < ?)", th.ID, now).
		Updates(map[string]any{"eval_id": evalID, "eval_lease_until": until, "eval_failures": failures})
	if res.Error != nil {
		return Thread{}, nil, res.Error
	}
	if res.RowsAffected != 1 {
		return Thread{}, nil, ErrNotFound
	}
	th.EvalID, th.EvalLeaseUntil, th.EvalFailures = evalID, &until, failures

	var batch []Trigger
	err = tx.db.Where("eval_state IN (?, ?) AND id IN (SELECT trigger_id FROM trigger_threads WHERE thread_id = ?)",
		EvalPending, EvalNotice, th.ID).Order("at, created_at").Find(&batch).Error
	if err != nil {
		return Thread{}, nil, err
	}
	ids := make([]string, len(batch))
	for i, t := range batch {
		ids[i] = t.ID
		batch[i].EvalID = evalID
	}
	if len(ids) > 0 {
		if err := tx.db.Model(&Trigger{}).Where("id IN ?", ids).
			Updates(map[string]any{"eval_state": EvalRunning, "eval_id": evalID}).Error; err != nil {
			return Thread{}, nil, err
		}
	}
	return th, batch, nil
}

// requeue puts an Eval's batch back to wait for the next Eval.
func (tx *Tx) requeue(threadID, evalID string) error {
	q := tx.db.Model(&Trigger{}).Where("eval_state = ? AND eval_id = ? AND id IN (SELECT trigger_id FROM trigger_threads WHERE thread_id = ?)",
		EvalRunning, evalID, threadID)
	return q.Update("eval_state", gorm.Expr("CASE WHEN owned THEN ? ELSE ? END", EvalNotice, EvalPending)).Error
}

// ReleaseEval ends an Eval without a result: its batch waits for the next
// Eval. When failed is true the Thread's failure count goes up, and the new
// count is returned.
func (tx *Tx) ReleaseEval(threadID, evalID string, failed bool) (int, error) {
	if err := tx.requeue(threadID, evalID); err != nil {
		return 0, err
	}
	var th Thread
	if err := tx.db.First(&th, "id = ?", threadID).Error; err != nil {
		return 0, err
	}
	failures := th.EvalFailures
	if failed {
		failures++
	}
	err := tx.db.Model(&Thread{}).Where("id = ? AND eval_id = ?", threadID, evalID).
		Updates(map[string]any{"eval_id": "", "eval_lease_until": nil, "eval_failures": failures}).Error
	return failures, err
}

// FinishEval ends an Eval whose batch was decided.
func (tx *Tx) FinishEval(threadID, evalID string) error {
	return tx.db.Model(&Thread{}).Where("id = ? AND eval_id = ?", threadID, evalID).
		Updates(map[string]any{"eval_id": "", "eval_lease_until": nil, "eval_failures": 0}).Error
}

// EvalStillOwns reports whether evalID still holds the Thread's lease.
func (tx *Tx) EvalStillOwns(threadID, evalID string, now time.Time) (bool, error) {
	var n int64
	err := tx.db.Model(&Thread{}).Where("id = ? AND eval_id = ? AND eval_lease_until >= ?", threadID, evalID, now).Count(&n).Error
	return n == 1, err
}

// SetTriggersState sets the Eval state of Triggers.
func (tx *Tx) SetTriggersState(ids []string, state string) error {
	if len(ids) == 0 {
		return nil
	}
	return tx.db.Model(&Trigger{}).Where("id IN ?", ids).Update("eval_state", state).Error
}

// MarkOwned records that a Ticket took a Trigger: Eval sees it only as a
// follow-up notice.
func (tx *Tx) MarkOwned(id string) error {
	return tx.db.Model(&Trigger{}).Where("id = ?", id).
		Updates(map[string]any{"owned": true, "eval_state": EvalNotice}).Error
}

// RecentTriggers returns up to limit Triggers of a Thread, oldest first.
func (tx *Tx) RecentTriggers(threadID string, limit int) ([]Trigger, error) {
	var ts []Trigger
	err := tx.db.Where("id IN (SELECT trigger_id FROM trigger_threads WHERE thread_id = ?)", threadID).
		Order("at DESC, created_at DESC").Limit(limit).Find(&ts).Error
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
	return ts, err
}

// CreateDecision stores a Decision.
func (tx *Tx) CreateDecision(d Decision) error { return tx.db.Create(&d).Error }

// CreateEffect queues an outgoing action in the outbox.
func (tx *Tx) CreateEffect(e Effect) error { return tx.db.Create(&e).Error }

// ThreadView is a Thread with everything that happened on it.
type ThreadView struct {
	Thread    Thread
	Triggers  []Trigger
	Decisions []Decision
	Effects   []Effect
	Tickets   []Ticket
}

// ThreadView loads a Thread and its history.
func (tx *Tx) ThreadView(id string) (ThreadView, error) {
	th, err := tx.Thread(id)
	if err != nil {
		return ThreadView{}, err
	}
	v := ThreadView{Thread: th}
	if err := tx.db.Where("id IN (SELECT trigger_id FROM trigger_threads WHERE thread_id = ?)", id).
		Order("at, created_at").Find(&v.Triggers).Error; err != nil {
		return ThreadView{}, err
	}
	if err := tx.db.Where("thread_id = ?", id).Order("created_at").Find(&v.Decisions).Error; err != nil {
		return ThreadView{}, err
	}
	if err := tx.db.Where("thread_id = ?", id).Order("id").Find(&v.Effects).Error; err != nil {
		return ThreadView{}, err
	}
	v.Tickets, _, err = tx.SubscribedTickets(id)
	return v, err
}

// Threads lists Threads, most recently active first. When createdBy is
// set, only Threads that identity started are listed.
func (tx *Tx) Threads(createdBy string, limit int) ([]Thread, error) {
	q := tx.db.Order("updated_at DESC").Limit(limit)
	if createdBy != "" {
		q = q.Where("created_by = ?", createdBy)
	}
	var ths []Thread
	return ths, q.Find(&ths).Error
}

// FirstTriggerText returns the text of a Thread's first Trigger, for titles.
func (tx *Tx) FirstTriggerText(threadID string) (string, error) {
	var t Trigger
	err := tx.db.Where("id IN (SELECT trigger_id FROM trigger_threads WHERE thread_id = ?)", threadID).
		Order("at, created_at").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return t.Text, err
}
