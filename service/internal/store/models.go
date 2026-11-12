package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ModelRole is the model the owner chose for one role. Its key lives in the
// vault.
type ModelRole struct {
	Role     string `gorm:"primaryKey"`
	Provider string
	Model    string
	// BaseURL is an OpenAI-compatible endpoint's address.
	BaseURL string
	// PriceIn and PriceOut are dollars per million tokens, when the owner
	// set them.
	PriceIn   float64
	PriceOut  float64
	HasPrice  bool
	UpdatedAt time.Time
}

// ModelRoles lists the roles the owner chose a model for.
func (s *Store) ModelRoles() ([]ModelRole, error) {
	var rs []ModelRole
	return rs, s.db.Order("role").Find(&rs).Error
}

// ModelRoleFor returns one role's choice.
func (s *Store) ModelRoleFor(role string) (ModelRole, error) {
	var r ModelRole
	err := s.db.First(&r, "role = ?", role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ModelRole{}, ErrNotFound
	}
	return r, err
}

// SaveModelRole creates or replaces a role's choice.
func (s *Store) SaveModelRole(r ModelRole) error { return s.db.Save(&r).Error }
