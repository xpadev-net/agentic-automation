package repositories

import (
	"agentic-automation/internal/models"
	"gorm.io/gorm"
)

// ClaimWebhookDelivery atomically claims a receipt. A duplicate is a no-op even
// when no execution was created, or a different execution was already active.
func ClaimWebhookDelivery(db *gorm.DB, deliveryID, eventType string) (bool, error) {
	err := db.Create(&models.WebhookDelivery{DeliveryID: deliveryID, EventType: eventType}).Error
	if isUniqueConstraintViolation(err) {
		return false, nil
	}
	return err == nil, err
}
