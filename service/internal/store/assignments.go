package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Assignment kinds and statuses.
const (
	AssignmentNew      = "new"
	AssignmentFollowUp = "followup"
	AssignmentRunning  = "running"
	AssignmentEnded    = "ended"
	// StateInterrupted marks an Assignment whose lease ran out, for example
	// because the server stopped while it ran.
	StateInterrupted = "interrupted"
)

// Assignment is one Executor attempt at a Ticket.
type Assignment struct {
	ID          string `gorm:"primaryKey"`
	TicketID    string
	Kind        string
	Status      string
	Recovered   bool
	StartedAt   time.Time
	LeaseUntil  time.Time
	EndedAt     *time.Time
	State       string
	Summary     string
	HumanAction string
	Result      string
	// AgentID is the agent that did the work.
	AgentID string
	// PausedAt is the interrupt id of a job step waiting for the person,
	// so their reply can resume it.
	PausedAt string
}

// Claimed is the work an Executor picked up.
type Claimed struct {
	Assignment Assignment
	Ticket     Ticket
	// Inputs are the messages delivered with this Assignment, oldest first.
	Inputs []Trigger
	// PausedAt is where the previous Assignment's job paused, if it did.
	PausedAt string
}

// ClaimAssignment picks one open Ticket that has new input, or whose last
// Assignment was interrupted, and no running Assignment, and starts an
// Assignment for it. A Ticket that never had an Assignment is new work;
// otherwise it is a follow-up. allowNew and allowFollowUp say which kinds
// may start now.
func (tx *Tx) ClaimAssignment(id string, now time.Time, lease time.Duration, allowNew, allowFollowUp bool) (Claimed, error) {
	// Assignments whose lease ran out were interrupted.
	if err := tx.db.Model(&Assignment{}).Where("status = ? AND lease_until < ?", AssignmentRunning, now).
		Updates(map[string]any{"status": AssignmentEnded, "state": StateInterrupted, "ended_at": now}).Error; err != nil {
		return Claimed{}, err
	}
	if !allowNew && !allowFollowUp {
		return Claimed{}, ErrNotFound
	}
	// Each agent works on at most its capacity of tasks at once; the rest
	// wait in its queue.
	q := tx.db.Model(&Ticket{}).Where("open AND NOT EXISTS (SELECT 1 FROM assignments a WHERE a.ticket_id = tickets.id AND a.status = ?)", AssignmentRunning).
		Where("(SELECT COUNT(*) FROM assignments a WHERE a.agent_id = tickets.agent_id AND a.status = ?) < "+
			"COALESCE((SELECT capacity FROM agents g WHERE g.id = tickets.agent_id), 1)", AssignmentRunning).
		Where("EXISTS (SELECT 1 FROM ticket_inputs i WHERE i.ticket_id = tickets.id AND i.delivered_at IS NULL) OR "+
			"(SELECT a.state FROM assignments a WHERE a.ticket_id = tickets.id ORDER BY a.started_at DESC LIMIT 1) = ?", StateInterrupted)
	hasPrev := "EXISTS (SELECT 1 FROM assignments a WHERE a.ticket_id = tickets.id)"
	switch {
	case !allowNew:
		q = q.Where(hasPrev)
	case !allowFollowUp:
		q = q.Where("NOT " + hasPrev)
	}
	var t Ticket
	err := q.Order("updated_at").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Claimed{}, ErrNotFound
	}
	if err != nil {
		return Claimed{}, err
	}
	var prev Assignment
	kind, recovered := AssignmentNew, false
	err = tx.db.Where("ticket_id = ?", t.ID).Order("started_at DESC").First(&prev).Error
	pausedAt := ""
	switch {
	case err == nil:
		kind, recovered = AssignmentFollowUp, prev.State == StateInterrupted
		pausedAt = prev.PausedAt
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return Claimed{}, err
	}
	a := Assignment{ID: id, TicketID: t.ID, Kind: kind, Status: AssignmentRunning, Recovered: recovered, StartedAt: now, LeaseUntil: now.Add(lease), AgentID: t.AgentID}
	if err := tx.db.Create(&a).Error; err != nil {
		return Claimed{}, err
	}
	var inputs []TicketInput
	if err := tx.db.Where("ticket_id = ? AND delivered_at IS NULL", t.ID).Order("id").Find(&inputs).Error; err != nil {
		return Claimed{}, err
	}
	c := Claimed{Assignment: a, Ticket: t, PausedAt: pausedAt}
	for _, in := range inputs {
		var tr Trigger
		if err := tx.db.First(&tr, "id = ?", in.TriggerID).Error; err != nil {
			return Claimed{}, err
		}
		c.Inputs = append(c.Inputs, tr)
	}
	if len(inputs) > 0 {
		if err := tx.db.Model(&TicketInput{}).Where("ticket_id = ? AND delivered_at IS NULL", t.ID).Update("delivered_at", now).Error; err != nil {
			return Claimed{}, err
		}
	}
	return c, nil
}

// EndAssignment records an Assignment's final report.
func (tx *Tx) EndAssignment(id, state, summary, humanAction, result string, now time.Time) error {
	return tx.db.Model(&Assignment{}).Where("id = ? AND status = ?", id, AssignmentRunning).Updates(map[string]any{
		"status": AssignmentEnded, "state": state, "summary": summary, "human_action": humanAction, "result": result, "ended_at": now,
	}).Error
}

// SetAssignmentPause records where an Assignment's job paused.
func (tx *Tx) SetAssignmentPause(id, interruptID string) error {
	return tx.db.Model(&Assignment{}).Where("id = ?", id).Update("paused_at", interruptID).Error
}

// NoteProgress records an internal progress summary on a running Assignment.
func (tx *Tx) NoteProgress(id, summary string) error {
	return tx.db.Model(&Assignment{}).Where("id = ?", id).Update("summary", summary).Error
}

// Assignments lists a Ticket's Assignments, oldest first.
func (tx *Tx) Assignments(ticketID string) ([]Assignment, error) {
	var as []Assignment
	err := tx.db.Where("ticket_id = ?", ticketID).Order("started_at").Find(&as).Error
	return as, err
}

// HandBack returns messages a Ticket owned to Eval, because its Executor
// found them beyond the Ticket. Only messages that were delivered to this
// Ticket can be handed back.
func (tx *Tx) HandBack(ticketID string, triggerIDs []string, reason string) (int64, error) {
	res := tx.db.Model(&Trigger{}).
		Where("id IN ? AND id IN (SELECT trigger_id FROM ticket_inputs WHERE ticket_id = ?) AND NOT is_bot", triggerIDs, ticketID).
		Updates(map[string]any{"owned": false, "eval_state": EvalPending, "handback_reason": reason, "handed_back_from": ticketID})
	return res.RowsAffected, res.Error
}
