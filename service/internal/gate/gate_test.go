package gate

import (
	"errors"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
)

var (
	ownerMail  = core.Identity{Source: core.SourceMail, ID: "owner@example.com"}
	ownerApp   = core.Identity{Source: core.SourceApp, ID: "device-owner"}
	ownerSlack = core.Identity{Source: core.SourceSlack, ID: "U-OWNER"}
	partner    = core.Identity{Source: core.SourceMail, ID: "partner@example.com"}
	stranger   = core.Identity{Source: core.SourceMail, ID: "stranger@example.net"}
	dependabot = core.Identity{Source: core.SourceGitHub, ID: "dependabot[bot]"}
	otherBot   = core.Identity{Source: core.SourceGitHub, ID: "random[bot]"}
)

func testPolicy(mode policy.AcceptMode) policy.Policy {
	return policy.Policy{
		Version:  7,
		Mode:     mode,
		Owner:    []core.Identity{ownerMail, ownerApp, ownerSlack},
		Toolsets: []core.Toolset{core.ToolsetResearch, core.ToolsetWriting, core.ToolsetCalendar, core.ToolsetMail},
		Delegates: []policy.Delegate{{
			Name:       "partner",
			Identities: []core.Identity{partner},
			// Code is listed but not enabled by the owner, so it stays refused.
			Toolsets: []core.Toolset{core.ToolsetCalendar, core.ToolsetCode},
		}},
		Bots: []core.Identity{dependabot},
		Rules: []policy.Rule{
			{Name: "bills", Text: "When I forward a bill, add the due date to my calendar.", Enabled: true},
			{Name: "r1", Text: "test rule", Enabled: true},
			{Name: "off", Text: "disabled rule", Enabled: false},
		},
	}
}

func trig(actor core.Identity, auth core.Authenticity, addressed, receivedAll bool) core.Trigger {
	return core.Trigger{
		DeliveryID:   "d1",
		Source:       actor.Source,
		Actor:        actor,
		Authenticity: auth,
		Addressed:    addressed,
		ReceivedAll:  receivedAll,
		ActorIsBot:   actor == dependabot || actor == otherBot,
	}
}

