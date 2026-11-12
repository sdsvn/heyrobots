package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"heyrobots/service/internal/google"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/store"
)

// Event and Invite are the payloads the calendar tools queue.
type Event struct {
	Title       string   `json:"title"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	Attendees   []string `json:"attendees,omitempty"`
	Location    string   `json:"location,omitempty"`
	Description string   `json:"description,omitempty"`
}

type Invite struct {
	EventID  string `json:"event_id"`
	Response string `json:"response"`
}

// Sender carries out calendar Effects. Both actions are idempotent: a new
// event's id comes from its Effect, so a repeat finds the event already
// there, and answering an invitation twice gives the same answer.
type Sender struct {
	Google *google.Google
}

// Idempotent is true.
func (s *Sender) Idempotent() bool { return true }

// EventID is the calendar id for an Effect's event. Google allows the
// letters a to v and digits.
func EventID(effectID uint) string { return "hrevent" + strconv.FormatUint(uint64(effectID), 32) }

// Send creates an event or answers an invitation.
func (s *Sender) Send(ctx context.Context, e store.Effect) (string, error) {
	acc, err := s.Google.Account()
	if err != nil {
		return "", outbox.Permanent(err)
	}
	if !google.HasScope(acc, google.ScopeCalendarEdit) {
		return "", outbox.Permanent(errors.New("changing the calendar needs the extra permission; connect Google again and allow it"))
	}
	cal, err := s.Google.Calendar(ctx)
	if err != nil {
		return "", err
	}
	switch e.Action {
	case "create_event":
		var ev Event
		if err := json.Unmarshal([]byte(e.Payload), &ev); err != nil {
			return "", outbox.Permanent(fmt.Errorf("the event's details are damaged: %w", err))
		}
		g := google.Event{ID: EventID(e.ID), Summary: ev.Title, Location: ev.Location, Description: ev.Description,
			Start: google.EventTime{DateTime: ev.Start}, End: google.EventTime{DateTime: ev.End}}
		for _, a := range ev.Attendees {
			g.Attendees = append(g.Attendees, google.Attendee{Email: a})
		}
		out, err := cal.Insert(ctx, g)
		return out.ID, permanent(err)
	case "answer_invite":
		var in Invite
		if err := json.Unmarshal([]byte(e.Payload), &in); err != nil {
			return "", outbox.Permanent(fmt.Errorf("the answer's details are damaged: %w", err))
		}
		return in.EventID, permanent(cal.Respond(ctx, in.EventID, in.Response))
	default:
		return "", outbox.Permanent(fmt.Errorf("the calendar can't do %q", e.Action))
	}
}

func permanent(err error) error {
	var ae *google.APIError
	if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != 429 {
		return outbox.Permanent(err)
	}
	return err
}
