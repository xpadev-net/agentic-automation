package models

import "time"

// WebhookDelivery records receipt independently of execution. A delivery can be
// ignored, denied, or coalesced with a running job without becoming an AgentRun.
// Receipts are retained for at-most-once webhook dispatch, including replays
// after a run finishes. Handler errors require a new delivery/manual retry.
type WebhookDelivery struct {
	DeliveryID string    `gorm:"primaryKey;size:191"`
	EventType  string    `gorm:"size:64"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index"`
}
