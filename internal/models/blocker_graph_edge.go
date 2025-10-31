package models

import (
	"time"
)

// BlockerGraphEdge represents a dependency edge in the task blocker graph
// task_id is blocked by depends_on_task_id
type BlockerGraphEdge struct {
	TaskID          int       `gorm:"column:task_id;primaryKey"`
	DependsOnTaskID int       `gorm:"column:depends_on_task_id;primaryKey;index"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime"`

	// Relationships
	Task          Issue `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE"`
	DependsOnTask Issue `gorm:"foreignKey:DependsOnTaskID;constraint:OnDelete:CASCADE"`
}

// TableName specifies the table name for BlockerGraphEdge
func (BlockerGraphEdge) TableName() string {
	return "blocker_graph_edges"
}