func TestAdmit(t *testing.T) {
	const v, u = core.Verified, core.Unverified
	tests := []struct {
		name      string
		mode      policy.AcceptMode
		trigger   core.Trigger
		hasTicket bool
		want      core.Strength
		wantRole  core.Role
	}{
		{"owner addresses the Assistant", policy.ModeObserve, trig(ownerApp, v, true, false), false, core.Explicit, core.RoleOwner},
		{"owner by verified email", policy.ModeObserve, trig(ownerMail, v, true, false), false, core.Explicit, core.RoleOwner},
		{"owner request works with accept mode off", policy.ModeOff, trig(ownerApp, v, true, false), false, core.Explicit, core.RoleOwner},
		{"owner follows up on a Thread with a Ticket", policy.ModeObserve, trig(ownerMail, v, false, true), true, core.Explicit, core.RoleOwner},
		{"owner not addressing the Assistant is inferred", policy.ModeObserve, trig(ownerMail, v, false, true), false, core.Inferred, core.RoleOwner},

		{"forged owner email is not admitted", policy.ModeObserve, trig(ownerMail, u, true, false), false, core.NotAdmitted, core.RoleOther},
		{"forged owner email on a Ticket Thread is only inferred", policy.ModeObserve, trig(ownerMail, u, true, false), true, core.Inferred, core.RoleOther},

		{"delegate addresses the Assistant", policy.ModeObserve, trig(partner, v, true, false), false, core.Explicit, core.RoleDelegate},
		{"delegate follows up on a Ticket Thread", policy.ModeObserve, trig(partner, v, false, true), true, core.Explicit, core.RoleDelegate},

		{"someone else addressing the Assistant is not admitted", policy.ModeAuto, trig(stranger, v, true, false), false, core.NotAdmitted, core.RoleOther},
		{"someone else in the inbox is an inferred candidate", policy.ModeObserve, trig(stranger, v, false, true), false, core.Inferred, core.RoleOther},
		{"someone else following up is inferred", policy.ModeObserve, trig(stranger, v, false, false), true, core.Inferred, core.RoleOther},
		{"someone else is not admitted when accept mode is off", policy.ModeOff, trig(stranger, v, false, true), false, core.NotAdmitted, core.RoleOther},
		{"someone else following up is not admitted when mode is off", policy.ModeOff, trig(stranger, v, false, false), true, core.NotAdmitted, core.RoleOther},

		{"listed bot addressing the Assistant is inferred", policy.ModeObserve, trig(dependabot, v, true, false), false, core.Inferred, core.RoleBot},
		{"listed bot never becomes explicit on a Ticket Thread", policy.ModeObserve, trig(dependabot, v, true, false), true, core.Inferred, core.RoleBot},
		{"listed bot not addressing and not receive-all", policy.ModeObserve, trig(dependabot, v, false, false), false, core.NotAdmitted, core.RoleBot},
		{"unlisted bot is not admitted", policy.ModeAuto, trig(otherBot, v, true, false), false, core.NotAdmitted, core.RoleBot},
		{"listed bot is not admitted when mode is off", policy.ModeOff, trig(dependabot, v, true, false), false, core.NotAdmitted, core.RoleBot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Admit(testPolicy(tt.mode), Input{Trigger: tt.trigger, ThreadHasTicket: tt.hasTicket})
			if got.Strength != tt.want || got.Role != tt.wantRole {
				t.Fatalf("got %s/%s (%s), want %s/%s", got.Strength, got.Role, got.Reason, tt.want, tt.wantRole)
			}
			if got.Reason == "" {
				t.Fatal("every admission must give a reason")
			}
		})
	}
}

func TestAdmitNeverMapsIdentitiesAcrossSources(t *testing.T) {
	// The owner's Slack ID sent through GitHub is a different identity.
	spoof := core.Identity{Source: core.SourceGitHub, ID: ownerSlack.ID}
	got := Admit(testPolicy(policy.ModeAuto), Input{Trigger: trig(spoof, core.Verified, true, false)})
	if got.Strength != core.NotAdmitted || got.Role != core.RoleOther {
		t.Fatalf("got %s/%s, want not_admitted/other", got.Strength, got.Role)
	}
}

func TestAdmitRecordsDelegateName(t *testing.T) {
	got := Admit(testPolicy(policy.ModeObserve), Input{Trigger: trig(partner, core.Verified, true, false)})
	if got.Delegate != "partner" {
		t.Fatalf("Delegate = %q, want partner", got.Delegate)
	}
}

