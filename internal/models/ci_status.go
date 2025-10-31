package models

import (
	"time"
)

// CIStatus represents a CI check status for a Pull Request
type CIStatus struct {
	ID           int        `gorm:"primaryKey;autoIncrement"`
	PRID         int        `gorm:"column:pr_id;index"`
	CheckSuiteID string     `gorm:"column:check_suite_id;size:191"`
	CheckRunID   *string    `gorm:"column:check_run_id;size:191"`
	Name         string     `gorm:"size:191"`
	Status       string     `gorm:"type:enum('queued','in_progress','completed');default:'queued';index:idx_ci_status_pr_status"`
	Conclusion   *string    `gorm:"type:enum('success','failure','cancelled','skipped','neutral');index"`
	Logs         *string    `gorm:"type:text"`
	LogsURL      *string    `gorm:"column:logs_url;size:512"`
	StartedAt    *time.Time `gorm:"column:started_at"`
	CompletedAt  *time.Time `gorm:"column:completed_at"`
	CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;autoUpdateTime"`

	// Relationships
	PullRequest PullRequest `gorm:"foreignKey:PRID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for CIStatus
func (CIStatus) TableName() string {
	return "ci_status"
}

