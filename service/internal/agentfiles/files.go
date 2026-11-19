// Package agentfiles materializes an agent's durable identity and work history.
package agentfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"heyrobots/service/internal/store"
)

// SyncProfile writes the managed identity file for a local agent.
func SyncProfile(root string, a store.Agent) error {
	dir := filepath.Join(root, "agents", a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", a.Name)
	b.WriteString("This file is generated from the agent record. Runtime permissions come from the database.\n\n")
	b.WriteString("## Responsibility\n\n")
	if strings.TrimSpace(a.Purpose) == "" {
		b.WriteString("Support the owner's team within the capabilities granted to this agent.\n")
	} else {
		b.WriteString(strings.TrimSpace(a.Purpose) + "\n")
	}
	b.WriteString("\n## Assumed role\n\n")
	if a.Specialist == "" {
		b.WriteString("Generalist\n")
	} else {
		fmt.Fprintf(&b, "%s specialist\n", a.Specialist)
	}
	b.WriteString("\n## Instructions\n\n")
	if strings.TrimSpace(a.Instructions) == "" {
		b.WriteString("No additional owner instructions.\n")
	} else {
		b.WriteString(strings.TrimSpace(a.Instructions) + "\n")
	}
	b.WriteString("\n## Granted toolsets\n\n")
	if ts := a.ToolsetList(); len(ts) > 0 {
		for _, t := range ts {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	} else {
		b.WriteString("- none\n")
	}
	b.WriteString("\n## Skills\n\n")
	if skills := a.SkillList(); len(skills) > 0 {
		for _, skill := range skills {
			fmt.Fprintf(&b, "- %s\n", skill)
		}
	} else {
		b.WriteString("- none\n")
	}
	return atomicWrite(filepath.Join(dir, "AGENT.md"), []byte(b.String()), 0o600)
}

// CopyProfile puts the managed identity into a task workspace for the worker.
func CopyProfile(root string, a store.Agent, workspace string) error {
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return err
	}
	profile, err := os.ReadFile(filepath.Join(root, "agents", a.ID, "AGENT.md"))
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(workspace, "AGENT.md"), profile, 0o600)
}

// SyncHistory writes the thread view and this Assignment's lifecycle record.
func SyncHistory(root string, a store.Agent, v store.ThreadView, as store.Assignment, state, summary, result string, now time.Time) error {
	dir := filepath.Join(root, "agents", a.ID)
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		return err
	}
	var conversation strings.Builder
	fmt.Fprintf(&conversation, "# Conversation %s\n\n", v.Thread.ID)
	for _, t := range v.Triggers {
		fmt.Fprintf(&conversation, "## %s — %s\n\n%s\n\n", t.At.UTC().Format(time.RFC3339), t.ActorID, t.Text)
	}
	for _, e := range v.Effects {
		if e.Kind != "message" && e.Kind != store.KindUI {
			continue
		}
		fmt.Fprintf(&conversation, "## %s — agent reply\n\n%s\n\n", e.CreatedAt.UTC().Format(time.RFC3339), e.Body)
	}
	if err := atomicWrite(filepath.Join(dir, "conversations", v.Thread.ID+".md"), []byte(conversation.String()), 0o600); err != nil {
		return err
	}
	var session strings.Builder
	fmt.Fprintf(&session, "# Assignment %s\n\n- Agent: %s (%s)\n- Ticket: %s\n- Thread: %s\n- Scope: %s\n- Started: %s\n- Recovered: %t\n- Updated: %s\n- State: %s\n\n",
		as.ID, a.Name, a.ID, as.TicketID, v.Thread.ID, scope(v, as.TicketID), as.StartedAt.UTC().Format(time.RFC3339), as.Recovered, now.UTC().Format(time.RFC3339), state)
	if strings.TrimSpace(summary) != "" {
		fmt.Fprintf(&session, "## Summary\n\n%s\n\n", strings.TrimSpace(summary))
	}
	if strings.TrimSpace(result) != "" {
		fmt.Fprintf(&session, "## Result\n\n%s\n", strings.TrimSpace(result))
	}
	return atomicWrite(filepath.Join(dir, "sessions", as.ID+".md"), []byte(session.String()), 0o600)
}

func scope(v store.ThreadView, ticketID string) string {
	for _, t := range v.Tickets {
		if t.ID == ticketID {
			m, err := t.DecodeMandate()
			if err == nil {
				return m.Scope
			}
		}
	}
	return ""
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-files-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
