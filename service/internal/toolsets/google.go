package toolsets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/quarantine"
	"heyrobots/service/internal/sources/mail"
	"heyrobots/service/internal/taint"
)

// Mail and calendar tools run in the service, never in the sandbox, so
// tokens stay out of reach of anything the agent runs. Everything other
// people wrote reaches the agent only through the quarantined reader.

type searchMailArgs struct {
	Query    string `json:"query" jsonschema:"a Gmail search, such as 'from:ann@example.com newer_than:7d' or 'subject:invoice'"`
	Question string `json:"question" jsonschema:"what you want to learn from the results"`
}

type readMailArgs struct {
	ID       string `json:"id" jsonschema:"the message id from search_mail"`
	Question string `json:"question" jsonschema:"what you want to learn from the message"`
}

// EmailArgs describe a message to draft or send.
type EmailArgs = mail.Draft

type mailHit struct {
	ID       string    `json:"id"`
	ThreadID string    `json:"thread_id"`
	From     string    `json:"from"`
	At       time.Time `json:"at"`
}

type searchResult struct {
	Messages []mailHit          `json:"messages"`
	Summary  quarantine.Extract `json:"summary"`
}

func mailTools(env Env) ([]tool.Tool, error) {
	if env.Google == nil {
		return nil, errors.New("connect a Google account to use mail")
	}
	search, err := functiontool.New(functiontool.Config{
		Name:        "search_mail",
		Description: "Search the owner's mailbox. Sender addresses and dates come from the mail system; the summary was read in quarantine and is untrusted.",
	}, func(tc agent.Context, a searchMailArgs) (searchResult, error) {
		gm, err := env.Google.Gmail(tc)
		if err != nil {
			return searchResult{}, err
		}
		ids, err := gm.Search(tc, a.Query, 15)
		if err != nil {
			return searchResult{}, err
		}
		var out searchResult
		var doc strings.Builder
		for _, id := range ids {
			m, err := gm.Raw(tc, id)
			if err != nil {
				continue
			}
			p, err := mail.Parse(m.Raw)
			if err != nil {
				continue
			}
			out.Messages = append(out.Messages, mailHit{ID: m.ID, ThreadID: m.ThreadID, From: p.From, At: m.At})
			fmt.Fprintf(&doc, "--- message %s from %s at %s\nSubject: %s\n%s\n\n", m.ID, p.From, m.At.Format(time.RFC3339), p.Subject, shorten(p.Text, 1500))
		}
		if len(out.Messages) == 0 {
			out.Summary = quarantine.Extract{Untrusted: true, Source: "mail search", Answer: "No messages match."}
			return out, nil
		}
		out.Summary, err = env.Reader.Read(tc, "mail search: "+a.Query, doc.String(), a.Question+" Name the message ids you rely on.")
		return out, err
	})
	if err != nil {
		return nil, err
	}
	read, err := functiontool.New(functiontool.Config{
		Name:        "read_mail",
		Description: "Read one message and answer a question about it. The message is read in quarantine; the result is untrusted.",
	}, func(tc agent.Context, a readMailArgs) (quarantine.Extract, error) {
		gm, err := env.Google.Gmail(tc)
		if err != nil {
			return quarantine.Extract{}, err
		}
		m, err := gm.Raw(tc, a.ID)
		if err != nil {
			return quarantine.Extract{Untrusted: true, Source: "mail " + a.ID, Answer: "The message could not be read: " + err.Error()}, nil
		}
		p, err := mail.Parse(m.Raw)
		if err != nil {
			return quarantine.Extract{Untrusted: true, Source: "mail " + a.ID, Answer: "The message could not be read."}, nil
		}
		doc := fmt.Sprintf("From: %s <%s>\nTo: %s\nCc: %s\nDate: %s\nSubject: %s\n\n%s", p.FromName, p.From,
			strings.Join(p.To, ", "), strings.Join(p.Cc, ", "), m.At.Format(time.RFC3339), p.Subject, p.Text)
		return env.Reader.Read(tc, "mail "+a.ID+" from "+p.From, doc, a.Question)
	})
	if err != nil {
		return nil, err
	}
	draft, err := functiontool.New(functiontool.Config{
		Name:        "draft_email",
		Description: "Save an email as a draft in the owner's mailbox. Nothing is sent; the owner can review and send it.",
	}, func(tc agent.Context, a EmailArgs) (map[string]any, error) {
		raw, threadID, err := mail.Build(tc, env.Google, a, "")
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
		gm, err := env.Google.Gmail(tc)
		if err != nil {
			return nil, err
		}
		id, err := gm.Draft(tc, raw, threadID)
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
		return map[string]any{"draft_id": id, "saved": true}, nil
	})
	if err != nil {
		return nil, err
	}
	send, err := functiontool.New(functiontool.Config{
		Name:        "send_email",
		Description: "Send an email from the owner's mailbox. It goes to the outbox, which checks it and may ask the owner to approve it first.",
	}, func(agent.Context, EmailArgs) (map[string]any, error) {
		return nil, errors.New("send_email must go through the outbox")
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{search, read, draft, send}, nil
}

// SendEmailProposal describes a send_email call for the outbox.
func SendEmailProposal(args map[string]any) (Proposal, error) {
	var a EmailArgs
	if err := decodeArgs(args, &a); err != nil {
		return Proposal{}, err
	}
	if len(a.To) == 0 || strings.TrimSpace(a.Body) == "" {
		return Proposal{}, errors.New("an email needs recipients and a body")
	}
	people := append(append([]string{}, a.To...), a.Cc...)
	return Proposal{
		Source: core.SourceMail, Action: "send_email", Reach: core.ReachOthers,
		Details: taint.Details{Recipients: people, Links: taint.Links(a.Body)},
		Summary: a.Subject, Body: a.Body,
		Payload: EmailArgs{To: a.To, Cc: a.Cc, Subject: a.Subject, ReplyTo: a.ReplyTo},
	}, nil
}

type listEventsArgs struct {
	From string `json:"from" jsonschema:"start date, YYYY-MM-DD"`
	To   string `json:"to" jsonschema:"end date, YYYY-MM-DD, inclusive"`
}

type readEventArgs struct {
	ID       string `json:"id" jsonschema:"the event id"`
	Question string `json:"question" jsonschema:"what you want to learn from the event"`
}

// EventArgs describe an event to create.
type EventArgs struct {
	Title       string   `json:"title"`
	Start       string   `json:"start" jsonschema:"RFC 3339 start, such as 2026-11-20T10:00:00+01:00"`
	End         string   `json:"end" jsonschema:"RFC 3339 end"`
	Attendees   []string `json:"attendees,omitempty" jsonschema:"addresses to invite; leave empty for an event only on the owner's calendar"`
	Location    string   `json:"location,omitempty"`
	Description string   `json:"description,omitempty"`
}

// InviteArgs answer an invitation.
type InviteArgs struct {
	EventID  string `json:"event_id"`
	Response string `json:"response" jsonschema:"accepted, declined or tentative"`
	// Organizer and Title are filled in by the server for the approval screen.
	Organizer string `json:"organizer,omitempty"`
	Title     string `json:"title,omitempty"`
}

type eventView struct {
	ID        string   `json:"id"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Title     string   `json:"title"`
	Organizer string   `json:"organizer,omitempty"`
	Attendees []string `json:"attendees,omitempty"`
	MyAnswer  string   `json:"my_answer,omitempty"`
}

func calendarTools(env Env) ([]tool.Tool, error) {
	if env.Google == nil {
		return nil, errors.New("connect a Google account to use the calendar")
	}
	list, err := functiontool.New(functiontool.Config{
		Name:        "list_events",
		Description: "List the owner's calendar events between two dates. Titles of events other people organized are left out; use read_event for those.",
	}, func(tc agent.Context, a listEventsArgs) (map[string]any, error) {
		from, err1 := time.Parse("2006-01-02", a.From)
		to, err2 := time.Parse("2006-01-02", a.To)
		if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) > 62*24*time.Hour {
			return map[string]any{"error": "give dates as YYYY-MM-DD, at most two months apart"}, nil
		}
		cal, err := env.Google.Calendar(tc)
		if err != nil {
			return nil, err
		}
		evs, err := cal.Between(tc, from, to.Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
		var out []eventView
		for _, e := range evs {
			v := eventView{ID: e.ID, Start: e.Start.DateTime + e.Start.Date, End: e.End.DateTime + e.End.Date, Title: e.Summary}
			if e.Organizer != nil && !e.Organizer.Self {
				v.Organizer = e.Organizer.Email
				v.Title = "(organized by someone else; use read_event)"
			}
			for _, at := range e.Attendees {
				if at.Self {
					v.MyAnswer = at.ResponseStatus
				} else {
					v.Attendees = append(v.Attendees, at.Email)
				}
			}
			out = append(out, v)
		}
		return map[string]any{"events": out}, nil
	})
	if err != nil {
		return nil, err
	}
	read, err := functiontool.New(functiontool.Config{
		Name:        "read_event",
		Description: "Read one event's title, place and description and answer a question. Read in quarantine; the result is untrusted.",
	}, func(tc agent.Context, a readEventArgs) (quarantine.Extract, error) {
		cal, err := env.Google.Calendar(tc)
		if err != nil {
			return quarantine.Extract{}, err
		}
		e, err := cal.Get(tc, a.ID)
		if err != nil {
			return quarantine.Extract{Untrusted: true, Source: "event " + a.ID, Answer: "The event could not be read: " + err.Error()}, nil
		}
		doc := fmt.Sprintf("Title: %s\nWhere: %s\nWhen: %s%s to %s%s\n\n%s", e.Summary, e.Location,
			e.Start.DateTime, e.Start.Date, e.End.DateTime, e.End.Date, e.Description)
		return env.Reader.Read(tc, "calendar event "+a.ID, doc, a.Question)
	})
	if err != nil {
		return nil, err
	}
	create, err := functiontool.New(functiontool.Config{
		Name:        "create_event",
		Description: "Add an event to the owner's calendar, inviting attendees if any. It goes to the outbox; invitations to others need the owner's approval.",
	}, func(agent.Context, EventArgs) (map[string]any, error) {
		return nil, errors.New("create_event must go through the outbox")
	})
	if err != nil {
		return nil, err
	}
	answer, err := functiontool.New(functiontool.Config{
		Name:        "answer_invite",
		Description: "Accept, decline or tentatively accept an invitation. It goes to the outbox and the organizer is told.",
	}, func(agent.Context, InviteArgs) (map[string]any, error) {
		return nil, errors.New("answer_invite must go through the outbox")
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{list, read, create, answer}, nil
}

// CreateEventProposal describes a create_event call. An event with no
// attendees reaches only the owner.
func CreateEventProposal(args map[string]any) (Proposal, error) {
	var a EventArgs
	if err := decodeArgs(args, &a); err != nil {
		return Proposal{}, err
	}
	start, err1 := time.Parse(time.RFC3339, a.Start)
	end, err2 := time.Parse(time.RFC3339, a.End)
	if err1 != nil || err2 != nil || !end.After(start) {
		return Proposal{}, errors.New("give start and end as RFC 3339 times, with the end after the start")
	}
	if strings.TrimSpace(a.Title) == "" {
		return Proposal{}, errors.New("an event needs a title")
	}
	reach := core.ReachOwner
	if len(a.Attendees) > 0 {
		reach = core.ReachOthers
	}
	body := fmt.Sprintf("%s\n%s to %s", a.Title, a.Start, a.End)
	if a.Location != "" {
		body += "\nWhere: " + a.Location
	}
	if a.Description != "" {
		body += "\n\n" + a.Description
	}
	return Proposal{
		Source: core.SourceCalendar, Action: "create_event", Reach: reach,
		Details: taint.Details{Recipients: a.Attendees, Dates: []string{a.Start, a.End}, Links: taint.Links(a.Description + " " + a.Location)},
		Summary: a.Title, Body: body, Payload: a,
	}, nil
}

// AnswerInviteProposal returns a describer for answer_invite that looks up
// the organizer, so the check and the approval screen know who hears back.
func AnswerInviteProposal(g *google.Google) ProposalFunc {
	return func(args map[string]any) (Proposal, error) {
		var a InviteArgs
		if err := decodeArgs(args, &a); err != nil {
			return Proposal{}, err
		}
		switch a.Response {
		case "accepted", "declined", "tentative":
		default:
			return Proposal{}, errors.New("response must be accepted, declined or tentative")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cal, err := g.Calendar(ctx)
		if err != nil {
			return Proposal{}, err
		}
		e, err := cal.Get(ctx, a.EventID)
		if err != nil {
			return Proposal{}, fmt.Errorf("that event couldn't be read: %w", err)
		}
		if e.Organizer == nil || e.Organizer.Self {
			return Proposal{}, errors.New("that isn't an invitation from someone else")
		}
		a.Organizer, a.Title = strings.ToLower(e.Organizer.Email), e.Summary
		return Proposal{
			Source: core.SourceCalendar, Action: "answer_invite", Reach: core.ReachOthers,
			Details: taint.Details{Recipients: []string{a.Organizer}},
			Summary: a.Response + ": " + e.Summary,
			Body:    fmt.Sprintf("Answer %q to the invitation %q from %s (%s%s).", a.Response, e.Summary, a.Organizer, e.Start.DateTime, e.Start.Date),
			Payload: a,
		}, nil
	}
}
