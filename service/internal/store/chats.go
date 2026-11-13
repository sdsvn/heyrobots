package store

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
)

// Group is a named set of agents the person chats with together.
type Group struct {
	ID   string `gorm:"primaryKey"`
	Name string
	// Members is a JSON array of agent ids.
	Members   string
	ThreadID  string
	CreatedAt time.Time
	// RemovedAt is set when the group is deleted; its chat stays readable.
	RemovedAt *time.Time
}

// MemberList returns the group's agent ids.
func (g Group) MemberList() []string {
	ids := []string{}
	_ = json.Unmarshal([]byte(g.Members), &ids)
	return ids
}

// ThreadRead is when a device last read a Thread.
type ThreadRead struct {
	DeviceID string `gorm:"primaryKey"`
	ThreadID string `gorm:"primaryKey"`
	ReadAt   time.Time
}

// Group finds a group by id, removed or not.
func (tx *Tx) Group(id string) (Group, error) {
	var g Group
	err := tx.db.First(&g, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Group{}, ErrNotFound
	}
	return g, err
}

// Groups lists the groups that weren't removed, oldest first.
func (tx *Tx) Groups() ([]Group, error) {
	var gs []Group
	return gs, tx.db.Where("removed_at IS NULL").Order("created_at").Find(&gs).Error
}

// SaveGroup creates or updates a group.
func (tx *Tx) SaveGroup(g Group) error { return tx.db.Save(&g).Error }

// ChatThread finds the app Thread with key, or creates it for a direct chat
// with agentID or for groupID's chat.
func (tx *Tx) ChatThread(key, agentID, groupID, createdBy string, now time.Time) (Thread, error) {
	th, err := tx.ThreadByKey(string(core.SourceApp), key)
	if !errors.Is(err, ErrNotFound) {
		return th, err
	}
	th = Thread{ID: ids.New("th"), Source: string(core.SourceApp), Key: key, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now, AgentID: agentID, GroupID: groupID}
	return th, tx.CreateThread(th)
}

// ChatAgents returns the agents a chat's work may go to: the one agent of a
// direct chat, or a group's members. ok is false for other conversations.
func (tx *Tx) ChatAgents(th Thread) (ids []string, ok bool, err error) {
	switch {
	case th.AgentID != "":
		return []string{th.AgentID}, true, nil
	case th.GroupID != "":
		g, err := tx.Group(th.GroupID)
		if err != nil {
			return nil, false, err
		}
		return g.MemberList(), true, nil
	}
	return nil, false, nil
}

// MarkRead records that a device read a Thread now.
func (tx *Tx) MarkRead(deviceID, threadID string, now time.Time) error {
	return tx.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&ThreadRead{DeviceID: deviceID, ThreadID: threadID, ReadAt: now}).Error
}

// Activity is what a chat list shows about a Thread.
type Activity struct {
	LastTrigger *Trigger
	// LastReply is the latest message the assistant queued on the Thread.
	LastReply *Effect
	// Seen is true when an agent has picked up LastTrigger.
	Seen bool
	// Unread counts messages from others and replies since the device read
	// the Thread.
	Unread int
	// Typing is true while an open task on the Thread is running.
	Typing bool
}

