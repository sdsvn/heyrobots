package outbox

import (
	"encoding/json"
	"errors"
	"strconv"
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
	person, key := m.Person(), brain.ReplyKey(d.To, d.Subject)
	nudges := 0
	last, err := tx.LastFollowUpFor(tk.AgentID, person, key)
	switch {
	case err == nil && now.Sub(last.CreatedAt) < 60*24*time.Hour:
		// This send is a nudge in a conversation the agent is already
		// waiting on.
		nudges = last.Nudges + 1
		if last.State == store.FollowUpOpen || last.State == store.FollowUpFired {
			last.State, last.Outcome, last.UpdatedAt = store.FollowUpDone, "Nudged them again.", now
			if err := tx.UpdateFollowUp(last); err != nil {
				return err
			}
		}
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	}
	to := strings.Join(d.To, ", ")
	if nudges > brain.MaxNudges {
		name := "An agent"
		if a, err := tx.Agent(tk.AgentID); err == nil {
			name = a.Name
		}
		return tx.CreateEffect(store.Effect{TicketID: e.TicketID, ThreadID: e.ThreadID, Source: string(core.SourceApp), Kind: "digest",
			Reach: string(core.ReachOwner), Body: name + ": " + to + " hasn't replied after " + strconv.Itoa(brain.MaxNudges) + " nudges about \"" + d.Subject + "\", so I stopped checking.",
			Status: store.EffectHeld, CreatedAt: now})
	}
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

// waitingOn is the digest's section on what each agent is waiting on,
// soonest first, or "" when nothing is.
func waitingOn(tx *store.Tx, loc *time.Location) (string, error) {
	fs, err := tx.FollowUps("", "", []string{store.FollowUpOpen, store.FollowUpFired}, 30)
	if err != nil || len(fs) == 0 {
		return "", err
	}
	var b strings.Builder
	b.WriteString("## Waiting on\n\n")
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
		when := f.DueAt.In(loc).Format("Mon 2 Jan")
		if f.State == store.FollowUpFired {
			when = "now"
		}
		b.WriteString("- " + name + ", " + when + ": " + f.What + "\n")
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
		if c["ready"]+c["failed"]+c["blocked"]+len(by) == 0 {
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
		add(by[store.CauseRouting], "handed back")
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
