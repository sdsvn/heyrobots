package store

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"heyrobots/service/internal/core"
)

// Agent is one standing member of the team: a persistent teammate with its
// own purpose, tools, skills and model, not a copy started per task.
type Agent struct {
	ID           string `gorm:"primaryKey"`
	Name         string
	Purpose      string
	Instructions string
	// Toolsets is a JSON array of the toolsets the agent may use. A task
	// gives it only those its Mandate also grants.
	Toolsets string
	// AnyToolset lets the agent use whatever a task grants, as the General
	// agent does.
	AnyToolset bool
	// Skills is a JSON array of skill names from the library.
	Skills string
	// Provider, Model and BaseURL choose the agent's own model; empty means
	// the worker's.
	Provider string
	Model    string
	BaseURL  string
	// Capacity is how many tasks it works on at once.
	Capacity int
	Color    string
	Badge    string
	Builtin  bool
	Enabled  bool
	// Home is the machine that runs a teammate from another machine; empty
	// for this machine's own agents. Such a row is only a card.
	Home string
	// FollowUpsOff holds the agent's due follow-ups until it is turned
	// back on.
	FollowUpsOff bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ToolsetList returns the agent's toolsets.
func (a Agent) ToolsetList() []core.Toolset {
	var ts []core.Toolset
	_ = json.Unmarshal([]byte(a.Toolsets), &ts)
	return ts
}

// SkillList returns the agent's skills.
func (a Agent) SkillList() []string {
	var ss []string
	_ = json.Unmarshal([]byte(a.Skills), &ss)
	return ss
}

// Allows reports whether the agent may use a toolset.
func (a Agent) Allows(ts core.Toolset) bool {
	if a.AnyToolset {
		return true
	}
	for _, t := range a.ToolsetList() {
		if t == ts {
			return true
		}
	}
	return false
}

// Agents lists the team, the General agent first.
func (s *Store) Agents() ([]Agent, error) {
	var as []Agent
	return as, s.db.Order("builtin DESC, name").Find(&as).Error
}

// Agent returns one agent.
func (s *Store) Agent(id string) (Agent, error) {
	var a Agent
	err := s.db.First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Agent{}, ErrNotFound
	}
	return a, err
}

// SaveAgent creates or replaces an agent.
func (s *Store) SaveAgent(a Agent) error { return s.db.Save(&a).Error }

// ErrAgentBusy is returned when an agent still has open tasks.
var ErrAgentBusy = errors.New("the agent still has open tasks")

// DeleteAgent removes an agent that has no open tasks. The built-in agent
// can't be removed.
func (s *Store) DeleteAgent(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var a Agent
		if err := tx.First(&a, "id = ?", id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if a.Builtin {
			return errors.New("the General agent can't be removed")
		}
		var open int64
		if err := tx.Model(&Ticket{}).Where("agent_id = ? AND open", id).Count(&open).Error; err != nil {
			return err
		}
		if open > 0 {
			return ErrAgentBusy
		}
		return tx.Delete(&Agent{}, "id = ?", id).Error
	})
}

// AgentLoad is what an agent is doing now.
type AgentLoad struct {
	// Running are the tasks it is working on.
	Running []Ticket
	// Waiting counts its open tasks with new input that wait for it.
	Waiting int
}

// AgentLoads returns what every agent is doing.
func (s *Store) AgentLoads() (map[string]AgentLoad, error) {
	out := map[string]AgentLoad{}
	var running []Ticket
	if err := s.db.Where("id IN (SELECT ticket_id FROM assignments WHERE status = ?)", AssignmentRunning).Find(&running).Error; err != nil {
		return nil, err
	}
	for _, t := range running {
		l := out[t.AgentID]
		l.Running = append(l.Running, t)
		out[t.AgentID] = l
	}
	var waiting []struct {
		AgentID string
		N       int
	}
	err := s.db.Model(&Ticket{}).Select("agent_id, COUNT(*) AS n").
		Where("open AND NOT EXISTS (SELECT 1 FROM assignments a WHERE a.ticket_id = tickets.id AND a.status = ?)", AssignmentRunning).
		Where("EXISTS (SELECT 1 FROM ticket_inputs i WHERE i.ticket_id = tickets.id AND i.delivered_at IS NULL)").
		Group("agent_id").Scan(&waiting).Error
	if err != nil {
		return nil, err
	}
	for _, w := range waiting {
		l := out[w.AgentID]
		l.Waiting = w.N
		out[w.AgentID] = l
	}
	return out, nil
}

// Agent returns one agent, inside a transaction.
func (tx *Tx) Agent(id string) (Agent, error) {
	var a Agent
	err := tx.db.First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Agent{}, ErrNotFound
	}
	return a, err
}

// OpenTickets returns the open Tickets, most recently changed first.
func (s *Store) OpenTickets(limit int) ([]Ticket, error) {
	var out []Ticket
	err := s.db.Where("open").Order("updated_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
