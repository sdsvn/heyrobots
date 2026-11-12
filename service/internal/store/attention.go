package store

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"

	"heyrobots/service/internal/core"
)

// Attention kinds: what waits on a person.
const (
	AttentionApproval = "approval"
	AttentionQuestion = "question"
	AttentionFailure  = "failure"
)

// AttentionItem is one thing that waits on a person: an action to approve,
// a task's question, or a send that failed.
type AttentionItem struct {
	// Key is stable for as long as the item waits, so a push is sent once.
	Key      string
	Kind     string
	ThreadID string
	TicketID string
	// EffectID is set for approvals and failures.
	EffectID uint
	// Text is shown in the app only. It never goes into a push.
	Text string
	At   time.Time
	// Action, Recipients and Reach describe an approval.
	Action     string
	Recipients string
	Reach      string
	// Choices are answers the person can tap for a question.
	Choices []string
}

// Attention lists what waits on a person, oldest first. With owner false,
// only questions on Threads that createdBy started are listed: approvals
// and failures concern the owner's accounts.
func (tx *Tx) Attention(owner bool, createdBy string) ([]AttentionItem, error) {
	var out []AttentionItem
	if owner {
		var es []Effect
		if err := tx.db.Where("status = ?", EffectAwaitingApproval).Order("id").Find(&es).Error; err != nil {
			return nil, err
		}
		for _, e := range es {
			out = append(out, AttentionItem{Key: "approval:" + uitoa(e.ID), Kind: AttentionApproval,
				ThreadID: e.ThreadID, TicketID: e.TicketID, EffectID: e.ID, Text: e.Action, At: e.CreatedAt,
				Action: e.Action, Recipients: e.Recipients, Reach: e.Reach})
		}
		es = nil
		if err := tx.db.Where("status IN ? AND acked_at IS NULL", []string{EffectFailed, EffectUncertain}).Order("id").Find(&es).Error; err != nil {
			return nil, err
		}
		for _, e := range es {
			text := e.LastError
			if e.Status == EffectUncertain {
				text = "It may or may not have been sent."
			}
			out = append(out, AttentionItem{Key: "failure:" + uitoa(e.ID), Kind: AttentionFailure,
				ThreadID: e.ThreadID, TicketID: e.TicketID, EffectID: e.ID, Text: text, At: e.CreatedAt})
		}
	}

	var tks []Ticket
	q := tx.db.Where("open AND stopped_by = '' AND stage IN ?", []string{string(core.StageQuestion), string(core.StageBlocked)})
	if !owner {
		q = q.Where("origin_thread_id IN (SELECT id FROM threads WHERE created_by = ?)", createdBy)
	}
	if err := q.Order("updated_at").Find(&tks).Error; err != nil {
		return nil, err
	}
	for _, tk := range tks {
		var a Assignment
		err := tx.db.Where("ticket_id = ? AND status = ?", tk.ID, AssignmentEnded).Order("ended_at DESC").First(&a).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		text := a.HumanAction
		if text == "" {
			text = a.Summary
		}
		// The key changes when the task asks again, so each new question
		// gets its own push.
		out = append(out, AttentionItem{Key: "question:" + tk.ID + ":" + a.ID, Kind: AttentionQuestion,
			ThreadID: tk.OriginThreadID, TicketID: tk.ID, Text: text, At: tk.UpdatedAt, Choices: tk.ChoiceList()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// AckEffect dismisses a failed or uncertain Effect from the attention feed.
func (tx *Tx) AckEffect(id uint, now time.Time) error {
	res := tx.db.Model(&Effect{}).Where("id = ? AND status IN ? AND acked_at IS NULL", id, []string{EffectFailed, EffectUncertain}).
		Update("acked_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Push records a push notification that was sent, by the key of what it
// was about.
type Push struct {
	Key string `gorm:"primaryKey"`
	At  time.Time
}

// Pushed returns which of the keys already had a push.
func (tx *Tx) Pushed(keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	var ps []Push
	if err := tx.db.Where("key IN ?", keys).Find(&ps).Error; err != nil {
		return nil, err
	}
	for _, p := range ps {
		out[p.Key] = true
	}
	return out, nil
}

// MarkPushed records that a push covered the keys.
func (tx *Tx) MarkPushed(keys []string, at time.Time) error {
	for _, k := range keys {
		if err := tx.db.Save(&Push{Key: k, At: at}).Error; err != nil {
			return err
		}
	}
	return nil
}

// PushesSince counts pushes sent since a time. A push that covered several
// items counts once, as its items share one time.
func (tx *Tx) PushesSince(since time.Time) (int64, error) {
	var n int64
	err := tx.db.Model(&Push{}).Where("at >= ?", since).Distinct("at").Count(&n).Error
	return n, err
}

func uitoa(n uint) string { return strconv.FormatUint(uint64(n), 10) }
