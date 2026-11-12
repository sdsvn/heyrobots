// Package gate decides, in plain code and before any model call, whether a
// Trigger may start work and with what strength. The router may interpret
// and route an admitted Trigger, but it cannot grant authority: Accept
// re-checks everything the router chose before a Ticket is created.
//
// The rules follow dsh-superagent's authorization model, adapted to one
// owner per Assistant:
//
//   - The owner or a delegate, verified, addressing the Assistant: explicit.
//   - The owner or a delegate following up on a Thread with a Ticket: explicit.
//   - The owner or a delegate not addressing it: inferred.
//   - Anyone else following up on a Thread with a Ticket: inferred.
//   - Anyone else in a scope that receives every message: inferred.
//   - Anyone else addressing the Assistant: not admitted.
//   - A listed bot that addresses it or posts where everything is received:
//     inferred. Other bots: not admitted.
//   - An unverified sender is treated as anyone else, whoever it claims to be.
//   - Every inferred case is not admitted while the accept mode is off.
package gate

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
)

// Input is what the gate judges: one Trigger and the state of its Thread.
type Input struct {
	Trigger core.Trigger
	// ThreadHasTicket is true when the Trigger's Thread already has a Ticket
	// that a person did not stop.
	ThreadHasTicket bool
}

// Admission is the gate's verdict on one Trigger.
type Admission struct {
	Strength core.Strength
	Role     core.Role
	// Delegate names the delegate, when Role is RoleDelegate.
	Delegate string
	FollowUp bool
	Reason   string
}

// Admit judges a Trigger against a Policy. It is a pure function.
func Admit(p policy.Policy, in Input) Admission {
	t := in.Trigger

	if t.ActorIsBot {
		if t.Authenticity != core.Verified || !p.IsListedBot(t.Actor) {
			return notAdmitted(core.RoleBot, "bot not on the bot list")
		}
		if !t.Addressed && !t.ReceivedAll {
			return notAdmitted(core.RoleBot, "bot message neither addressed nor in a receive-all scope")
		}
		return inferred(p, Admission{Role: core.RoleBot, Reason: "listed bot"})
	}

	role, d := core.RoleOther, (*policy.Delegate)(nil)
	if t.Authenticity == core.Verified {
		role, d = p.RoleOf(t.Actor)
	}
	a := Admission{Role: role, FollowUp: in.ThreadHasTicket}
	if d != nil {
		a.Delegate = d.Name
	}

	switch role {
	case core.RoleOwner, core.RoleDelegate:
		switch {
		case t.Addressed:
			a.Strength, a.Reason = core.Explicit, string(role)+" addressed the Assistant"
			return a
		case in.ThreadHasTicket:
			a.Strength, a.Reason = core.Explicit, string(role)+" followed up on a Thread with a Ticket"
			return a
		default:
			a.Reason = string(role) + " did not address the Assistant"
			return inferred(p, a)
		}
	default:
		if t.Authenticity != core.Verified {
			a.Reason = "unverified sender"
		}
		switch {
		case in.ThreadHasTicket:
			a.Reason = joinReason(a.Reason, "follow-up from someone else")
			return inferred(p, a)
		case t.ReceivedAll && !t.Addressed:
			a.Reason = joinReason(a.Reason, "message in a receive-all scope")
			return inferred(p, a)
		default:
			a.Strength = core.NotAdmitted
			a.Reason = joinReason(a.Reason, "someone else may not ask for work")
			return a
		}
	}
}

func inferred(p policy.Policy, a Admission) Admission {
	if p.Mode == policy.ModeOff {
		a.Strength = core.NotAdmitted
		a.Reason = joinReason(a.Reason, "accept mode is off")
		return a
	}
	a.Strength = core.Inferred
	return a
}

func notAdmitted(role core.Role, reason string) Admission {
	return Admission{Strength: core.NotAdmitted, Role: role, Reason: reason}
}

