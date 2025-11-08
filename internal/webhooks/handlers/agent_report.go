package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	errorcodes "agentic-automation/internal/errors"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/utils"
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ReportRequest represents the request body for agent execution report
type ReportRequest struct {
	Status       string `json:"status" binding:"required,oneof=succeeded failed"`
	AgentType    string `json:"agent_type" binding:"required,oneof=claude-code cursor-agent"`
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

// PlanReportRequest represents the request body for plan creation/execution reports.
type PlanReportRequest struct {
	Status          string `json:"status" binding:"required,oneof=plan_created plan_rejected"`
	AgentType       string `json:"agent_type" binding:"required,oneof=claude-code cursor-agent"`
	PlanContent     string `json:"plan_content,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
	Logs            string `json:"logs,omitempty"`
}

const planPreviewLogLimit = 100

type planRejectionHTTPError struct {
	status  int
	code    string
	message string
	err     error
}

func (e *planRejectionHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.err != nil {
		return e.err.Error()
	}
	return e.message
}

// executionRunAlreadyExistsError indicates that an execution run already exists for a plan
type executionRunAlreadyExistsError struct {
	executionRunID int
}

func (e *executionRunAlreadyExistsError) Error() string {
	return fmt.Sprintf("execution run already exists: %d", e.executionRunID)
}

var (
	kubernetesClientFactory     = clients.NewKubernetesClient
	kubernetesJobServiceFactory = services.NewKubernetesJobService
	postPlanRejectionComment    = func(
		ctx context.Context,
		logger *zap.Logger,
		db *gorm.DB,
		reviewFeedback *models.ReviewFeedback,
		sanitizedReason string,
	) error {
		prRepo := repositories.NewPullRequestRepository(db)
		pr, err := prRepo.FindByID(reviewFeedback.PRID)
		if err != nil {
			logger.Error("Failed to load PullRequest for plan rejection comment",
				zap.Error(err),
				zap.Int("pr_id", reviewFeedback.PRID),
			)
			return &planRejectionHTTPError{
				status:  http.StatusInternalServerError,
				code:    "INTERNAL_ERROR",
				message: "Failed to load pull request",
				err:     fmt.Errorf("load pull request: %w", err),
			}
		}

		repoParts := strings.SplitN(pr.Repo, "/", 2)
		if len(repoParts) != 2 {
			logger.Error("Invalid repository format for pull request",
				zap.String("repo", pr.Repo),
				zap.Int("pr_id", pr.ID),
			)
			return &planRejectionHTTPError{
				status:  http.StatusInternalServerError,
				code:    "INVALID_REPOSITORY_FORMAT",
				message: "Pull request repository has invalid format",
				err:     fmt.Errorf("invalid repository format: %s", pr.Repo),
			}
		}

		owner, repoName := repoParts[0], repoParts[1]
		if appGitHubClient == nil {
			ghApp, err := clients.NewGitHubAppClient(logger)
			if err != nil {
				logger.Error("Failed to initialize GitHub App client for plan rejection",
					zap.Error(err),
				)
				return &planRejectionHTTPError{
					status:  http.StatusInternalServerError,
					code:    "GITHUB_CLIENT_ERROR",
					message: "Failed to initialize GitHub client",
					err:     fmt.Errorf("init github app client: %w", err),
				}
			}
			appGitHubClient = ghApp
		}

		rawClient, err := appGitHubClient.ForRepo(ctx, owner, repoName)
		if err != nil {
			logger.Error("Failed to create per-repo GitHub client",
				zap.Error(err),
				zap.String("owner", owner),
				zap.String("repo", repoName),
			)
			return &planRejectionHTTPError{
				status:  http.StatusInternalServerError,
				code:    "GITHUB_CLIENT_ERROR",
				message: "Failed to initialize GitHub client",
				err:     fmt.Errorf("init repo github client: %w", err),
			}
		}

		githubClient := clients.NewFromGitHub(rawClient, logger)
		comment := fmt.Sprintf("⚠️ プラン作成が却下されました。\n\n理由:\n%s", sanitizedReason)
		if _, err := githubClient.CreateIssueComment(ctx, owner, repoName, pr.Number, comment); err != nil {
			logger.Error("Failed to post plan rejection comment",
				zap.Error(err),
				zap.String("owner", owner),
				zap.String("repo", repoName),
				zap.Int("pr_number", pr.Number),
			)
			return &planRejectionHTTPError{
				status:  http.StatusInternalServerError,
				code:    "GITHUB_COMMENT_ERROR",
				message: "Failed to post plan rejection comment",
				err:     fmt.Errorf("post rejection comment: %w", err),
			}
		}

		return nil
	}
)

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

	var statusEnvelope struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindBodyWith(&statusEnvelope, binding.JSON); err != nil {
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

	status := strings.TrimSpace(statusEnvelope.Status)
	if status == "" {
		logger.Warn("Missing status field in request body",
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid request body: status is required",
		})
		return
	}

	isPlanReport := status == "plan_created" || status == "plan_rejected"

	// Get database connection and repository
	db := config.GetDB()
	agentRunRepo := repositories.NewAgentRunRepository(db)

	if isPlanReport {
		var planReq PlanReportRequest
		if err := c.ShouldBindBodyWith(&planReq, binding.JSON); err != nil {
			logger.Warn("Invalid plan report body",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "INVALID_REQUEST",
				"message": "Invalid plan report body: " + err.Error(),
			})
			return
		}

		handlePlanReport(c, agentRunID, &planReq, agentRunRepo, db)
		return
	}

	var req ReportRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
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

	// Get AgentRun by ID
	agentRun, err := agentRunRepo.GetByID(agentRunID)
	if err != nil {
		if goerrors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("AgentRun not found",
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			code := errorcodes.ERR_AGENT_RUN_NOT_FOUND
			userMsg := errorcodes.GetUserMessage(errorcodes.NewCodedError(code, "", nil), "ja")
			c.JSON(http.StatusNotFound, gin.H{
				"error":      string(code),
				"message":    userMsg,
				"error_code": string(code),
			})
			return
		}

		logger.Error("Failed to retrieve AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		code := errorcodes.ERR_INTERNAL_SERVER_ERROR
		userMsg := errorcodes.GetUserMessage(errorcodes.NewCodedError(code, "", nil), "ja")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      string(code),
			"message":    userMsg,
			"error_code": string(code),
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
			if goerrors.Is(err, gorm.ErrRecordNotFound) {
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
			parts = append(parts, strings.TrimSpace(utils.SanitizeUTF8(req.ErrorMessage)))
		}
		summary, excerpt := utils.ExtractErrorSummary(req.Logs, utils.ErrSummaryMaxLines, utils.ErrSummaryMaxBytes)
		summary = utils.SanitizeUTF8(summary)
		excerpt = utils.SanitizeUTF8(excerpt)
		if strings.TrimSpace(summary) != "" {
			parts = append(parts, strings.TrimSpace(summary))
		}
		if len(parts) > 0 {
			combined := strings.Join(parts, "\n---\n")
			// Ensure combined stays within ErrSummaryMaxBytes for safety
			limit := utils.GetDBOutputLimitBytes()
			sanitized := utils.SanitizeUTF8(combined)
			trimmed := utils.TruncateWithSuffix(sanitized, limit, "… [truncated]")
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
		limit := utils.GetDBOutputLimitBytes()
		if len(outBytes) > limit {
			// replace with compact summary noting truncation to keep JSON valid
			compact := map[string]any{
				"schema_version": "1",
				"status":         req.Status,
				"agent_type":     req.AgentType,
				"truncated":      true,
			}
			if b, err2 := json.Marshal(compact); err2 == nil {
				agentRun.Output = datatypes.JSON(b)
			} else {
				agentRun.Output = datatypes.JSON(outBytes[:limit])
			}
		} else {
			agentRun.Output = datatypes.JSON(outBytes)
		}
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
	// Plan execution completion: Update ReviewFeedback status (Phase 5)
	// Update ReviewFeedback.PlanCreationStatus when plan execution completes
	// ------------------------------------------------------------------
	if agentRun.ExecutionMode == "plan_execution" {
		if agentRun.ReviewFeedbackID != nil {
			reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
			reviewFeedback, err := reviewFeedbackRepo.FindByID(*agentRun.ReviewFeedbackID)
			if err != nil {
				logger.Warn("Failed to load ReviewFeedback for plan execution completion",
					zap.Error(err),
					zap.Int("review_feedback_id", *agentRun.ReviewFeedbackID),
					zap.Int("agent_run_id", agentRunID),
				)
			} else if reviewFeedback != nil {
				previousStatus := reviewFeedback.PlanCreationStatus
				if req.Status == "succeeded" {
					reviewFeedback.PlanCreationStatus = "executed"
					logger.Info("Plan execution completed successfully",
						zap.Int("review_feedback_id", reviewFeedback.ID),
						zap.Int("agent_run_id", agentRunID),
						zap.String("previous_status", previousStatus),
					)
				} else if req.Status == "failed" {
					reviewFeedback.PlanCreationStatus = "created"
					logger.Warn("Plan execution failed, reverting to 'created' state for retry",
						zap.Int("review_feedback_id", reviewFeedback.ID),
						zap.Int("agent_run_id", agentRunID),
						zap.String("previous_status", previousStatus),
						zap.String("error_message", finalErrorMessage),
					)
				}

				if err := reviewFeedbackRepo.Update(reviewFeedback); err != nil {
					logger.Warn("Failed to update ReviewFeedback after plan execution",
						zap.Error(err),
						zap.Int("review_feedback_id", reviewFeedback.ID),
						zap.Int("agent_run_id", agentRunID),
						zap.String("status", req.Status),
					)
				}
			}
		}
	}

	// ------------------------------------------------------------------
	// Retry progress notification (US3 T102)
	// Post retry progress comment on failure when retry_count < 50
	// ------------------------------------------------------------------
	if req.Status == "failed" && agentRun.RetryCount < services.MaxRetryAttempts {
		// Load Issue for repo context
		issueRepo := repositories.NewIssueRepository()
		issue, err := issueRepo.FindByID(agentRun.IssueID)
		if err != nil {
			logger.Warn("Failed to load Issue for retry progress notification",
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
						logger.Warn("Failed to init GitHub App client; skip retry progress notifications", zap.Error(err))
						// Skip notifications safely
					}
				}

				if appGitHubClient != nil {
					rawClient, err := appGitHubClient.ForRepo(c.Request.Context(), owner, repo)
					if err != nil {
						logger.Warn("Failed to init per-repo GitHub client; skip retry progress notifications", zap.Error(err))
					} else {
						githubClient := clients.NewFromGitHub(rawClient, logger)
						githubNotification := services.NewGitHubNotificationService(githubClient, logger)

						prNumber := 0
						if req.PRNumber != nil {
							prNumber = *req.PRNumber
						}

						// Prepare error reason for notification
						errorReason := finalErrorMessage
						if errorReason == "" {
							errorReason = "Agent execution failed"
						}

						// Notify retry progress (non-blocking, log errors but don't fail)
						if err := githubNotification.NotifyRetryProgress(
							c.Request.Context(),
							owner,
							repo,
							issue.Number,
							prNumber,
							agentRun.RetryCount,
							services.MaxRetryAttempts,
							errorReason,
							agentRun.IdempotencyKey,
						); err != nil {
							logger.Warn("Failed to post retry progress notifications",
								zap.Error(err),
								zap.String("owner", owner),
								zap.String("repo", repo),
								zap.Int("issue_number", issue.Number),
								zap.Int("pr_number", prNumber),
								zap.Int("agent_run_id", agentRunID),
							)
						} else {
							logger.Info("Posted retry progress notifications",
								zap.String("owner", owner),
								zap.String("repo", repo),
								zap.Int("issue_number", issue.Number),
								zap.Int("pr_number", prNumber),
								zap.Int("agent_run_id", agentRunID),
							)
						}
					}
				}
			}
		}
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
	// Delete Kubernetes Job on success (Issue #222)
	// Delete the corresponding Job when status is "succeeded"
	// This must be done BEFORE PR notification to ensure cleanup even if
	// PR notification fails (e.g., due to GitHub client initialization errors)
	// ------------------------------------------------------------------
	if req.Status == "succeeded" {
		kubernetesClient, err := kubernetesClientFactory(logger)
		if err != nil {
			logger.Warn("Failed to initialize Kubernetes client for job deletion",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
			)
		} else {
			var jobName string
			// Generate job name based on execution mode
			if agentRun.ExecutionMode == "plan_creation" && agentRun.ReviewFeedbackID != nil {
				// Plan creation jobs use a different naming scheme
				jobName = kubernetesClient.GeneratePlanCreationJobName(agentRunID, *agentRun.ReviewFeedbackID)
			} else {
				// Normal jobs and plan execution jobs use standard naming
				jobName = kubernetesClient.GenerateJobName(agentRunID)
			}

			// Delete job (non-blocking - log errors but don't fail the response)
			if err := kubernetesClient.DeleteJob(c.Request.Context(), jobName); err != nil {
				logger.Warn("Failed to delete Kubernetes Job after successful execution",
					zap.Error(err),
					zap.Int("agent_run_id", agentRunID),
					zap.String("job_name", jobName),
					zap.String("execution_mode", agentRun.ExecutionMode),
				)
			} else {
				logger.Info("Successfully deleted Kubernetes Job after successful execution",
					zap.Int("agent_run_id", agentRunID),
					zap.String("job_name", jobName),
					zap.String("execution_mode", agentRun.ExecutionMode),
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

func handlePlanReport(c *gin.Context, agentRunID int, req *PlanReportRequest, agentRunRepo repositories.AgentRunRepository, db *gorm.DB) {
	logger := config.GetLogger()
	ctx := c.Request.Context()

	agentRun, err := agentRunRepo.GetByID(agentRunID)
	if err != nil {
		if goerrors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("AgentRun not found for plan report",
				zap.Int("agent_run_id", agentRunID),
			)
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "AGENT_RUN_NOT_FOUND",
				"message": "AgentRun with ID " + strconv.Itoa(agentRunID) + " not found",
			})
			return
		}
		logger.Error("Failed to load AgentRun for plan report",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to load agent run",
		})
		return
	}

	if agentRun.ExecutionMode != "plan_creation" {
		logger.Warn("Plan report received for non plan-creation run",
			zap.Int("agent_run_id", agentRunID),
			zap.String("execution_mode", agentRun.ExecutionMode),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_PLAN_REPORT",
			"message": "Plan report can only be submitted for plan creation runs",
		})
		return
	}

	// ReviewFeedbackID can be nil for issue-triggered plan creation (e.g., /run-agent from issue)
	var reviewFeedback *models.ReviewFeedback
	if agentRun.ReviewFeedbackID != nil {
		reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
		var err error
		reviewFeedback, err = reviewFeedbackRepo.FindByID(*agentRun.ReviewFeedbackID)
		if err != nil {
			logger.Error("Failed to load ReviewFeedback for plan report",
				zap.Error(err),
				zap.Int("review_feedback_id", *agentRun.ReviewFeedbackID),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to load review feedback",
			})
			return
		}
		if reviewFeedback == nil {
			logger.Warn("ReviewFeedback not found for plan report",
				zap.Int("review_feedback_id", *agentRun.ReviewFeedbackID),
			)
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "REVIEW_FEEDBACK_NOT_FOUND",
				"message": "ReviewFeedback not found",
			})
			return
		}
	} else {
		logger.Info("Plan report received without ReviewFeedbackID (issue-triggered plan creation)",
			zap.Int("agent_run_id", agentRunID),
		)
	}

	// Check for duplicate plan report
	// For review-triggered: check ReviewFeedback.PlanCreationStatus
	// For issue-triggered: check AgentRun.State
	// Store original state for rollback purposes (for issue-triggered plan creation)
	var originalStateForRollback string
	if reviewFeedback != nil {
		// Review-triggered plan creation: check ReviewFeedback status
		currentStatus := strings.TrimSpace(reviewFeedback.PlanCreationStatus)
		if currentStatus == "created" || currentStatus == "rejected" || currentStatus == "executed" {
			logger.Info("Duplicate plan report ignored (review-triggered)",
				zap.Int("agent_run_id", agentRunID),
				zap.Int("review_feedback_id", reviewFeedback.ID),
				zap.String("plan_creation_status", currentStatus),
			)
			c.JSON(http.StatusOK, gin.H{
				"message":                "Plan report already processed",
				"plan_creation_status":   currentStatus,
				"plan_agent_run_id":      reviewFeedback.PlanAgentRunID,
				"execution_agent_run_id": reviewFeedback.ExecutionAgentRunID,
			})
			return
		}
		// For review-triggered, use current state for rollback
		originalStateForRollback = agentRun.State
	} else {
		// Issue-triggered plan creation: check for duplicate reports by checking existing execution runs
		// State transition will happen inside handlePlanCreated after execution run is created
		if req.Status == "plan_created" {
			// Save original state for rollback purposes
			originalStateForRollback = agentRun.State

			// Check if plan creation run is already succeeded or failed (duplicate report)
			if agentRun.State == "succeeded" || agentRun.State == "failed" {
				// Find existing execution AgentRun if exists (linked to current plan creation run)
				var executionRuns []*models.AgentRun
				if err := db.Where("plan_agent_run_id = ? AND execution_mode = ?",
					agentRunID, "plan_execution").Order("created_at DESC").Limit(1).Find(&executionRuns).Error; err == nil && len(executionRuns) > 0 {
					executionRun := executionRuns[0]
					// Check execution run state: if queued or failed, allow retry of execution setup
					if executionRun.State == "queued" || executionRun.State == "failed" {
						logger.Info("Plan run succeeded but execution run is queued/failed, retrying execution setup",
							zap.Int("agent_run_id", agentRunID),
							zap.Int("execution_agent_run_id", executionRun.ID),
							zap.String("execution_state", executionRun.State),
						)
						// Continue processing to retry execution setup (don't treat as duplicate)
						// The execution run will be reused and execution setup will be retried
					} else {
						// Execution run is started or succeeded, treat as successfully processed
						logger.Info("Duplicate plan report ignored (issue-triggered, execution already in progress)",
							zap.Int("agent_run_id", agentRunID),
							zap.String("state", agentRun.State),
							zap.Int("execution_agent_run_id", executionRun.ID),
							zap.String("execution_state", executionRun.State),
						)
						response := gin.H{
							"message":                "Plan report already processed",
							"plan_agent_run_id":      agentRunID,
							"state":                  agentRun.State,
							"execution_agent_run_id": executionRun.ID,
						}
						c.JSON(http.StatusOK, response)
						return
					}
				} else {
					// No execution run found, treat as duplicate
					logger.Info("Duplicate plan report ignored (issue-triggered, state check, no execution run)",
						zap.Int("agent_run_id", agentRunID),
						zap.String("state", agentRun.State),
					)
					response := gin.H{
						"message":           "Plan report already processed",
						"plan_agent_run_id": agentRunID,
						"state":             agentRun.State,
					}
					c.JSON(http.StatusOK, response)
					return
				}
			}
		} else {
			// For plan_rejected, check state normally (no atomic transition needed)
			if agentRun.State == "succeeded" || agentRun.State == "failed" {
				logger.Info("Duplicate plan report ignored (issue-triggered)",
					zap.Int("agent_run_id", agentRunID),
					zap.String("state", agentRun.State),
				)

				// Find existing execution AgentRun if exists (linked to current plan creation run)
				var executionAgentRunID *int
				var executionRuns []*models.AgentRun
				if err := db.Where("plan_agent_run_id = ? AND execution_mode = ?",
					agentRunID, "plan_execution").Order("created_at DESC").Limit(1).Find(&executionRuns).Error; err == nil && len(executionRuns) > 0 {
					executionAgentRunID = &executionRuns[0].ID
				}

				response := gin.H{
					"message":           "Plan report already processed",
					"plan_agent_run_id": agentRunID,
					"state":             agentRun.State,
				}
				if executionAgentRunID != nil {
					response["execution_agent_run_id"] = *executionAgentRunID
				}
				c.JSON(http.StatusOK, response)
				return
			}
			// For plan_rejected, use current state for rollback
			originalStateForRollback = agentRun.State
		}
	}

	sanitizedLogs := sanitizePlanLogs(req.Logs)
	now := time.Now()
	agentRun.AgentType = req.AgentType
	agentRun.CompletedAt = &now

	// Get ReviewFeedbackRepository only if needed
	var reviewFeedbackRepo *repositories.ReviewFeedbackRepository
	if reviewFeedback != nil {
		reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
	}

	switch req.Status {
	case "plan_created":
		handlePlanCreated(c, ctx, agentRunID, agentRun, reviewFeedback, req, sanitizedLogs, agentRunRepo, reviewFeedbackRepo, db, originalStateForRollback)
	case "plan_rejected":
		handlePlanRejected(c, ctx, agentRunID, agentRun, reviewFeedback, req, sanitizedLogs, agentRunRepo, reviewFeedbackRepo, db)
	default:
		logger.Warn("Unsupported plan report status",
			zap.String("status", req.Status),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_PLAN_STATUS",
			"message": "Unsupported plan report status",
		})
	}
}

func handlePlanCreated(
	c *gin.Context,
	ctx context.Context,
	agentRunID int,
	agentRun *models.AgentRun,
	reviewFeedback *models.ReviewFeedback,
	req *PlanReportRequest,
	sanitizedLogs string,
	agentRunRepo repositories.AgentRunRepository,
	reviewFeedbackRepo *repositories.ReviewFeedbackRepository,
	db *gorm.DB,
	originalState string,
) {
	logger := config.GetLogger()
	planContent := strings.TrimSpace(req.PlanContent)
	if planContent == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_PLAN_CONTENT",
			"message": "plan_content is required when status is plan_created",
		})
		return
	}

	sanitizedFullPlan := utils.SanitizeUTF8(planContent)
	planContentForStorage := utils.TruncateWithSuffix(sanitizedFullPlan, utils.GetDBOutputLimitBytes(), "… [truncated]")

	// Check if state is already "succeeded" (atomic transition already completed)
	// In this case, only update plan content and skip execution AgentRun creation
	if agentRun.State == "succeeded" {
		logger.Info("Plan creation AgentRun already succeeded, updating plan content only",
			zap.Int("agent_run_id", agentRunID),
		)

		// Update plan content and output only
		agentRun.PlanContent = &planContentForStorage
		agentRun.Output = buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, "")
		if err := agentRunRepo.Update(agentRun); err != nil {
			logger.Error("Failed to update plan content for already-succeeded AgentRun",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to update plan content",
			})
			return
		}

		// For issue-triggered plan creation, check if execution AgentRun already exists (linked to current plan creation run)
		if reviewFeedback == nil {
			var executionRuns []*models.AgentRun
			if err := db.Where("plan_agent_run_id = ? AND execution_mode = ?",
				agentRunID, "plan_execution").Order("created_at DESC").Limit(1).Find(&executionRuns).Error; err == nil && len(executionRuns) > 0 {
				logger.Info("Execution AgentRun already exists, skipping creation",
					zap.Int("plan_agent_run_id", agentRunID),
					zap.Int("execution_agent_run_id", executionRuns[0].ID),
				)
				c.JSON(http.StatusOK, gin.H{
					"message":                "Plan content updated, execution already in progress",
					"plan_agent_run_id":      agentRunID,
					"execution_agent_run_id": executionRuns[0].ID,
				})
				return
			}
		}

		// For review-triggered plan creation, execution AgentRun should already exist
		if reviewFeedback != nil && reviewFeedback.ExecutionAgentRunID != nil {
			logger.Info("Execution AgentRun already exists for review-triggered plan",
				zap.Int("plan_agent_run_id", agentRunID),
				zap.Int("execution_agent_run_id", *reviewFeedback.ExecutionAgentRunID),
			)
			c.JSON(http.StatusOK, gin.H{
				"message":                "Plan content updated, execution already in progress",
				"plan_agent_run_id":      agentRunID,
				"execution_agent_run_id": *reviewFeedback.ExecutionAgentRunID,
			})
			return
		}

		// If we reach here, state is succeeded but no execution AgentRun exists
		// This should not happen in normal flow, but we'll continue with execution creation
		logger.Warn("Plan creation AgentRun is succeeded but no execution AgentRun found, proceeding with creation",
			zap.Int("agent_run_id", agentRunID),
		)
	}

	// Prepare rollback functions (only for reviewFeedback if it exists)
	var previousStatus string
	var previousPlanContent *string
	var previousPlanAgentRunID *int
	var previousExecutionID *int
	if reviewFeedback != nil {
		previousStatus = reviewFeedback.PlanCreationStatus
		previousPlanContent = reviewFeedback.PlanContent
		previousPlanAgentRunID = reviewFeedback.PlanAgentRunID
		previousExecutionID = reviewFeedback.ExecutionAgentRunID
	}

	previousAgentRunPlan := agentRun.PlanContent
	// Use originalState parameter for rollback (captured before atomic state transition)
	// If originalState is empty, fall back to current state (should not happen in normal flow)
	previousAgentRunState := originalState
	if previousAgentRunState == "" {
		previousAgentRunState = agentRun.State
	}
	previousAgentRunError := agentRun.ErrorMessage
	previousAgentRunOutput := agentRun.Output
	previousAgentRunCompletedAt := agentRun.CompletedAt

	restoreReviewAndAgent := func() {
		if reviewFeedback != nil {
			reviewFeedback.PlanCreationStatus = previousStatus
			reviewFeedback.PlanContent = previousPlanContent
			reviewFeedback.PlanAgentRunID = previousPlanAgentRunID
			reviewFeedback.ExecutionAgentRunID = previousExecutionID
		}

		agentRun.PlanContent = previousAgentRunPlan
		agentRun.State = previousAgentRunState
		agentRun.ErrorMessage = previousAgentRunError
		agentRun.Output = previousAgentRunOutput
		agentRun.CompletedAt = previousAgentRunCompletedAt
	}

	agentRunUpdated := false
	reviewFeedbackUpdated := false
	persistRollback := func() {
		if agentRunUpdated {
			if err := agentRunRepo.Update(agentRun); err != nil {
				logger.Warn("Failed to rollback plan AgentRun state",
					zap.Error(err),
					zap.Int("agent_run_id", agentRunID),
				)
			}
		}
		if reviewFeedbackUpdated && reviewFeedback != nil && reviewFeedbackRepo != nil {
			if err := reviewFeedbackRepo.Update(reviewFeedback); err != nil {
				logger.Warn("Failed to rollback ReviewFeedback plan state",
					zap.Error(err),
					zap.Int("review_feedback_id", reviewFeedback.ID),
				)
			}
		}
	}

	// Update plan creation AgentRun
	planRunID := agentRunID
	agentRun.PlanContent = &planContentForStorage
	// For issue-triggered plan creation, state transition will happen in transaction after execution run is created
	// For review-triggered plan creation, set state to "succeeded" here
	if reviewFeedback == nil {
		// Issue-triggered: state will be updated in transaction
		// Only update plan content and output for now
		agentRun.ErrorMessage = nil
		agentRun.Output = buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, "")
		// Don't update state yet - will be done in transaction
	} else {
		// Review-triggered: update state here
		if agentRun.State != "succeeded" {
			agentRun.State = "succeeded"
		}
		agentRun.ErrorMessage = nil
		agentRun.Output = buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, "")

		if err := agentRunRepo.Update(agentRun); err != nil {
			logger.Error("Failed to update plan creation AgentRun",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
			)
			restoreReviewAndAgent()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to update plan agent run",
			})
			return
		}
		agentRunUpdated = true
	}

	// Update ReviewFeedback only if it exists (for review-triggered plan creation)
	if reviewFeedback != nil && reviewFeedbackRepo != nil {
		reviewFeedback.PlanContent = &planContentForStorage
		reviewFeedback.PlanCreationStatus = "creating"
		reviewFeedback.PlanAgentRunID = &planRunID

		if err := reviewFeedbackRepo.Update(reviewFeedback); err != nil {
			logger.Error("Failed to update ReviewFeedback with plan content",
				zap.Error(err),
				zap.Int("review_feedback_id", reviewFeedback.ID),
			)
			restoreReviewAndAgent()
			persistRollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to update review feedback",
			})
			return
		}
		reviewFeedbackUpdated = true
	}

	// Initialize Kubernetes client early for job cleanup (Issue #222)
	// This ensures cleanup happens even if early return occurs due to new reviews
	kubernetesClient, err := kubernetesClientFactory(logger)
	if err != nil {
		logger.Warn("Failed to initialize Kubernetes client for job cleanup",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.Int("review_feedback_id", reviewFeedback.ID),
		)
		// Continue processing even if client initialization fails
		// defer will skip deletion if kubernetesClient is nil
	}

	// Defer job deletion to ensure cleanup happens regardless of return path (Issue #222)
	// This handles early returns when new reviews invalidate the plan
	defer func() {
		if kubernetesClient != nil && reviewFeedback.ID > 0 && agentRunID > 0 {
			planCreationJobName := kubernetesClient.GeneratePlanCreationJobName(agentRunID, reviewFeedback.ID)
			if err := kubernetesClient.DeleteJob(ctx, planCreationJobName); err != nil {
				logger.Warn("Failed to delete plan creation Kubernetes Job",
					zap.Error(err),
					zap.Int("plan_agent_run_id", agentRunID),
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.String("job_name", planCreationJobName),
				)
			} else {
				logger.Info("Successfully deleted plan creation Kubernetes Job",
					zap.Int("plan_agent_run_id", agentRunID),
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.String("job_name", planCreationJobName),
				)
			}
		}
	}()

	issueRepo := repositories.NewIssueRepository()
	issue, err := issueRepo.FindByID(agentRun.IssueID)
	if err != nil {
		logger.Error("Failed to load Issue for plan execution",
			zap.Error(err),
			zap.Int("issue_id", agentRun.IssueID),
		)
		restoreReviewAndAgent()
		persistRollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to load issue for plan execution",
		})
		return
	}

	// Handle review-triggered plan creation (with ReviewFeedback)
	if reviewFeedback != nil && reviewFeedbackRepo != nil {
		prRepo := repositories.NewPullRequestRepository(db)
		pr, err := prRepo.FindByID(reviewFeedback.PRID)
		if err != nil {
			logger.Error("Failed to load PullRequest for plan execution",
				zap.Error(err),
				zap.Int("pr_id", reviewFeedback.PRID),
			)
			restoreReviewAndAgent()
			persistRollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to load pull request",
			})
			return
		}

		// Check for new reviews BEFORE creating execution job
		// If new reviews exist, invalidate the current plan and recreate it with updated context
		// This prevents executing a stale plan that has been invalidated by new review input
		if reviewFeedback.GitHubCommentID != nil && *reviewFeedback.GitHubCommentID > 0 {
			newerReviews, err := reviewFeedbackRepo.FindNewerReviewsByPRID(reviewFeedback.PRID, *reviewFeedback.GitHubCommentID)
			if err != nil {
				logger.Warn("Failed to check for newer reviews before execution job creation",
					zap.Error(err),
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.Int64("github_comment_id", *reviewFeedback.GitHubCommentID),
				)
				// Continue with execution job creation even if check fails
			} else if len(newerReviews) > 0 {
				logger.Info("New reviews found before execution job creation, recreating plan with updated context",
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.Int("newer_reviews_count", len(newerReviews)),
					zap.Int64("original_comment_id", *reviewFeedback.GitHubCommentID),
				)

				// Aggregate new review contents
				var aggregatedContent strings.Builder
				if reviewFeedback.Content != nil && *reviewFeedback.Content != "" {
					aggregatedContent.WriteString(*reviewFeedback.Content)
				}
				for _, newReview := range newerReviews {
					if newReview.Content != nil && *newReview.Content != "" {
						if aggregatedContent.Len() > 0 {
							aggregatedContent.WriteString("\n\n--- Additional Review ---\n\n")
						}
						aggregatedContent.WriteString(*newReview.Content)
					}
				}

				// Update reviewFeedback with aggregated content and reset plan status
				aggregatedContentStr := aggregatedContent.String()
				reviewFeedback.Content = &aggregatedContentStr
				reviewFeedback.PlanCreationStatus = "pending"
				reviewFeedback.PlanContent = nil
				reviewFeedback.PlanAgentRunID = nil
				reviewFeedback.ExecutionAgentRunID = nil

				// Update to the latest GitHubCommentID
				latestReview := newerReviews[0] // Already ordered by created_at DESC
				if latestReview.GitHubCommentID != nil {
					reviewFeedback.GitHubCommentID = latestReview.GitHubCommentID
				}

				if err := reviewFeedbackRepo.Update(reviewFeedback); err != nil {
					logger.Error("Failed to update review feedback for plan recreation",
						zap.Error(err),
						zap.Int("review_feedback_id", reviewFeedback.ID),
					)
					// Continue with normal response even if update fails
				} else {
					// Trigger plan recreation by calling startPlanCreationIfNeeded
					// Create minimal deps structure for this
					planDeps := PullRequestReviewCommentDeps{
						Logger:                   logger,
						ReviewFeedbackRepository: reviewFeedbackRepo,
					}

					// Use the aggregated content as the comment body
					commentBody := aggregatedContentStr
					if commentBody == "" {
						commentBody = "Review feedback"
					}

					// Use the latest comment ID or a placeholder
					commentID := int64(0)
					if latestReview.GitHubCommentID != nil {
						commentID = *latestReview.GitHubCommentID
					}

					// Trigger plan recreation (async - don't wait for result)
					go func() {
						// Create a new context for the background goroutine
						bgCtx := context.Background()
						_, planErr := startPlanCreationIfNeeded(
							bgCtx,
							planDeps,
							logger,
							pr,
							commentBody,
							commentID,
							"", // commentUserLogin - not critical for recreation
							0,  // commentUserID - not critical for recreation
							fmt.Sprintf("plan-recreation-%d", reviewFeedback.ID),
						)
						if planErr != nil {
							logger.Error("Failed to recreate plan with new reviews",
								zap.Error(planErr),
								zap.Int("review_feedback_id", reviewFeedback.ID),
							)
						} else {
							logger.Info("Plan recreation triggered successfully",
								zap.Int("review_feedback_id", reviewFeedback.ID),
							)
						}
					}()
				}

				// Early return: skip execution job creation since plan is being recreated
				c.JSON(http.StatusOK, gin.H{
					"message":      "Plan invalidated by new reviews, recreating plan",
					"agent_run_id": agentRunID,
				})
				return
			}
		}

		// Defensive check: verify no existing execution AgentRun exists for this review feedback
		// This provides an additional safety layer beyond the atomic state transition
		if reviewFeedback.ExecutionAgentRunID != nil {
			logger.Info("Execution AgentRun already exists for review-triggered plan, skipping creation",
				zap.Int("plan_agent_run_id", agentRunID),
				zap.Int("execution_agent_run_id", *reviewFeedback.ExecutionAgentRunID),
				zap.Int("review_feedback_id", reviewFeedback.ID),
			)
			c.JSON(http.StatusOK, gin.H{
				"message":                "Plan created, execution already in progress",
				"plan_agent_run_id":      agentRunID,
				"execution_agent_run_id": *reviewFeedback.ExecutionAgentRunID,
			})
			return
		}

		// Create plan execution AgentRun for review-triggered plan creation
		executionPRID := reviewFeedback.PRID
		executionRun := &models.AgentRun{
			IdempotencyKey:   fmt.Sprintf("plan-exec-%d-%d-%d", reviewFeedback.ID, agentRunID, time.Now().UnixNano()),
			IssueID:          agentRun.IssueID,
			PRID:             &executionPRID,
			State:            "queued",
			AgentType:        req.AgentType,
			ExecutionMode:    "plan_execution",
			PlanContent:      &planContentForStorage,
			ReviewFeedbackID: &reviewFeedback.ID,
		}

		if err := db.Create(executionRun).Error; err != nil {
			logger.Error("Failed to create execution AgentRun",
				zap.Error(err),
				zap.Int("review_feedback_id", reviewFeedback.ID),
			)
			restoreReviewAndAgent()
			persistRollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to create execution agent run",
			})
			return
		}

		branchName := fmt.Sprintf("feature/issue-%d", issue.Number)
		if trimmed := strings.TrimSpace(pr.Branch); trimmed != "" {
			branchName = trimmed
		}

		kubernetesClient, err := kubernetesClientFactory(logger)
		if err != nil {
			logger.Error("Failed to initialize Kubernetes client for plan execution",
				zap.Error(err),
			)
			restoreReviewAndAgent()
			persistRollback()
			if deleteErr := db.Delete(executionRun).Error; deleteErr != nil {
				logger.Warn("Failed to delete execution AgentRun after Kubernetes client error",
					zap.Error(deleteErr),
					zap.Int("execution_agent_run_id", executionRun.ID),
				)
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "KUBERNETES_CLIENT_ERROR",
				"message": "Failed to initialize Kubernetes client",
			})
			return
		}

		jobService := kubernetesJobServiceFactory(kubernetesClient, logger)
		job, err := jobService.CreateJobForPlanExecution(ctx, executionRun, issue, sanitizedFullPlan, branchName)
		if err != nil {
			logger.Error("Failed to create plan execution job",
				zap.Error(err),
				zap.Int("execution_agent_run_id", executionRun.ID),
			)
			failureReason := utils.TruncateWithSuffix(utils.SanitizeUTF8(err.Error()), utils.GetDBOutputLimitBytes(), "… [truncated]")
			executionRun.State = "failed"
			executionRun.ErrorMessage = &failureReason
			if updateErr := agentRunRepo.Update(executionRun); updateErr != nil {
				logger.Warn("Failed to record execution AgentRun failure state",
					zap.Error(updateErr),
					zap.Int("execution_agent_run_id", executionRun.ID),
				)
			}

			restoreReviewAndAgent()
			persistRollback()
			if deleteErr := db.Delete(executionRun).Error; deleteErr != nil {
				logger.Warn("Failed to delete execution AgentRun after job creation failure",
					zap.Error(deleteErr),
					zap.Int("execution_agent_run_id", executionRun.ID),
				)
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "PLAN_EXECUTION_JOB_CREATION_FAILED",
				"message": "Failed to create plan execution job",
			})
			return
		}

		reviewFeedback.PlanCreationStatus = "created"
		reviewFeedback.ExecutionAgentRunID = &executionRun.ID
		if err := reviewFeedbackRepo.Update(reviewFeedback); err != nil {
			logger.Error("Failed to link execution AgentRun to ReviewFeedback",
				zap.Error(err),
				zap.Int("execution_agent_run_id", executionRun.ID),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to update review feedback",
			})
			return
		}

		logger.Info("Plan created and execution job started",
			zap.Int("plan_agent_run_id", agentRunID),
			zap.Int("execution_agent_run_id", executionRun.ID),
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("plan_preview", previewString(planContentForStorage, planPreviewLogLimit)),
		)

		c.JSON(http.StatusOK, gin.H{
			"message":      "Plan created and execution job started",
			"agent_run_id": executionRun.ID,
			"job_name":     job.Name,
		})
		return
	}

	// Handle issue-triggered plan creation (without ReviewFeedback)
	// Create plan execution AgentRun for issue-triggered plan creation
	// Extract branch name from plan creation AgentRun's Input
	var branchName string
	if len(agentRun.Input) > 0 {
		var inputMap map[string]interface{}
		if err := json.Unmarshal(agentRun.Input, &inputMap); err == nil {
			if bn, ok := inputMap["branch_name"].(string); ok && strings.TrimSpace(bn) != "" {
				branchName = strings.TrimSpace(bn)
				logger.Info("Extracted branch name from plan creation AgentRun Input",
					zap.String("branch_name", branchName),
					zap.Int("plan_agent_run_id", agentRunID),
				)
			}
		} else {
			logger.Warn("Failed to unmarshal plan creation AgentRun Input for branch name extraction",
				zap.Error(err),
				zap.Int("plan_agent_run_id", agentRunID),
			)
		}
	}

	// Fallback: resolve from existing PRs if not found in Input
	if branchName == "" {
		pullRequestRepo := repositories.NewPullRequestRepository(db)
		// Prefer PR associated with this agent run if available
		if agentRun.PRID != nil {
			pr, prErr := pullRequestRepo.FindByID(*agentRun.PRID)
			if prErr == nil && pr != nil && pr.Status == "open" {
				branchName = pr.Branch
				logger.Info("Found branch name from PR associated with agent run",
					zap.String("branch_name", branchName),
					zap.Int("plan_agent_run_id", agentRunID),
					zap.Int("pr_id", *agentRun.PRID),
					zap.Int("pr_number", pr.Number),
				)
			}
		}

		// Fallback: scan all PRs for the issue if no branch found from agent run's PR
		if branchName == "" {
			prs, prErr := pullRequestRepo.FindByIssueID(issue.ID)
			if prErr == nil && len(prs) > 0 {
				// Use the first open PR if multiple exist
				for _, pr := range prs {
					if pr.Status == "open" {
						branchName = pr.Branch
						logger.Info("Found branch name from existing PR for issue",
							zap.String("branch_name", branchName),
							zap.Int("plan_agent_run_id", agentRunID),
							zap.Int("issue_id", issue.ID),
							zap.Int("pr_number", pr.Number),
						)
						break
					}
				}
			}
		}
	}

	// Last resort: use default format
	if branchName == "" {
		branchName = fmt.Sprintf("feature/issue-%d", issue.Number)
		logger.Info("Using default branch name format",
			zap.String("branch_name", branchName),
			zap.Int("plan_agent_run_id", agentRunID),
			zap.Int("issue_number", issue.Number),
		)
	}

	// Defensive check: verify no existing execution AgentRun exists (linked to current plan creation run)
	// This provides an additional safety layer beyond the atomic state transition
	// However, if execution run exists but is queued/failed, we should retry execution setup
	var existingExecutionRun *models.AgentRun
	var existingExecutionRuns []*models.AgentRun
	if err := db.Where("plan_agent_run_id = ? AND execution_mode = ?",
		agentRunID, "plan_execution").Order("created_at DESC").Limit(1).Find(&existingExecutionRuns).Error; err == nil && len(existingExecutionRuns) > 0 {
		existingExecutionRun = existingExecutionRuns[0]
		// If execution run is started or succeeded, treat as already in progress
		if existingExecutionRun.State == "started" || existingExecutionRun.State == "succeeded" {
			logger.Info("Execution AgentRun already exists for issue-triggered plan, skipping creation",
				zap.Int("plan_agent_run_id", agentRunID),
				zap.Int("execution_agent_run_id", existingExecutionRun.ID),
				zap.String("execution_state", existingExecutionRun.State),
			)
			c.JSON(http.StatusOK, gin.H{
				"message":                "Plan created, execution already in progress",
				"plan_agent_run_id":      agentRunID,
				"execution_agent_run_id": existingExecutionRun.ID,
			})
			return
		}
		// If execution run is queued or failed, we'll reuse it and retry execution setup
		logger.Info("Execution AgentRun exists but is queued/failed, will retry execution setup",
			zap.Int("plan_agent_run_id", agentRunID),
			zap.Int("execution_agent_run_id", existingExecutionRun.ID),
			zap.String("execution_state", existingExecutionRun.State),
		)
		// Continue processing to retry execution setup with existing execution run
		// Skip transaction and go directly to execution start retry
	}

	// Get completion timestamp for transactional update
	// Use agentRun.CompletedAt if already set, otherwise use current time
	now := time.Now()
	if agentRun.CompletedAt != nil {
		now = *agentRun.CompletedAt
	}

	// If existing execution run is queued/failed, skip transaction and retry execution setup directly
	var executionRun *models.AgentRun
	if existingExecutionRun != nil && (existingExecutionRun.State == "queued" || existingExecutionRun.State == "failed") {
		// Reuse existing execution run and retry execution setup
		executionRun = existingExecutionRun
		// Update plan content if needed
		if executionRun.PlanContent == nil || *executionRun.PlanContent != planContentForStorage {
			executionRun.PlanContent = &planContentForStorage
			if err := agentRunRepo.Update(executionRun); err != nil {
				logger.Warn("Failed to update execution run plan content during retry",
					zap.Error(err),
					zap.Int("execution_agent_run_id", executionRun.ID),
				)
			}
		}
		// Skip transaction and go directly to execution start retry
		// Capture original state before modifying for optimistic locking
		originalState := agentRun.State
		// Mark agentRun as updated for consistency
		agentRun.State = "succeeded"
		agentRun.PlanContent = &planContentForStorage
		agentRun.ErrorMessage = nil
		agentRun.Output = buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, "")
		agentRunUpdated = true

		// Persist plan creation run state to database
		// Use optimistic locking with WHERE condition to ensure state hasn't changed
		result := db.Model(&models.AgentRun{}).
			Where("id = ? AND state = ?", agentRunID, originalState).
			Updates(map[string]interface{}{
				"state":         "succeeded",
				"plan_content":  planContentForStorage,
				"error_message": nil,
				"output":        buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, ""),
				"completed_at":  now,
			})

		if result.Error != nil {
			logger.Error("Failed to persist plan creation run state when reusing execution run",
				zap.Error(result.Error),
				zap.Int("plan_agent_run_id", agentRunID),
			)
			restoreReviewAndAgent()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to persist plan creation run state",
			})
			return
		}

		// Check if update actually affected any rows
		// If RowsAffected == 0, the state was changed by another goroutine
		if result.RowsAffected == 0 {
			// Reload to check current state
			var reloadedRun models.AgentRun
			if err := db.First(&reloadedRun, agentRunID).Error; err != nil {
				logger.Error("Failed to reload plan creation run after state update",
					zap.Error(err),
					zap.Int("plan_agent_run_id", agentRunID),
				)
				restoreReviewAndAgent()
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "INTERNAL_ERROR",
					"message": "Failed to verify plan creation run state",
				})
				return
			}
			// If already succeeded, that's fine (idempotent)
			if reloadedRun.State == "succeeded" {
				logger.Info("Plan creation run already succeeded, continuing with execution setup",
					zap.Int("plan_agent_run_id", agentRunID),
				)
				// Continue processing - state is already correct
			} else {
				// State changed unexpectedly
				logger.Error("Plan creation run state changed unexpectedly when reusing execution run",
					zap.Int("plan_agent_run_id", agentRunID),
					zap.String("expected_state", originalState),
					zap.String("actual_state", reloadedRun.State),
				)
				restoreReviewAndAgent()
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "STATE_CHANGE_ERROR",
					"message": fmt.Sprintf("Plan creation run state changed unexpectedly: expected %s, got %s", originalState, reloadedRun.State),
				})
				return
			}
		}
	} else {
		// Extract prompt from plan creation AgentRun.Input for plan execution
		// Note: This prompt is stored in executionRun.Input but not used by CreateJobForPlanExecution
		// which uses the planContent directly. Keeping for backward compatibility and reference.
		var executionPrompt string
		if len(agentRun.Input) > 0 {
			var inputMap map[string]interface{}
			if err := json.Unmarshal(agentRun.Input, &inputMap); err == nil {
				if prompt, ok := inputMap["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
					executionPrompt = strings.TrimSpace(prompt)
				}
			}
		}
		// Fallback to issue title/body if prompt not found
		if executionPrompt == "" {
			executionPrompt = fmt.Sprintf("Issue #%d: %s", issue.Number, issue.Title)
			if issue.Body != nil && strings.TrimSpace(*issue.Body) != "" {
				executionPrompt += "\n\n" + strings.TrimSpace(*issue.Body)
			}
		}

		// Create plan execution AgentRun (ExecutionMode: "plan_execution")
		executionRun = &models.AgentRun{
			IdempotencyKey:   fmt.Sprintf("plan-exec-%d-%d-%d", agentRun.IssueID, agentRunID, time.Now().UnixNano()),
			IssueID:          agentRun.IssueID,
			PRID:             nil, // No PR yet for issue-triggered execution
			State:            "queued",
			AgentType:        req.AgentType,
			ExecutionMode:    "plan_execution",
			PlanContent:      &planContentForStorage, // Include plan content for reference
			ReviewFeedbackID: nil,                    // No review feedback for issue-triggered execution
			PlanAgentRunID:   &agentRunID,            // Link to plan creation AgentRun
			RetryCount:       0,
		}

		// Build structured input JSON for plan execution
		inputPayload := map[string]any{
			"schema_version": "1",
			"prompt":         executionPrompt,
			"agent_type":     req.AgentType,
			"plan_content":   planContentForStorage,
			"branch_name":    branchName,
		}
		inputBytes, marshalErr := json.Marshal(inputPayload)
		if marshalErr != nil {
			logger.Warn("Failed to marshal structured input payload for plan execution", zap.Error(marshalErr))
			// Fallback to minimal JSON
			inputBytes, _ = json.Marshal(map[string]any{
				"schema_version": "1",
				"prompt":         executionPrompt,
				"agent_type":     req.AgentType,
				"branch_name":    branchName,
			})
		}
		executionRun.Input = datatypes.JSON(inputBytes)

		// Use transaction to atomically create execution run and transition plan creation run to succeeded
		// This ensures that if execution run creation succeeds, plan creation run state is also updated
		// If execution run creation fails, plan creation run state remains unchanged (allowing retry)
		err = db.Transaction(func(tx *gorm.DB) error {
			// First, check the current state of the plan creation run within transaction
			// Use optimistic locking to ensure state hasn't changed
			var currentRun models.AgentRun
			if err := tx.First(&currentRun, agentRunID).Error; err != nil {
				return err
			}

			// If already succeeded, check for existing execution run and return error to rollback
			if currentRun.State == "succeeded" {
				// Check for existing execution run within transaction
				var existingExecutionRuns []*models.AgentRun
				if err := tx.Where("plan_agent_run_id = ? AND execution_mode = ?",
					agentRunID, "plan_execution").Order("created_at DESC").Limit(1).Find(&existingExecutionRuns).Error; err != nil {
					return err
				}
				if len(existingExecutionRuns) > 0 {
					// Return custom error to rollback transaction and signal that execution run already exists
					return &executionRunAlreadyExistsError{executionRunID: existingExecutionRuns[0].ID}
				}
				// If no execution run exists but state is succeeded, this is unexpected but idempotent
				return nil
			}

			// Verify state matches expected value before proceeding
			if currentRun.State != agentRun.State {
				return fmt.Errorf("plan creation run state changed unexpectedly: expected %s, got %s", agentRun.State, currentRun.State)
			}

			// Create execution run within transaction (only if state check passed)
			if err := tx.Create(executionRun).Error; err != nil {
				return err
			}

			// Transition plan creation run to "succeeded" state within transaction
			// Use optimistic locking with WHERE condition to ensure state hasn't changed
			result := tx.Model(&models.AgentRun{}).
				Where("id = ? AND state = ?", agentRunID, agentRun.State).
				Updates(map[string]interface{}{
					"state":         "succeeded",
					"plan_content":  planContentForStorage,
					"error_message": nil,
					"output":        buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, ""),
					"completed_at":  now,
				})

			if result.Error != nil {
				return result.Error
			}

			// Check if update actually affected any rows
			// If RowsAffected == 0, the state was changed by another goroutine
			if result.RowsAffected == 0 {
				// Reload to check current state
				var reloadedRun models.AgentRun
				if err := tx.First(&reloadedRun, agentRunID).Error; err != nil {
					return err
				}
				// If already succeeded, that's fine (idempotent) but execution run was already created
				// This should not happen due to the check above, but handle it gracefully
				if reloadedRun.State == "succeeded" {
					// Delete the execution run we just created since state was already succeeded
					if deleteErr := tx.Delete(executionRun).Error; deleteErr != nil {
						return fmt.Errorf("failed to delete execution run after state check: %w", deleteErr)
					}
					// Check for existing execution run
					var existingExecutionRuns []*models.AgentRun
					if err := tx.Where("plan_agent_run_id = ? AND execution_mode = ?",
						agentRunID, "plan_execution").Order("created_at DESC").Limit(1).Find(&existingExecutionRuns).Error; err != nil {
						return err
					}
					if len(existingExecutionRuns) > 0 {
						return &executionRunAlreadyExistsError{executionRunID: existingExecutionRuns[0].ID}
					}
					return nil
				}
				// Otherwise, state changed unexpectedly
				return fmt.Errorf("plan creation run state changed unexpectedly: expected %s, got %s", agentRun.State, reloadedRun.State)
			}

			return nil
		})

		if err != nil {
			// Check if error is executionRunAlreadyExistsError (idempotent case)
			var existsErr *executionRunAlreadyExistsError
			if goerrors.As(err, &existsErr) {
				logger.Info("Execution AgentRun already exists for issue-triggered plan, skipping creation",
					zap.Int("plan_agent_run_id", agentRunID),
					zap.Int("execution_agent_run_id", existsErr.executionRunID),
				)
				c.JSON(http.StatusOK, gin.H{
					"message":                "Plan created, execution already in progress",
					"plan_agent_run_id":      agentRunID,
					"execution_agent_run_id": existsErr.executionRunID,
				})
				return
			}

			logger.Error("Failed to create plan execution AgentRun or transition plan creation run state",
				zap.Error(err),
				zap.Int("plan_agent_run_id", agentRunID),
			)
			restoreReviewAndAgent()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to create plan execution agent run",
			})
			return
		}
	}

	// Mark agentRun as updated for rollback purposes (if not already done in retry path)
	if !agentRunUpdated {
		agentRun.State = "succeeded"
		agentRun.PlanContent = &planContentForStorage
		agentRun.ErrorMessage = nil
		agentRun.Output = buildPlanOutputJSON("plan_created", req.AgentType, planContentForStorage, sanitizedLogs, "")
		agentRunUpdated = true
	}

	// Reset failed execution run to queued state before transitioning to started
	// TransitionToStarted only allows queued -> started transitions, so we must reset failed runs first
	if executionRun.State == "failed" {
		executionRun.State = "queued"
		executionRun.StartedAt = nil
		executionRun.CompletedAt = nil
		executionRun.ErrorMessage = nil
		if err := agentRunRepo.Update(executionRun); err != nil {
			logger.Error("Failed to reset failed execution run to queued state",
				zap.Error(err),
				zap.Int("execution_agent_run_id", executionRun.ID),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to reset execution run state",
			})
			return
		}
		logger.Info("Reset failed execution run to queued state for retry",
			zap.Int("execution_agent_run_id", executionRun.ID),
		)
	}

	// Transition plan execution AgentRun to started state
	// Note: execution run and plan creation run state transition are already committed in transaction
	// If this fails, execution run exists and can be retried
	stateMachine := services.NewAgentRunStateMachine(agentRunRepo, logger)
	if err := stateMachine.TransitionToStarted(executionRun.ID); err != nil {
		logger.Error("Failed to transition plan execution AgentRun to started state",
			zap.Error(err),
			zap.Int("execution_agent_run_id", executionRun.ID),
		)
		// Execution run already exists in database, so we can retry later
		// Don't delete it or rollback plan creation run state
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "STATE_TRANSITION_ERROR",
			"message": "Failed to transition execution agent run to started",
		})
		return
	}

	kubernetesClient, err = kubernetesClientFactory(logger)
	if err != nil {
		logger.Error("Failed to initialize Kubernetes client for plan execution",
			zap.Error(err),
		)
		// Execution run already exists in database, so we can retry later
		// Rollback execution run state to queued so it can be retried
		if rollbackErr := stateMachine.TransitionToQueued(executionRun.ID); rollbackErr != nil {
			logger.Warn("Failed to rollback execution AgentRun state",
				zap.Error(rollbackErr),
				zap.Int("execution_agent_run_id", executionRun.ID),
			)
		}
		restoreReviewAndAgent()
		persistRollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "KUBERNETES_CLIENT_ERROR",
			"message": "Failed to initialize Kubernetes client",
		})
		return
	}

	jobService := kubernetesJobServiceFactory(kubernetesClient, logger)
	job, err := jobService.CreateJobForPlanExecution(ctx, executionRun, issue, sanitizedFullPlan, branchName)
	if err != nil {
		logger.Error("Failed to create plan execution job",
			zap.Error(err),
			zap.Int("execution_agent_run_id", executionRun.ID),
		)
		// Rollback execution run state to queued so it can be retried
		// The execution run should be in "started" state at this point
		if rollbackErr := stateMachine.TransitionToQueued(executionRun.ID); rollbackErr != nil {
			logger.Warn("Failed to rollback execution AgentRun state",
				zap.Error(rollbackErr),
				zap.Int("execution_agent_run_id", executionRun.ID),
			)
			// If rollback fails because state is not "started", try direct reset to queued
			// This handles edge cases where the state might have changed
			currentRun, getErr := agentRunRepo.GetByID(executionRun.ID)
			if getErr == nil && currentRun != nil && currentRun.State != "queued" {
				currentRun.State = "queued"
				currentRun.StartedAt = nil
				currentRun.CompletedAt = nil
				currentRun.ErrorMessage = nil
				if directUpdateErr := agentRunRepo.Update(currentRun); directUpdateErr != nil {
					logger.Warn("Failed to reset execution AgentRun to queued state directly",
						zap.Error(directUpdateErr),
						zap.Int("execution_agent_run_id", executionRun.ID),
						zap.String("current_state", currentRun.State),
					)
				} else {
					logger.Info("Reset execution AgentRun to queued state directly after TransitionToQueued failed",
						zap.Int("execution_agent_run_id", executionRun.ID),
					)
				}
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "PLAN_EXECUTION_JOB_CREATION_FAILED",
			"message": "Failed to create plan execution job",
		})
		return
	}

	logger.Info("Plan created and plan execution job started",
		zap.Int("plan_agent_run_id", agentRunID),
		zap.Int("execution_agent_run_id", executionRun.ID),
		zap.String("plan_preview", previewString(planContentForStorage, planPreviewLogLimit)),
	)

	// Note: Plan creation job deletion is handled by defer function defined earlier
	// This ensures cleanup happens even if early return occurs due to new reviews

	c.JSON(http.StatusOK, gin.H{
		"message":      "Plan created and plan execution job started",
		"agent_run_id": executionRun.ID,
		"job_name":     job.Name,
	})
}

func handlePlanRejected(
	c *gin.Context,
	ctx context.Context,
	agentRunID int,
	agentRun *models.AgentRun,
	reviewFeedback *models.ReviewFeedback,
	req *PlanReportRequest,
	sanitizedLogs string,
	agentRunRepo repositories.AgentRunRepository,
	reviewFeedbackRepo *repositories.ReviewFeedbackRepository,
	db *gorm.DB,
) {
	logger := config.GetLogger()
	reason := strings.TrimSpace(req.RejectionReason)
	if reason == "" {
		code := errorcodes.ERR_VALIDATION_MISSING_REQUIRED
		userMsg := errorcodes.GetUserMessage(errorcodes.NewCodedError(code, "", nil), "ja")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      string(code),
			"message":    userMsg,
			"error_code": string(code),
		})
		return
	}

	sanitizedReason := utils.TruncateWithSuffix(utils.SanitizeUTF8(reason), utils.GetDBOutputLimitBytes(), "… [truncated]")
	planRunID := agentRunID

	// Prepare rollback functions (only for reviewFeedback if it exists)
	var previousStatus string
	var previousPlanContent *string
	var previousPlanAgentRunID *int
	var previousExecutionID *int
	if reviewFeedback != nil {
		previousStatus = reviewFeedback.PlanCreationStatus
		previousPlanContent = reviewFeedback.PlanContent
		previousPlanAgentRunID = reviewFeedback.PlanAgentRunID
		previousExecutionID = reviewFeedback.ExecutionAgentRunID
	}

	previousAgentRunPlan := agentRun.PlanContent
	previousAgentRunState := agentRun.State
	previousAgentRunError := agentRun.ErrorMessage
	previousAgentRunOutput := agentRun.Output
	previousAgentRunCompletedAt := agentRun.CompletedAt

	restoreAgentRun := func() {
		agentRun.PlanContent = previousAgentRunPlan
		agentRun.State = previousAgentRunState
		agentRun.ErrorMessage = previousAgentRunError
		agentRun.Output = previousAgentRunOutput
		agentRun.CompletedAt = previousAgentRunCompletedAt
	}

	restoreReviewFeedback := func() {
		if reviewFeedback != nil {
			reviewFeedback.PlanCreationStatus = previousStatus
			reviewFeedback.PlanContent = previousPlanContent
			reviewFeedback.PlanAgentRunID = previousPlanAgentRunID
			reviewFeedback.ExecutionAgentRunID = previousExecutionID
		}
	}

	agentRun.PlanContent = nil
	agentRun.State = "failed"
	agentRun.ErrorMessage = &sanitizedReason
	agentRun.Output = buildPlanOutputJSON("plan_rejected", req.AgentType, "", sanitizedLogs, sanitizedReason)

	if err := agentRunRepo.Update(agentRun); err != nil {
		logger.Error("Failed to update plan creation AgentRun after rejection",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
		)
		restoreAgentRun()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to update plan agent run",
		})
		return
	}

	// Post rejection comment only if ReviewFeedback exists (for review-triggered plan creation)
	if reviewFeedback != nil && reviewFeedbackRepo != nil {
		if err := postPlanRejectionComment(ctx, logger, db, reviewFeedback, sanitizedReason); err != nil {
			restoreAgentRun()
			if updateErr := agentRunRepo.Update(agentRun); updateErr != nil {
				logger.Warn("Failed to rollback plan AgentRun state after rejection error",
					zap.Error(updateErr),
					zap.Int("agent_run_id", agentRunID),
				)
			}
			if httpErr, ok := err.(*planRejectionHTTPError); ok {
				c.JSON(httpErr.status, gin.H{
					"error":   httpErr.code,
					"message": httpErr.message,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "GITHUB_COMMENT_ERROR",
				"message": "Failed to post plan rejection comment",
			})
			return
		}

		reviewFeedback.PlanCreationStatus = "rejected"
		reviewFeedback.PlanAgentRunID = &planRunID
		reviewFeedback.ExecutionAgentRunID = nil
		reviewFeedback.PlanContent = nil

		if err := reviewFeedbackRepo.Update(reviewFeedback); err != nil {
			logger.Error("Failed to update ReviewFeedback for plan rejection",
				zap.Error(err),
				zap.Int("review_feedback_id", reviewFeedback.ID),
			)
			restoreReviewFeedback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to update review feedback",
			})
			return
		}
	}

	if reviewFeedback != nil {
		logger.Info("Plan creation rejected",
			zap.Int("plan_agent_run_id", agentRunID),
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("reason_preview", previewString(sanitizedReason, planPreviewLogLimit)),
		)
	} else {
		logger.Info("Plan creation rejected (issue-triggered)",
			zap.Int("plan_agent_run_id", agentRunID),
			zap.String("reason_preview", previewString(sanitizedReason, planPreviewLogLimit)),
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Plan rejected",
	})
}

var planLogSanitizePatterns = []*regexp.Regexp{
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghs_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{48}`),
	regexp.MustCompile(`ANTHROPIC_API_KEY[=:\s]+[A-Za-z0-9-_]+`),
	regexp.MustCompile(`CURSOR_API_KEY[=:\s]+[A-Za-z0-9-_]+`),
}

