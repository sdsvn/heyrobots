package executor

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/store"
)

// agentSection tells the worker which agent it is and what it remembers.
func (r *run) agentSection() string {
	a := r.agent
	var b strings.Builder
	fmt.Fprintf(&b, "# You are %s\n\n", a.Name)
	if a.Purpose != "" {
		fmt.Fprintf(&b, "Your job on the team: %s\n", a.Purpose)
	}
	if a.ID == core.GeneralAgent {
		b.WriteString("You lead the team's general work. Before you take on work a specialist fits, or work an agent has struggled with, call team to see who can do what and what went wrong lately, and say who would be better placed when that helps the owner.\n")
	}
	if strings.TrimSpace(a.Instructions) != "" {
		b.WriteString("\nThe owner's instructions for you:\n")
		b.WriteString(strings.TrimSpace(a.Instructions))
		b.WriteString("\n")
	}
	return b.String()
}

// chatContextLen is how many earlier messages of a chat a new task sees.
const chatContextLen = 20

// chatSection gives a new task in a chat what was said there lately: the
// people's messages and every agent's replies, oldest first. A chat is one
// long conversation, so a task started in it shouldn't begin knowing
// nothing of it. It goes in the first user turn, so the cached system
// prompt stays the same.
func (r *run) chatSection() string {
	if r.c.Assignment.Kind != store.AssignmentNew {
		return ""
	}
	type line struct {
		at   time.Time
		text string
	}
	var lines []line
	var team []string
	_ = r.e.Store.Tx(func(tx *store.Tx) error {
		v, err := tx.ThreadView(r.c.Ticket.OriginThreadID)
		if err != nil || (v.Thread.AgentID == "" && v.Thread.GroupID == "") {
			return err
		}
		if g, err := tx.Group(v.Thread.GroupID); err == nil {
			for _, id := range g.MemberList() {
				if a, err := tx.Agent(id); err == nil && a.ID != r.agent.ID {
					team = append(team, "@"+a.Name+" ("+a.Purpose+")")
				}
			}
		}
		now := map[string]bool{}
		for _, t := range r.c.Inputs {
			now[t.ID] = true
		}
		for _, t := range v.Triggers {
			if now[t.ID] || t.Source == string(core.SourceAgent) || t.Source == string(core.SourceFollowUp) {
				continue
			}
			who := "Owner"
			if role, _ := r.pol.RoleOf(core.Identity{Source: core.SourceKind(t.ActorSource), ID: t.ActorID}); role != core.RoleOwner {
				who = t.ActorID
			}
			lines = append(lines, line{t.At, who + ": " + shorten(t.Text, 600)})
		}
		names := map[string]string{}
		for _, tk := range v.Tickets {
			if a, err := tx.Agent(tk.AgentID); err == nil {
				names[tk.ID] = a.Name
			}
		}
		for _, e := range v.Effects {
			if e.Kind != "message" || e.TicketID == r.c.Ticket.ID {
				continue
			}
			who := names[e.TicketID]
			if who == "" {
				who = "Bit"
			}
			if who == r.agent.Name {
				who = "You (" + who + ")"
			}
			lines = append(lines, line{e.CreatedAt, who + ": " + shorten(e.Body, 600)})
		}
		return nil
	})
	var b strings.Builder
	if len(team) > 0 {
		b.WriteString("This is a group chat. Your teammates here: " + strings.Join(team, ", ") +
			". To give one of them a piece of the work, write @Name and what you need in your reply; they get it as a request and answer here. Only do that when the piece fits them better than you.\n\n")
	}
	if len(lines) == 0 {
		return b.String()
	}
	slices.SortStableFunc(lines, func(a, b line) int { return a.at.Compare(b.at) })
	b.WriteString("Earlier in this chat, oldest first, as context only")
	if len(lines) > chatContextLen {
		lines = lines[len(lines)-chatContextLen:]
		b.WriteString(" (older messages left out)")
	}
	b.WriteString(":\n<chat>\n")
	for _, l := range lines {
		b.WriteString(l.text + "\n")
	}
	b.WriteString("</chat>\n\n")
	return b.String()
}

// workspace is the task's folder. In a chat, an agent keeps one folder for
// all its tasks there, so a file it made earlier in the chat is still
// there when a later message asks about it; elsewhere each task has its own.
//
// ponytail: an agent working on two things at once in one chat shares the
// folder, and each run rewrites its instructions file there; per-run
// instruction files if that ever confuses an agent.
func (r *run) workspace() string {
	if r.ws != "" {
		return r.ws
	}
	r.ws = filepath.Join(r.e.Workspaces, r.c.Ticket.ID)
	_ = r.e.Store.Tx(func(tx *store.Tx) error {
		th, err := tx.Thread(r.c.Ticket.OriginThreadID)
		if err == nil && (th.AgentID != "" || th.GroupID != "") {
			r.ws = filepath.Join(r.e.Workspaces, "chats", th.ID, r.agent.ID)
		}
		return err
	})
	return r.ws
}
