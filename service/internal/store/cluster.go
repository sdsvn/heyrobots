package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Member is another heyrobots server in the owner's cluster, or this one.
type Member struct {
	ID   string `gorm:"primaryKey"`
	Name string
	// Address is where its cluster link listens; empty when it only dials out.
	Address string
	// PublicKey is its ed25519 key, base64; links must present it.
	PublicKey string
	// Cards is a JSON array of its agents' cards, as of CardsAt.
	Cards   string
	CardsAt time.Time
	// Have is how many events it holds, which picks the newest copy.
	Have      int64
	LastSeen  *time.Time
	RemovedAt *time.Time
	CreatedAt time.Time
}

// ChatEvent is one entry of a shared chat's log, written once by Origin.
type ChatEvent struct {
	Origin string `gorm:"primaryKey"`
	Seq    int64  `gorm:"primaryKey;autoIncrement:false"`
	// HLC is a hybrid logical clock: wall milliseconds shifted left 16 bits
	// plus a counter, so order survives clocks that drift.
	HLC      int64  `gorm:"index"`
	ThreadID string `gorm:"index"`
	Kind     string
	// Ref keeps Origin from writing the same thing twice.
	Ref  string `gorm:"index"`
	Body string
	// CreatedAt is when this machine stored it.
	CreatedAt time.Time
}

// Decode reads the event's body.
func (e ChatEvent) Decode(v any) error { return json.Unmarshal([]byte(e.Body), v) }

// Members lists every member, removed ones too.
func (s *Store) Members() ([]Member, error) {
	var ms []Member
	return ms, s.db.Order("id").Find(&ms).Error
}

// SaveMember creates or replaces a member.
func (s *Store) SaveMember(m Member) error { return s.db.Save(&m).Error }

// SyncRemoteAgents replaces the cards of home's agents with cards. Each is
// stored as home.id, so it never clashes with this machine's own agents.
func (s *Store) SyncRemoteAgents(home string, cards []Agent, now time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var keep []string
		for _, a := range cards {
			a.ID, a.Home, a.Builtin = home+"."+a.ID, home, false
			a.Instructions, a.Provider, a.Model, a.BaseURL = "", "", "", ""
			a.UpdatedAt = now
			if a.CreatedAt.IsZero() {
				a.CreatedAt = now
			}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&a).Error; err != nil {
				return err
			}
			keep = append(keep, a.ID)
		}
		q := tx.Where("home = ?", home)
		if len(keep) > 0 {
			q = q.Where("id NOT IN ?", keep)
		}
		return q.Delete(&Agent{}).Error
	})
}

// AppendEvent writes an event from this machine once per ref and reports
// whether it was new. Its clock is past every event already stored.
func (tx *Tx) AppendEvent(self, threadID, kind, ref string, body any, now time.Time) (bool, error) {
	if ref != "" {
		var n int64
		if err := tx.db.Model(&ChatEvent{}).Where("origin = ? AND ref = ?", self, ref).Count(&n).Error; err != nil || n > 0 {
			return false, err
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	var last struct{ Seq, HLC int64 }
	if err := tx.db.Model(&ChatEvent{}).Select("COALESCE(MAX(CASE WHEN origin = ? THEN seq END), 0) AS seq, COALESCE(MAX(hlc), 0) AS hlc", self).
		Scan(&last).Error; err != nil {
		return false, err
	}
	hlc := max(now.UnixMilli()<<16, last.HLC+1)
	return true, tx.db.Create(&ChatEvent{Origin: self, Seq: last.Seq + 1, HLC: hlc, ThreadID: threadID, Kind: kind, Ref: ref, Body: string(raw), CreatedAt: now}).Error
}

// AddEvent stores an event from another machine and reports whether it was new.
func (s *Store) AddEvent(e ChatEvent) (bool, error) {
	res := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&e)
	return res.RowsAffected == 1, res.Error
}

// Heads returns the highest sequence number held from each origin.
func (s *Store) Heads() (map[string]int64, error) {
	var rows []struct {
		Origin string
		Seq    int64
	}
	if err := s.db.Model(&ChatEvent{}).Select("origin, MAX(seq) AS seq").Group("origin").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Origin] = r.Seq
	}
	return out, nil
}

// EventsAfter returns up to limit events the holder of heads lacks, in
// order of each origin's sequence.
func (s *Store) EventsAfter(heads map[string]int64, limit int) ([]ChatEvent, error) {
	mine, err := s.Heads()
	if err != nil {
		return nil, err
	}
	var out []ChatEvent
	for origin, top := range mine {
		if top <= heads[origin] || len(out) >= limit {
			continue
		}
		var es []ChatEvent
		if err := s.db.Where("origin = ? AND seq > ?", origin, heads[origin]).Order("seq").Limit(limit - len(out)).Find(&es).Error; err != nil {
			return nil, err
		}
		out = append(out, es...)
	}
	return out, nil
}

