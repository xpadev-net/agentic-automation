package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/errors"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// PullRequestReviewPayload represents the GitHub webhook payload for pull_request_review events
type PullRequestReviewPayload struct {
	Action      string                       `json:"action"`
	Review      PullRequestReviewReview      `json:"review"`
	PullRequest PullRequestReviewPullRequest `json:"pull_request"`
	Repository  PullRequestReviewRepository  `json:"repository"`
}

// PullRequestReviewReview represents the Review object in pull_request_review webhook payload
type PullRequestReviewReview struct {
	ID    int64  `json:"id"`
	Body  string `json:"body"`
	State string `json:"state"` // "approved", "changes_requested", "commented"
	User  User   `json:"user"`
}

// PullRequestReviewPullRequest represents the PullRequest object in pull_request_review webhook payload
type PullRequestReviewPullRequest struct {
	Number int `json:"number"`
}

// PullRequestReviewRepository represents the Repository object in pull_request_review webhook payload
type PullRequestReviewRepository struct {
	FullName string `json:"full_name"` // "owner/repo"
}

// PullRequestReviewDeps represents injectable dependencies for HandlePullRequestReview
type PullRequestReviewDeps struct {
	Logger                   *zap.Logger
	GitHubClient             *clients.Client
	CodexApprovalDetector    *services.CodexApprovalDetector
	PullRequestRepository    *repositories.PullRequestRepository
	ReviewFeedbackRepository *repositories.ReviewFeedbackRepository
	// Optional DI for US4 merge re-evaluation
	MergeConditionChecker services.MergeConditionChecker
	AutoMergeService      services.AutoMergeService
}

// HandlePullRequestReview handles GitHub pull_request_review webhook events
// It detects Codex approval and creates ReviewFeedback records
func HandlePullRequestReview(c *gin.Context) {
	// Delegate to WithDeps using production dependencies
	logger := config.GetLogger()

	// Build production dependencies
	deps := PullRequestReviewDeps{
		Logger: logger,
	}

	// GitHub App クライアントは DI から取得（なければここで生成）
	if appGitHubClient == nil {
		ghApp, err := clients.NewGitHubAppClient(logger)
		if err != nil {
			logger.Warn("GitHub App client not initialized", zap.Error(err))
		} else {
			appGitHubClient = ghApp
		}
	}

	HandlePullRequestReviewWithDeps(c, deps)
}

