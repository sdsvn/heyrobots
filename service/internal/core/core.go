// Package core holds the domain types shared by every part of heyrobots.
//
// It follows the dsh-superagent domain model: a Source produces Triggers,
// Triggers form Threads, Eval decides, and an accept creates a Ticket with an
// immutable Mandate. Core must not import ADK or any model code, so the
// deterministic parts of the system stay plain Go.
package core

import (
	"slices"
	"strings"
	"time"
)

// SourceKind names a kind of Source, such as the app chat or a mailbox.
type SourceKind string

const (
	SourceApp      SourceKind = "app"
	SourceMail     SourceKind = "mail"
	SourceCalendar SourceKind = "calendar"
	SourceSlack    SourceKind = "slack"
	SourceGitHub   SourceKind = "github"
	SourceSchedule SourceKind = "schedule"
	// SourceAgent is a group member asking another member for a piece of
	// work, with the authority of the task it came from.
	SourceAgent SourceKind = "agent"
	// SourceFollowUp is a follow-up an agent set coming due, with the
	// authority of the task that set it.
	SourceFollowUp SourceKind = "followup"
)

// ReplySource is where an answer to a message from src goes: a member's
// request lives in the app's chat, so its answer does too.
func ReplySource(src SourceKind) SourceKind {
	if src == SourceAgent || src == SourceFollowUp {
		return SourceApp
	}
	return src
}

// KnownSources lists every kind of Source.
var KnownSources = []SourceKind{SourceApp, SourceMail, SourceCalendar, SourceSlack, SourceGitHub, SourceSchedule}

// Identity is an actor in one Source's own terms, such as an email address
// on the mail Source or a login on GitHub. The Source normalizes ID (for
// example, lower-cases email addresses) before it reaches the domain.
//
// Identities on different Sources are never mapped to each other: the same
// ID on two Sources is two different identities.
type Identity struct {
	Source SourceKind `json:"source"`
	ID     string     `json:"id"`
}

// String formats an identity as source:id.
func (i Identity) String() string { return string(i.Source) + ":" + i.ID }

// Authenticity says whether a Source could prove who sent a Trigger.
type Authenticity int

const (
	// Unverified means the Source could not prove the sender, for example an
	// email whose DKIM signature does not align with its From domain.
	Unverified Authenticity = iota
	// Verified means the Source proved the sender, for example a paired app
	// device, a signed webhook or an email with aligned DKIM.
	Verified
)

// Trigger is one immutable event from a Source.
type Trigger struct {
	// DeliveryID is unique within its Source and makes intake idempotent.
	DeliveryID   string
	Source       SourceKind
	Actor        Identity
	Authenticity Authenticity
	// ActorIsBot is set by the Source for bot accounts, such as GitHub
	// logins ending in "[bot]" or Slack bot users.
	ActorIsBot bool
	// Addressed means the message addresses the Assistant directly: a
	// mention, a direct message, the app chat, or mail to its address.
	Addressed bool
	// ReceivedAll means the Trigger arrived because its scope delivers every
	// message, such as the owner's inbox, not because it addressed the
	// Assistant.
	ReceivedAll bool
	ThreadKeys  []string
	Text        string
	At          time.Time
}

// Strength is how much authority an admitted Trigger carries.
type Strength string

const (
	// Explicit is a request from a person allowed to ask for work.
	Explicit Strength = "explicit"
	// Inferred is a candidate for proactive work under the owner's rules.
	Inferred Strength = "inferred"
	// NotAdmitted means the Trigger cannot start work.
	NotAdmitted Strength = "not_admitted"
)

// Role is who a sender is to the Assistant.
type Role string

const (
	RoleOwner    Role = "owner"
	RoleDelegate Role = "delegate"
	RoleOther    Role = "other"
	RoleBot      Role = "bot"
)

// Toolset is a kind of work an Executor can do, such as research or code.
type Toolset string

const (
	ToolsetResearch Toolset = "research"
	ToolsetWriting  Toolset = "writing"
	ToolsetCalendar Toolset = "calendar"
	ToolsetMail     Toolset = "mail"
	ToolsetData     Toolset = "data"
	ToolsetCode     Toolset = "code"
	// ToolsetSetup sets up the team: finds and connects add-ons, adds
	// skills and gives them to agents, each change after the owner's OK.
	ToolsetSetup Toolset = "setup"
)

