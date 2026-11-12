// Package calendar turns the owner's Google Calendar into a Source.
//
// Two kinds of event become Triggers, and both can only become proactive
// work under the owner's rules:
//   - An invitation the owner hasn't answered. Anyone can send one, so its
//     organizer is unverified.
//   - A meeting with other people that starts soon, for rules such as
//     "prepare a note before a meeting". A meeting the owner organized
//     carries the owner's calendar identity; any other carries its
//     organizer's, unverified.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
)

// Lead is how long before a meeting the "starts soon" Trigger fires.
const Lead = 30 * time.Minute

// Source polls the connected calendar.
type Source struct {
	Google *google.Google
	Intake *intake.Intake
	Store  *store.Store
	Log    *slog.Logger
	Now    func() time.Time
	OnNew  func()
	Every  time.Duration

	mu   sync.Mutex
	kick chan struct{}
}

// Kick asks for a poll soon.
func (s *Source) Kick() {
	s.mu.Lock()
	if s.kick == nil {
		s.kick = make(chan struct{}, 1)
	}
	ch := s.kick
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Run polls until ctx ends.
func (s *Source) Run(ctx context.Context) {
	s.Kick()
	s.mu.Lock()
	ch := s.kick
	s.mu.Unlock()
	every := s.Every
	if every == 0 {
		every = 2 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-t.C:
		}
		if err := s.Poll(ctx); err != nil && !errors.Is(err, google.ErrNotConnected) {
			s.Log.Warn("calendar poll", "err", err)
		}
	}
}

// Poll looks for new invitations and meetings that start soon.
func (s *Source) Poll(ctx context.Context) error {
	acc, err := s.Google.Account()
	if err != nil {
		return err
	}
	if acc.Status != store.AccountConnected {
		return nil
	}
	cal, err := s.Google.Calendar(ctx)
	if err != nil {
		return err
	}
	stored := 0
	changed, next, err := cal.Changes(ctx, acc.CalendarCursor)
	if errors.Is(err, google.ErrCursorExpired) {
		changed, next, err = cal.Changes(ctx, "")
	}
	if err != nil {
		return err
	}
	for _, e := range changed {
		if t, ok := Invitation(acc, e); ok {
			n, err := s.ingest(t)
			if err != nil {
				return err
			}
			stored += n
		}
	}
	if err := s.Store.Tx(func(tx *store.Tx) error {
		return tx.UpdateAccount(google.Provider, map[string]any{"calendar_cursor": next})
	}); err != nil {
		return err
	}

	now := s.now()
	soon, err := cal.Between(ctx, now.Add(Lead-5*time.Minute), now.Add(Lead+10*time.Minute))
	if err != nil {
		return err
	}
	for _, e := range soon {
		if t, ok := StartsSoon(acc, e, now); ok {
			n, err := s.ingest(t)
			if err != nil {
				return err
			}
			stored += n
		}
	}
	if stored > 0 && s.OnNew != nil {
		s.OnNew()
	}
	return nil
}

// Invitation returns the Trigger for an unanswered invitation.
func Invitation(acc store.Account, e google.Event) (core.Trigger, bool) {
	if e.Status == "cancelled" || e.Organizer == nil || e.Organizer.Self {
		return core.Trigger{}, false
	}
	self, ok := selfAttendee(e)
	if !ok || self.ResponseStatus != "needsAction" {
		return core.Trigger{}, false
	}
	organizer := strings.ToLower(e.Organizer.Email)
	return core.Trigger{
		DeliveryID: "invite:" + e.ID, Source: core.SourceCalendar,
		Actor:        core.Identity{Source: core.SourceCalendar, ID: organizer},
		Authenticity: core.Unverified, ReceivedAll: true,
		ThreadKeys: []string{"event:" + e.ID},
		Text:       "Invitation (not answered yet)\n" + describe(e),
	}, true
}

// StartsSoon returns the Trigger for a meeting with other people that
// starts within the lead time.
func StartsSoon(acc store.Account, e google.Event, now time.Time) (core.Trigger, bool) {
	start := e.Start.Time()
	if e.Status == "cancelled" || e.Start.DateTime == "" || start.Before(now) || start.Sub(now) > Lead+10*time.Minute {
		return core.Trigger{}, false
	}
	others := 0
	for _, a := range e.Attendees {
		if !a.Self {
			others++
		}
	}
	if self, ok := selfAttendee(e); others == 0 || (ok && self.ResponseStatus == "declined") {
		return core.Trigger{}, false
	}
	actor, auth := core.Identity{Source: core.SourceCalendar, ID: acc.Email}, core.Verified
	if e.Organizer != nil && !e.Organizer.Self {
		actor, auth = core.Identity{Source: core.SourceCalendar, ID: strings.ToLower(e.Organizer.Email)}, core.Unverified
	}
	mins := int(start.Sub(now).Round(time.Minute).Minutes())
	return core.Trigger{
		DeliveryID: "soon:" + e.ID + ":" + e.Start.DateTime, Source: core.SourceCalendar,
		Actor: actor, Authenticity: auth, ReceivedAll: true,
		// Its own conversation: on the event's, a meeting the owner
		// organized would look like the owner following up on a task.
		ThreadKeys: []string{"soon:" + e.ID + ":" + e.Start.DateTime},
		Text:       fmt.Sprintf("Meeting starts in %d minutes\n%s", mins, describe(e)),
		At:         now,
	}, true
}

func selfAttendee(e google.Event) (google.Attendee, bool) {
	for _, a := range e.Attendees {
		if a.Self {
			return a, true
		}
	}
	return google.Attendee{}, false
}

func describe(e google.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Event id: %s\nTitle: %s\n", e.ID, e.Summary)
	if e.Start.DateTime != "" {
		fmt.Fprintf(&b, "When: %s to %s\n", e.Start.DateTime, e.End.DateTime)
	} else {
		fmt.Fprintf(&b, "When: all day %s\n", e.Start.Date)
	}
	if e.Location != "" {
		fmt.Fprintf(&b, "Where: %s\n", e.Location)
	}
	if e.Organizer != nil {
		fmt.Fprintf(&b, "Organizer: %s\n", e.Organizer.Email)
	}
	var people []string
	for _, a := range e.Attendees {
		people = append(people, a.Email+" ("+a.ResponseStatus+")")
	}
	if len(people) > 0 {
		fmt.Fprintf(&b, "Attendees: %s\n", strings.Join(people, ", "))
	}
	if d := strings.TrimSpace(e.Description); d != "" {
		if len(d) > 4000 {
			d = d[:4000] + "…"
		}
		fmt.Fprintf(&b, "\n%s\n", d)
	}
	return b.String()
}

func (s *Source) ingest(t core.Trigger) (int, error) {
	res, err := s.Intake.Ingest(intake.Message{Trigger: t, ThreadKeys: t.ThreadKeys, CreatedBy: t.Actor})
	if err != nil || res.Duplicate {
		return 0, err
	}
	return 1, nil
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
