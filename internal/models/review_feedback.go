package models

import (
	"time"
)

// ReviewFeedback represents feedback from a code review (e.g., Codex)
type ReviewFeedback struct {
	ID               int     `gorm:"primaryKey;autoIncrement"`
	PRID             int     `gorm:"column:pr_id;index"`
	Source           string  `gorm:"type:enum('Codex');default:'Codex'"`
	Content          *string `gorm:"type:text"`
	Status           string  `gorm:"type:enum('requested','received','commented');default:'requested'"`
	ApprovalDetected bool    `gorm:"column:approval_detected;type:boolean;default:false;index"`
	GitHubCommentID  *int64  `gorm:"column:github_comment_id;type:bigint"`
	// PlanCreationStatus tracks the lifecycle of automated review handling.
	//
	// State flow:
	//   pending (初期状態)
	//     → creating (プラン作成Pod起動済み・多重起動防止)
	//     → created (プラン作成成功)
	//         → executed (プラン実行完了)
	//     ↘ rejected (プラン却下)
	PlanCreationStatus  string    `gorm:"type:enum('pending','creating','created','rejected','executed');default:'pending'"`
	PlanContent         *string   `gorm:"type:text"`
	PlanAgentRunID      *int      `gorm:"column:plan_agent_run_id;index"`
	ExecutionAgentRunID *int      `gorm:"column:execution_agent_run_id;index"`
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt           time.Time `gorm:"column:updated_at;autoUpdateTime"`

	// Relationships
	PullRequest PullRequest `gorm:"foreignKey:PRID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for ReviewFeedback
func (ReviewFeedback) TableName() string {
	return "review_feedback"
}