// KnownToolsets lists every built-in toolset the service can run.
var KnownToolsets = []Toolset{
	ToolsetResearch, ToolsetWriting, ToolsetCalendar,
	ToolsetMail, ToolsetData, ToolsetCode, ToolsetSetup,
}

// MCPPrefix starts the name of a toolset that is one of the owner's MCP
// servers, such as "mcp:linear".
const MCPPrefix = "mcp:"

// MCPToolset is the toolset of the MCP server with this name.
func MCPToolset(server string) Toolset { return Toolset(MCPPrefix + server) }

// MCPServer returns the server name of an MCP toolset.
func (t Toolset) MCPServer() (string, bool) {
	name, ok := strings.CutPrefix(string(t), MCPPrefix)
	return name, ok && ValidMCPName(name)
}

// ValidMCPName reports whether a name can name an MCP server: a lower-case
// letter, then up to 23 lower-case letters, digits or underscores. Tool
// names the model sees are built from it, so it must stay short and plain.
func ValidMCPName(name string) bool {
	if len(name) == 0 || len(name) > 24 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// IsToolset reports whether t names a toolset: a built-in one or an MCP
// server's. Whether that server exists is checked when the task runs.
func (t Toolset) IsToolset() bool {
	if _, ok := t.MCPServer(); ok {
		return true
	}
	return slices.Contains(KnownToolsets, t)
}

// Mandate is what an accept grants. It is immutable once created: wider
// scope needs a new Ticket.
type Mandate struct {
	PolicyVersion int
	Scope         string
	Strength      Strength
	Role          Role
	// Delegate names the delegate who asked, when Role is RoleDelegate.
	Delegate     string
	MatchedRules []string
	Requester    Identity
	// Received records how the Source received the Trigger.
	Received Received
	Toolsets []Toolset
	// Agent is the id of the agent that leads the work. Empty means the
	// General agent, as for tasks from before there were agents.
	Agent string `json:",omitempty"`
	// Job names the standing job whose steps the task runs, for work a
	// schedule or trigger started. Empty for free-form work.
	Job string `json:",omitempty"`
	// Removed are toolsets the work asked for that the chat's agent can't
	// use, so the agent can say so instead of guessing.
	Removed    []Toolset `json:",omitempty"`
	AcceptedAt time.Time
}

// Person names whose brain the work reads and writes: "owner", or
// "delegate-<name>" for a delegate's work, so what an agent learns for one
// person never reaches another's tasks.
func (m Mandate) Person() string {
	if m.Role != RoleDelegate {
		return "owner"
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(m.Delegate) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return "delegate-" + strings.TrimRight(b.String(), "-")
}

// GeneralAgent is the id of the built-in agent that can do anything a task
// is granted.
const GeneralAgent = "ag_general"

// LeadAgent returns the Mandate's agent, or the General agent.
func (m Mandate) LeadAgent() string {
	if m.Agent == "" {
		return GeneralAgent
	}
	return m.Agent
}

// Received records how a Trigger reached the Assistant.
type Received struct {
	Source      SourceKind
	Addressed   bool
	ReceivedAll bool
	FollowUp    bool
}

// TicketStage is where a Ticket stands, for the app's "waiting on you" view.
type TicketStage string

const (
	StageWorking  TicketStage = "working"
	StageWaiting  TicketStage = "waiting"
	StageApproval TicketStage = "awaiting_approval"
	StageQuestion TicketStage = "awaiting_answer"
	StageReady    TicketStage = "ready"
	StageBlocked  TicketStage = "blocked"
	StageFailed   TicketStage = "failed"
	StageEnded    TicketStage = "ended"
)

// Ticket is a unit of authorized work. It is open or ended; a person's
// message on one of its Threads reopens it unless a person stopped it.
type Ticket struct {
	ID        string
	Mandate   Mandate
	Open      bool
	Stage     TicketStage
	StoppedBy string
	CreatedAt time.Time
}