// Activity summarises a Thread for a device that speaks as me.
func (tx *Tx) Activity(threadID, deviceID string, me core.Identity) (Activity, error) {
	var a Activity
	var readAt time.Time
	var r ThreadRead
	if err := tx.db.Where("device_id = ? AND thread_id = ?", deviceID, threadID).Limit(1).Find(&r).Error; err != nil {
		return a, err
	}
	readAt = r.ReadAt
	inThread := "id IN (SELECT trigger_id FROM trigger_threads WHERE thread_id = ?)"
	var ts []Trigger
	// A member's request to another shows as that member's reply, not as a message.
	if err := tx.db.Where(inThread, threadID).Where("source <> 'agent'").Order("at DESC, created_at DESC").Limit(1).Find(&ts).Error; err != nil {
		return a, err
	}
	if len(ts) == 1 {
		a.LastTrigger = &ts[0]
		var n int64
		if err := tx.db.Model(&TicketInput{}).Where("trigger_id = ? AND delivered_at IS NOT NULL", ts[0].ID).Count(&n).Error; err != nil {
			return a, err
		}
		a.Seen = n > 0
	}
	var es []Effect
	if err := tx.db.Where("thread_id = ? AND kind = ?", threadID, "message").Order("id DESC").Limit(1).Find(&es).Error; err != nil {
		return a, err
	}
	if len(es) == 1 {
		a.LastReply = &es[0]
	}
	var fromOthers, replies, running int64
	if err := tx.db.Model(&Trigger{}).Where(inThread, threadID).Where("created_at > ?", readAt).
		Where("NOT (actor_source = ? AND actor_id = ?)", string(me.Source), me.ID).Count(&fromOthers).Error; err != nil {
		return a, err
	}
	if err := tx.db.Model(&Effect{}).Where("thread_id = ? AND kind = ? AND created_at > ?", threadID, "message", readAt).Count(&replies).Error; err != nil {
		return a, err
	}
	if err := tx.db.Model(&Assignment{}).Where("status = ? AND ticket_id IN (SELECT s.ticket_id FROM subscriptions s JOIN tickets t ON t.id = s.ticket_id "+
		"WHERE s.thread_id = ? AND t.open)", AssignmentRunning, threadID).Count(&running).Error; err != nil {
		return a, err
	}
	a.Unread, a.Typing = int(fromOthers+replies), running > 0
	return a, nil
}

// ErrBusy means an agent is still working in the conversation.
var ErrBusy = errors.New("an agent is still working here")

// DeleteThread removes a conversation for good: its messages, replies,
// decisions and the tasks it started. A group chat's group goes with it.
// Spending records stay. It refuses while an agent works on one of its
// tasks, so no running task writes into a conversation that is gone.
func (tx *Tx) DeleteThread(id string, now time.Time) error {
	th, err := tx.Thread(id)
	if err != nil {
		return err
	}
	tickets := tx.db.Model(&Ticket{}).Select("id").Where("origin_thread_id = ?", id)
	var running int64
	if err := tx.db.Model(&Assignment{}).Where("status = ? AND ticket_id IN (?)", AssignmentRunning, tickets).Count(&running).Error; err != nil {
		return err
	}
	if running > 0 {
		return ErrBusy
	}
	triggers := tx.db.Table("trigger_threads").Select("trigger_id").Where("thread_id = ?", id)
	steps := []struct {
		model any
		where string
		arg   any
	}{
		{&TicketInput{}, "ticket_id IN (?)", tickets},
		{&Assignment{}, "ticket_id IN (?)", tickets},
		{&Subscription{}, "ticket_id IN (?) OR thread_id = ?", nil},
		{&Effect{}, "thread_id = ?", id},
		{&Decision{}, "thread_id = ?", id},
		{&ThreadRead{}, "thread_id = ?", id},
		// Only triggers no other conversation shares.
		{&Trigger{}, "id IN (?) AND id NOT IN (SELECT trigger_id FROM trigger_threads WHERE thread_id <> ?)", nil},
	}
	for _, s := range steps {
		q := tx.db.Where(s.where, s.arg)
		switch s.model.(type) {
		case *Subscription:
			q = tx.db.Where(s.where, tickets, id)
		case *Trigger:
			q = tx.db.Where(s.where, triggers, id)
		}
		if err := q.Delete(s.model).Error; err != nil {
			return err
		}
	}
	if err := tx.db.Exec("DELETE FROM trigger_threads WHERE thread_id = ?", id).Error; err != nil {
		return err
	}
	if err := tx.db.Where("origin_thread_id = ?", id).Delete(&Ticket{}).Error; err != nil {
		return err
	}
	if th.GroupID != "" {
		if err := tx.db.Model(&Group{}).Where("id = ? AND removed_at IS NULL", th.GroupID).Update("removed_at", now).Error; err != nil {
			return err
		}
	}
	return tx.db.Delete(&Thread{}, "id = ?", id).Error
}
