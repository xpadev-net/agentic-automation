package models

import (
	"time"
)

// PullRequest represents a GitHub Pull Request
type PullRequest struct {
	ID         int       `gorm:"primaryKey;autoIncrement"`
	Repo       string    `gorm:"size:255;index:idx_pr_repo_number,unique"`
	Number     int       `gorm:"index:idx_pr_repo_number,unique"`
	IssueID    *int      `gorm:"column:issue_id;index"`
	Branch     string    `gorm:"size:255"`
	BaseBranch string    `gorm:"column:base_branch;size:255;default:'main'"`
	Status     string    `gorm:"type:enum('open','closed','merged');default:'open'"`
	Mergeable  *bool     `gorm:"type:boolean"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`

	// Relationships
	Issue          *Issue           `gorm:"foreignKey:IssueID;constraint:OnDelete:SET NULL"`
	ReviewFeedback []ReviewFeedback `gorm:"foreignKey:PRID;constraint:OnDelete:CASCADE"`
	CIStatuses     []CIStatus       `gorm:"foreignKey:PRID;constraint:OnDelete:CASCADE"`
	AgentRuns      []AgentRun       `gorm:"foreignKey:PRID;constraint:OnDelete:SET NULL"`
}

// TableName specifies the table name for PullRequest
func (PullRequest) TableName() string {
	return "pull_requests"
}
