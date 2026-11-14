package outbox

import (
	"encoding/json"
	"strconv"
	"errors"
	"strings"
	"time"

	"heyrobots/service/internal/brain"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/store"
)

// afterSent records a sent action in its agent's brain: the diary line of
// the task that proposed it, and for an email, a check for a reply after a
// few working days. A draft that waits or is declined starts no check.
func afterSent(tx *store.Tx, e store.Effect, now time.Time) error {
	if e.Kind != "action" || e.TicketID == "" || e.Origin != "" {
		return nil
	}
	p, err := tx.Policy()
	if err != nil {
		return err
	}
	loc := p.Limits.Location()
	if err := tx.NoteDiaryOutcome(e.TicketID, "Sent "+now.In(loc).Format("Mon 2 Jan 15:04")+": "+describe(e)); err != nil {
		return err
	}
	if e.Source != string(core.SourceMail) || e.Action != "send_email" {
		return nil
	}
	var d struct {
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		ReplyTo string   `json:"reply_to_message_id"`
	}
	if err := json.Unmarshal([]byte(e.Payload), &d); err != nil || len(d.To) == 0 {
		return nil
	}
	tk, err := tx.Ticket(e.TicketID)
	if err != nil || tk.AgentID == "" {
		return nil
	}
	m, err := tk.DecodeMandate()
	if err != nil {
		return nil
	}
	// A mail that answers one the agent was waiting on a reply to is a
	// nudge in that conversation, even when the check that drafted it has
	// closed; after a reply, or anything else, it starts a new one.
	person, key, nudges := m.Person(), "reply:"+e.Ref, 0
	if d.ReplyTo != "" {
		prev, err := tx.ReplyCheckFor(tk.AgentID, person, d.ReplyTo)
		switch {
		case err == nil && prev.Outcome != store.RepliedOutcome && prev.State != store.FollowUpCancelled && now.Sub(prev.CreatedAt) < 30*24*time.Hour:
			key, nudges = prev.Key, prev.Nudges+1
			if prev.State == store.FollowUpOpen || prev.State == store.FollowUpFired {
				prev.State, prev.Outcome, prev.UpdatedAt = store.FollowUpDone, "Nudged them again.", now
				if err := tx.UpdateFollowUp(prev); err != nil {
					return err
				}
			}
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return err
		}
	}
	to := strings.Join(d.To, ", ")
	m.Toolsets = slicesOr(m.Toolsets)
	mj, _ := json.Marshal(m)
	_, _, err = tx.SaveFollowUp(store.BrainFollowUp{
		ID: ids.New("fu"), AgentID: tk.AgentID, Person: person, Kind: store.FollowUpReply, Mandate: string(mj), Tainted: true,
		ThreadID: e.ThreadID, TicketID: e.TicketID, Key: key, Ref: e.Ref, Nudges: nudges,
		What:  "Check whether " + to + " replied to \"" + d.Subject + "\"",
		Why:   "You emailed " + to + " on " + now.In(loc).Format("Mon 2 Jan"),
		Check: "a reply from them in that mail thread",
		DueAt: brain.WorkingDays(now, brain.ReplyWait, loc), State: store.FollowUpOpen, CreatedAt: now,
	})
	return err
}

// afterDeclined notes a declined action, with the owner's reason, in its
// task's diary line.
func afterDeclined(tx *store.Tx, e store.Effect, note string, now time.Time) error {
	if e.TicketID == "" {
		return nil
	}
	// The owner's own words stay out of a delegate's brain.
	if tk, err := tx.Ticket(e.TicketID); err == nil {
		if m, err := tk.DecodeMandate(); err == nil && m.Role != core.RoleOwner {
			note = ""
		}
	}
	text := "The owner declined " + describe(e)
	if n := strings.TrimSpace(note); n != "" {
		text += ", saying: \"" + n + "\""
	}
	if err := tx.NoteDiaryOutcome(e.TicketID, text); err != nil {
		return err
	}
	return incident(tx, e, store.CauseDeclined, "Declined "+describe(e), note, now)
}

