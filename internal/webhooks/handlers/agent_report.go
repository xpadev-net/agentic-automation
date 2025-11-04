package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/utils"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ReportRequest represents the request body for agent execution report
type ReportRequest struct {
	Status       string `json:"status" binding:"required,oneof=succeeded failed"`
	AgentType    string `json:"agent_type" binding:"required,oneof=claude-code cursor-agents"`
	PRNumber     *int   `json:"pr_number,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Logs         string `json:"logs,omitempty"`
}

// ReportResponse represents the response for successful report
type ReportResponse struct {
	Message    string `json:"message"`
	AgentRunID int    `json:"agent_run_id"`
	PRURL      string `json:"pr_url,omitempty"`
}

// HandleAgentReport handles POST /api/agent-runs/:id/report requests
// It receives execution results from agent-runner Pods and updates AgentRun state
func HandleAgentReport(c *gin.Context) {
	logger := config.GetLogger()

	// Get AgentRun ID from path parameter
	idStr := c.Param("id")
	agentRunID, err := strconv.Atoi(idStr)
	if err != nil {
		logger.Warn("Invalid agent run ID in path",
			zap.String("id", idStr),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid agent run ID",
		})
		return
	}

	// Parse request body
	var req ReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Get database connection and repository
	db := config.GetDB()
	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Get AgentRun by ID
	agentRun, err := agentRunRepo.GetByID(agentRunID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("AgentRun not found",
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "AGENT_RUN_NOT_FOUND",
				"message": "AgentRun with ID " + idStr + " not found",
			})
			return
		}

		logger.Error("Failed to retrieve AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to retrieve agent run",
		})
		return
	}

	// Prepare values common to both success and failure
	now := time.Now()
	agentRun.State = req.Status
	agentRun.AgentType = req.AgentType
	agentRun.CompletedAt = &now

	var prURL string
	var logsExcerpt string
	var finalErrorMessage string

	if req.Status == "succeeded" {
		// Load Issue for repo to build PR URL
		issueRepo := repositories.NewIssueRepository()
		issue, err := issueRepo.FindByID(agentRun.IssueID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warn("Issue not found for AgentRun",
					zap.Int("agent_run_id", agentRunID),
					zap.Int("issue_id", agentRun.IssueID),
				)
				c.JSON(http.StatusNotFound, gin.H{
					"error":   "ISSUE_NOT_FOUND",
					"message": "Issue not found for the agent run",
				})
				return
			}
			logger.Error("Failed to load Issue for AgentRun",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.Int("issue_id", agentRun.IssueID),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to load related issue",
			})
			return
		}

		// Upsert PullRequest and link to AgentRun
		prRepo := repositories.NewPullRequestRepository(db)
		pr := &models.PullRequest{
			Repo:    issue.Repo,
			Number:  *req.PRNumber,
			IssueID: &agentRun.IssueID,
			Branch:  req.Branch,
			Status:  "open",
		}
		if err := prRepo.Upsert(pr); err != nil {
			logger.Error("Failed to upsert PullRequest",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.String("repo", issue.Repo),
				zap.Int("pr_number", *req.PRNumber),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to upsert pull request",
			})
			return
		}

		// Reload PR to obtain ID (Upsert doesn't mutate ID reliably)
		savedPR, err := prRepo.FindByRepoAndNumber(issue.Repo, *req.PRNumber)
		if err != nil {
			logger.Error("Failed to load PullRequest after upsert",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.String("repo", issue.Repo),
				zap.Int("pr_number", *req.PRNumber),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to load pull request",
			})
			return
		}

		// Link PR to AgentRun and build PR URL
		agentRun.PRID = &savedPR.ID
		prURL = fmt.Sprintf("https://github.com/%s/pull/%d", issue.Repo, savedPR.Number)

		// Create ReviewFeedback record for review request (US3 T103)
		reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
		reviewFeedback, err := reviewFeedbackRepo.CreateRequestedReview(savedPR.ID, nil)
		if err != nil {
			logger.Warn("Failed to create ReviewFeedback record for PR",
				zap.Error(err),
				zap.Int("pr_id", savedPR.ID),
				zap.Int("agent_run_id", agentRunID),
			)
		} else {
			logger.Info("Created ReviewFeedback record for review request",
				zap.Int("review_feedback_id", reviewFeedback.ID),
				zap.Int("pr_id", savedPR.ID),
				zap.Int("agent_run_id", agentRunID),
				zap.String("status", reviewFeedback.Status),
			)
		}
	}

	// Validate required fields for succeeded status (after ensuring AgentRun exists)
	if req.Status == "succeeded" {
		if req.PRNumber == nil || *req.PRNumber <= 0 {
			logger.Warn("Missing or invalid PR number for succeeded status",
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "INVALID_REQUEST",
				"message": "pr_number is required and must be > 0 when status is succeeded",
			})
			return
		}
	}

	// Update commit SHA if provided
	if req.CommitSHA != "" {
		agentRun.CommitSHA = &req.CommitSHA
	}

	// Failure handling: extract error summary and record audit log
	if req.Status == "failed" {
		var parts []string
		if strings.TrimSpace(req.ErrorMessage) != "" {
			parts = append(parts, strings.TrimSpace(req.ErrorMessage))
		}
		summary, excerpt := utils.ExtractErrorSummary(req.Logs, utils.ErrSummaryMaxLines, utils.ErrSummaryMaxBytes)
		if strings.TrimSpace(summary) != "" {
			parts = append(parts, strings.TrimSpace(summary))
		}
		if len(parts) > 0 {
			combined := strings.Join(parts, "\n---\n")
			// Ensure combined stays within ErrSummaryMaxBytes for safety
			trimmed := combined
			if len(trimmed) > utils.ErrSummaryMaxBytes {
				trimmed = trimmed[:utils.ErrSummaryMaxBytes]
			}
			agentRun.ErrorMessage = &trimmed
			finalErrorMessage = trimmed
			preview := trimmed
			if len(preview) > 100 {
				preview = preview[:100]
			}
			config.GetLogger().Info("Agent run failure summary prepared",
				zap.Int("agent_run_id", agentRunID),
				zap.String("error_preview", preview),
			)
		}
		logsExcerpt = excerpt
		if err := services.RecordAgentRunFailure(agentRun, summary, excerpt); err != nil {
			config.GetLogger().Warn("Failed to record audit log for agent-run failure", zap.Error(err))
		}
	}

	// Build structured output JSON (schema v1)
	outputPayload := map[string]any{
		"schema_version": "1",
		"status":         req.Status,
		"agent_type":     req.AgentType,
	}
	if req.PRNumber != nil {
		outputPayload["pr_number"] = *req.PRNumber
	}
	if prURL != "" {
		outputPayload["pr_url"] = prURL
	}
	if req.CommitSHA != "" {
		outputPayload["commit_sha"] = req.CommitSHA
	}
	if finalErrorMessage != "" {
		outputPayload["error_message"] = finalErrorMessage
	} else if strings.TrimSpace(req.ErrorMessage) != "" {
		outputPayload["error_message"] = strings.TrimSpace(req.ErrorMessage)
	}
	if logsExcerpt != "" {
		outputPayload["logs_excerpt"] = logsExcerpt
	}

	if outBytes, mErr := json.Marshal(outputPayload); mErr == nil {
		agentRun.Output = datatypes.JSON(outBytes)
	} else {
		logger.Warn("Failed to marshal structured output payload", zap.Error(mErr))
	}

	// Save updated AgentRun
	if err := agentRunRepo.Update(agentRun); err != nil {
		logger.Error("Failed to update AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to update agent run",
		})
		return
	}

	// Log successful report
	if prURL != "" {
		logger.Info("Agent execution report received",
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("agent_type", req.AgentType),
			zap.String("pr_url", prURL),
			zap.String("path", c.Request.URL.Path),
		)
	} else {
		logger.Info("Agent execution report received",
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("agent_type", req.AgentType),
			zap.String("path", c.Request.URL.Path),
		)
	}

	// ------------------------------------------------------------------
	// Max retries exceeded notification (US3 T101)
	// Send Discord notification when max retries (50) is exceeded
	// ------------------------------------------------------------------
	if req.Status == "failed" && agentRun.RetryCount >= 50 {
		// Load Issue for notification context
		issueRepo := repositories.NewIssueRepository()
		issue, err := issueRepo.FindByID(agentRun.IssueID)
		if err != nil {
			logger.Warn("Failed to load Issue for max retries notification",
				zap.Error(err),
				zap.Int("issue_id", agentRun.IssueID),
				zap.Int("agent_run_id", agentRunID),
				zap.Int("retry_count", agentRun.RetryCount),
			)
		} else {
			// Initialize Discord client and notification service
			discordClient := clients.NewDiscordClient("", logger)
			discordNotification := services.NewDiscordNotificationService(discordClient, logger)

			// Send max retries notification (non-blocking)
			if err := discordNotification.NotifyMaxRetries(c.Request.Context(), agentRun, issue); err != nil {
				logger.Warn("Failed to send Discord max retries notification",
					zap.Error(err),
					zap.Int("agent_run_id", agentRunID),
					zap.Int("issue_number", issue.Number),
					zap.Int("retry_count", agentRun.RetryCount),
				)
			} else {
				logger.Info("Sent Discord max retries notification",
					zap.Int("agent_run_id", agentRunID),
					zap.Int("issue_number", issue.Number),
					zap.Int("retry_count", agentRun.RetryCount),
				)
			}
		}
	}

	// ------------------------------------------------------------------
	// PR created notification (US2 T083)
	// Post status comments to both Issue and PR upon success
	// ------------------------------------------------------------------
	if req.Status == "succeeded" && req.PRNumber != nil {
		// Load Issue for repo context
		issueRepo := repositories.NewIssueRepository()
		issue, err := issueRepo.FindByID(agentRun.IssueID)
		if err != nil {
			logger.Warn("Failed to load Issue for PR notification",
				zap.Error(err),
				zap.Int("issue_id", agentRun.IssueID),
				zap.Int("agent_run_id", agentRunID),
			)
		} else {
			// Parse owner/repo from Issue.Repo (format: owner/repo)
			owner := ""
			repo := ""
			if parts := strings.SplitN(issue.Repo, "/", 2); len(parts) == 2 {
				owner, repo = parts[0], parts[1]
			}

			if owner != "" && repo != "" {
				// Initialize GitHub App client (DI/global), then per-repo client
				if appGitHubClient == nil {
					if ghApp, err := clients.NewGitHubAppClient(logger); err == nil {
						appGitHubClient = ghApp
					} else {
						logger.Warn("Failed to init GitHub App client; skip PR created notifications", zap.Error(err))
						// Skip notifications safely
						goto RESP
					}
				}

				rawClient, err := appGitHubClient.ForRepo(c.Request.Context(), owner, repo)
				if err != nil {
					logger.Warn("Failed to init per-repo GitHub client; skip PR created notifications", zap.Error(err))
					goto RESP
				}
				githubClient := clients.NewFromGitHub(rawClient, logger)
				githubNotification := services.NewGitHubNotificationService(githubClient, logger)

				prNumber := *req.PRNumber
				prURL := "https://github.com/" + owner + "/" + repo + "/pull/" + strconv.Itoa(prNumber)
				branch := req.Branch
				sha := req.CommitSHA
				idemKey := agentRun.IdempotencyKey

				if err := githubNotification.NotifyPRCreated(
					c.Request.Context(),
					owner,
					repo,
					issue.Number,
					prNumber,
					prURL,
					branch,
					sha,
					idemKey,
				); err != nil {
					logger.Warn("Failed to post PR created notifications",
						zap.Error(err),
						zap.String("owner", owner),
						zap.String("repo", repo),
						zap.Int("issue_number", issue.Number),
						zap.Int("pr_number", prNumber),
					)
				} else {
					logger.Info("Posted PR created notifications",
						zap.String("owner", owner),
						zap.String("repo", repo),
						zap.Int("issue_number", issue.Number),
						zap.Int("pr_number", prNumber),
					)
				}
			}
		}
	}

	// Return success response
RESP:
	c.JSON(http.StatusOK, ReportResponse{
		Message:    "Report received, AgentRun #" + idStr + " updated to " + req.Status,
		AgentRunID: agentRunID,
		PRURL:      prURL,
	})
}
