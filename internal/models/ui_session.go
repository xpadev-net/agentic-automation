package models

import (
	"time"
)

// UISession represents a WebUI login session created via GitHub OAuth.
// AccessToken is stored AES-256-GCM encrypted (base64) and is used by the
// Operator to check the viewer's repository permissions on their behalf.
type UISession struct {
	ID          string    `gorm:"primaryKey;size:64"`
	GitHubLogin string    `gorm:"column:github_login;size:191;index:idx_ui_session_login"`
	AccessToken string    `gorm:"column:access_token;type:text"`
	ExpiresAt   time.Time `gorm:"column:expires_at;index:idx_ui_session_expires"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	LastSeenAt  time.Time `gorm:"column:last_seen_at"`
}

// TableName specifies the table name for UISession
func (UISession) TableName() string {
	return "ui_sessions"
}
