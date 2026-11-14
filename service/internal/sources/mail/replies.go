package mail

import (
	"bytes"
	"context"
	netmail "net/mail"
	"slices"
	"strings"

	"heyrobots/service/internal/google"
)

// Replies checks a sent mail's thread for an answer, for agents' reply
// checks, without waking a model.
type Replies struct{ Google *google.Google }

// Replied reports whether a message from someone else came in the sent
// message's thread after it. ok is false when mail isn't connected.
func (r Replies) Replied(ctx context.Context, messageID string) (bool, bool, error) {
	gm, err := r.Google.Gmail(ctx)
	if err != nil {
		return false, false, nil
	}
	sent, err := gm.Raw(ctx, messageID)
	if err != nil {
		return false, false, err
	}
	ids, err := gm.ThreadMessages(ctx, sent.ThreadID)
	if err != nil {
		return false, false, err
	}
	i := slices.Index(ids, messageID)
	if i < 0 {
		// The sent mail isn't in its own thread: nothing to go by.
		return false, false, nil
	}
	for _, id := range ids[i+1:] {
		m, err := gm.Raw(ctx, id)
		if err != nil {
			return false, false, err
		}
		if !slices.Contains(m.Labels, "SENT") && !slices.Contains(m.Labels, "DRAFT") && !autoReply(m.Raw) {
			return true, true, nil
		}
	}
	return false, true, nil
}

// autoReply reports whether a message says it was sent automatically, such
// as an out-of-office answer, which is no reply.
func autoReply(raw []byte) bool {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	h := msg.Header
	auto := strings.ToLower(h.Get("Auto-Submitted"))
	prec := strings.ToLower(h.Get("Precedence"))
	return (auto != "" && auto != "no") || h.Get("X-Autoreply") != "" || h.Get("X-Autorespond") != "" || prec == "auto_reply" || prec == "bulk"
}
