package models

import (
	"time"
)

// Issue represents a GitHub Issue
type Issue struct {
	ID            int       `gorm:"primaryKey;autoIncrement"`
	Repo          string    `gorm:"size:255;index:idx_issue_repo_number,unique"`
	Number        int       `gorm:"index:idx_issue_repo_number,unique"`
	GitHubIssueID uint64    `gorm:"column:github_issue_id;type:bigint unsigned;index"` // GitHub's numeric issue ID
	Title         string    `gorm:"size:512"`
	Body          *string   `gorm:"type:text"`
	Labels        string    `gorm:"type:json"` // JSON array stored as string, parsed in application layer
	State         string    `gorm:"type:enum('open','closed');default:'open'"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt     time.Time `gorm:"column:updated_at;autoUpdateTime"`

	// Relationships
	AgentRuns         []AgentRun         `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	PullRequests      []PullRequest      `gorm:"foreignKey:IssueID;constraint:OnDelete:SET NULL"`
	BlockerGraphEdges []BlockerGraphEdge `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE"`
	DependencyEdges   []BlockerGraphEdge `gorm:"foreignKey:DependsOnTaskID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for Issue
func (Issue) TableName() string {
	return "issues"
}
