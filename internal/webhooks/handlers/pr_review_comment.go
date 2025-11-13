package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/errors"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/utils"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// PullRequestReviewCommentPayload represents the GitHub webhook payload for pull_request_review_comment events
type PullRequestReviewCommentPayload struct {
	Action      string                              `json:"action"`
	Comment     PullRequestReviewCommentComment     `json:"comment"`
	PullRequest PullRequestReviewCommentPullRequest `json:"pull_request"`
	Repository  PullRequestReviewCommentRepository  `json:"repository"`
}

// PullRequestReviewCommentComment represents the Comment object in pull_request_review_comment webhook payload
type PullRequestReviewCommentComment struct {
	ID        int    `json:"id"`
	Body      string `json:"body"`
	User      User   `json:"user"`
	CreatedAt string `json:"created_at"`
}

// PullRequestReviewCommentPullRequest represents the PullRequest object in pull_request_review_comment webhook payload
type PullRequestReviewCommentPullRequest struct {
	Number int `json:"number"`
}

// PullRequestReviewCommentRepository represents the Repository object in pull_request_review_comment webhook payload
type PullRequestReviewCommentRepository struct {
	FullName string `json:"full_name"` // "owner/repo"
}

// CodexReviewService interface for requesting Codex reviews
// This interface matches the implementation in services.CodexReviewService
type CodexReviewService interface {
	RequestReview(ctx context.Context, owner, repo string, prNumber, prID int, idempotencyKey string) (*models.ReviewFeedback, error)
}

// PullRequestReviewCommentDeps represents injectable dependencies for HandlePullRequestReviewComment
type PullRequestReviewCommentDeps struct {
	Logger                   *zap.Logger
	GitHubClient             *clients.Client
	AuthorizationService     Authorization // Reuse from issue_comment.go
	CodexReviewService       CodexReviewService
	PullRequestRepository    *repositories.PullRequestRepository
	ReviewFeedbackRepository *repositories.ReviewFeedbackRepository
	KubernetesJobService     services.KubernetesJobService
	// Optional DI for US4 merge re-evaluation
	MergeConditionChecker services.MergeConditionChecker
	AutoMergeService      services.AutoMergeService
}

// HandlePullRequestReviewComment handles GitHub pull_request_review_comment webhook events
// It detects "@codex review" trigger and initiates Codex review request
func HandlePullRequestReviewComment(c *gin.Context) {
	// Delegate to WithDeps using production dependencies
	logger := config.GetLogger()

	// Build production dependencies
	deps := PullRequestReviewCommentDeps{
		Logger: logger,
	}

	// GitHub App クライアントは DI から取得（なければここで生成）
	if appGitHubClient == nil {
		ghApp, err := clients.NewGitHubAppClient(logger)
		if err != nil {
			// テスト環境などで資格情報が無い場合でもここではエラーにせず後段で処理
			logger.Warn("GitHub App client not initialized", zap.Error(err))
		} else {
			appGitHubClient = ghApp
		}
	}

	HandlePullRequestReviewCommentWithDeps(c, deps)
}

