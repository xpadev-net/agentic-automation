package models

import (
	"time"
)

// AgentRunLog is a single persisted log line emitted by an agent-runner pod
// and pushed to the Operator for the WebUI. Seq is a per-run monotonically
// increasing counter assigned by the runner; gaps mean lines were dropped.
type AgentRunLog struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement"`
	AgentRunID int        `gorm:"column:agent_run_id;index:idx_agent_run_logs_run,priority:1;uniqueIndex:uq_agent_run_log_seq,priority:1"`
	Seq        int64      `gorm:"column:seq;uniqueIndex:uq_agent_run_log_seq,priority:2"`
	TS         *time.Time `gorm:"column:ts"`
	Line       string     `gorm:"column:line;type:mediumtext"`
	CreatedAt  time.Time  `gorm:"column:created_at;autoCreateTime;index:idx_agent_run_logs_created"`
}

// TableName specifies the table name for AgentRunLog
func (AgentRunLog) TableName() string {
	return "agent_run_logs"
}
