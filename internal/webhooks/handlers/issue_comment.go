package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/utils"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/datatypes"
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

// appGitHubClient holds a process-wide GitHub App client (DI from server)
var appGitHubClient *clients.GitHubClient

// SetAppGitHubClient allows the server to inject a shared GitHub App client
func SetAppGitHubClient(c *clients.GitHubClient) {
	appGitHubClient = c
}

// HandleIssueComment handles GitHub issue_comment webhook events
// It detects "/run-agent" trigger and initiates AI agent execution
func HandleIssueComment(c *gin.Context) {
	// Delegate to WithDeps using production dependencies
	logger := config.GetLogger()
	db := config.GetDB()

	// Build production dependencies
	deps := IssueCommentDeps{
		Logger: logger,
	}

	// GitHub App クライアントは DI から取得（なければここで生成）
	if appGitHubClient == nil {
		ghApp, err := clients.NewGitHubAppClient(logger)
		if err != nil {
			logger.Error("Failed to initialize GitHub App client", zap.Error(err))
			c.Error(err)
			return
		}
		appGitHubClient = ghApp
	}

	// Initialize Kubernetes client
	k8sClient, err := clients.NewKubernetesClient(logger)
	if err != nil {
		logger.Error("Failed to initialize Kubernetes client", zap.Error(err))
		c.Error(err)
		return
	}
	deps.KubernetesClient = k8sClient

	// Initialize services
	agentRunRepo := repositories.NewAgentRunRepository(db)
	deps.TriggerService = services.NewTriggerDetectionService(logger)
	deps.AuthorizationService = services.NewAuthorizationService(deps.GitHubClient, logger)
	deps.IssueContextService = services.NewIssueContextService(deps.GitHubClient, logger)
	deps.AgentTypeDetectorService = services.NewAgentTypeDetectorService(logger)
	deps.StateMachine = services.NewAgentRunStateMachine(agentRunRepo, logger)
	deps.GitHubNotificationService = services.NewGitHubNotificationService(deps.GitHubClient, logger)

	HandleIssueCommentWithDeps(c, deps)
}

// IssueCommentDeps represents injectable dependencies for HandleIssueComment
// Narrow interfaces for dependency injection (allow fakes in tests)
type Authorization interface {
	CheckPermission(ctx context.Context, owner, repo, username string) (bool, error)
}

type IssueContext interface {
	CollectIssueContext(ctx context.Context, owner, repo string, issueNumber int) (*services.IssueContext, error)
	FormatPrompt(issueCtx *services.IssueContext) string
}

type GitHubNotification interface {
	PostExecutionStartComment(ctx context.Context, owner, repo string, issueNumber int, agentType string, agentRunID int) error
}

type IssueCommentDeps struct {
	Logger                    *zap.Logger
	GitHubClient              *clients.Client
	KubernetesClient          *clients.KubernetesClient
	TriggerService            *services.TriggerDetectionService
	AuthorizationService      Authorization
	IssueContextService       IssueContext
	AgentTypeDetectorService  *services.AgentTypeDetectorService
	StateMachine              services.AgentRunStateMachine
	GitHubNotificationService GitHubNotification
}

// HandleIssueCommentWithDeps handles issue_comment webhook with injected dependencies (for tests)
func HandleIssueCommentWithDeps(c *gin.Context, deps IssueCommentDeps) {
	// Step 1: Initialization
	logger := deps.Logger
	if logger == nil {
		logger = config.GetLogger()
	}

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
	db := config.GetDB()
	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Use injected services/clients, fallback to production if missing
	triggerService := deps.TriggerService
	if triggerService == nil {
		triggerService = services.NewTriggerDetectionService(logger)
	}
	// GitHub 依存サービスは owner/repo 決定後に設定する
	var authorizationService Authorization
	var issueContextService IssueContext
	agentTypeDetectorService := deps.AgentTypeDetectorService
	if agentTypeDetectorService == nil {
		agentTypeDetectorService = services.NewAgentTypeDetectorService(logger)
	}
	stateMachine := deps.StateMachine
	if stateMachine == nil {
		stateMachine = services.NewAgentRunStateMachine(agentRunRepo, logger)
	}
	var githubNotificationService GitHubNotification

	// Initialize Kubernetes job service
	var jobService services.KubernetesJobService
	if deps.KubernetesClient != nil {
		jobService = services.NewKubernetesJobService(deps.KubernetesClient, logger)
	} else {
		k8sClient, err := clients.NewKubernetesClient(logger)
		if err != nil {
			logger.Error("Failed to initialize Kubernetes client",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
			)
			c.Error(err)
			return
		}
		jobService = services.NewKubernetesJobService(k8sClient, logger)
	}

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

	// リポジトリ単位の認証済みクライアントを生成（テストで注入済みならそれを優先）
	if deps.GitHubClient == nil {
		rawClient, err := appGitHubClient.ForRepo(ctx, owner, repo)
		if err != nil {
			logger.Error("Failed to init per-repo GitHub client",
				zap.Error(err),
				zap.String("owner", owner),
				zap.String("repo", repo),
				zap.String("delivery_id", deliveryID),
			)
			c.Error(err)
			return
		}
		deps.GitHubClient = clients.NewFromGitHub(rawClient, logger)
	}
	authorizationService = services.NewAuthorizationService(deps.GitHubClient, logger)
	issueContextService = services.NewIssueContextService(deps.GitHubClient, logger)
	githubNotificationService = services.NewGitHubNotificationService(deps.GitHubClient, logger)

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
	agentRun, err := repositories.NewAgentRunRepository(db).GetByIDempotencyKey(deliveryID)
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

	// Build structured input JSON (schema v1)
	inputPayload := map[string]any{
		"schema_version": "1",
		"prompt":         prompt,
		"agent_type":     agentType,
		"issue": map[string]any{
			"repo":           payload.Repository.FullName,
			"number":         payload.Issue.Number,
			"has_body":       issueContext.Body != "",
			"labels":         issueContext.Labels,
			"comments_count": len(issueContext.Comments),
		},
	}
	inputBytes, marshalErr := json.Marshal(inputPayload)
	if marshalErr != nil {
		logger.Warn("Failed to marshal structured input payload", zap.Error(marshalErr))
		// Fallback to minimal JSON with prompt only
		inputBytes, _ = json.Marshal(map[string]any{
			"schema_version": "1",
			"prompt":         prompt,
			"agent_type":     agentType,
		})
	}

	// Assign JSON to AgentRun.Input
	// Use datatypes.JSON to match MySQL JSON column type
	// Note: import added if not present
	agentRun.Input = datatypes.JSON(inputBytes)
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
