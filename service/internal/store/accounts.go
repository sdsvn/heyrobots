package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Account statuses.
const (
	AccountConnected = "connected"
	// AccountBroken needs the owner to connect again, for example because
	// the provider revoked the token.
	AccountBroken = "broken"
)

// Account is a connected provider account. Its tokens live in the vault.
type Account struct {
	Provider string `gorm:"primaryKey"`
	Email    string
	// Scopes are the granted scopes, space separated.
	Scopes string
	// AssistantAddress is where mail to the Assistant goes, such as
	// you+assistant@gmail.com.
	AssistantAddress string
	ConnectedAt      time.Time
	Status           string
	StatusError      string
	// MailCursor and CalendarCursor are where polling resumes.
	MailCursor     string
	CalendarCursor string
	CheckedAt      *time.Time
}

// Account returns a connected account.
func (tx *Tx) Account(provider string) (Account, error) {
	var a Account
	err := tx.db.First(&a, "provider = ?", provider).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// SaveAccount creates or replaces an account.
func (tx *Tx) SaveAccount(a Account) error { return tx.db.Save(&a).Error }

// UpdateAccount changes some fields of an account.
func (tx *Tx) UpdateAccount(provider string, updates map[string]any) error {
	return tx.db.Model(&Account{}).Where("provider = ?", provider).Updates(updates).Error
}

// DeleteAccount forgets an account.
func (tx *Tx) DeleteAccount(provider string) error {
	return tx.db.Delete(&Account{}, "provider = ?", provider).Error
}

// Accounts lists connected accounts.
func (tx *Tx) Accounts() ([]Account, error) {
	var as []Account
	return as, tx.db.Order("provider").Find(&as).Error
}

// Schedule makes the schedule Source send a Trigger at set times.
type Schedule struct {
	ID   string `gorm:"primaryKey"`
	Name string
	// Note is what the Trigger says, in the owner's words.
	Note string
	// Days are weekdays as "mon,tue,…"; empty means every day.
	Days string
	// Minute is the local time of day, in minutes after midnight.
	Minute    int
	CreatedBy string
	CreatedAt time.Time
	LastRunAt *time.Time
	Enabled   bool
}

// Schedules lists schedules by name.
func (tx *Tx) Schedules() ([]Schedule, error) {
	var ss []Schedule
	return ss, tx.db.Order("name").Find(&ss).Error
}

// CreateSchedule stores a schedule.
func (tx *Tx) CreateSchedule(s Schedule) error { return tx.db.Create(&s).Error }

// DeleteSchedule removes a schedule.
func (tx *Tx) DeleteSchedule(id string) error {
	res := tx.db.Delete(&Schedule{}, "id = ?", id)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

// JobRun is when a standing job last fired, and when it was first seen,
// so a job added today doesn't fire for this morning.
type JobRun struct {
	Key       string `gorm:"primaryKey"`
	FirstSeen time.Time
	LastRunAt *time.Time
}

// JobRun returns a job's last firing and first sighting, creating the
// sighting now if it is new.
func (tx *Tx) JobRun(key string, now time.Time) (*time.Time, time.Time, error) {
	var r JobRun
	err := tx.db.First(&r, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		r = JobRun{Key: key, FirstSeen: now.UTC()}
		return nil, r.FirstSeen, tx.db.Create(&r).Error
	}
	return r.LastRunAt, r.FirstSeen, err
}

// JobRunIfAny returns a job's row, or nil when it was never seen.
func (tx *Tx) JobRunIfAny(key string) (*JobRun, error) {
	var r JobRun
	err := tx.db.First(&r, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

// MarkJobRun records that a job fired.
func (tx *Tx) MarkJobRun(key string, at time.Time) error {
	return tx.db.Model(&JobRun{}).Where("key = ?", key).Update("last_run_at", at).Error
}

// MarkScheduleRun records when a schedule last fired.
func (tx *Tx) MarkScheduleRun(id string, at time.Time) error {
	return tx.db.Model(&Schedule{}).Where("id = ?", id).Update("last_run_at", at).Error
}

// SentRefs reports which Source message ids the Assistant itself sent, so
// its own mail is never read back as a request.
func (tx *Tx) SentRefs(source string, refs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(refs) == 0 {
		return out, nil
	}
	var got []string
	if err := tx.db.Model(&Effect{}).Where("source = ? AND ref IN ?", source, refs).Pluck("ref", &got).Error; err != nil {
		return nil, err
	}
	for _, r := range got {
		out[r] = true
	}
	return out, nil
}