// ThreadEvents returns a Thread's events of one kind, oldest first.
func (s *Store) ThreadEvents(threadID, kind string) ([]ChatEvent, error) {
	var es []ChatEvent
	return es, s.db.Where("thread_id = ? AND kind = ?", threadID, kind).Order("hlc, origin").Find(&es).Error
}

// RecentEvents returns events of one kind stored since since.
func (s *Store) RecentEvents(kind string, since time.Time) ([]ChatEvent, error) {
	var es []ChatEvent
	return es, s.db.Where("kind = ? AND created_at >= ?", kind, since).Order("hlc, origin").Find(&es).Error
}

// HeldTriggers returns Triggers still waiting for the machines to agree
// who routes them, received since since.
func (s *Store) HeldTriggers(since time.Time) ([]Trigger, error) {
	var ts []Trigger
	return ts, s.db.Where("eval_state = ? AND created_at >= ?", EvalHeld, since).Order("created_at").Find(&ts).Error
}

// ReleaseHeld lets Eval route a held Trigger, led by lead when it is set.
// It reports whether the Trigger was still held.
func (s *Store) ReleaseHeld(triggerID, lead string) (bool, error) {
	updates := map[string]any{"eval_state": EvalPending}
	if lead != "" {
		updates["lead_agent"] = lead
	}
	res := s.db.Model(&Trigger{}).Where("id = ? AND eval_state = ?", triggerID, EvalHeld).Updates(updates)
	return res.RowsAffected == 1, res.Error
}

// ReleaseHandedOff lets Eval take a copied message another machine handed
// to lead, whether it was held or stored as handled. It reports whether it
// did; a message is released once.
func (s *Store) ReleaseHandedOff(triggerID, lead string) (bool, error) {
	res := s.db.Model(&Trigger{}).Where("id = ? AND origin <> '' AND lead_agent = '' AND eval_state IN ?", triggerID, []string{EvalHeld, EvalDone}).
		Updates(map[string]any{"eval_state": EvalPending, "lead_agent": lead})
	return res.RowsAffected == 1, res.Error
}

// TriggerByDelivery finds a Trigger by its Source's delivery id.
func (s *Store) TriggerByDelivery(source, deliveryID string) (Trigger, error) {
	var t Trigger
	err := s.db.First(&t, "source = ? AND delivery_id = ?", source, deliveryID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Trigger{}, ErrNotFound
	}
	return t, err
}

// SetTriggerOrigin records the machine a copied message came from.
func (s *Store) SetTriggerOrigin(id, origin string) error {
	return s.db.Model(&Trigger{}).Where("id = ?", id).Update("origin", origin).Error
}

// CopyEffect stores or updates the copy of another machine's reply.
func (s *Store) CopyEffect(e Effect) (Effect, error) {
	var cur Effect
	err := s.db.Where("origin = ? AND remote_id = ?", e.Origin, e.RemoteID).First(&cur).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return e, s.db.Create(&e).Error
	case err != nil:
		return Effect{}, err
	}
	e.ID = cur.ID
	return e, s.db.Save(&e).Error
}

// EnsureCopiedThread creates the copy of another machine's chat with the
// same id. A chat with the same key that was started here keeps its key,
// and the copy gets its own.
func (s *Store) EnsureCopiedThread(th Thread) (Thread, error) {
	var cur Thread
	err := s.db.First(&cur, "id = ?", th.ID).Error
	if err == nil {
		if cur.AgentID == th.AgentID && cur.GroupID == th.GroupID {
			return cur, nil
		}
		// Not a new message: the chat's last activity stays as it was.
		res := s.db.Model(&Thread{}).Where("id = ?", th.ID).UpdateColumns(map[string]any{"agent_id": th.AgentID, "group_id": th.GroupID})
		cur.AgentID, cur.GroupID = th.AgentID, th.GroupID
		return cur, res.Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Thread{}, err
	}
	var n int64
	if err := s.db.Model(&Thread{}).Where("source = ? AND key = ?", th.Source, th.Key).Count(&n).Error; err != nil {
		return Thread{}, err
	}
	if n > 0 {
		th.Key += ":" + th.ID
	}
	return th, s.db.Create(&th).Error
}

// SaveCopiedGroup stores another machine's group with the same id.
func (s *Store) SaveCopiedGroup(g Group) error {
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&g).Error
}

// SharedThreadIDs lists the app chats the owner's machines share.
func (s *Store) SharedThreadIDs(owner string) ([]string, error) {
	var out []string
	return out, s.db.Model(&Thread{}).Where("source = 'app' AND created_by = ? AND key <> ?", owner, NoticesKey).Pluck("id", &out).Error
}

// IsRemoteAgent reports whether id names another machine's agent card.
func IsRemoteAgent(id string) bool { return strings.Contains(id, ".") }
