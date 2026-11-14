package mail

import (
	"context"
	"slices"

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
	for _, id := range ids[i+1:] {
		m, err := gm.Raw(ctx, id)
		if err != nil {
			return false, false, err
		}
		if !slices.Contains(m.Labels, "SENT") && !slices.Contains(m.Labels, "DRAFT") {
			return true, true, nil
		}
	}
	return false, true, nil
}
