package models

import (
	"time"
)

// AgentRun represents an execution of an AI agent for an Issue
type AgentRun struct {
	ID             int        `gorm:"primaryKey;autoIncrement"`
	IdempotencyKey string     `gorm:"column:idempotency_key;uniqueIndex;size:191"`
	IssueID        int        `gorm:"column:issue_id;index"`
	PRID           *int       `gorm:"column:pr_id;index"`
	State          string     `gorm:"type:enum('queued','started','succeeded','failed');default:'queued';index"`
	AgentType      string     `gorm:"column:agent_type;type:enum('claude-code','cursor-agents');default:'claude-code'"`
	Input          string     `gorm:"type:json"` // JSON stored as string
	Output         string     `gorm:"type:json"` // JSON stored as string
	RetryCount     int        `gorm:"column:retry_count;default:0"`
	ErrorMessage   *string    `gorm:"column:error_message;type:text"`
	CommitSHA      *string    `gorm:"column:commit_sha;size:191"`
	StartedAt      *time.Time `gorm:"column:started_at"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
	CreatedAt      time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;autoUpdateTime"`

	// Relationships
	Issue         Issue          `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	PullRequest   *PullRequest   `gorm:"foreignKey:PRID;constraint:OnDelete:SET NULL"`
	OperationLogs []OperationLog `gorm:"foreignKey:RunID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for AgentRun
func (AgentRun) TableName() string {
	return "agent_runs"
}
