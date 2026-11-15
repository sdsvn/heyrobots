package store

import "time"

// AgentOutcome is how one finished turn of a specialist went: which
// checks passed, which still failed at the end, and what it cost. It is a
// record for the owner, never read by the router.
type AgentOutcome struct {
	ID         uint   `gorm:"primaryKey"`
	TicketID   string `gorm:"index"`
	AgentID    string `gorm:"index"`
	Specialist string
	State      string
	// Checked is set when checks applied to the report.
	Checked bool
	// FirstTry is set when every check passed the first time.
	FirstTry bool
	// Passed lists what the passing checks did, and Failed the reasons
	// still failing when the turn ended, as JSON arrays.
	Passed    string
	Failed    string
	Refusals  int
	Tokens    int64
	CreatedAt time.Time `gorm:"index"`
}

// AddOutcome records a finished turn, with the tokens its task used since
// the turn started.
func (tx *Tx) AddOutcome(o AgentOutcome, since time.Time) error {
	var n struct{ T int64 }
	if err := tx.db.Model(&Usage{}).Select("COALESCE(SUM(input_tokens + output_tokens), 0) AS t").Where("ticket_id = ? AND at >= ?", o.TicketID, since).Scan(&n).Error; err != nil {
		return err
	}
	o.Tokens = n.T
	return tx.db.Create(&o).Error
}

// OutcomeSummary is an agent's record over a period.
type OutcomeSummary struct {
	Checked, FirstTry, Failed int
	Tokens                    int64
}

// Outcomes sums an agent's checked turns since a time.
func (s *Store) Outcomes(agentID string, since time.Time) (OutcomeSummary, error) {
	var os []AgentOutcome
	if err := s.db.Where("agent_id = ? AND checked AND created_at >= ?", agentID, since).Find(&os).Error; err != nil {
		return OutcomeSummary{}, err
	}
	var out OutcomeSummary
	for _, o := range os {
		out.Checked++
		out.Tokens += o.Tokens
		if o.FirstTry {
			out.FirstTry++
		}
		if o.Failed != "" && o.Failed != "[]" && o.Failed != "null" {
			out.Failed++
		}
	}
	return out, nil
}

// AgentOutcomes lists an agent's recorded turns, oldest first.
func (tx *Tx) AgentOutcomes(agentID string) ([]AgentOutcome, error) {
	var out []AgentOutcome
	return out, tx.db.Where("agent_id = ?", agentID).Order("id").Find(&out).Error
}
