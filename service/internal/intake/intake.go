// Package intake stores incoming Triggers exactly once, attaches them to
// their Threads, and delivers them to the Tickets subscribed to those
// Threads. Delivery to a Ticket grants no new authority: each Ticket stays
// limited by its Mandate.
package intake

import (
	"errors"
	"fmt"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gate"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/store"
)

// Message is one event from a Source, before it is stored.
type Message struct {
	Trigger core.Trigger
	// ThreadKeys are the Source's keys for the Threads the Trigger belongs
	// to. A key that has no Thread yet creates one.
	ThreadKeys []string
	// ThreadIDs name existing Threads the Trigger belongs to, whatever
	// their Source: a person's reply in the app to a conversation a
	// schedule started lands on that conversation.
	ThreadIDs []string
	// CreatedBy is recorded on Threads this Message creates.
	CreatedBy core.Identity
	// Hold keeps a message no Ticket took from Eval until the owner's
	// machines agree which one routes it.
	Hold bool
	// Done stores a copy of a message another machine already handled:
	// a Ticket here may still take it, but Eval never sees it.
	Done bool
}

// Result says what intake did with a Message.
type Result struct {
	TriggerID string
	ThreadIDs []string
	// Duplicate is true when the Source had already delivered this event.
	Duplicate bool
	// Owned is true when a Ticket took the message; Eval then sees it only
	// as a follow-up notice.
	Owned bool
	// Resumed lists ended Tickets the message reopened.
	Resumed []string
}

// Intake stores Triggers.
type Intake struct {
	store *store.Store
	now   func() time.Time
}

// New returns an Intake.
func New(s *store.Store, now func() time.Time) *Intake { return &Intake{store: s, now: now} }