// HandlePullRequestReviewWithDeps handles pull_request_review webhook with injected dependencies (for tests)
func HandlePullRequestReviewWithDeps(c *gin.Context, deps PullRequestReviewDeps) {
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

	var payload PullRequestReviewPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Step 4: Action 検証
	if payload.Action != models.PullRequestReviewActionSubmitted {
		logger.Info("Ignoring non-submitted action",
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

	// Step 5: リポジトリ情報抽出
	repoParts := strings.Split(payload.Repository.FullName, "/")
	if len(repoParts) != 2 {
		logger.Error("Invalid repository full name format",
			zap.String("full_name", payload.Repository.FullName),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_INVALID_PAYLOAD, "invalid repository full name format", nil))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// Step 6: PullRequest レコード取得
	prRepo := deps.PullRequestRepository
	if prRepo == nil {
		prRepo = repositories.NewPullRequestRepository(config.GetDB())
	}

	pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			// PRレコードが見つからない場合（無関係なリポジトリや古いPR）は、
			// エラーを返さずにレビュー処理をスキップして成功を返す
			// これにより、GitHubがwebhookをリトライしないようにする
			logger.Info("PullRequest not found, ignoring review",
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", payload.PullRequest.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "ignored",
				"reason":      "pull_request_not_found",
				"delivery_id": deliveryID,
			})
			return
		}
		logger.Error("Failed to find PullRequest record",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}
	// pr == nil チェックは削除（FindByRepoAndNumberはエラーを返すため到達しない）

	// Step 7: GitHub クライアント初期化
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

	// Step 8: Review comments 取得
	reviewID := payload.Review.ID
	reviewComments, err := githubClient.ListPullRequestCommentsForReview(ctx, owner, repo, payload.PullRequest.Number, reviewID)
	if err != nil {
		logger.Warn("Failed to fetch review comments, continuing with review body only",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int64("review_id", reviewID),
			zap.Int("pr_number", payload.PullRequest.Number),
		)
		// Continue processing even if review comments fetch fails
		reviewComments = []*github.PullRequestComment{}
	}

	// Step 9: コンテキスト構築（レビュー本文 + 関連コメント）
	reviewBody := strings.TrimSpace(payload.Review.Body)
	contextParts := []string{}
	if reviewBody != "" {
		contextParts = append(contextParts, reviewBody)
	}

	for _, comment := range reviewComments {
		if comment != nil && comment.Body != nil && strings.TrimSpace(*comment.Body) != "" {
			contextParts = append(contextParts, *comment.Body)
		}
	}

	fullContext := strings.Join(contextParts, "\n\n--- Review Comment ---\n\n")
	if fullContext == "" {
		fullContext = reviewBody // Fallback to review body only
	}

	logger.Info("Review context built",
		zap.String("delivery_id", deliveryID),
		zap.Int64("review_id", reviewID),
		zap.Int("pr_number", payload.PullRequest.Number),
		zap.Int("review_comments_count", len(reviewComments)),
		zap.Int("context_length", len(fullContext)),
	)

	// Step 10: Codex approval 検出
	approvalDetector := deps.CodexApprovalDetector
	if approvalDetector == nil {
		approvalDetector = services.NewCodexApprovalDetector(logger)
	}

	approvalDetected := approvalDetector.DetectApproval(
		fullContext, // Use full context including review comments
		payload.Review.User.Login,
		payload.Review.User.ID,
	)

	logger.Info("Codex approval detection completed",
		zap.Bool("approval_detected", approvalDetected),
		zap.String("delivery_id", deliveryID),
		zap.Int64("review_id", reviewID),
		zap.String("reviewer", payload.Review.User.Login),
	)

	// Step 11: ReviewFeedback レコード作成/更新
	reviewFeedbackRepo := deps.ReviewFeedbackRepository
	if reviewFeedbackRepo == nil {
		reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
	}

	reviewIDInt64 := int64(payload.Review.ID)
	var feedback *models.ReviewFeedback

	// 既存の'requested'があれば'received'へ更新、無ければ'received'を新規作成
	if list, err := reviewFeedbackRepo.FindByPRIDAndStatus(pr.ID, "requested"); err == nil && len(list) > 0 {
		latest := list[0]
		if err := reviewFeedbackRepo.UpdateToReceived(latest.ID, fullContext, approvalDetected, &reviewIDInt64); err != nil {
			logger.Warn("Failed to update ReviewFeedback to received",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
				zap.Int("feedback_id", latest.ID),
			)
		} else {
			feedback = latest
			logger.Info("ReviewFeedback updated to received",
				zap.Int("feedback_id", latest.ID),
				zap.Bool("approval_detected", approvalDetected),
				zap.String("delivery_id", deliveryID),
			)
		}
	} else {
		created, err := reviewFeedbackRepo.CreateReceivedReview(pr.ID, fullContext, approvalDetected, &reviewIDInt64)
		if err != nil {
			logger.Warn("Failed to create received ReviewFeedback",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
			)
		} else {
			feedback = created
			logger.Info("ReviewFeedback created as received",
				zap.Int("feedback_id", created.ID),
				zap.Bool("approval_detected", approvalDetected),
				zap.String("delivery_id", deliveryID),
			)
		}
	}

	// Step 12: マージ条件評価と自動マージ（approval検出時のみ）
	if approvalDetected {
		// CIStatusProvider/CodexApprovalCheckerのアダプタを作成
		ciProvider := &ciStatusProviderAdapter{
			githubClient: githubClient,
			owner:        owner,
			repo:         repo,
			prNumber:     pr.Number,
			logger:       logger,
		}
		conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
		// 本イベントで承認検知済みのため常に true を返すアダプタを使用
		codexChecker := &alwaysApprovedChecker{}
		checker := deps.MergeConditionChecker
		if checker == nil {
			checker = services.NewMergeConditionChecker(ciProvider, codexChecker, conflictDetector, logger)
		}

		res, err := checker.Check(ctx, owner, repo, pr.Number)
		if err != nil {
			logger.Error("merge condition check failed",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", pr.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.Error(err)
			return
		}

		logger.Info("merge condition evaluated",
			zap.Bool("mergeable", res.Mergeable),
			zap.String("ci_state", string(res.CIState)),
			zap.String("conflict", string(res.Conflict)),
			zap.Int("reasons_count", len(res.Reasons)),
			zap.String("delivery_id", deliveryID),
		)

		if res.Mergeable {
			// Use injected AutoMergeService if available (for testing), otherwise create new one
			var am services.AutoMergeService
			if deps.AutoMergeService != nil {
				am = deps.AutoMergeService
			} else if appGitHubClient != nil {
				am = services.NewAutoMergeService(appGitHubClient, logger)
			}

			if am == nil {
				logger.Info("auto-merge skipped (GitHub App client unavailable)",
					zap.String("delivery_id", deliveryID),
					zap.Int("pr_number", pr.Number),
					zap.String("repo", payload.Repository.FullName),
				)
				c.JSON(http.StatusOK, gin.H{
					"status":      "mergeable_auto_merge_skipped",
					"delivery_id": deliveryID,
					"reason":      "app_github_client_unavailable",
				})
				return
			}

			mergeRes, mergeErr := am.AttemptAutoMerge(ctx, owner, repo, pr.Number)
			if mergeErr != nil {
				logger.Warn("auto-merge attempt returned error",
					zap.Error(mergeErr),
					zap.String("delivery_id", deliveryID),
				)
				// Issue 情報取得（通知に使用）
				var issue *models.Issue
				if pr.IssueID != nil {
					if i, err := repositories.NewIssueRepository().FindByID(*pr.IssueID); err == nil {
						issue = i
					} else {
						logger.Warn("failed to load issue for merge failure notification", zap.Error(err))
					}
				}
				// Discord: notify merge failure (best-effort)
				func() {
					discordClient := clients.NewDiscordClient("", logger)
					if discordClient == nil {
						return
					}
					discordSvc := services.NewDiscordNotificationService(discordClient, logger)
					_ = discordSvc.NotifyMergeFailure(ctx, pr, issue, mergeErr.Error(), services.ClassifyMergeError(mergeErr))
				}()
				// 任意通知（軽量）：PR に結果コメントを投稿（ベストエフォート）
				_, _ = githubClient.CreateIssueComment(ctx, owner, repo, pr.Number, "⚠️ Auto-merge attempt failed after Codex approval. Please check CI/conflicts.")
				c.JSON(http.StatusOK, gin.H{
					"status":      "merge_attempt_failed",
					"delivery_id": deliveryID,
				})
				return
			}

			// マージ失敗（結果で通知）
			if mergeRes != nil && !mergeRes.Merged {
				// Issue 情報取得（通知に使用）
				var issue *models.Issue
				if pr.IssueID != nil {
					if i, err := repositories.NewIssueRepository().FindByID(*pr.IssueID); err == nil {
						issue = i
					} else {
						logger.Warn("failed to load issue for merge failure notification", zap.Error(err))
					}
				}
				// Discord: notify merge failure (best-effort)
				func() {
					discordClient := clients.NewDiscordClient("", logger)
					if discordClient == nil {
						return
					}
					discordSvc := services.NewDiscordNotificationService(discordClient, logger)
					_ = discordSvc.NotifyMergeFailure(ctx, pr, issue, mergeRes.ErrorMessage, mergeRes.ErrorType)
				}()
				c.JSON(http.StatusOK, gin.H{
					"status":      "merge_condition_met_but_not_merged",
					"delivery_id": deliveryID,
					"merge_result": gin.H{
						"merged":  mergeRes.Merged,
						"message": mergeRes.Message,
					},
				})
				return
			}

			// マージ成功
			if mergeRes != nil && mergeRes.Merged {
				logger.Info("Auto-merge succeeded",
					zap.String("delivery_id", deliveryID),
					zap.Int("pr_number", pr.Number),
					zap.String("repo", payload.Repository.FullName),
				)
				c.JSON(http.StatusOK, gin.H{
					"status":      "merged",
					"delivery_id": deliveryID,
					"merge_result": gin.H{
						"merged": true,
					},
				})
				return
			}
		}
	}

	// Step 13: 成功レスポンス返却
	response := gin.H{
		"status":                "processed",
		"delivery_id":           deliveryID,
		"pr_number":             payload.PullRequest.Number,
		"approval_detected":     approvalDetected,
		"review_comments_count": len(reviewComments),
	}
	if feedback != nil {
		response["review_feedback_id"] = feedback.ID
	}

	c.JSON(http.StatusOK, response)
}
