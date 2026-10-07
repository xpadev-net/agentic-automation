package repositories

import (
	"agentic-automation/internal/models"
	"time"

	"gorm.io/gorm"
)

// UISessionRepository provides persistence for WebUI login sessions.
type UISessionRepository struct {
	db *gorm.DB
}

// NewUISessionRepository creates a new UISessionRepository.
func NewUISessionRepository(db *gorm.DB) *UISessionRepository {
	return &UISessionRepository{db: db}
}

// Create inserts a new session row.
func (r *UISessionRepository) Create(session *models.UISession) error {
	return r.db.Create(session).Error
}

// GetByID returns the session with the given ID, or gorm.ErrRecordNotFound.
func (r *UISessionRepository) GetByID(id string) (*models.UISession, error) {
	var session models.UISession
	if err := r.db.Where("id = ?", id).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// Touch updates last_seen_at for an active session.
func (r *UISessionRepository) Touch(id string, seenAt time.Time) error {
	return r.db.Model(&models.UISession{}).Where("id = ?", id).Update("last_seen_at", seenAt).Error
}

// Delete removes a session (logout).
func (r *UISessionRepository) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&models.UISession{}).Error
}

// DeleteExpired removes sessions past their expiry time and returns the
// number of deleted rows.
func (r *UISessionRepository) DeleteExpired(now time.Time) (int64, error) {
	res := r.db.Where("expires_at < ?", now).Delete(&models.UISession{})
	return res.RowsAffected, res.Error
}