// Ingest stores a Message once, in one transaction.
func (in *Intake) Ingest(m Message) (Result, error) {
	t := m.Trigger
	if t.DeliveryID == "" || t.Source == "" || len(m.ThreadKeys)+len(m.ThreadIDs) == 0 {
		return Result{}, errors.New("intake: a Trigger needs a delivery id, a Source and at least one Thread")
	}
	var res Result
	err := in.store.Tx(func(tx *store.Tx) error {
		existing, err := tx.TriggerByDelivery(string(t.Source), t.DeliveryID)
		if err == nil {
			res.TriggerID, res.Duplicate = existing.ID, true
			res.ThreadIDs, err = tx.ThreadIDsOf(existing.ID)
			return err
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		now := in.now().UTC()
		if t.At.IsZero() {
			t.At = now
		}
		for _, id := range m.ThreadIDs {
			th, err := tx.Thread(id)
			if err != nil {
				return err
			}
			res.ThreadIDs = append(res.ThreadIDs, th.ID)
		}
		for _, key := range m.ThreadKeys {
			th, err := tx.ThreadByKey(string(t.Source), key)
			if errors.Is(err, store.ErrNotFound) {
				th = store.Thread{ID: ids.New("th"), Source: string(t.Source), Key: key, CreatedBy: m.CreatedBy.String(), CreatedAt: now, UpdatedAt: now}
				err = tx.CreateThread(th)
			}
			if err != nil {
				return err
			}
			res.ThreadIDs = append(res.ThreadIDs, th.ID)
		}
		row := store.Trigger{
			ID: ids.New("tr"), Source: string(t.Source), DeliveryID: t.DeliveryID,
			ActorSource: string(t.Actor.Source), ActorID: t.Actor.ID, Authenticity: int(t.Authenticity),
			IsBot: t.ActorIsBot, Addressed: t.Addressed, ReceivedAll: t.ReceivedAll,
			Text: t.Text, At: t.At, CreatedAt: now, EvalState: store.EvalPending,
		}
		if err := tx.CreateTrigger(row, res.ThreadIDs); err != nil {
			return err
		}
		res.TriggerID = row.ID
		if err := in.fanOut(tx, row, res.ThreadIDs, now, &res); err != nil {
			return err
		}
		if res.Owned {
			if err := tx.MarkOwned(row.ID); err != nil {
				return err
			}
		} else if m.Hold || m.Done {
			state := store.EvalHeld
			if m.Done {
				state = store.EvalDone
			}
			if err := tx.SetTriggersState([]string{row.ID}, state); err != nil {
				return err
			}
		}
		for _, id := range res.ThreadIDs {
			if err := tx.TouchThread(id, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Two deliveries of the same event can race; the loser sees the winner's row.
		if dup, derr := in.duplicate(t); derr == nil {
			return dup, nil
		}
		return Result{}, fmt.Errorf("intake: %w", err)
	}
	return res, nil
}

func (in *Intake) duplicate(t core.Trigger) (Result, error) {
	var res Result
	err := in.store.Tx(func(tx *store.Tx) error {
		existing, err := tx.TriggerByDelivery(string(t.Source), t.DeliveryID)
		if err != nil {
			return err
		}
		res.TriggerID, res.Duplicate = existing.ID, true
		res.ThreadIDs, err = tx.ThreadIDsOf(existing.ID)
		return err
	})
	return res, err
}

// fanOut delivers the Trigger to every Ticket subscribed to its Threads.
//
//   - A Ticket a person stopped receives nothing.
//   - An open Ticket receives everything, including bot messages, as input.
//   - A person's message to an open Ticket's own Thread is owned by that
//     Ticket; Eval then sees it only as a notice and may only stop.
//   - A person's message reopens an ended Ticket if its Mandate still holds
//     under the current Policy, except in a chat with an agent or a group,
//     where it is new work. Bot messages never reopen a Ticket.
//   - A Thread followed only for context lends no authority and never
//     reopens a Ticket.
func (in *Intake) fanOut(tx *store.Tx, t store.Trigger, threadIDs []string, now time.Time, res *Result) error {
	p, err := tx.Policy()
	if err != nil {
		return err
	}
	delivered := map[string]bool{}
	for _, threadID := range threadIDs {
		tickets, subs, err := tx.SubscribedTickets(threadID)
		if err != nil {
			return err
		}
		th, err := tx.Thread(threadID)
		if err != nil {
			return err
		}
		// A chat with an agent or a group is one long conversation: a new
		// message there is new work, not a reason to reopen every task it
		// ever held. Only a task still open takes it.
		chat := th.AgentID != "" || th.GroupID != ""
		for i, tk := range tickets {
			if tk.StoppedBy != "" || delivered[tk.ID] {
				continue
			}
			sub := subs[i]
			switch {
			case tk.Open && chat && !t.IsBot && !sub.ContextOnly && core.TicketStage(tk.Stage) == core.StageWorking:
				// The task is busy. Whether this message is about it or new
				// work is for Eval to judge, so new work needn't wait behind
				// it; Eval attaches it here when it belongs.
				continue
			case tk.Open:
				if err := tx.AddTicketInput(tk.ID, t.ID, now); err != nil {
					return err
				}
				delivered[tk.ID] = true
				if !t.IsBot && !sub.ContextOnly {
					res.Owned = true
					// A person's reply answers the Ticket's question: it no
					// longer waits on them, and its answer choices go.
					if stage := core.TicketStage(tk.Stage); stage == core.StageQuestion || stage == core.StageBlocked {
						if err := tx.SetTicketState(tk.ID, true, core.StageWorking, "", now); err != nil {
							return err
						}
					}
				}
			case !t.IsBot && !sub.ContextOnly && !chat:
				m, err := tk.DecodeMandate()
				if err != nil {
					return err
				}
				if gate.Revalidate(p, m) != nil {
					// The message is treated as having reached no Ticket.
					continue
				}
				if err := tx.SetTicketState(tk.ID, true, core.StageWorking, "", now); err != nil {
					return err
				}
				if err := tx.AddTicketInput(tk.ID, t.ID, now); err != nil {
					return err
				}
				delivered[tk.ID] = true
				res.Owned = true
				res.Resumed = append(res.Resumed, tk.ID)
			}
		}
	}
	return nil
}
