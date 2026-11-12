package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"heyrobots/service/internal/google"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/store"
)

// Draft is a message to draft or send, as a tool describes it.
type Draft struct {
	To      []string `json:"to" jsonschema:"recipient addresses"`
	Cc      []string `json:"cc,omitempty" jsonschema:"copy addresses"`
	Subject string   `json:"subject" jsonschema:"the subject line"`
	Body    string   `json:"body,omitempty" jsonschema:"the plain-text message"`
	ReplyTo string   `json:"reply_to_message_id,omitempty" jsonschema:"the id of the message this answers, to keep the thread"`
}

// Build writes a message from the owner's account, threaded as a reply
// when ReplyTo names a message, and returns it with its Gmail thread.
func Build(ctx context.Context, g *google.Google, d Draft, effect string) ([]byte, string, error) {
	acc, err := g.Account()
	if err != nil {
		return nil, "", err
	}
	out := Outgoing{From: acc.Email, To: d.To, Cc: d.Cc, Subject: d.Subject, Body: d.Body, Effect: effect}
	threadID := ""
	if d.ReplyTo != "" {
		gm, err := g.Gmail(ctx)
		if err != nil {
			return nil, "", err
		}
		m, err := gm.Raw(ctx, d.ReplyTo)
		if err != nil {
			return nil, "", fmt.Errorf("the message to reply to couldn't be read: %w", err)
		}
		p, err := Parse(m.Raw)
		if err != nil {
			return nil, "", err
		}
		threadID, out.InReplyTo, out.References = m.ThreadID, p.MessageID, p.References
		if strings.TrimSpace(out.Subject) == "" {
			out.Subject = ReplySubject(p.Subject)
		}
	}
	raw, err := out.Compose(time.Now())
	return raw, threadID, err
}

// Sender sends mail Effects through Gmail. Gmail can't tell a repeated
// send from a new one, so it isn't idempotent: a send cut off by a
// restart is marked uncertain rather than sent twice.
type Sender struct {
	Google *google.Google
	Store  *store.Store
}

// Idempotent is false.
func (s *Sender) Idempotent() bool { return false }

// Send sends an action approved or allowed by the outbox, or a reply to
// the requester in their mail thread.
func (s *Sender) Send(ctx context.Context, e store.Effect) (string, error) {
	acc, err := s.Google.Account()
	if err != nil {
		return "", outbox.Permanent(err)
	}
	if !google.HasScope(acc, google.ScopeGmailSend) {
		return "", outbox.Permanent(google.ErrNeedsSend)
	}
	effect := "ef_" + strconv.FormatUint(uint64(e.ID), 10)
	var d Draft
	switch {
	case e.Kind == "action" && e.Action == "send_email":
		if err := json.Unmarshal([]byte(e.Payload), &d); err != nil {
			return "", outbox.Permanent(fmt.Errorf("the action's details are damaged: %w", err))
		}
	case e.Kind == "message":
		// A reply to the person who asked, in their own mail thread.
		d, err = s.replyDraft(ctx, e)
		if err != nil {
			return "", err
		}
	default:
		return "", outbox.Permanent(fmt.Errorf("mail can't do %q", e.Action))
	}
	d.Body = e.Body
	raw, threadID, err := Build(ctx, s.Google, d, effect)
	if err != nil {
		return "", outbox.Permanent(err)
	}
	gm, err := s.Google.Gmail(ctx)
	if err != nil {
		return "", err
	}
	id, err := gm.Send(ctx, raw, threadID)
	var ae *google.APIError
	if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != 429 {
		return "", outbox.Permanent(err)
	}
	return id, err
}

func (s *Sender) replyDraft(ctx context.Context, e store.Effect) (Draft, error) {
	if strings.TrimSpace(e.Recipients) == "" {
		return Draft{}, outbox.Permanent(errors.New("a reply needs someone to go to"))
	}
	var th store.Thread
	if err := s.Store.Tx(func(tx *store.Tx) error {
		var err error
		th, err = tx.Thread(e.ThreadID)
		return err
	}); err != nil {
		return Draft{}, outbox.Permanent(err)
	}
	gm, err := s.Google.Gmail(ctx)
	if err != nil {
		return Draft{}, err
	}
	ids, err := gm.ThreadMessages(ctx, th.Key)
	if err != nil || len(ids) == 0 {
		return Draft{}, fmt.Errorf("the mail thread couldn't be read: %w", err)
	}
	return Draft{To: strings.Split(e.Recipients, ", "), ReplyTo: ids[len(ids)-1]}, nil
}