func sanitizePlanLogs(logs string) string {
	trimmed := strings.TrimSpace(logs)
	if trimmed == "" {
		return ""
	}
	sanitized := utils.SanitizeUTF8(trimmed)
	for _, pattern := range planLogSanitizePatterns {
		sanitized = pattern.ReplaceAllString(sanitized, "***")
	}
	return utils.TruncateWithSuffix(sanitized, utils.GetDBOutputLimitBytes(), "… [truncated]")
}

func previewString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

func buildPlanOutputJSON(status, agentType, planContent, logs, rejectionReason string) datatypes.JSON {
	payload := map[string]any{
		"schema_version": "plan/v1",
		"status":         status,
		"agent_type":     agentType,
	}
	if strings.TrimSpace(planContent) != "" {
		payload["plan_content"] = planContent
	}
	if strings.TrimSpace(logs) != "" {
		payload["logs"] = logs
	}
	if strings.TrimSpace(rejectionReason) != "" {
		payload["rejection_reason"] = rejectionReason
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		config.GetLogger().Warn("Failed to marshal plan output payload", zap.Error(err))
		return nil
	}

	limit := utils.GetDBOutputLimitBytes()
	if len(bytes) <= limit {
		return datatypes.JSON(bytes)
	}

	truncatedPayload := map[string]any{
		"schema_version": "plan/v1",
		"status":         status,
		"agent_type":     agentType,
		"truncated":      true,
	}
	if planContent != "" {
		truncatedPayload["plan_preview"] = previewString(planContent, planPreviewLogLimit)
	}
	if rejectionReason != "" {
		truncatedPayload["rejection_preview"] = previewString(rejectionReason, planPreviewLogLimit)
	}
	if logs != "" {
		truncatedPayload["logs_preview"] = previewString(logs, planPreviewLogLimit)
	}

	if b, err := json.Marshal(truncatedPayload); err == nil {
		return datatypes.JSON(b)
	}

	return datatypes.JSON(bytes[:limit])
}
