package executor

import (
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
)

type teamArgs struct{}

// TeamMember is one agent and the open tasks it leads.
type TeamMember struct {
	Name    string     `json:"name"`
	Purpose string     `json:"purpose,omitempty"`
	You     bool       `json:"you,omitempty"`
	Machine string     `json:"machine,omitempty"`
	Tasks   []TeamTask `json:"tasks"`
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
		Description: "See your teammates and the open tasks each one is doing or waiting on. Use it when the person asks what the team or another agent is working on.",
	}, func(_ agent.Context, _ teamArgs) (map[string]any, error) {
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
		var out []TeamMember
		for _, a := range agents {
			if !a.Enabled {
				continue
			}
			m := TeamMember{Name: a.Name, Purpose: a.Purpose, You: a.ID == r.agent.ID, Tasks: []TeamTask{}}
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
		return map[string]any{"team": out}, nil
	})
}
