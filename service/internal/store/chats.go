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
	if err := tx.db.Where(inThread, threadID).Order("at DESC, created_at DESC").Limit(1).Find(&ts).Error; err != nil {
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
