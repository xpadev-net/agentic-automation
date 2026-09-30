-- +goose Up
CREATE TABLE webhook_deliveries (
 delivery_id VARCHAR(191) NOT NULL PRIMARY KEY,
 event_type VARCHAR(64) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 INDEX idx_webhook_deliveries_created_at (created_at)
);

-- +goose Down
DROP TABLE webhook_deliveries;
