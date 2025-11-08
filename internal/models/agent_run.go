package models

import (
	"time"

	"gorm.io/datatypes"
)

// AgentRun represents an execution of an AI agent for an Issue
type AgentRun struct {
	ID             int    `gorm:"primaryKey;autoIncrement"`
	IdempotencyKey string `gorm:"column:idempotency_key;uniqueIndex;size:191"`
	IssueID        int    `gorm:"column:issue_id;index"`
	PRID           *int   `gorm:"column:pr_id;index"`
	State          string `gorm:"type:enum('queued','started','succeeded','failed');default:'queued';index"`
	AgentType      string `gorm:"column:agent_type;type:enum('claude-code','cursor-agent');default:'claude-code'"`
	// ExecutionMode distinguishes normal runs from plan creation/execution workflows.
	//   normal         : 従来のIssue対応フロー
	//   plan_creation  : レビュー指摘からプランを作成するモード
	//   plan_execution : 作成済みプランを実行するモード
	ExecutionMode string  `gorm:"type:enum('normal','plan_creation','plan_execution');default:'normal'"`
	PlanContent   *string `gorm:"type:text"`
	// ReviewFeedbackID links plan-related runs back to their originating feedback.
	ReviewFeedbackID *int `gorm:"column:review_feedback_id;index"`
	// PlanAgentRunID links execution runs to their plan creation run (for issue-triggered plans).
	PlanAgentRunID *int           `gorm:"column:plan_agent_run_id;index"`
	Input          datatypes.JSON `gorm:"type:json"`
	Output           datatypes.JSON `gorm:"type:json"`
	RetryCount       int            `gorm:"column:retry_count;default:0"`
	ErrorMessage     *string        `gorm:"column:error_message;type:text"`
	CommitSHA        *string        `gorm:"column:commit_sha;size:191"`
	S3SessionKey     *string        `gorm:"column:s3_session_key;size:512"`
	SessionSavedAt   *time.Time     `gorm:"column:session_saved_at"`
	StartedAt        *time.Time     `gorm:"column:started_at"`
	CompletedAt      *time.Time     `gorm:"column:completed_at"`
	CreatedAt        time.Time      `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt        time.Time      `gorm:"column:updated_at;autoUpdateTime"`

	// Relationships
	Issue         Issue          `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	PullRequest   *PullRequest   `gorm:"foreignKey:PRID;constraint:OnDelete:SET NULL"`
	OperationLogs []OperationLog `gorm:"foreignKey:RunID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for AgentRun
func (AgentRun) TableName() string {
	return "agent_runs"
}
