package store

import (
	"time"
)

// Incident causes. The server sets one only where it is certain what went
// wrong; there is no cause for "the agent did badly".
const (
	// CauseSetup: the agent lacked a toolset the work needed. The key is
	// the toolset. It is the owner's or IT's to fix, not the agent's fault.
	CauseSetup = "setup"
	// CauseRouting: the agent handed work back as not its own; the
	// router's miss, not the agent's.
	CauseRouting = "routing"
	// CauseDeclined: the owner declined an action the agent proposed. The
	// key is the action; the note is the owner's reason, if they gave one.
	CauseDeclined = "declined"
	// CauseStopped: the owner stopped the agent's task.
	CauseStopped = "stopped"
	// CauseFailed and CauseBlocked: the agent's own report said so. The
	// detail is its own words.
	CauseFailed  = "failed"
	CauseBlocked = "blocked"
	// CauseSendFailed: an action the owner approved couldn't be sent. The
	// key is the action.
	CauseSendFailed = "send_failed"
)

// IncidentWindow is how long an incident counts.
const IncidentWindow = 30 * 24 * time.Hour

// Incident is one time an agent's work went wrong, written by the server
// where it happened. It is what the team brain learns from.
type Incident struct {
	ID       int64  `gorm:"primaryKey"`
	AgentID  string `gorm:"index:idx_incidents_agent_at"`
	Person   string
	Cause    string
	Key      string
	TicketID string
	ThreadID string
	// Detail is what happened, in the server's words or, for failed and
	// blocked, the agent's own report.
	Detail string
	// Note is the owner's own words about it, such as why they declined.
	Note string
	// Tainted is set when the task had read outside content, so its
	// detail may echo it.
	Tainted    bool
	At         time.Time `gorm:"index:idx_incidents_agent_at"`
	ResolvedAt *time.Time
}

// AddIncident records an incident. A setup gap is recorded once per
// Ticket and toolset, however often its messages are routed again.
func (tx *Tx) AddIncident(in Incident) error {
	if in.AgentID == "" {
		return nil
	}
	if in.TicketID != "" && in.Cause == CauseSetup {
		var n int64
		if err := tx.db.Model(&Incident{}).Where("ticket_id = ? AND cause = ? AND key = ?", in.TicketID, in.Cause, in.Key).Count(&n).Error; err != nil || n > 0 {
			return err
		}
	}
	return tx.db.Create(&in).Error
}

// Incidents lists unresolved incidents since a time, newest first. With
// agentID empty it lists every agent's; with person set, only theirs.
func (tx *Tx) Incidents(agentID, person string, since time.Time, limit int) ([]Incident, error) {
	q := tx.db.Where("resolved_at IS NULL AND at >= ?", since)
	if agentID != "" {
		q = q.Where("agent_id = ?", agentID)
	}
	if person != "" {
		q = q.Where("person = ?", person)
	}
	var out []Incident
	return out, q.Order("at DESC").Limit(limit).Find(&out).Error
}

// Incident finds one incident.
func (tx *Tx) Incident(id int64) (Incident, error) {
	var in Incident
	err := tx.db.First(&in, "id = ?", id).Error
	if err != nil {
		return in, ErrNotFound
	}
	return in, nil
}

// ResolveIncident marks an incident fixed, or open again when at is nil.
func (tx *Tx) ResolveIncident(id int64, at *time.Time) error {
	return tx.db.Model(&Incident{}).Where("id = ?", id).Update("resolved_at", at).Error
}

// TaskCounts is how many of each agent's tasks ended each way since a
// time: ready, failed, blocked and dropped.
func (tx *Tx) TaskCounts(since time.Time) (map[string]map[string]int, error) {
	var rows []struct {
		AgentID string
		State   string
		N       int
	}
	err := tx.db.Raw(`SELECT agent_id, state, COUNT(DISTINCT ticket_id) AS n FROM assignments
		WHERE status = ? AND ended_at >= ? AND agent_id <> '' GROUP BY agent_id, state`, AssignmentEnded, since).Scan(&rows).Error
	out := map[string]map[string]int{}
	for _, r := range rows {
		if out[r.AgentID] == nil {
			out[r.AgentID] = map[string]int{}
		}
		out[r.AgentID][r.State] = r.N
	}
	return out, err
}