func TestCheckToolsets(t *testing.T) {
	p := testPolicy(policy.ModeAuto)
	owner := Admit(p, Input{Trigger: trig(ownerApp, core.Verified, true, false)})
	delegate := Admit(p, Input{Trigger: trig(partner, core.Verified, true, false)})
	inferredFromInbox := Admit(p, Input{Trigger: trig(stranger, core.Verified, false, true)})
	refused := Admit(p, Input{Trigger: trig(stranger, core.Verified, true, false)})

	tests := []struct {
		name      string
		admission Admission
		toolsets  []core.Toolset
		wantErr   error
	}{
		{"owner uses enabled toolsets", owner, []core.Toolset{core.ToolsetResearch, core.ToolsetMail}, nil},
		{"owner can't use a toolset they didn't enable", owner, []core.Toolset{core.ToolsetCode}, ErrToolsetRefused},
		{"unknown toolset is refused", owner, []core.Toolset{"shell"}, ErrUnknownToolset},
		{"delegate uses their own toolset", delegate, []core.Toolset{core.ToolsetCalendar}, nil},
		{"delegate can't use the owner's other toolsets", delegate, []core.Toolset{core.ToolsetMail}, ErrToolsetRefused},
		{"delegate can't use a toolset the owner didn't enable", delegate, []core.Toolset{core.ToolsetCode}, ErrToolsetRefused},
		{"inferred work uses the owner's toolsets", inferredFromInbox, []core.Toolset{core.ToolsetMail}, nil},
		{"not admitted can't use any toolset", refused, nil, ErrNotAdmitted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckToolsets(p, tt.admission, tt.toolsets); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckToolsetsAfterDelegateRemoved(t *testing.T) {
	p := testPolicy(policy.ModeAuto)
	a := Admit(p, Input{Trigger: trig(partner, core.Verified, true, false)})
	p.Delegates = nil
	if err := CheckToolsets(p, a, []core.Toolset{core.ToolsetCalendar}); !errors.Is(err, ErrUnknownDelegate) {
		t.Fatalf("err = %v, want ErrUnknownDelegate", err)
	}
}

func TestAccept(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	inbox := Input{Trigger: trig(stranger, core.Verified, false, true)}
	ownerReq := Input{Trigger: trig(ownerApp, core.Verified, true, false)}

	t.Run("observe mode records would-accept instead of accepting", func(t *testing.T) {
		p := testPolicy(policy.ModeObserve)
		_, err := Accept(p, inbox, Admit(p, inbox), AcceptRequest{MatchedRules: []string{"bills"}})
		if !errors.Is(err, ErrObserveOnly) {
			t.Fatalf("err = %v, want ErrObserveOnly", err)
		}
	})
	t.Run("auto mode needs a matching rule", func(t *testing.T) {
		p := testPolicy(policy.ModeAuto)
		_, err := Accept(p, inbox, Admit(p, inbox), AcceptRequest{})
		if !errors.Is(err, ErrNoMatchedRule) {
			t.Fatalf("err = %v, want ErrNoMatchedRule", err)
		}
	})
	t.Run("auto mode accepts with a rule", func(t *testing.T) {
		p := testPolicy(policy.ModeAuto)
		m, err := Accept(p, inbox, Admit(p, inbox), AcceptRequest{
			Scope: "add bill due date", MatchedRules: []string{"bills"},
			Toolsets: []core.Toolset{core.ToolsetCalendar}, At: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		if m.Strength != core.Inferred || len(m.MatchedRules) != 1 || !m.Received.ReceivedAll {
			t.Fatalf("unexpected mandate %+v", m)
		}
	})
	t.Run("explicit request is accepted in every mode", func(t *testing.T) {
		for _, mode := range []policy.AcceptMode{policy.ModeOff, policy.ModeObserve, policy.ModeAuto} {
			p := testPolicy(mode)
			if _, err := Accept(p, ownerReq, Admit(p, ownerReq), AcceptRequest{Toolsets: []core.Toolset{core.ToolsetResearch}}); err != nil {
				t.Fatalf("mode %s: %v", mode, err)
			}
		}
	})
	t.Run("router's toolset choice is checked at accept time", func(t *testing.T) {
		p := testPolicy(policy.ModeAuto)
		_, err := Accept(p, ownerReq, Admit(p, ownerReq), AcceptRequest{Toolsets: []core.Toolset{core.ToolsetCode}})
		if !errors.Is(err, ErrToolsetRefused) {
			t.Fatalf("err = %v, want ErrToolsetRefused", err)
		}
	})
	t.Run("not admitted is never accepted", func(t *testing.T) {
		p := testPolicy(policy.ModeAuto)
		in := Input{Trigger: trig(stranger, core.Verified, true, false)}
		if _, err := Accept(p, in, Admit(p, in), AcceptRequest{}); !errors.Is(err, ErrNotAdmitted) {
			t.Fatalf("err = %v, want ErrNotAdmitted", err)
		}
	})
	t.Run("mandate records the policy version, requester and how it arrived", func(t *testing.T) {
		p := testPolicy(policy.ModeObserve)
		in := Input{Trigger: trig(partner, core.Verified, false, false), ThreadHasTicket: true}
		rules := []string{"r1"}
		toolsets := []core.Toolset{core.ToolsetCalendar}
		m, err := Accept(p, in, Admit(p, in), AcceptRequest{Scope: "book dinner", MatchedRules: rules, Toolsets: toolsets, At: at})
		if err != nil {
			t.Fatal(err)
		}
		if m.PolicyVersion != 7 || m.Requester != partner || m.Role != core.RoleDelegate ||
			m.Delegate != "partner" || !m.Received.FollowUp || m.Scope != "book dinner" || !m.AcceptedAt.Equal(at) {
			t.Fatalf("unexpected mandate %+v", m)
		}
		// The Mandate keeps its own copies, so later changes can't alter it.
		rules[0], toolsets[0] = "changed", core.ToolsetCode
		if m.MatchedRules[0] != "r1" || m.Toolsets[0] != core.ToolsetCalendar {
			t.Fatal("mandate shares slices with the request")
		}
	})
}

func TestAcceptRefusesRulesThatAreNotEnabled(t *testing.T) {
	p := testPolicy(policy.ModeAuto)
	in := Input{Trigger: trig(stranger, core.Verified, false, true)}
	for _, rule := range []string{"off", "invented-by-the-router"} {
		_, err := Accept(p, in, Admit(p, in), AcceptRequest{MatchedRules: []string{rule}})
		if !errors.Is(err, ErrRuleNotEnabled) {
			t.Fatalf("rule %q: err = %v, want ErrRuleNotEnabled", rule, err)
		}
	}
}

func TestRevalidate(t *testing.T) {
	p := testPolicy(policy.ModeAuto)
	ownerReq := Input{Trigger: trig(ownerApp, core.Verified, true, false)}
	ownerMandate, _ := Accept(p, ownerReq, Admit(p, ownerReq), AcceptRequest{})
	delegateReq := Input{Trigger: trig(partner, core.Verified, true, false)}
	delegateMandate, _ := Accept(p, delegateReq, Admit(p, delegateReq), AcceptRequest{})
	inbox := Input{Trigger: trig(stranger, core.Verified, false, true)}
	inferredMandate, _ := Accept(p, inbox, Admit(p, inbox), AcceptRequest{MatchedRules: []string{"bills"}})

	if err := Revalidate(p, ownerMandate); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if err := Revalidate(p, delegateMandate); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if err := Revalidate(p, inferredMandate); err != nil {
		t.Fatalf("inferred: %v", err)
	}

	noDelegate := testPolicy(policy.ModeAuto)
	noDelegate.Delegates = nil
	if err := Revalidate(noDelegate, delegateMandate); err == nil {
		t.Fatal("a removed delegate's Mandate must not revalidate")
	}
	observe := testPolicy(policy.ModeObserve)
	if err := Revalidate(observe, inferredMandate); err == nil {
		t.Fatal("an inferred Mandate must not revalidate outside auto mode")
	}
	ruleOff := testPolicy(policy.ModeAuto)
	ruleOff.Rules[0].Enabled = false
	if err := Revalidate(ruleOff, inferredMandate); err == nil {
		t.Fatal("an inferred Mandate must not revalidate once its rule is off")
	}
}

func TestMCPToolsetsAreToolsets(t *testing.T) {
	p := policy.Policy{Toolsets: []core.Toolset{core.ToolsetResearch, core.MCPToolset("tracker")}}
	owner := Admission{Strength: core.Explicit, Role: core.RoleOwner}
	if err := CheckToolsets(p, owner, []core.Toolset{"mcp:tracker"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckToolsets(p, owner, []core.Toolset{"mcp:linear"}); !errors.Is(err, ErrToolsetRefused) {
		t.Fatalf("a server the owner didn't enable: %v", err)
	}
	if err := CheckToolsets(p, owner, []core.Toolset{"mcp:Bad Name"}); !errors.Is(err, ErrUnknownToolset) {
		t.Fatalf("a malformed name: %v", err)
	}
}
