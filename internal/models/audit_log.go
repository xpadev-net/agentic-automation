package models

import (
	"time"
)

// AuditLog represents an immutable audit trail entry for system operations
type AuditLog struct {
	ID             int       `gorm:"primaryKey;autoIncrement"`
	EventType      string    `gorm:"column:event_type;index:idx_audit_event_created;size:191"`
	Actor          string    `gorm:"size:191;index"`
	ResourceType   string    `gorm:"column:resource_type;index:idx_audit_resource;size:191"`
	ResourceID     int       `gorm:"column:resource_id;index:idx_audit_resource"`
	Payload        string    `gorm:"type:json"` // JSON stored as string
	IdempotencyKey *string   `gorm:"column:idempotency_key;index;size:191"`
	IPAddress      *string   `gorm:"column:ip_address;size:45"`
	UserAgent      *string   `gorm:"column:user_agent;type:text"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime"`
	// Note: No UpdatedAt - audit logs are immutable
}

// TableName specifies the table name for AuditLog
func (AuditLog) TableName() string {
	return "audit_logs"
}

