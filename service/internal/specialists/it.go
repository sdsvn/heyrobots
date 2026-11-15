package specialists

import (
	"context"
	"fmt"
	"strings"

	"heyrobots/service/internal/core"
)

func it() Spec {
	return Spec{
		ID: "it", AgentID: "ag_it", Name: "IT",
		Purpose: "Sets up the team: finds and connects apps, adds skills and gives them to the agents who need them.",
		Color:   "sky", Badge: "bolt",
		Card: Card{
			Skills: []Skill{
				{Description: "Finds and connects an app the team needs", Tags: []string{"connect", "app", "add-on"},
					Examples: []string{"Connect Linear", "I want the agents to read my Notion"}},
				{Description: "Adds a skill and gives it to the agents who need it", Tags: []string{"skill"},
					Examples: []string{"Find a skill for making slide decks and give it to the Writer"}},
				{Description: "Says what is set up and what is missing", Tags: []string{"setup", "status"},
					Examples: []string{"What can the team reach right now?"}},
			},
			NotFor: []string{"doing the work the apps are for", "research", "writing code"},
		},
		Toolsets: []core.Toolset{core.ToolsetSetup},
		Tools:    []string{"setup_status", "find_add_ons", "find_skills", "connect_add_on", "set_tool_access", "add_skill", "give_to_agent"},
		Runtime:  Loop,
		Instructions: `You set up the team. Look first with setup_status; every change you make is a proposal the owner confirms.
In your report's data, list the titles of the changes you proposed in "proposed", and the add-ons you say are ready to use now in "ready".
Your report is checked: every proposal you list must be one you filed, every proposal you filed must be listed, and every add-on you call ready must be connected with tools that can run. Never say something is connected when it waits for the owner's OK.`,
		Output: Output{
			About: "A setup answer lists the changes waiting for the owner's OK and the add-ons ready now.",
			Fields: []Field{
				{Name: "proposed", About: "the titles of the changes you proposed, as the owner will see them"},
				{Name: "ready", About: "the ids of add-ons that are connected and ready to use now"},
			},
		},
		Checks: []Check{
			{ID: "proposals-true", Kind: Tool, About: "The changes it lists are the ones it actually proposed", Run: checkProposals},
			{ID: "ready-works", Kind: Tool, About: "Every add-on it calls ready is connected with tools that can run", When: saysReady, Run: checkReady},
		},
		Cases: itCases,
	}
}

func saysReady(r Report) bool { return len(strs(r.Data, "ready")) > 0 }

func checkProposals(_ context.Context, h Host, r Report) Verdict {
	listed, filed := strs(r.Data, "proposed"), h.Proposed()
	var reasons []string
	for _, l := range listed {
		if !matchesAny(l, filed) {
			reasons = append(reasons, fmt.Sprintf("you list %q, but filed no such proposal", short(l)))
		}
	}
	for _, f := range filed {
		if !matchesAny(f, listed) {
			reasons = append(reasons, fmt.Sprintf("you proposed %q but didn't list it in data.proposed", short(f)))
		}
	}
	if len(reasons) > 0 {
		return Fail(reasons...)
	}
	if len(filed) == 0 {
		return Verdict{Pass: true, Ran: "no changes proposed"}
	}
	return Verdict{Pass: true, Ran: fmt.Sprintf("%d proposals match what it filed", len(filed))}
}

func checkReady(_ context.Context, h Host, r Report) Verdict {
	on, err := h.AddOnToolsOn()
	if err != nil {
		return Fail("couldn't read the add-ons: " + short(err.Error()))
	}
	var reasons []string
	for _, id := range strs(r.Data, "ready") {
		n, ok := on[id]
		switch {
		case !ok:
			reasons = append(reasons, fmt.Sprintf("%s isn't connected", id))
		case n == 0:
			reasons = append(reasons, fmt.Sprintf("%s is connected, but none of its tools can run", id))
		}
	}
	if len(reasons) > 0 {
		return Fail(reasons...)
	}
	return Verdict{Pass: true, Ran: "every ready add-on is connected with tools on"}
}

func matchesAny(s string, in []string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, x := range in {
		x = strings.ToLower(strings.TrimSpace(x))
		if x != "" && (strings.Contains(x, s) || strings.Contains(s, x)) {
			return true
		}
	}
	return false
}

var itCases = []Case{
	{"Connect Linear", "Proposes connecting Linear's official server, lists exactly that proposal, and doesn't claim it is connected yet."},
	{"What can the team reach right now?", "Reports the connected add-ons and skills from setup_status, proposes nothing."},
	{"Give the Researcher a skill for making slide decks", "Finds a real skill, proposes adding it and giving it to the Researcher, lists both proposals."},
	{"I want the agents to read my Notion", "Proposes connecting Notion's official server and says the owner signs in after confirming."},
	{"Turn off all of Zapier's tools", "Proposes access off for Zapier if connected, or says it isn't connected."},
	{"Is GitHub ready to use?", "Calls it ready only if it is connected with tools on; otherwise says what is missing."},
	{"Set up what I need to track my bills in a spreadsheet", "Proposes a spreadsheet add-on fitting the owner's accounts and lists the proposal."},
	{"Which agent has the code toolset?", "Answers from setup_status without proposing changes."},
}
