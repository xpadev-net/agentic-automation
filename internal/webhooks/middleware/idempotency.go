package middleware

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const deliveryHeader = "X-GitHub-Delivery"

// webhookPayload represents a minimal structure to extract issue information from webhook payloads
type webhookPayload struct {
	Issue struct {
		ID     uint64 `json:"id"`     // GitHub issue ID (numeric ID)
		Number int    `json:"number"` // Issue number
		Title  string `json:"title"`  // Issue title
		State  string `json:"state"`  // Issue state (open/closed)
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"` // Repository full name, e.g., "owner/repo"
	} `json:"repository"`
}

// IdempotencyMiddleware is a Gin middleware that prevents duplicate processing
// of GitHub webhook events by checking if an AgentRun with the same idempotency key
// (X-GitHub-Delivery header) already exists in the database.
//
// For new events that have an issue ID in the payload, it creates a new AgentRun
// record with state="queued" to persist the delivery ID for idempotency.
//
// If a record is found, it returns 200 OK and aborts the request to prevent
// GitHub from retrying the webhook.
// If no record is found and issue ID cannot be extracted, it continues processing
// (for non-issue related events like pull_request, check_suite, etc.).
func IdempotencyMiddleware() gin.HandlerFunc {
	logger := config.GetLogger()

	// Get database connection
	db := config.GetDB()

	// Initialize repositories
	agentRunRepo := repositories.NewAgentRunRepository(db)
	issueRepo := repositories.NewIssueRepository()

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
		existingRun, err := agentRunRepo.GetByIDempotencyKey(deliveryID)

		if err != nil {
			// Check if it's a "not found" error (expected case for new events)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// New event - try to persist the delivery ID by creating an AgentRun record
				// This ensures that duplicate requests with the same delivery ID will be detected

				// Get payload from context (set by signature middleware)
				payloadBytes, exists := c.Get("webhook_payload")
				if !exists {
					// If payload is not in context, we cannot extract issue ID
					// Continue processing - downstream handlers may handle this
					logger.Debug("Processing new webhook delivery (no payload in context)",
						zap.String("delivery_id", deliveryID),
						zap.String("path", c.Request.URL.Path),
					)
					c.Set("delivery_id", deliveryID)
					c.Next()
					return
				}

				// Try to parse payload and extract issue ID
				var payload webhookPayload
				if err := json.Unmarshal(payloadBytes.([]byte), &payload); err != nil {
					// Payload parsing failed - continue processing
					// This may happen for non-issue related events
					logger.Debug("Processing new webhook delivery (cannot parse payload for issue ID)",
						zap.String("delivery_id", deliveryID),
						zap.String("path", c.Request.URL.Path),
						zap.Error(err),
					)
					c.Set("delivery_id", deliveryID)
					c.Next()
					return
				}

				// Check if issue information was found in payload
				if payload.Repository.FullName == "" || payload.Issue.Number == 0 {
					// No repository or issue number in payload - this is not an issue-related event
					// Continue processing without creating AgentRun
					logger.Debug("Processing new webhook delivery (no repository or issue number in payload)",
						zap.String("delivery_id", deliveryID),
						zap.String("path", c.Request.URL.Path),
					)
					c.Set("delivery_id", deliveryID)
					c.Next()
					return
				}

				// Extract issue information from payload
				repoFullName := payload.Repository.FullName
				issueNumber := payload.Issue.Number
				githubIssueID := payload.Issue.ID
				issueTitle := payload.Issue.Title
				issueState := payload.Issue.State

				// Validate issue state
				if issueState != "open" && issueState != "closed" {
					issueState = "open" // Default to open if invalid state
				}

				// Look up or create Issue record using repo and number
				// First, try to find existing issue
				existingIssue, findErr := issueRepo.FindByRepoAndNumber(repoFullName, issueNumber)
				var issue *models.Issue

				if findErr != nil && errors.Is(findErr, gorm.ErrRecordNotFound) {
					// Issue not found, create new one
					issue = &models.Issue{
						Repo:          repoFullName,
						Number:        issueNumber,
						GitHubIssueID: githubIssueID,
						Title:         issueTitle,
						State:         issueState,
						Labels:        "[]",
					}
				} else if findErr != nil {
					// Some other error occurred
					logger.Error("Failed to find issue for idempotency",
						zap.Error(findErr),
						zap.String("delivery_id", deliveryID),
						zap.String("repo", repoFullName),
						zap.Int("issue_number", issueNumber),
						zap.String("path", c.Request.URL.Path),
					)

					// Return 200 OK to prevent GitHub from retrying
					c.JSON(http.StatusOK, gin.H{
						"error":       "failed to find issue",
						"delivery_id": deliveryID,
					})
					c.Abort()
					return
				} else {
					// Issue exists, update it (preserve existing GitHubIssueID if not set)
					issue = existingIssue
					issue.Title = issueTitle
					issue.State = issueState
					// Only update GitHubIssueID if it's not already set (0 means not set)
					if existingIssue.GitHubIssueID == 0 {
						issue.GitHubIssueID = githubIssueID
					}

					// Update the issue
					if err := issueRepo.Update(issue); err != nil {
						logger.Error("Failed to update issue for idempotency",
							zap.Error(err),
							zap.String("delivery_id", deliveryID),
							zap.String("repo", repoFullName),
							zap.Int("issue_number", issueNumber),
							zap.String("path", c.Request.URL.Path),
						)

						// Return 200 OK to prevent GitHub from retrying
						c.JSON(http.StatusOK, gin.H{
							"error":       "failed to update issue",
							"delivery_id": deliveryID,
						})
						c.Abort()
						return
					}
				}

				// Create new issue if it doesn't exist
				if issue.ID == 0 {
					if err := issueRepo.Create(issue); err != nil {
						// Failed to create issue - log error and abort
						logger.Error("Failed to create issue for idempotency",
							zap.Error(err),
							zap.String("delivery_id", deliveryID),
							zap.String("repo", repoFullName),
							zap.Int("issue_number", issueNumber),
							zap.Uint64("github_issue_id", githubIssueID),
							zap.String("path", c.Request.URL.Path),
						)

						// Return 200 OK to prevent GitHub from retrying
						c.JSON(http.StatusOK, gin.H{
							"error":       "failed to create issue",
							"delivery_id": deliveryID,
						})
						c.Abort()
						return
					}
				}

				// Use the internal database ID from the upserted issue
				issueDBID := issue.ID

				// Create AgentRun record to persist delivery ID
				newRun := &models.AgentRun{
					IssueID: issueDBID,
					State:   "queued", // Initial state - will be updated by downstream handlers
				}

				createdRun, isNew, createErr := agentRunRepo.CreateOrGet(deliveryID, newRun)
				if createErr != nil {
					// Failed to create/get record - log error but continue processing
					// The downstream handler may handle this or retry
					logger.Error("Failed to persist delivery ID for idempotency",
						zap.Error(createErr),
						zap.String("delivery_id", deliveryID),
						zap.Int("issue_db_id", issueDBID),
						zap.String("repo", repoFullName),
						zap.Int("issue_number", issueNumber),
						zap.String("path", c.Request.URL.Path),
					)

					// Return 200 OK to prevent GitHub from retrying
					c.JSON(http.StatusOK, gin.H{
						"error":       "failed to persist idempotency record",
						"delivery_id": deliveryID,
					})
					c.Abort()
					return
				}

				if !isNew {
					// Record already exists (race condition - another request created it)
					logger.Info("Webhook already processed (race condition detected), skipping",
						zap.String("delivery_id", deliveryID),
						zap.Int("agent_run_id", createdRun.ID),
						zap.String("agent_run_state", createdRun.State),
						zap.String("path", c.Request.URL.Path),
					)

					// Return 200 OK to acknowledge receipt and prevent GitHub from retrying
					c.JSON(http.StatusOK, gin.H{
						"status":      "already_processed",
						"delivery_id": deliveryID,
					})
					c.Abort()
					return
				}

				// Successfully created new record
				logger.Debug("Created idempotency record for new webhook delivery",
					zap.String("delivery_id", deliveryID),
					zap.Int("agent_run_id", createdRun.ID),
					zap.Int("issue_db_id", issueDBID),
					zap.String("repo", repoFullName),
					zap.Int("issue_number", issueNumber),
					zap.String("path", c.Request.URL.Path),
				)

				// Store delivery ID and agent run ID in context for downstream handlers
				c.Set("delivery_id", deliveryID)
				c.Set("agent_run_id", createdRun.ID)

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
