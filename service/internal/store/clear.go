package store

import "gorm.io/gorm"

// Cleared says what clearing the history removed.
type Cleared struct {
	Tasks, Chats, Actions int64
	ForgotBrain           bool
}

// historyTables hold the history of chats and tasks, in the order they are
// cleared. Agents, groups, apps, skills, rules, jobs, settings, devices,
// the spending ledger and what job triggers have seen all stay. So does
// the log this machine shares with its team: its numbering must go on
// where it left off, or the other machines would drop its new messages,
// and they would send back everything it had cleared.
var historyTables = []string{
	"ticket_inputs", "assignments", "subscriptions", "decisions", "trigger_threads", "triggers",
	"effects", "thread_reads", "incidents", "digests", "agent_outcomes", "brain_follow_ups",
}

// ClearHistory removes every chat and task and what hangs off them, in one
// transaction. Actions waiting to be sent are removed with them, so they
// can never go out later. A direct or group chat with agents stays, empty.
// What agents learned stays unless forgetBrain is set. The model
// sessions, kept in the same database by the agent framework, go too.
func (s *Store) ClearHistory(forgetBrain bool) (Cleared, error) {
	var c Cleared
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("tickets").Count(&c.Tasks).Error; err != nil {
			return err
		}
		if err := tx.Table("effects").Where("status NOT IN ?", []string{EffectSent, EffectFailed, EffectRejected, EffectCancelled}).Count(&c.Actions).Error; err != nil {
			return err
		}
		if err := tx.Table("threads").Where("agent_id = '' AND group_id = ''").Count(&c.Chats).Error; err != nil {
			return err
		}
		for _, t := range historyTables {
			if err := tx.Exec("DELETE FROM " + t).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("DELETE FROM tickets").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM threads WHERE agent_id = '' AND group_id = ''").Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE threads SET eval_id = '', eval_lease_until = NULL, eval_failures = 0").Error; err != nil {
			return err
		}
		for _, t := range []string{"events", "sessions"} {
			var n int64
			if err := tx.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", t).Scan(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				if err := tx.Exec("DELETE FROM " + t).Error; err != nil {
					return err
				}
			}
		}
		if forgetBrain {
			c.ForgotBrain = true
			for _, t := range []string{"brain_facts", "brain_diaries"} {
				if err := tx.Exec("DELETE FROM " + t).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	return c, err
}