func joinReason(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// Errors returned by Accept and CheckToolsets.
var (
	ErrNotAdmitted     = errors.New("trigger was not admitted")
	ErrObserveOnly     = errors.New("accept mode is observe: record as would accept")
	ErrNoMatchedRule   = errors.New("proactive work needs at least one matching rule")
	ErrUnknownToolset  = errors.New("unknown toolset")
	ErrToolsetRefused  = errors.New("toolset not allowed for this requester")
	ErrUnknownDelegate = errors.New("delegate is no longer in the policy")
	ErrRuleNotEnabled  = errors.New("matched rule is not an enabled rule")
)

// CheckToolsets refuses any toolset the requester may not use. The owner
// may use the toolsets they enabled. A delegate making an explicit request
// may use only their own toolsets that the owner also enabled. Inferred
// work runs under the owner's rules, so it may use the owner's toolsets.
func CheckToolsets(p policy.Policy, a Admission, requested []core.Toolset) error {
	if a.Strength == core.NotAdmitted {
		return ErrNotAdmitted
	}
	allowed := p.Toolsets
	if a.Role == core.RoleDelegate && a.Strength == core.Explicit {
		d, ok := p.DelegateNamed(a.Delegate)
		if !ok {
			return ErrUnknownDelegate
		}
		allowed = nil
		for _, ts := range d.Toolsets {
			if slices.Contains(p.Toolsets, ts) {
				allowed = append(allowed, ts)
			}
		}
	}
	for _, ts := range requested {
		if !ts.IsToolset() {
			return fmt.Errorf("%w: %q", ErrUnknownToolset, ts)
		}
		if !slices.Contains(allowed, ts) {
			return fmt.Errorf("%w: %q", ErrToolsetRefused, ts)
		}
	}
	return nil
}

// AllowedAgents returns the agents a requester's work may go to: any agent
// for the owner's own request, a delegate's agents for theirs, and for
// proactive work the agents its rules name. A delegate or rule that names
// none may use the General agent. any is true when every agent is allowed.
func AllowedAgents(p policy.Policy, a Admission, rules []string) (ids []string, any bool) {
	add := func(list []string) {
		if len(list) == 0 {
			list = []string{core.GeneralAgent}
		}
		for _, id := range list {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	switch {
	case a.Strength == core.Explicit && a.Role == core.RoleOwner:
		return nil, true
	case a.Strength == core.Explicit && a.Role == core.RoleDelegate:
		if d, ok := p.DelegateNamed(a.Delegate); ok {
			add(d.Agents)
		}
	case a.Strength == core.Inferred:
		for _, name := range rules {
			if r, ok := p.EnabledRule(name); ok {
				add(r.Agents)
			}
		}
	}
	return ids, false
}

// ErrAgentRefused is returned when the work may not go to that agent.
var ErrAgentRefused = errors.New("that agent isn't one this requester's work may go to")

// AcceptRequest is what the router asks for when it accepts a Trigger.
type AcceptRequest struct {
	Scope        string
	MatchedRules []string
	// Agent is the id of the agent that leads the work; empty means the
	// General agent.
	Agent    string
	Job      string
	Toolsets []core.Toolset
	At       time.Time
}

// Accept runs inside the router's accept tool, before any Ticket or Mandate
// is written. It is the second deterministic check: the gate ran before the
// router chose toolsets, so Accept checks the router's choices against the
// same Policy and returns the Mandate to store.
func Accept(p policy.Policy, in Input, a Admission, req AcceptRequest) (core.Mandate, error) {
	switch a.Strength {
	case core.Explicit:
	case core.Inferred:
		if p.Mode != policy.ModeAuto {
			return core.Mandate{}, ErrObserveOnly
		}
		if len(req.MatchedRules) == 0 {
			return core.Mandate{}, ErrNoMatchedRule
		}
	default:
		return core.Mandate{}, ErrNotAdmitted
	}
	// The router names the rules it matched, but only enabled rules in the
	// Policy count. An explicit request may name none.
	for _, name := range req.MatchedRules {
		if _, ok := p.EnabledRule(name); !ok {
			return core.Mandate{}, fmt.Errorf("%w: %q", ErrRuleNotEnabled, name)
		}
	}
	if err := CheckToolsets(p, a, req.Toolsets); err != nil {
		return core.Mandate{}, err
	}
	agent := req.Agent
	if agent == "" {
		agent = core.GeneralAgent
	}
	if ids, any := AllowedAgents(p, a, req.MatchedRules); !any && !slices.Contains(ids, agent) {
		return core.Mandate{}, fmt.Errorf("%w: %q", ErrAgentRefused, agent)
	}
	t := in.Trigger
	return core.Mandate{
		PolicyVersion: p.Version,
		Scope:         req.Scope,
		Strength:      a.Strength,
		Role:          a.Role,
		Delegate:      a.Delegate,
		MatchedRules:  slices.Clone(req.MatchedRules),
		Requester:     t.Actor,
		Received: core.Received{
			Source:      t.Source,
			Addressed:   t.Addressed,
			ReceivedAll: t.ReceivedAll,
			FollowUp:    a.FollowUp,
		},
		Toolsets:   slices.Clone(req.Toolsets),
		Agent:      agent,
		Job:        req.Job,
		AcceptedAt: req.At,
	}, nil
}

// Revalidate checks, before an ended Ticket resumes, that its Mandate still
// holds under the current Policy. It follows dsh-superagent's rules:
//
//   - An inferred Mandate needs auto mode and its matched rules still enabled.
//   - Any other Mandate needs its requester to keep their role: the owner
//     still the owner, a delegate still a delegate with the same name.
//
// The Source must also still listen to the Thread; callers check that.
func Revalidate(p policy.Policy, m core.Mandate) error {
	if m.Strength == core.Inferred {
		if p.Mode != policy.ModeAuto {
			return ErrObserveOnly
		}
		for _, name := range m.MatchedRules {
			if _, ok := p.EnabledRule(name); !ok {
				return fmt.Errorf("%w: %q", ErrRuleNotEnabled, name)
			}
		}
		return nil
	}
	role, d := p.RoleOf(m.Requester)
	switch {
	case m.Role == core.RoleOwner && role == core.RoleOwner:
		return nil
	case m.Role == core.RoleDelegate && role == core.RoleDelegate && d.Name == m.Delegate:
		return nil
	}
	return fmt.Errorf("%w: requester no longer holds the role they asked with", ErrNotAdmitted)
}
