package outbox

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// ErrNotAwaiting is returned when an Effect isn't waiting for approval.
var ErrNotAwaiting = errors.New("that action isn't waiting for approval")

// Decision is the owner's answer to an action waiting for approval.
type Decision struct {
	EffectID uint
	Approve  bool
	// DeviceID is the owner device that answered.
	DeviceID string
	// Note is the owner's own words, passed to the task on a rejection.
	Note string
	// UserVerified is true when the app confirmed the person with
	// biometrics just before sending the answer.
	UserVerified bool
	At           time.Time
}

// ErrElsewhere is returned for an action another machine holds.
var ErrElsewhere = errors.New("this action belongs to another machine")

// Decide records the owner's answer. Only owner devices may call it; the
// caller checks the role. An approved action goes back to the outbox,
// where it is checked again just before sending. A rejected one is never
// sent, and its task hears about it so it can change course.
//
// The answer is recorded as a Trigger from the owner on the app, so it is
// part of the task's history and audit trail like any other message. A
// rejection's record names no recipient: the owner's words become trusted
// for later actions, and a declined recipient must never become trusted
// that way.
func Decide(tx *store.Tx, d Decision) (store.Effect, error) {
	e, err := tx.Effect(d.EffectID)
	if err != nil {
		return store.Effect{}, err
	}
	if e.Status != store.EffectAwaitingApproval {
		return e, ErrNotAwaiting
	}
	if e.Origin != "" {
		// A copy of another machine's action: only its own machine decides it.
		return e, ErrElsewhere
	}
	now := d.At.UTC()
	updates := map[string]any{"decided_by": d.DeviceID, "decided_at": now}
	var text string
	if d.Approve {
		updates["status"], updates["approval"], updates["last_error"] = store.EffectPending, store.ApprovalApproved, ""
		text = "Approved: " + describe(e) + "."
		if d.UserVerified {
			text += " (Confirmed on the device with biometrics.)"
		}
	} else {
		updates["status"], updates["approval"] = store.EffectRejected, store.ApprovalRejected
		text = fmt.Sprintf("I declined the proposed %s (action %d). Don't send it.", strings.ReplaceAll(e.Action, "_", " "), e.ID)
		if n := strings.TrimSpace(d.Note); n != "" {
			text += "\n\n" + n
		}
	}
	ok, err := tx.UpdateEffect(e.ID, store.EffectAwaitingApproval, updates)
	if err != nil {
		return e, err
	}
	if !ok {
		return e, ErrNotAwaiting
	}
	tr := store.Trigger{
		ID: ids.New("tr"), Source: string(core.SourceApp), DeliveryID: d.DeviceID + ":decision:" + strconv.FormatUint(uint64(e.ID), 10),
		ActorSource: string(policy.AppOwner.Source), ActorID: policy.AppOwner.ID, Authenticity: int(core.Verified),
		Addressed: true, ReceivedAll: true, Text: text, At: now, CreatedAt: now, EvalState: store.EvalDone, Owned: true,
	}
	if err := tx.CreateTrigger(tr, []string{e.ThreadID}); err != nil {
		return e, err
	}
	if err := tx.TouchThread(e.ThreadID, now); err != nil {
		return e, err
	}
	if !d.Approve && e.TicketID != "" {
		tk, err := tx.Ticket(e.TicketID)
		if err != nil {
			return e, err
		}
		if tk.Open && tk.StoppedBy == "" {
			if err := tx.AddTicketInput(tk.ID, tr.ID, now); err != nil {
				return e, err
			}
		}
	}
	return tx.Effect(e.ID)
}