// incident records what went wrong with an action, against the agent whose
// task proposed it.
func incident(tx *store.Tx, e store.Effect, cause, detail, note string, now time.Time) error {
	if e.TicketID == "" || e.Origin != "" {
		return nil
	}
	tk, err := tx.Ticket(e.TicketID)
	if err != nil || tk.AgentID == "" {
		return nil
	}
	m, _ := tk.DecodeMandate()
	return tx.AddIncident(store.Incident{AgentID: tk.AgentID, Person: m.Person(), Cause: cause, Key: e.Action, TicketID: e.TicketID,
		ThreadID: e.ThreadID, Detail: detail, Note: strings.TrimSpace(note), At: now})
}

func slicesOr(ts []core.Toolset) []core.Toolset {
	if ts == nil {
		return []core.Toolset{}
	}
	return ts
}

// waitingOn is the digest's section on what the agents will follow up on
// today, or "" when nothing comes due, so a quiet day makes no digest.
func waitingOn(tx *store.Tx, loc *time.Location, now time.Time) (string, error) {
	all, err := tx.FollowUps("", "", []string{store.FollowUpOpen, store.FollowUpFired}, 200)
	if err != nil {
		return "", err
	}
	var fs []store.BrainFollowUp
	for _, f := range all {
		if f.State == store.FollowUpFired || f.DueAt.Before(now.Add(24*time.Hour)) {
			fs = append(fs, f)
		}
	}
	if len(fs) == 0 {
		return "", nil
	}
	later := len(all) - len(fs)
	var b strings.Builder
	b.WriteString("## Coming up today\n\n")
	names := map[string]string{}
	for _, f := range fs {
		name, ok := names[f.AgentID]
		if !ok {
			name = "An agent"
			if a, err := tx.Agent(f.AgentID); err == nil {
				name = a.Name
			}
			names[f.AgentID] = name
		}
		when := "today " + f.DueAt.In(loc).Format("15:04")
		if f.State == store.FollowUpFired || f.DueAt.Before(now) {
			when = "now"
		}
		b.WriteString("- " + name + ", " + when + ": " + f.What + "\n")
	}
	if later > 0 {
		b.WriteString("- and " + strconv.Itoa(later) + " more later, on each agent's page\n")
	}
	return b.String() + "\n", nil
}

// teamWeek is the Monday digest's section on how each agent's work went in
// the last week, with a suggested fix where something keeps going wrong.
// Only counts, the server's own words and the owner's own notes appear.
func teamWeek(tx *store.Tx, now time.Time) (string, error) {
	since := now.AddDate(0, 0, -7)
	counts, err := tx.TaskCounts(since)
	if err != nil {
		return "", err
	}
	incidents, err := tx.Incidents("", "", since, 1000)
	if err != nil {
		return "", err
	}
	agents, err := tx.Agents()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, a := range agents {
		c := counts[a.ID]
		by := map[string]int{}
		setup := map[string]int{}
		var notes []string
		for _, in := range incidents {
			if in.AgentID != a.ID {
				continue
			}
			by[in.Cause]++
			if in.Cause == store.CauseSetup && !a.Allows(core.Toolset(in.Key)) {
				setup[in.Key]++
			}
			if in.Cause == store.CauseDeclined && in.Note != "" && in.Person == "owner" && len(notes) < 2 {
				notes = append(notes, "\""+in.Note+"\"")
			}
		}
		if c["ready"]+c["failed"]+c["blocked"]+by[store.CauseDeclined]+by[store.CauseStopped]+by[store.CauseSendFailed]+len(setup) == 0 {
			continue
		}
		var parts []string
		add := func(n int, what string) {
			if n > 0 {
				parts = append(parts, strconv.Itoa(n)+" "+what)
			}
		}
		add(c["ready"], "done")
		add(c["failed"], "failed")
		add(c["blocked"], "blocked")
		add(by[store.CauseDeclined], "declined by you")
		add(by[store.CauseStopped], "stopped")
		add(by[store.CauseSendFailed], "couldn't be sent")
		b.WriteString("- " + a.Name + ": " + strings.Join(parts, ", "))
		if len(notes) > 0 {
			b.WriteString(". You said: " + strings.Join(notes, ", "))
		}
		b.WriteString("\n")
		for ts, n := range setup {
			if n >= 2 {
				b.WriteString("  - It was asked " + strconv.Itoa(n) + " times for work that needs " + ts + ", which it can't use. Ask IT to give it " + ts + ", or send that work to another agent.\n")
			}
		}
	}
	if b.Len() == 0 {
		return "", nil
	}
	return "## How the team did this week\n\n" + b.String() + "\n", nil
}
