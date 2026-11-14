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
func afterDeclined(tx *store.Tx, e store.Effect, note string) error {
	if e.TicketID == "" {
		return nil
	}
	text := "The owner declined " + describe(e)
	if n := strings.TrimSpace(note); n != "" {
		text += ", saying: \"" + n + "\""
	}
	return tx.NoteDiaryOutcome(e.TicketID, text)
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
