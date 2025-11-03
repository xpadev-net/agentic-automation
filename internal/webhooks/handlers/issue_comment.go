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
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// IssueCommentPayload represents the GitHub webhook payload for issue_comment events
type IssueCommentPayload struct {
	Action     string                 `json:"action"`
	Issue      IssueCommentIssue      `json:"issue"`
	Comment    IssueCommentComment    `json:"comment"`
	Repository IssueCommentRepository `json:"repository"`
}

// IssueCommentIssue represents the Issue object in issue_comment webhook payload
type IssueCommentIssue struct {
	ID     int     `json:"id"`     // GitHub issue ID
	Number int     `json:"number"` // Issue number
	Title  string  `json:"title"`
	Body   *string `json:"body"`
	State  string  `json:"state"` // "open" or "closed"
	Labels []Label `json:"labels"`
	User   User    `json:"user"`
}

// IssueCommentComment represents the Comment object in issue_comment webhook payload
type IssueCommentComment struct {
	ID        int    `json:"id"`
	Body      string `json:"body"`
	User      User   `json:"user"`
	CreatedAt string `json:"created_at"`
}

// IssueCommentRepository represents the Repository object in issue_comment webhook payload
type IssueCommentRepository struct {
	FullName string `json:"full_name"` // "owner/repo"
	Owner    User   `json:"owner"`
	Name     string `json:"name"`
}

// Label represents a GitHub label
type Label struct {
	Name string `json:"name"`
}

// User represents a GitHub user
type User struct {
	Login string `json:"login"`
}

const deliveryHeader = "X-GitHub-Delivery"

