package models

import (
	"time"
)

// OperationLog represents a log entry for an idempotent operation within an AgentRun
type OperationLog struct {
	ID            int       `gorm:"primaryKey;autoIncrement"`
	RunID         int       `gorm:"column:run_id;index:idx_operation_run_type"`
	OperationType string    `gorm:"column:operation_type;type:enum('pr-create','post-comment','request-review','merge');index:idx_operation_run_type"`
	OperationID   string    `gorm:"column:operation_id;uniqueIndex;size:191"`
	Status        string    `gorm:"type:enum('pending','succeeded','failed');default:'pending'"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"`
	// Note: No UpdatedAt - operations are logged once, status may be updated but not logged again

	// Relationships
	AgentRun AgentRun `gorm:"foreignKey:RunID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for OperationLog
func (OperationLog) TableName() string {
	return "operation_logs"
}
