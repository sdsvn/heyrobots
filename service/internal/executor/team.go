package executor

import (
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/store"
)

type teamArgs struct {
	Agent string `json:"agent,omitempty" jsonschema:"an agent's name, to see what went wrong in its work over the last 30 days"`
}

// TeamMember is one agent, what it can do, how its work went lately and
// the open tasks it leads.
type TeamMember struct {
	Name     string   `json:"name"`
	Purpose  string   `json:"purpose,omitempty"`
	You      bool     `json:"you,omitempty"`
	Machine  string   `json:"machine,omitempty"`
	Toolsets []string `json:"toolsets,omitempty"`
	Skills   []string `json:"skills,omitempty"`
	// Last30Days counts its tasks by how they ended, and what went wrong.
	Last30Days map[string]int `json:"last_30_days,omitempty"`
	Tasks      []TeamTask     `json:"tasks"`
}

// TeamIncident is one time an agent's work went wrong.
type TeamIncident struct {
	When   string `json:"when"`
	What   string `json:"what"`
	Detail string `json:"detail,omitempty"`
	Owner  string `json:"owner_said,omitempty"`
}

// TeamTask is one open task.
type TeamTask struct {
	Scope string `json:"scope"`
	Stage string `json:"stage"`
	Since string `json:"since"`
}

// teamTool shows the agent what its teammates are doing. It is a tool, not
// part of the prompt, so the prompt stays the same between calls.
func (r *run) teamTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "team",
		Description: "See your teammates: what each can do, how its work went in the last 30 days, and the open tasks it is doing or waiting on. Name an agent to see what went wrong in its work. Use it to decide who is best placed for work, or when the person asks what the team is doing.",
	}, func(_ agent.Context, args teamArgs) (map[string]any, error) {
		if r.mandate.Role != core.RoleOwner {
			return map[string]any{"error": "only the owner's tasks can see the team"}, nil
		}
		agents, err := r.e.Store.Agents()
		if err != nil {
			return nil, err
		}
		tickets, err := r.e.Store.OpenTickets(100)
		if err != nil {
			return nil, err
		}
		since := r.e.Now().Add(-store.IncidentWindow)
		var counts map[string]map[string]int
		var incidents []store.Incident
		_ = r.e.Store.Tx(func(tx *store.Tx) error {
			counts, _ = tx.TaskCounts(since)
			incidents, _ = tx.Incidents("", "", since, 500)
			return nil
		})
		if name := strings.TrimSpace(args.Agent); name != "" {
			return r.teamHistory(agents, incidents, name), nil
		}
		var out []TeamMember
		for _, a := range agents {
			if !a.Enabled {
				continue
			}
			m := TeamMember{Name: a.Name, Purpose: a.Purpose, You: a.ID == r.agent.ID, Tasks: []TeamTask{}, Skills: a.SkillList(), Last30Days: map[string]int{}}
			if a.AnyToolset {
				m.Toolsets = []string{"any"}
			}
			for _, t := range a.ToolsetList() {
				m.Toolsets = append(m.Toolsets, string(t))
			}
			for state, n := range counts[a.ID] {
				if state == "ready" || state == "failed" || state == "blocked" {
					m.Last30Days[map[string]string{"ready": "done", "failed": "failed", "blocked": "blocked"}[state]] = n
				}
			}
			for _, in := range incidents {
				if in.AgentID == a.ID && in.Cause != store.CauseFailed && in.Cause != store.CauseBlocked {
					if in.Cause == store.CauseSetup && a.Allows(core.Toolset(in.Key)) {
						continue
					}
					m.Last30Days[in.Cause]++
				}
			}
			if a.Home != "" {
				m.Machine = "another of your machines; its tasks show there"
			}
			for _, t := range tickets {
				if t.AgentID != a.ID || t.ID == r.c.Ticket.ID {
					continue
				}
				md, _ := t.DecodeMandate()
				m.Tasks = append(m.Tasks, TeamTask{Scope: md.Scope, Stage: t.Stage, Since: t.UpdatedAt.UTC().Format("2006-01-02 15:04 UTC")})
			}
			out = append(out, m)
		}
		return map[string]any{"team": out, "note": "last_30_days counts tasks done, failed and blocked, and times work was declined, stopped, handed back (routing) or asked for a toolset it lacks (setup)"}, nil
	})
}

// teamHistory lists what went wrong in one agent's work. A delegate's
// incidents show as what happened only, without their words.
func (r *run) teamHistory(agents []store.Agent, incidents []store.Incident, name string) map[string]any {
	var a store.Agent
	for _, x := range agents {
		if strings.EqualFold(x.Name, name) {
			a = x
		}
	}
	if a.ID == "" {
		return map[string]any{"error": "there's no agent called " + name}
	}
	loc := r.pol.Limits.Location()
	out := []TeamIncident{}
	for _, in := range incidents {
		if in.AgentID != a.ID {
			continue
		}
		if in.Cause == store.CauseSetup && a.Allows(core.Toolset(in.Key)) {
			continue
		}
		ti := TeamIncident{When: in.At.In(loc).Format("Mon 2 Jan"), What: incidentWhat(in)}
		if in.Person == "owner" {
			if !in.Tainted {
				ti.Detail = shorten(in.Detail, 300)
			}
			ti.Owner = in.Note
		}
		out = append(out, ti)
	}
	return map[string]any{"agent": a.Name, "went_wrong": out,
		"note": "routing means it handed work back as not its own, the router's miss; setup means it lacked a toolset, a setup gap; failed and blocked details are its own reports"}
}

func incidentWhat(in store.Incident) string {
	switch in.Cause {
	case store.CauseSetup:
		return "needed the " + in.Key + " toolset, which it doesn't have"
	case store.CauseRouting:
		return "handed work back as not its own"
	case store.CauseDeclined:
		return "the owner declined its " + strings.ReplaceAll(in.Key, "_", " ")
	case store.CauseStopped:
		return "the owner stopped its task"
	case store.CauseSendFailed:
		return "an approved " + strings.ReplaceAll(in.Key, "_", " ") + " couldn't be sent"
	}
	return "its task " + in.Cause
}
