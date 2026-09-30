package middleware

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const deliveryHeader = "X-GitHub-Delivery"

type webhookPayload struct {
	Issue struct {
		ID     uint64 `json:"id"`
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// IdempotencyMiddleware separates webhook receipt deduplication from execution
// admission. Ignored, denied, and coalesced deliveries never reserve AgentRuns.
// Receipts are claimed before dispatch, preserving at-most-once side effects even
// for approval comments that do not create an execution.
func IdempotencyMiddleware() gin.HandlerFunc {
	db := config.GetDB()
	repo := repositories.NewAgentRunRepository(db)
	return func(c *gin.Context) {
		deliveryID := c.GetHeader(deliveryHeader)
		c.Set("delivery_id", deliveryID)
		if deliveryID != "" {
			run, err := repo.GetByIDempotencyKey(deliveryID)
			if err == nil {
				c.AbortWithStatusJSON(http.StatusOK, gin.H{"status": "already_processed", "delivery_id": deliveryID, "agent_run_id": run.ID})
				return
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "failed to check idempotency"})
				return
			}
		}

		duplicate := false
		err := db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			if deliveryID != "" {
				claimed, err := repositories.ClaimWebhookDelivery(tx, deliveryID, c.GetHeader("X-GitHub-Event"))
				if err != nil {
					return err
				}
				if !claimed {
					duplicate = true
					return nil
				}
			}
			// Claim and projection commit together. A duplicate old delivery must not
			// overwrite the projection (for example, close an already reopened Issue).
			if raw, exists := c.Get("webhook_payload"); exists {
				if body, ok := raw.([]byte); ok {
					var payload webhookPayload
					if json.Unmarshal(body, &payload) == nil && payload.Repository.FullName != "" && payload.Issue.Number > 0 {
						state := payload.Issue.State
						if state != "open" && state != "closed" {
							state = "open"
						}
						issue := models.Issue{Repo: payload.Repository.FullName, Number: payload.Issue.Number, GitHubIssueID: payload.Issue.ID, Title: payload.Issue.Title, State: state, Labels: "[]"}
						return tx.Clauses(clause.OnConflict{
							Columns:   []clause.Column{{Name: "repo"}, {Name: "number"}},
							DoUpdates: clause.AssignmentColumns([]string{"title", "state", "updated_at"}),
						}).Create(&issue).Error
					}
				}
			}
			return nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "failed to persist webhook receipt"})
			return
		}
		if duplicate {
			c.AbortWithStatusJSON(http.StatusOK, gin.H{"status": "already_processed", "delivery_id": deliveryID})
			return
		}
		c.Next()
	}
}
