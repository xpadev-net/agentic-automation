package middleware

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const deliveryHeader = "X-GitHub-Delivery"

// IdempotencyMiddleware is a Gin middleware that prevents duplicate processing
// of GitHub webhook events by checking if an AgentRun with the same idempotency key
// (X-GitHub-Delivery header) already exists in the database.
//
// If a record is found, it returns 200 OK and aborts the request to prevent
// GitHub from retrying the webhook.
// If no record is found, it stores the delivery ID in the context and continues
// to the next handler.
func IdempotencyMiddleware() gin.HandlerFunc {
	logger := config.GetLogger()

	// Get database connection
	db := config.GetDB()

	// Initialize repository
	repository := repositories.NewAgentRunRepository(db)

	return func(c *gin.Context) {
		// Get delivery ID from header
		deliveryID := c.GetHeader(deliveryHeader)

		// Validate delivery ID
		if deliveryID == "" {
			logger.Warn("Missing X-GitHub-Delivery header",
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
			)
			// Continue processing even without delivery ID (defensive programming)
			// GitHub should always send this header, but we don't want to block
			// in case of unusual circumstances
			c.Next()
			return
		}

		// Check if this delivery ID has already been processed
		existingRun, err := repository.GetByIDempotencyKey(deliveryID)

		if err != nil {
			// Check if it's a "not found" error (expected case for new events)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// New event, continue processing
				logger.Debug("Processing new webhook delivery",
					zap.String("delivery_id", deliveryID),
					zap.String("path", c.Request.URL.Path),
				)

				// Store delivery ID in context for downstream handlers
				c.Set("delivery_id", deliveryID)

				// Continue to next handler
				c.Next()
				return
			}

			// Database error (unexpected)
			logger.Error("Failed to check idempotency",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
			)

			// Return 200 OK to prevent GitHub from retrying
			// We log the error but don't want GitHub to keep retrying
			c.JSON(http.StatusOK, gin.H{
				"error":       "idempotency check failed",
				"delivery_id": deliveryID,
			})
			c.Abort()
			return
		}

		// Existing record found - this webhook has already been processed
		logger.Info("Webhook already processed, skipping",
			zap.String("delivery_id", deliveryID),
			zap.Int("agent_run_id", existingRun.ID),
			zap.String("agent_run_state", existingRun.State),
			zap.String("path", c.Request.URL.Path),
		)

		// Return 200 OK to acknowledge receipt and prevent GitHub from retrying
		c.JSON(http.StatusOK, gin.H{
			"status":      "already_processed",
			"delivery_id": deliveryID,
		})
		c.Abort()
	}
}
