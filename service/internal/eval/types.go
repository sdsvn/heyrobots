// Package eval runs Eval: for each Thread with waiting Triggers, it gates
// every Trigger in plain code, asks the router what the admitted ones mean,
// and checks each of the router's choices again before storing anything.
// It holds one Eval per Thread at a time; Triggers that arrive meanwhile
// form the next Eval's batch.
package eval

import (
	"context"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
)

// Candidate is what the router sees: the admitted Triggers of one batch
// and enough of the Thread to judge them. The router never sees Triggers
// the gate refused.
type Candidate struct {
	ThreadID string
	Source   core.SourceKind
	Mode     policy.AcceptMode
	// Rules are the enabled proactive rules.
	Rules    []policy.Rule
	Triggers []CandidateTrigger
	// Agents is the team: who can lead new work, and how busy each is.
	Agents  []AgentSummary
	Tickets []TicketSummary
	// History holds recent earlier messages on the Thread, oldest first, as
	// context only.
	History []HistoryItem
}

// CandidateTrigger is one admitted Trigger.
type CandidateTrigger struct {
	ID       string
	Strength core.Strength
	Role     core.Role
	Actor    string
	Text     string
	At       time.Time
	// Owned is true for a person's follow-up that a Ticket already took.
	// The router may only stop that Ticket.
	Owned bool
	// Group is set for a message in a group chat, where the members
	// answer and one message may go to several of them.
	Group bool
	// Toolsets are the toolsets this sender may ask for.
	Toolsets []core.Toolset
	// Agents are the ids of the agents this sender's work may go to.
	Agents []string
	// Job is the standing job this trigger starts, for a schedule or a
	// trigger that names one; the router passes it on unchanged.
	Job string
	// HandedBack is why a Ticket's Executor returned this message, and
	// HandedBackFrom is that Ticket. It must not be attached back there.
	HandedBack     string
	HandedBackFrom string
}

// AgentSummary describes one agent to the router: what it's for, which
// toolsets it can use and how busy it is. Never its instructions.
type AgentSummary struct {
	ID       string
	Name     string
	Purpose  string
	Toolsets []core.Toolset
	// AnyToolset means it can use whatever toolsets the work needs.
	AnyToolset bool
	Working    int
	Waiting    int
}

// TicketSummary describes a Ticket on the Thread.
type TicketSummary struct {
	ID      string
	Scope   string
	Open    bool
	Stage   core.TicketStage
	Stopped bool
}

// HistoryItem is an earlier message on the Thread.
type HistoryItem struct {
	Actor string
	Text  string
	At    time.Time
}

// Action is what a Route asks for.
type Action string

const (
	ActionAccept Action = "accept"
	ActionAttach Action = "attach"
	ActionIgnore Action = "ignore"
	ActionStop   Action = "stop"
)

// Route is one routing choice by the router. Eval checks it before acting.
type Route struct {
	Action     Action
	TriggerIDs []string
	// Scope says what the work is, for accept.
	Scope        string
	Toolsets     []core.Toolset
	MatchedRules []string
	// Agent is the id of the agent that leads new work.
	Agent string
	// Job is the standing job the trigger asks to run, when it names one.
	Job string
	// TicketID is the Ticket to attach to or stop.
	TicketID string
	// Note is a short message to the requester, at most 30 words.
	Note   string
	Reason string
}

// Router interprets a Candidate. It can't grant authority: every Route is
// checked again in plain code.
type Router interface {
	Route(ctx context.Context, c Candidate) ([]Route, error)
}
