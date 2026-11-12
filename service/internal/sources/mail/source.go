package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
)

// Source polls the connected Gmail account.
//
// Each new message becomes a Trigger:
//   - Mail to the Assistant's plus-address addresses it. From the owner's
//     own address, and proven, it is a request; from anyone else it is
//     not admitted.
//   - Other mail in the inbox is received because the inbox receives
//     everything. It can only become proactive work under the owner's
//     rules.
//   - Mail the Assistant sent itself, spam, trash and drafts are skipped.
//
// The sender counts as proven when a valid DKIM signature from the From
// address's own domain covers the message, or, for the owner's own
// address, when Gmail filed it as sent from this very account. Mail one
// sends to oneself inside Gmail doesn't always carry a DKIM signature,
// and only someone signed in to the account can make Gmail file a message
// as sent. A From line alone is never trusted.
type Source struct {
	Google *google.Google
	Intake *intake.Intake
	Store  *store.Store
	Log    *slog.Logger
	Now    func() time.Time
	// LookupTXT resolves DKIM keys; nil uses DNS.
	LookupTXT func(string) ([]string, error)
	// OnNew is called after new Triggers were stored.
	OnNew func()
	// Every is how often to poll; a minute by default.
	Every time.Duration

	mu   sync.Mutex
	kick chan struct{}
}

// MaxPerPoll caps the messages read in one poll; the rest wait.
const MaxPerPoll = 200

// Kick asks for a poll soon, for example right after connecting.
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
		every = time.Minute
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
			s.Log.Warn("mail poll", "err", err)
		}
	}
}

// Poll reads mail that arrived since the last poll.
func (s *Source) Poll(ctx context.Context) error {
	acc, err := s.Google.Account()
	if err != nil {
		return err
	}
	if acc.Status != store.AccountConnected {
		return nil
	}
	gm, err := s.Google.Gmail(ctx)
	if err != nil {
		return err
	}
	if acc.MailCursor == "" {
		// Start from now: mail that arrived before connecting is history,
		// not new events.
		id, err := gm.HistoryID(ctx)
		if err != nil {
			return s.broken(err)
		}
		return s.setCursor(id)
	}
	ids, next, err := gm.Added(ctx, acc.MailCursor)
	if errors.Is(err, google.ErrCursorExpired) {
		s.Log.Warn("mail history expired; continuing from now")
		id, err := gm.HistoryID(ctx)
		if err != nil {
			return err
		}
		return s.setCursor(id)
	}
	if err != nil {
		return s.broken(err)
	}
	stored := 0
	for i, id := range ids {
		if i == MaxPerPoll {
			// Pick up the rest on the next poll; intake drops repeats.
			s.Log.Info("mail poll capped", "left", len(ids)-i)
			next = acc.MailCursor
			break
		}
		msg, err := gm.Raw(ctx, id)
		var ae *google.APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			continue // deleted before it could be read
		}
		if err != nil {
			return err
		}
		ok, err := s.ingest(acc, msg)
		if err != nil {
			return fmt.Errorf("message %s: %w", id, err)
		}
		if ok {
			stored++
		}
	}
	if err := s.setCursor(next); err != nil {
		return err
	}
	if stored > 0 && s.OnNew != nil {
		s.OnNew()
	}
	return nil
}

// Classify decides what a message is to the Assistant. It returns false
// when the message should be skipped.
func Classify(acc store.Account, msg google.Message, p Parsed, lookup func(string) ([]string, error)) (core.Trigger, bool) {
	labels := msg.Labels
	if has(labels, "SPAM") || has(labels, "TRASH") || has(labels, "DRAFT") || p.From == "" {
		return core.Trigger{}, false
	}
	sent := has(labels, "SENT")
	if p.SentByUs && sent {
		return core.Trigger{}, false
	}
	addressed := slices.Contains(p.Recipients(), strings.ToLower(acc.AssistantAddress))
	inbox := has(labels, "INBOX")
	if !addressed && !inbox {
		return core.Trigger{}, false
	}
	auth := core.Unverified
	if aligned, _ := DKIMAligned(msg.Raw, p.From, lookup); aligned {
		auth = core.Verified
	} else if sent && p.From == acc.Email {
		auth = core.Verified
	}
	at := msg.At
	if at.IsZero() {
		at = p.Date
	}
	var text strings.Builder
	fmt.Fprintf(&text, "From: %s <%s>\nTo: %s\n", p.FromName, p.From, strings.Join(p.To, ", "))
	if len(p.Cc) > 0 {
		fmt.Fprintf(&text, "Cc: %s\n", strings.Join(p.Cc, ", "))
	}
	fmt.Fprintf(&text, "Subject: %s\n\n%s", p.Subject, p.Text)
	return core.Trigger{
		DeliveryID:   msg.ID,
		Source:       core.SourceMail,
		Actor:        core.Identity{Source: core.SourceMail, ID: p.From},
		Authenticity: auth,
		Addressed:    addressed,
		ReceivedAll:  inbox,
		ThreadKeys:   []string{msg.ThreadID},
		Text:         text.String(),
		At:           at,
	}, true
}

func (s *Source) ingest(acc store.Account, msg google.Message) (bool, error) {
	p, err := Parse(msg.Raw)
	if err != nil {
		s.Log.Warn("unreadable message skipped", "id", msg.ID, "err", err)
		return false, nil
	}
	t, ok := Classify(acc, msg, p, s.LookupTXT)
	if !ok {
		return false, nil
	}
	res, err := s.Intake.Ingest(intake.Message{Trigger: t, ThreadKeys: t.ThreadKeys, CreatedBy: t.Actor})
	if err != nil {
		return false, err
	}
	return !res.Duplicate, nil
}

func (s *Source) setCursor(id string) error {
	now := s.now().UTC()
	return s.Store.Tx(func(tx *store.Tx) error {
		return tx.UpdateAccount(google.Provider, map[string]any{"mail_cursor": id, "checked_at": now})
	})
}

// broken records an authorization failure so the app can ask the owner to
// connect again.
func (s *Source) broken(err error) error {
	var ae *google.APIError
	if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
		_ = s.Store.Tx(func(tx *store.Tx) error {
			return tx.UpdateAccount(google.Provider, map[string]any{"status": store.AccountBroken, "status_error": ae.Message})
		})
	}
	return err
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func has(list []string, v string) bool { return slices.Contains(list, v) }