// HandlePullRequestReviewCommentWithDeps handles pull_request_review_comment webhook with injected dependencies (for tests)
func HandlePullRequestReviewCommentWithDeps(c *gin.Context, deps PullRequestReviewCommentDeps) {
	// Step 1: Initialization
	logger := deps.Logger
	if logger == nil {
		logger = config.GetLogger()
	}

	// Create context
	ctx := c.Request.Context()

	// Step 2: Delivery ID 取得
	deliveryID := c.GetHeader(deliveryHeader)
	if deliveryID == "" {
		logger.Warn("Missing X-GitHub-Delivery header",
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "missing X-GitHub-Delivery header", nil))
		return
	}

	// Step 3: Payload parsing
	payloadData, exists := c.Get("webhook_payload")
	if !exists {
		logger.Error("Webhook payload not found in context",
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_PAYLOAD_NOT_FOUND, "webhook payload not found in context", nil))
		return
	}

	payloadBytes, ok := payloadData.([]byte)
	if !ok {
		logger.Error("Invalid webhook payload type",
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_INVALID_PAYLOAD, "invalid webhook payload type", nil))
		return
	}

	var payload PullRequestReviewCommentPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(err)
		return
	}

	// Step 4: Action 検証
	if payload.Action != models.PullRequestReviewCommentActionCreated {
		logger.Info("Ignoring non-created action",
			zap.String("action", payload.Action),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "ignored",
			"action":      payload.Action,
			"delivery_id": deliveryID,
		})
		return
	}

	// Step 5: トリガー検出
	logger.Info("Checking for trigger in comment",
		zap.String("delivery_id", deliveryID),
		zap.Int("pr_number", payload.PullRequest.Number),
		zap.String("repo", payload.Repository.FullName),
		zap.String("comment_user", payload.Comment.User.Login),
	)

	commentBodyPreview := payload.Comment.Body
	if len(commentBodyPreview) > 100 {
		commentBodyPreview = commentBodyPreview[:100]
	}

	triggerDetected := utils.ContainsCodexReviewTrigger(payload.Comment.Body)
	if !triggerDetected {
		logger.Info("No trigger detected in comment",
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
			zap.Int("comment_id", payload.Comment.ID),
			zap.String("comment_created_at", payload.Comment.CreatedAt),
			zap.String("comment_body_preview", commentBodyPreview),
			zap.String("comment_user", payload.Comment.User.Login),
			zap.String("trigger_string", utils.CodexReviewTrigger),
			zap.String("detection_reason", "trigger string '@codex review' not found in comment body"),
		)
	} else {
		logger.Info("Trigger detected in comment",
			zap.Bool("trigger_detected", true),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)
	}

	// Step 6: リポジトリ情報抽出
	repoParts := strings.Split(payload.Repository.FullName, "/")
	if len(repoParts) != 2 {
		logger.Error("Invalid repository full name format",
			zap.String("full_name", payload.Repository.FullName),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_INVALID_REPO_FORMAT, "invalid repository full name format", nil))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// Step 7: PullRequest 取得（プラン作成判定に利用）
	prRepo := deps.PullRequestRepository
	if prRepo == nil {
		prRepo = repositories.NewPullRequestRepository(config.GetDB())
	}

	pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			// PRレコードが見つからない場合（無関係なリポジトリや古いPR）は、
			// エラーを返さずにプラン作成をスキップして成功を返す
			// これにより、GitHubが通常のコメントでもwebhookをリトライしないようにする
			logger.Info("PullRequest not found, skipping plan creation",
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", payload.PullRequest.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "no_trigger",
				"reason":      "pr_not_found",
				"delivery_id": deliveryID,
			})
			return
		}
		logger.Error("Failed to get PullRequest",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}

	logger.Info("PullRequest retrieved",
		zap.Int("pr_id", pr.ID),
		zap.Int("pr_number", pr.Number),
		zap.String("repo", pr.Repo),
		zap.String("delivery_id", deliveryID),
	)

	// plan作成処理は削除: pull_request_review_commentイベントではplan作成を実行しない
	// plan作成はpull_request_reviewイベントでのみ実行される
	logger.Info("Plan creation skipped for pull_request_review_comment event",
		zap.String("delivery_id", deliveryID),
		zap.Int("pr_number", payload.PullRequest.Number),
		zap.String("repo", payload.Repository.FullName),
		zap.Bool("trigger_detected", triggerDetected),
		zap.String("note", "plan creation is handled by pull_request_review event only"),
	)

	if !triggerDetected {
		// トリガーが検出されない場合は、plan作成なしで早期リターン
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_trigger",
			"delivery_id": deliveryID,
			"note":        "plan creation is handled by pull_request_review event only",
		})
		return
	}

	// Step 7: 権限チェック（サービス初期化含む）
	// GitHub クライアント初期化（deps が nil の場合）
	githubClient := deps.GitHubClient
	if githubClient == nil {
		if appGitHubClient == nil {
			logger.Error("GitHub App client not available",
				zap.String("delivery_id", deliveryID),
			)
			c.Error(errors.NewCodedError(errors.ERR_INTERNAL_SERVER_ERROR, "github client not provided", nil))
			return
		}
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
		githubClient = clients.NewFromGitHub(rawClient, logger)
	}

	// AuthorizationService 初期化（deps が nil の場合）
	authorizationService := deps.AuthorizationService
	if authorizationService == nil {
		authorizationService = services.NewAuthorizationService(githubClient, logger)
	}

	// CodexReviewService 初期化（deps が nil の場合）
	codexReviewService := deps.CodexReviewService
	if codexReviewService == nil {
		reviewFeedbackRepo := deps.ReviewFeedbackRepository
		if reviewFeedbackRepo == nil {
			reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
		}
		codexReviewService = services.NewCodexReviewService(githubClient, reviewFeedbackRepo, logger)
	}

	hasPermission, err := authorizationService.CheckPermission(ctx, owner, repo, payload.Comment.User.Login)
	if err != nil {
		// Extract error details for enhanced logging
		var errorType string
		var httpStatusCode int
		var ghErr *clients.GitHubError
		if stderrors.As(err, &ghErr) {
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
		logger.Warn("User lacks permission to trigger Codex review",
			zap.String("delivery_id", deliveryID),
			zap.String("user", payload.Comment.User.Login),
			zap.String("repo", payload.Repository.FullName),
			zap.Int("pr_number", payload.PullRequest.Number),
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

	// Step 9: CodexReviewService 呼び出し
	// codexReviewService は Step 7 で初期化済み（実装が存在する場合）
	if codexReviewService == nil {
		// T091 で実装される予定のサービスが未実装の場合
		// 警告を出して処理を続行（レビューリクエストとReviewFeedback作成をスキップ）
		logger.Warn("CodexReviewService not available (T091 not implemented yet), skipping review request",
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		// 処理を成功として返す（レビューリクエストとReviewFeedback作成をスキップ）
		c.JSON(http.StatusOK, gin.H{
			"status":      "processed_skipped",
			"reason":      "codex_review_service_not_implemented",
			"delivery_id": deliveryID,
			"pr_number":   payload.PullRequest.Number,
		})
		return
	}

	// Generate idempotency key for this review request
	idempotencyKey := fmt.Sprintf("codex_review_request:pr_review_comment:%d:%d", pr.ID, payload.Comment.ID)

	_, err = codexReviewService.RequestReview(ctx, owner, repo, payload.PullRequest.Number, pr.ID, idempotencyKey)
	if err != nil {
		logger.Error("Failed to request Codex review",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}

	logger.Info("Codex review requested successfully",
		zap.String("delivery_id", deliveryID),
		zap.Int("pr_number", payload.PullRequest.Number),
		zap.String("repo", payload.Repository.FullName),
	)

	// Step 11: 成功レスポンス返却
	c.JSON(http.StatusOK, gin.H{
		"status":      "processed",
		"delivery_id": deliveryID,
		"pr_number":   payload.PullRequest.Number,
		"note":        "plan creation is handled by pull_request_review event only",
	})

	// TODO (T096/T098): Add retry progress notification when review feedback triggers retry
	// When implementing retry orchestrator for review feedback (T096/T098), add NotifyRetryProgress call here:
	//
	// 1. After review feedback is detected as non-approval and retry is triggered
	// 2. Get AgentRun and check retry_count < services.MaxRetryAttempts
	// 3. Generate review comment summary (use FeedbackAggregator result, reviewComments)
	// 4. Get Issue and PR information
	// 5. Call NotifyRetryProgress with:
	//    - owner, repo: from payload.Repository.FullName
	//    - issueNumber: from PR's linked Issue
	//    - prNumber: payload.PullRequest.Number
	//    - retryCount: agentRun.RetryCount
	//    - maxRetries: services.MaxRetryAttempts
	//    - errorReason: review comment summary (truncated to 100 chars, default "Review feedback received" if empty)
	//    - idempotencyKey: agentRun.IdempotencyKey
	//
	// See internal/webhooks/handlers/agent_report.go for reference implementation.
}

type planCreationResult struct {
	Status            string
	ReviewFeedbackID  int
	PlanAgentRunID    int
	PlanCreationState string
}

func (r *planCreationResult) hasStarted() bool {
	return r != nil && r.Status == "started"
}

func startPlanCreationIfNeeded(
	ctx context.Context,
	deps PullRequestReviewCommentDeps,
	logger *zap.Logger,
	pr *models.PullRequest,
	commentBody string,
	hasReviewCommentContext bool,
	commentID int64,
	commentUserLogin string,
	commentUserID int64,
	deliveryID string,
) (*planCreationResult, error) {
	commentBody = strings.TrimSpace(commentBody)
	if commentBody == "" && !hasReviewCommentContext {
		logger.Info("Skipping plan creation: empty review comment with no additional context",
			zap.Int("pr_id", pr.ID),
			zap.String("delivery_id", deliveryID),
		)
		return &planCreationResult{Status: "skipped_empty_comment"}, nil
	}

	minCommentLength := config.GetEnvInt("PLAN_CREATION_MIN_COMMENT_LENGTH", 20)
	if minCommentLength < 0 {
		minCommentLength = 0
	}
	if utf8.RuneCountInString(commentBody) < minCommentLength {
		if hasReviewCommentContext {
			logger.Info("Proceeding with plan creation despite short comment due to review comment context",
				zap.Int("pr_id", pr.ID),
				zap.Int("comment_length", utf8.RuneCountInString(commentBody)),
				zap.Int("min_length", minCommentLength),
				zap.String("delivery_id", deliveryID),
			)
		} else {
			logger.Info("Skipping plan creation: comment shorter than minimum threshold",
				zap.Int("pr_id", pr.ID),
				zap.Int("comment_length", utf8.RuneCountInString(commentBody)),
				zap.Int("min_length", minCommentLength),
				zap.String("delivery_id", deliveryID),
			)
			return &planCreationResult{Status: "skipped_short_comment"}, nil
		}
	}

	reviewFeedbackRepo := deps.ReviewFeedbackRepository
	if reviewFeedbackRepo == nil {
		reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
	}

	var reviewFeedback *models.ReviewFeedback
	commentIDPtr := &commentID

	// First, check if a ReviewFeedback record already exists for this GitHub comment ID.
	// This prevents duplicate processing when the same comment is reprocessed (e.g., webhook retries).
	existingByCommentID, err := reviewFeedbackRepo.FindByGitHubCommentID(commentID)
	if err != nil {
		logger.Error("Failed to load review feedback by GitHub comment ID",
			zap.Error(err),
			zap.Int64("github_comment_id", commentID),
			zap.Int("pr_id", pr.ID),
			zap.String("delivery_id", deliveryID),
		)
		return nil, err
	}

	if existingByCommentID != nil {
		// Use the existing record to prevent duplicate plan creation
		reviewFeedback = existingByCommentID
		logger.Info("Found existing review feedback for GitHub comment ID",
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.Int64("github_comment_id", commentID),
			zap.String("status", reviewFeedback.Status),
			zap.String("plan_creation_status", reviewFeedback.PlanCreationStatus),
			zap.String("delivery_id", deliveryID),
		)

		// If the existing record is in "requested" status and this is a Codex comment,
		// update it to "received" to match the original behavior
		if reviewFeedback.Status == "requested" {
			codexDetector := services.NewCodexApprovalDetector(logger)
			isCodexComment := codexDetector.IsCodexBot(commentUserLogin, commentUserID)
			if isCodexComment {
				if updateErr := reviewFeedbackRepo.UpdateToReceived(reviewFeedback.ID, commentBody, false, commentIDPtr); updateErr != nil {
					logger.Error("Failed to update existing requested review feedback to received",
						zap.Error(updateErr),
						zap.Int("review_feedback_id", reviewFeedback.ID),
						zap.String("delivery_id", deliveryID),
					)
					return nil, updateErr
				}
				reviewFeedback, err = reviewFeedbackRepo.FindByID(reviewFeedback.ID)
				if err != nil {
					logger.Error("Failed to reload review feedback after update",
						zap.Error(err),
						zap.Int("review_feedback_id", reviewFeedback.ID),
						zap.String("delivery_id", deliveryID),
					)
					return nil, err
				}
				logger.Info("Updated existing requested review feedback to received (Codex comment)",
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.Int64("github_comment_id", commentID),
					zap.String("delivery_id", deliveryID),
				)
			}
		}
	} else {
		// No existing record found by comment ID, proceed with the original logic
		// Check if the comment is from Codex bot to determine if we should update existing requested records
		codexDetector := services.NewCodexApprovalDetector(logger)
		isCodexComment := codexDetector.IsCodexBot(commentUserLogin, commentUserID)

		requestedList, err := reviewFeedbackRepo.FindByPRIDAndStatus(pr.ID, "requested")
		if err != nil {
			logger.Error("Failed to load requested review feedback records",
				zap.Error(err),
				zap.Int("pr_id", pr.ID),
				zap.String("delivery_id", deliveryID),
			)
			return nil, err
		}

		if len(requestedList) > 0 {
			latest := requestedList[0]
			// Only update requested records if:
			// 1. The comment is from Codex bot, AND
			// 2. The requested record's GitHubCommentID matches the current comment ID
			// This prevents human comments from overwriting Codex review requests
			shouldUpdate := isCodexComment && latest.GitHubCommentID != nil && *latest.GitHubCommentID == commentID

			if shouldUpdate {
				if updateErr := reviewFeedbackRepo.UpdateToReceived(latest.ID, commentBody, false, commentIDPtr); updateErr != nil {
					logger.Error("Failed to update review feedback to received",
						zap.Error(updateErr),
						zap.Int("review_feedback_id", latest.ID),
						zap.String("delivery_id", deliveryID),
					)
					return nil, updateErr
				}
				reviewFeedback, err = reviewFeedbackRepo.FindByID(latest.ID)
				if err != nil {
					logger.Error("Failed to reload review feedback after update",
						zap.Error(err),
						zap.Int("review_feedback_id", latest.ID),
						zap.String("delivery_id", deliveryID),
					)
					return nil, err
				}
				logger.Info("Updated existing requested review feedback to received",
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.Int64("github_comment_id", commentID),
					zap.Bool("is_codex_comment", isCodexComment),
					zap.String("delivery_id", deliveryID),
				)
			} else {
				// Human comment or comment ID mismatch: create a new received record
				// This preserves the existing requested record for Codex to respond to later
				reviewFeedback, err = reviewFeedbackRepo.CreateReceivedReview(pr.ID, commentBody, false, commentIDPtr)
				if err != nil {
					logger.Error("Failed to create received review feedback",
						zap.Error(err),
						zap.Int("pr_id", pr.ID),
						zap.String("delivery_id", deliveryID),
					)
					return nil, err
				}
				logger.Info("Created new received review feedback (preserving existing requested record)",
					zap.Int("review_feedback_id", reviewFeedback.ID),
					zap.Int64("github_comment_id", commentID),
					zap.Bool("is_codex_comment", isCodexComment),
					zap.Int("existing_requested_count", len(requestedList)),
					zap.String("delivery_id", deliveryID),
				)
			}
		} else {
			reviewFeedback, err = reviewFeedbackRepo.CreateReceivedReview(pr.ID, commentBody, false, commentIDPtr)
			if err != nil {
				logger.Error("Failed to create received review feedback",
					zap.Error(err),
					zap.Int("pr_id", pr.ID),
					zap.String("delivery_id", deliveryID),
				)
				return nil, err
			}
		}
	}

	if reviewFeedback == nil {
		logger.Warn("Review feedback record unavailable after processing",
			zap.Int("pr_id", pr.ID),
			zap.String("delivery_id", deliveryID),
		)
		return &planCreationResult{Status: "skipped_feedback_missing"}, nil
	}

	planState := strings.TrimSpace(reviewFeedback.PlanCreationStatus)
	if planState == "creating" || planState == "created" || planState == "executed" {
		logger.Info("Plan creation already in progress or completed",
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("plan_creation_status", planState),
			zap.String("delivery_id", deliveryID),
		)
		result := &planCreationResult{
			Status:            "skipped_plan_already_started",
			ReviewFeedbackID:  reviewFeedback.ID,
			PlanCreationState: planState,
		}
		if reviewFeedback.PlanAgentRunID != nil {
			result.PlanAgentRunID = *reviewFeedback.PlanAgentRunID
		}
		return result, nil
	}

	if pr.IssueID == nil {
		logger.Warn("Skipping plan creation: PR not linked to issue",
			zap.Int("pr_id", pr.ID),
			zap.String("delivery_id", deliveryID),
		)
		return &planCreationResult{
			Status:           "skipped_missing_issue",
			ReviewFeedbackID: reviewFeedback.ID,
		}, nil
	}

	issueRepo := repositories.NewIssueRepository()
	issue, err := issueRepo.FindByID(*pr.IssueID)
	if err != nil {
		logger.Error("Failed to load issue for plan creation",
			zap.Error(err),
			zap.Int("issue_id", *pr.IssueID),
			zap.String("delivery_id", deliveryID),
		)
		return nil, err
	}
	if issue == nil {
		logger.Warn("Skipping plan creation: linked issue not found",
			zap.Int("issue_id", *pr.IssueID),
			zap.String("delivery_id", deliveryID),
		)
		return &planCreationResult{
			Status:           "skipped_issue_not_found",
			ReviewFeedbackID: reviewFeedback.ID,
		}, nil
	}

	agentTypeDetector := services.NewAgentTypeDetectorService(logger)
	agentType := agentTypeDetector.DetectAgentType(issue)

	agentRunRepo := repositories.NewAgentRunRepository(config.GetDB())
	planRun := &models.AgentRun{
		IssueID:          issue.ID,
		PRID:             &pr.ID,
		State:            "queued",
		AgentType:        agentType,
		ExecutionMode:    "plan_creation",
		ReviewFeedbackID: &reviewFeedback.ID,
		Input:            datatypes.JSON([]byte("{}")),
		Output:           datatypes.JSON([]byte("{}")),
	}

	// Detect plan recreation: if PlanAgentRunID is already set, this is a recreation
	// Use a unique idempotency key with timestamp to create a new AgentRun with a new job name
	var idempotencyKey string
	if reviewFeedback.PlanAgentRunID != nil {
		// Plan recreation: use unique idempotency key with timestamp
		idempotencyKey = fmt.Sprintf("plan_creation:review_feedback:%d:recreation:%d", reviewFeedback.ID, time.Now().UnixNano())
		logger.Info("Detected plan recreation, using unique idempotency key",
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.Int("previous_agent_run_id", *reviewFeedback.PlanAgentRunID),
			zap.String("delivery_id", deliveryID),
		)
	} else {
		// New plan creation: use standard idempotency key
		idempotencyKey = fmt.Sprintf("plan_creation:review_feedback:%d", reviewFeedback.ID)
	}
	createdRun, isNew, err := agentRunRepo.CreateOrGet(idempotencyKey, planRun)
	if err != nil {
		logger.Error("Failed to create or get plan creation agent run",
			zap.Error(err),
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("delivery_id", deliveryID),
		)
		return nil, err
	}

	planAgentRun := createdRun
	if !isNew {
		logger.Info("Reusing existing plan creation agent run",
			zap.Int("agent_run_id", createdRun.ID),
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("delivery_id", deliveryID),
		)
		planAgentRun.AgentType = agentType
		planAgentRun.ExecutionMode = "plan_creation"
		planAgentRun.ReviewFeedbackID = &reviewFeedback.ID
		if err := agentRunRepo.Update(planAgentRun); err != nil {
			logger.Warn("Failed to update existing plan creation agent run",
				zap.Error(err),
				zap.Int("agent_run_id", planAgentRun.ID),
			)
		}
	}

	// Atomically update PlanCreationStatus from 'pending' to 'creating' for this PR
	// This prevents concurrent plan creation attempts for the same PR across multiple ReviewFeedback records
	started, err := reviewFeedbackRepo.TryStartPlanCreationForPR(pr.ID, reviewFeedback.ID, planAgentRun.ID)
	if err != nil {
		logger.Error("Failed to atomically start plan creation for PR",
			zap.Error(err),
			zap.Int("pr_id", pr.ID),
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.Int("agent_run_id", planAgentRun.ID),
			zap.String("delivery_id", deliveryID),
		)
		return nil, err
	}

	if !started {
		// Another process already started plan creation for this PR, or this ReviewFeedback is not in 'pending' state
		// Check if there's an existing plan creation in progress for this PR
		allFeedbacks, err := reviewFeedbackRepo.FindByPRID(pr.ID)
		if err != nil {
			logger.Error("Failed to load review feedbacks for PR after concurrent update",
				zap.Error(err),
				zap.Int("pr_id", pr.ID),
				zap.String("delivery_id", deliveryID),
			)
			return nil, err
		}

		// Find the ReviewFeedback that is currently in 'creating' state
		var creatingFeedback *models.ReviewFeedback
		for _, fb := range allFeedbacks {
			if fb.PlanCreationStatus == "creating" {
				creatingFeedback = fb
				break
			}
		}

		if creatingFeedback != nil {
			logger.Info("Plan creation already started for this PR by another ReviewFeedback",
				zap.Int("pr_id", pr.ID),
				zap.Int("current_review_feedback_id", reviewFeedback.ID),
				zap.Int("creating_review_feedback_id", creatingFeedback.ID),
				zap.String("delivery_id", deliveryID),
			)
			result := &planCreationResult{
				Status:            "skipped_plan_already_started",
				ReviewFeedbackID:  reviewFeedback.ID,
				PlanCreationState: "creating",
			}
			if creatingFeedback.PlanAgentRunID != nil {
				result.PlanAgentRunID = *creatingFeedback.PlanAgentRunID
			}
			return result, nil
		}

		// Reload the current review feedback to get the current state
		updatedFeedback, err := reviewFeedbackRepo.FindByID(reviewFeedback.ID)
		if err != nil {
			logger.Error("Failed to reload review feedback after concurrent update",
				zap.Error(err),
				zap.Int("review_feedback_id", reviewFeedback.ID),
				zap.String("delivery_id", deliveryID),
			)
			return nil, err
		}
		if updatedFeedback == nil {
			logger.Warn("Review feedback not found after concurrent update",
				zap.Int("review_feedback_id", reviewFeedback.ID),
				zap.String("delivery_id", deliveryID),
			)
			return &planCreationResult{
				Status:           "skipped_feedback_missing",
				ReviewFeedbackID: reviewFeedback.ID,
			}, nil
		}

		logger.Info("Plan creation already started by another process",
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("plan_creation_status", updatedFeedback.PlanCreationStatus),
			zap.String("delivery_id", deliveryID),
		)
		result := &planCreationResult{
			Status:            "skipped_plan_already_started",
			ReviewFeedbackID:  updatedFeedback.ID,
			PlanCreationState: updatedFeedback.PlanCreationStatus,
		}
		if updatedFeedback.PlanAgentRunID != nil {
			result.PlanAgentRunID = *updatedFeedback.PlanAgentRunID
		}
		return result, nil
	}

	jobService := deps.KubernetesJobService
	if jobService == nil {
		kubernetesClient, clientErr := clients.NewKubernetesClient(logger)
		if clientErr != nil {
			logger.Error("Failed to initialize Kubernetes client for plan creation",
				zap.Error(clientErr),
				zap.String("delivery_id", deliveryID),
			)
			// Rollback: reset PlanCreationStatus to 'pending'
			if rollbackErr := reviewFeedbackRepo.UpdatePlanCreationStatus(reviewFeedback.ID, "pending"); rollbackErr != nil {
				logger.Error("Failed to rollback plan creation status after Kubernetes client error",
					zap.Error(rollbackErr),
					zap.Int("review_feedback_id", reviewFeedback.ID),
				)
			}
			return nil, clientErr
		}
		jobService = services.NewKubernetesJobService(kubernetesClient, logger)
	}

	branchName := strings.TrimSpace(pr.Branch)
	if branchName == "" {
		branchName = fmt.Sprintf("feature/issue-%d", issue.Number)
	}

	job, jobErr := jobService.CreateJobForPlanCreation(ctx, planAgentRun, issue, reviewFeedback, branchName)
	if jobErr != nil {
		logger.Error("Failed to create plan creation job",
			zap.Error(jobErr),
			zap.Int("agent_run_id", planAgentRun.ID),
			zap.Int("review_feedback_id", reviewFeedback.ID),
			zap.String("delivery_id", deliveryID),
		)
		// Rollback: reset PlanCreationStatus to 'pending'
		if rollbackErr := reviewFeedbackRepo.UpdatePlanCreationStatus(reviewFeedback.ID, "pending"); rollbackErr != nil {
			logger.Error("Failed to rollback plan creation status after job creation failure",
				zap.Error(rollbackErr),
				zap.Int("review_feedback_id", reviewFeedback.ID),
			)
		}
		return nil, jobErr
	}

	logger.Info("Plan creation job started",
		zap.Int("agent_run_id", planAgentRun.ID),
		zap.Int("review_feedback_id", reviewFeedback.ID),
		zap.String("delivery_id", deliveryID),
		zap.String("branch_name", branchName),
		zap.Bool("job_created", job != nil),
	)

	result := &planCreationResult{
		Status:            "started",
		ReviewFeedbackID:  reviewFeedback.ID,
		PlanAgentRunID:    planAgentRun.ID,
		PlanCreationState: "creating",
	}
	return result, nil
}
