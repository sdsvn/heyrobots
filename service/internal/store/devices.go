package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Device is a paired app. Only a hash of its token is stored.
type Device struct {
	ID           string `gorm:"primaryKey"`
	Name         string
	Platform     string
	Role         string
	DelegateName string
	TokenHash    []byte
	CreatedAt    time.Time
	LastSeenAt   *time.Time
	RevokedAt    *time.Time
}

// PairingCode is a one-time code that pairs one device. Only a hash of the
// code is stored.
type PairingCode struct {
	CodeHash     []byte `gorm:"primaryKey"`
	Role         string
	DelegateName string
	CreatedBy    string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	UsedAt       *time.Time
}

// Errors from pairing.
var (
	ErrCodeInvalid = errors.New("pairing code is invalid, used or expired")
)

// CreatePairingCode stores a new pairing code.
func (s *Store) CreatePairingCode(c PairingCode) error {
	return s.db.Create(&c).Error
}

// Pair uses a pairing code and creates the device in one transaction, so a
// code can pair exactly one device even when two requests race.
func (s *Store) Pair(codeHash []byte, now time.Time, d Device) (Device, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&PairingCode{}).
			Where("code_hash = ? AND used_at IS NULL AND expires_at > ?", codeHash, now).
			Update("used_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrCodeInvalid
		}
		var c PairingCode
		if err := tx.Where("code_hash = ?", codeHash).First(&c).Error; err != nil {
			return err
		}
		d.Role, d.DelegateName, d.CreatedAt = c.Role, c.DelegateName, now
		return tx.Create(&d).Error
	})
	return d, err
}

// DeviceByTokenHash returns the active device with this token hash.
func (s *Store) DeviceByTokenHash(h []byte) (Device, error) {
	var d Device
	err := s.db.Where("token_hash = ? AND revoked_at IS NULL", h).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Device{}, ErrNotFound
	}
	return d, err
}

// TouchDevice records when a device was last seen.
func (s *Store) TouchDevice(id string, now time.Time) error {
	return s.db.Model(&Device{}).Where("id = ?", id).Update("last_seen_at", now).Error
}

// ListDevices returns every device, newest first.
func (s *Store) ListDevices() ([]Device, error) {
	var ds []Device
	err := s.db.Order("created_at DESC").Find(&ds).Error
	return ds, err
}

// RevokeDevice revokes an active device.
func (s *Store) RevokeDevice(id string, now time.Time) error {
	res := s.db.Model(&Device{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveOwnerDevices counts owner devices that are not revoked. Setup is
// open only while this is zero.
func (s *Store) ActiveOwnerDevices() (int64, error) {
	var n int64
	err := s.db.Model(&Device{}).Where("role = ? AND revoked_at IS NULL", "owner").Count(&n).Error
	return n, err
}

// DeleteExpiredPairingCodes removes codes that expired or were used before
// the given time.
func (s *Store) DeleteExpiredPairingCodes(before time.Time) error {
	return s.db.Where("expires_at < ? OR used_at < ?", before, before).Delete(&PairingCode{}).Error
}