// HandleIssueComment handles GitHub issue_comment webhook events
// It detects "/run-agent" trigger and initiates AI agent execution
func HandleIssueComment(c *gin.Context) {
	// Step 1: Initialization
	logger := config.GetLogger()
	db := config.GetDB()

	// Get delivery ID from header (idempotency key)
	deliveryID := c.GetHeader(deliveryHeader)
	if deliveryID == "" {
		logger.Warn("Missing X-GitHub-Delivery header",
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

	// Create context
	ctx := c.Request.Context()

	// Step 2: Payload parsing
	payloadData, exists := c.Get("webhook_payload")
	if !exists {
		logger.Error("Webhook payload not found in context",
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("webhook payload not found in context"))
		return
	}

	payloadBytes, ok := payloadData.([]byte)
	if !ok {
		logger.Error("Invalid webhook payload type",
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("invalid webhook payload type"))
		return
	}

	var payload IssueCommentPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(err)
		return
	}

	// Only process "created" actions (ignore edited/deleted)
	if payload.Action != models.IssueCommentActionCreated {
		logger.Info("Ignoring non-created action",
			zap.String("action", payload.Action),
			zap.String("delivery_id", deliveryID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "ignored",
			"action":      payload.Action,
			"delivery_id": deliveryID,
		})
		return
	}

	// Step 3: Issue state validation
	if payload.Issue.State != "open" {
		logger.Warn("Cannot trigger agent on closed issue",
			zap.String("delivery_id", deliveryID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
			zap.String("issue_state", payload.Issue.State),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "rejected",
			"reason":      "issue_not_open",
			"issue_state": payload.Issue.State,
			"delivery_id": deliveryID,
		})
		return
	}

	// Step 4: Repository/Service initialization
	issueRepo := repositories.NewIssueRepository()
	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Initialize GitHub client
	githubToken, err := config.GetEnvRequired("GITHUB_TOKEN")
	if err != nil {
		logger.Error("Failed to get GitHub token",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}
	githubClient, err := clients.NewClient(githubToken, logger)
	if err != nil {
		logger.Error("Failed to initialize GitHub client",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Initialize services
	triggerService := services.NewTriggerDetectionService(logger)
	authorizationService := services.NewAuthorizationService(githubClient, logger)
	issueContextService := services.NewIssueContextService(githubClient, logger)
	agentTypeDetectorService := services.NewAgentTypeDetectorService(logger)
	stateMachine := services.NewAgentRunStateMachine(agentRunRepo, logger)
	githubNotificationService := services.NewGitHubNotificationService(githubClient, logger)

	// Initialize Kubernetes client
	kubernetesClient, err := clients.NewKubernetesClient(logger)
	if err != nil {
		logger.Error("Failed to initialize Kubernetes client",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}
	jobService := services.NewKubernetesJobService(kubernetesClient, logger)

	// Step 5: Trigger detection
	logger.Info("Checking for trigger in comment",
		zap.String("delivery_id", deliveryID),
		zap.Int("issue_number", payload.Issue.Number),
		zap.String("repo", payload.Repository.FullName),
		zap.String("comment_user", payload.Comment.User.Login),
	)

	triggerDetected := triggerService.DetectRunAgentTrigger(payload.Comment.Body)
	if !triggerDetected {
		// Create comment body preview (first 100 characters for security)
		commentBodyPreview := payload.Comment.Body
		if len(commentBodyPreview) > 100 {
			commentBodyPreview = commentBodyPreview[:100]
		}

		logger.Info("No trigger detected in comment",
			zap.String("delivery_id", deliveryID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
			zap.Int("comment_id", payload.Comment.ID),
			zap.String("comment_created_at", payload.Comment.CreatedAt),
			zap.String("comment_body_preview", commentBodyPreview),
			zap.String("comment_user", payload.Comment.User.Login),
			zap.String("trigger_string", utils.RunAgentTrigger),
			zap.String("detection_reason", "trigger string '/run-agent' not found in comment body"),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_trigger",
			"delivery_id": deliveryID,
		})
		return
	}

	logger.Info("Trigger detected in comment",
		zap.Bool("trigger_detected", true),
		zap.String("delivery_id", deliveryID),
		zap.Int("issue_number", payload.Issue.Number),
		zap.String("repo", payload.Repository.FullName),
	)

	// Step 6: Permission check
	repoParts := strings.Split(payload.Repository.FullName, "/")
	if len(repoParts) != 2 {
		logger.Error("Invalid repository full name format",
			zap.String("full_name", payload.Repository.FullName),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(errors.New("invalid repository full name format"))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	hasPermission, err := authorizationService.CheckPermission(ctx, owner, repo, payload.Comment.User.Login)
	if err != nil {
		// Extract error details for enhanced logging
		var errorType string
		var httpStatusCode int
		var ghErr *clients.GitHubError
		if errors.As(err, &ghErr) {
			errorType = "GitHubError"
			if ghErr.ErrorResponse != nil && ghErr.ErrorResponse.Response != nil {
				httpStatusCode = ghErr.ErrorResponse.Response.StatusCode
			}
		} else {
			// Determine error type based on error message
			errMsg := err.Error()
			if strings.Contains(errMsg, "rate limit") {
				errorType = "rate_limit_error"
			} else if strings.Contains(errMsg, "network") || strings.Contains(errMsg, "timeout") {
				errorType = "network_error"
			} else {
				errorType = "unknown_error"
			}
		}

		logFields := []zap.Field{
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.String("user", payload.Comment.User.Login),
			zap.String("repo", payload.Repository.FullName),
			zap.String("api_method", "GetPermissionLevel"),
			zap.String("error_type", errorType),
		}
		if httpStatusCode > 0 {
			logFields = append(logFields, zap.Int("http_status_code", httpStatusCode))
		}

		logger.Error("Failed to check user permission",
			logFields...,
		)
		c.Error(err)
		return
	}

	if !hasPermission {
		logger.Warn("User lacks permission to trigger agent",
			zap.String("delivery_id", deliveryID),
			zap.String("user", payload.Comment.User.Login),
			zap.String("repo", payload.Repository.FullName),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("authorization_required_level", "write/maintain/admin"),
			zap.String("authorization_policy", "FR-018: Collaborator+ permission required"),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "permission_denied",
			"delivery_id": deliveryID,
		})
		return
	}

	logger.Info("User permission verified",
		zap.Bool("authorized", true),
		zap.String("user", payload.Comment.User.Login),
		zap.String("repo", payload.Repository.FullName),
		zap.String("delivery_id", deliveryID),
	)

	// Step 7: Get AgentRun (created by idempotency middleware)
	agentRun, err := agentRunRepo.GetByIDempotencyKey(deliveryID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("AgentRun not found for delivery ID (should be created by middleware)",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("issue_number", payload.Issue.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.Error(errors.New("agent run not found for delivery ID"))
			return
		}
		logger.Error("Failed to get AgentRun by idempotency key",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	logger.Info("AgentRun retrieved",
		zap.Int("agent_run_id", agentRun.ID),
		zap.String("state", agentRun.State),
		zap.String("delivery_id", deliveryID),
	)

	// Check if AgentRun is already processed
	if agentRun.State != "queued" {
		logger.Info("AgentRun already processed",
			zap.Int("agent_run_id", agentRun.ID),
			zap.String("state", agentRun.State),
			zap.String("delivery_id", deliveryID),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":       "already_processed",
			"agent_run_id": agentRun.ID,
			"state":        agentRun.State,
			"delivery_id":  deliveryID,
		})
		return
	}

	// Step 8: Get Issue (created by idempotency middleware)
	issue, err := issueRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.Issue.Number)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Issue not found (should be created by middleware)",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("issue_number", payload.Issue.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.Error(errors.New("issue not found"))
			return
		}
		logger.Error("Failed to get Issue",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}

	logger.Info("Issue retrieved",
		zap.Int("issue_id", issue.ID),
		zap.Int("issue_number", issue.Number),
		zap.String("repo", issue.Repo),
		zap.String("delivery_id", deliveryID),
	)

	// Step 9: Collect Issue context
	issueContext, err := issueContextService.CollectIssueContext(ctx, owner, repo, payload.Issue.Number)
	if err != nil {
		logger.Error("Failed to collect Issue context",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}

	logger.Info("Issue context collected",
		zap.Int("comments_count", len(issueContext.Comments)),
		zap.Int("labels_count", len(issueContext.Labels)),
		zap.Bool("has_body", issueContext.Body != ""),
		zap.String("delivery_id", deliveryID),
	)

	// Step 10: Format prompt
	prompt := issueContextService.FormatPrompt(issueContext)

	// Step 10.5: Set labels from issueContext for agent type detection
	if len(issueContext.Labels) > 0 {
		labelsJSON, err := json.Marshal(issueContext.Labels)
		if err != nil {
			logger.Warn("Failed to marshal issue labels",
				zap.Error(err),
				zap.Int("issue_id", issue.ID),
				zap.String("delivery_id", deliveryID),
			)
		} else {
			issue.Labels = string(labelsJSON)
			if err := issueRepo.Update(issue); err != nil {
				logger.Warn("Failed to update issue labels",
					zap.Error(err),
					zap.Int("issue_id", issue.ID),
					zap.String("delivery_id", deliveryID),
				)
			} else {
				logger.Info("Issue labels updated from GitHub",
					zap.Int("labels_count", len(issueContext.Labels)),
					zap.Int("issue_id", issue.ID),
					zap.String("delivery_id", deliveryID),
				)
			}
		}
	}

	// Step 11: Detect agent type
	agentType := agentTypeDetectorService.DetectAgentType(issue)

	logger.Info("Agent type detected",
		zap.String("agent_type", agentType),
		zap.Int("issue_id", issue.ID),
		zap.String("delivery_id", deliveryID),
	)

	// Step 12: Update AgentRun
	agentRun.AgentType = agentType
	agentRun.Input = prompt
	if err := agentRunRepo.Update(agentRun); err != nil {
		logger.Error("Failed to update AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRun.ID),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Step 13: State transition (queued -> started)
	if err := stateMachine.TransitionToStarted(agentRun.ID); err != nil {
		logger.Error("Failed to transition AgentRun to started state",
			zap.Error(err),
			zap.Int("agent_run_id", agentRun.ID),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	logger.Info("AgentRun state transitioned to started",
		zap.Int("agent_run_id", agentRun.ID),
		zap.String("delivery_id", deliveryID),
	)

	// Step 14: Create Kubernetes Job
	job, err := jobService.CreateJobForAgentRun(ctx, agentRun, issue, prompt)
	if err != nil {
		logger.Error("Failed to create Kubernetes Job, rolling back state",
			zap.Error(err),
			zap.Int("agent_run_id", agentRun.ID),
			zap.String("delivery_id", deliveryID),
		)
		// Rollback state to queued for retry
		if rollbackErr := stateMachine.TransitionToQueued(agentRun.ID); rollbackErr != nil {
			logger.Error("Failed to rollback AgentRun state",
				zap.Error(rollbackErr),
				zap.Int("agent_run_id", agentRun.ID),
				zap.String("delivery_id", deliveryID),
			)
		} else {
			logger.Info("AgentRun state rolled back to queued for retry",
				zap.Int("agent_run_id", agentRun.ID),
				zap.String("delivery_id", deliveryID),
			)
		}
		c.Error(err)
		return
	}

	logger.Info("Kubernetes Job created successfully",
		zap.Int("agent_run_id", agentRun.ID),
		zap.String("job_name", job.Name),
		zap.String("job_uid", string(job.UID)),
		zap.String("namespace", job.Namespace),
		zap.String("delivery_id", deliveryID),
	)

	// Post GitHub status comment
	if err := githubNotificationService.PostExecutionStartComment(
		ctx,
		owner,
		repo,
		payload.Issue.Number,
		agentRun.AgentType,
		agentRun.ID,
	); err != nil {
		// Non-blocking: log error but don't fail the webhook processing
		logger.Warn("Failed to post GitHub execution start comment",
			zap.Error(err),
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
			zap.String("delivery_id", deliveryID),
		)
	} else {
		logger.Info("GitHub execution start comment posted successfully",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("issue_number", payload.Issue.Number),
			zap.String("repo", payload.Repository.FullName),
			zap.String("delivery_id", deliveryID),
		)
	}

	// Step 15: Return success response
	c.JSON(http.StatusOK, gin.H{
		"status":       "processed",
		"agent_run_id": agentRun.ID,
		"job_name":     job.Name,
		"delivery_id":  deliveryID,
	})
}
