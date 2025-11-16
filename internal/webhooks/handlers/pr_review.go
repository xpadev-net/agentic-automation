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
	Logger                   *config.AppLogger
	GitHubClient             *clients.Client
	CodexApprovalDetector    *services.CodexApprovalDetector
	PullRequestRepository    *repositories.PullRequestRepository
	ReviewFeedbackRepository *repositories.ReviewFeedbackRepository
	// Optional DI for US4 merge re-evaluation
	MergeConditionChecker services.MergeConditionChecker
	AutoMergeService      services.AutoMergeService
	// Optional DI for plan creation
	KubernetesJobService services.KubernetesJobService
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
			logger.Warn("GitHub App client not initialized", config.Error(err))
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
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "missing X-GitHub-Delivery header", nil))
		return
	}

	// Step 3: Payload parsing
	payloadData, exists := c.Get("webhook_payload")
	if !exists {
		logger.Error("Webhook payload not found in context",
			config.String("delivery_id", deliveryID),
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_PAYLOAD_NOT_FOUND, "webhook payload not found in context", nil))
		return
	}

	payloadBytes, ok := payloadData.([]byte)
	if !ok {
		logger.Error("Invalid webhook payload type",
			config.String("delivery_id", deliveryID),
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_INVALID_PAYLOAD, "invalid webhook payload type", nil))
		return
	}

	var payload PullRequestReviewPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			config.Error(err),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Step 4: Action 検証
	if payload.Action != models.PullRequestReviewActionSubmitted {
		logger.Info("Ignoring non-submitted action",
			config.String("action", payload.Action),
			config.String("delivery_id", deliveryID),
			config.Int("pr_number", payload.PullRequest.Number),
			config.String("repo", payload.Repository.FullName),
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
			config.String("full_name", payload.Repository.FullName),
			config.String("delivery_id", deliveryID),
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
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", payload.PullRequest.Number),
				config.String("repo", payload.Repository.FullName),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "ignored",
				"reason":      "pull_request_not_found",
				"delivery_id": deliveryID,
			})
			return
		}
		logger.Error("Failed to find PullRequest record",
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.Int("pr_number", payload.PullRequest.Number),
			config.String("repo", payload.Repository.FullName),
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
				config.String("delivery_id", deliveryID),
			)
			c.Error(errors.NewCodedError(errors.ERR_INTERNAL_SERVER_ERROR, "github client not provided", nil))
			return
		}
		rawClient, err := appGitHubClient.ForRepo(ctx, owner, repo)
		if err != nil {
			logger.Error("Failed to init per-repo GitHub client",
				config.Error(err),
				config.String("owner", owner),
				config.String("repo", repo),
				config.String("delivery_id", deliveryID),
			)
			c.Error(err)
			return
		}
		githubClient = clients.NewFromGitHub(rawClient, logger)
	}

	// Step 8: PR本文の取得（先頭に付与するため）
	prBody := ""
	if githubClient != nil {
		if prDetail, err := githubClient.GetPullRequest(ctx, owner, repo, payload.PullRequest.Number); err != nil {
			logger.Warn("Failed to fetch pull request body, continuing without it",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", payload.PullRequest.Number),
			)
		} else if prDetail != nil && prDetail.Body != nil {
			prBody = strings.TrimSpace(*prDetail.Body)
		}
	}

	// Step 9: Review comments 取得
	reviewID := payload.Review.ID
	reviewComments, err := githubClient.ListPullRequestCommentsForReview(ctx, owner, repo, payload.PullRequest.Number, reviewID)
	if err != nil {
		logger.Warn("Failed to fetch review comments, continuing with review body only",
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.Int64("review_id", reviewID),
			config.Int("pr_number", payload.PullRequest.Number),
		)
		// Continue processing even if review comments fetch fails
		reviewComments = []*github.PullRequestComment{}
	}

	// Step 10: コンテキスト構築（PR本文 + レビュー本文 + 関連コメント）
	reviewBody := strings.TrimSpace(payload.Review.Body)
	contextParts := []string{}
	reviewContextParts := []string{}
	if prBody != "" {
		contextParts = append(contextParts, "--- Pull Request Body ---\n\n"+prBody)
	}
	if reviewBody != "" {
		contextParts = append(contextParts, reviewBody)
		reviewContextParts = append(reviewContextParts, reviewBody)
	}

	for _, comment := range reviewComments {
		if comment != nil && comment.Body != nil && strings.TrimSpace(*comment.Body) != "" {
			contextParts = append(contextParts, *comment.Body)
			reviewContextParts = append(reviewContextParts, *comment.Body)
		}
	}

	fullContext := strings.Join(contextParts, "\n\n--- Review Comment ---\n\n")
	if fullContext == "" {
		fullContext = reviewBody // Fallback to review body only
	}

	reviewOnlyContext := strings.Join(reviewContextParts, "\n\n--- Review Comment ---\n\n")
	if reviewOnlyContext == "" {
		reviewOnlyContext = reviewBody // Fallback to review body only
	}

	logger.Info("Review context built",
		config.String("delivery_id", deliveryID),
		config.Int64("review_id", reviewID),
		config.Int("pr_number", payload.PullRequest.Number),
		config.Int("review_comments_count", len(reviewComments)),
		config.Int("context_length", len(fullContext)),
		config.Int("pr_body_len", len(prBody)),
	)

	// Step 11: Codex approval 検出
	approvalDetector := deps.CodexApprovalDetector
	if approvalDetector == nil {
		approvalDetector = services.NewCodexApprovalDetector(logger)
	}

	approvalDetected := approvalDetector.DetectApproval(
		reviewOnlyContext, // Exclude PR body to avoid false approvals
		payload.Review.User.Login,
		payload.Review.User.ID,
	)

	logger.Info("Codex approval detection completed",
		config.Bool("approval_detected", approvalDetected),
		config.String("delivery_id", deliveryID),
		config.Int64("review_id", reviewID),
		config.String("reviewer", payload.Review.User.Login),
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
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_id", pr.ID),
				config.Int("feedback_id", latest.ID),
			)
		} else {
			feedback = latest
			logger.Info("ReviewFeedback updated to received",
				config.Int("feedback_id", latest.ID),
				config.Bool("approval_detected", approvalDetected),
				config.String("delivery_id", deliveryID),
			)
		}
	} else {
		created, err := reviewFeedbackRepo.CreateReceivedReview(pr.ID, fullContext, approvalDetected, &reviewIDInt64)
		if err != nil {
			logger.Warn("Failed to create received ReviewFeedback",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_id", pr.ID),
			)
		} else {
			feedback = created
			logger.Info("ReviewFeedback created as received",
				config.Int("feedback_id", created.ID),
				config.Bool("approval_detected", approvalDetected),
				config.String("delivery_id", deliveryID),
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
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", pr.Number),
				config.String("repo", payload.Repository.FullName),
			)
			c.Error(err)
			return
		}

		logger.Info("merge condition evaluated",
			config.Bool("mergeable", res.Mergeable),
			config.String("ci_state", string(res.CIState)),
			config.String("conflict", string(res.Conflict)),
			config.Int("reasons_count", len(res.Reasons)),
			config.String("delivery_id", deliveryID),
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
					config.String("delivery_id", deliveryID),
					config.Int("pr_number", pr.Number),
					config.String("repo", payload.Repository.FullName),
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
					config.Error(mergeErr),
					config.String("delivery_id", deliveryID),
				)
				// Issue 情報取得（通知に使用）
				var issue *models.Issue
				if pr.IssueID != nil {
					if i, err := repositories.NewIssueRepository().FindByID(*pr.IssueID); err == nil {
						issue = i
					} else {
						logger.Warn("failed to load issue for merge failure notification", config.Error(err))
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
						logger.Warn("failed to load issue for merge failure notification", config.Error(err))
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
					config.String("delivery_id", deliveryID),
					config.Int("pr_number", pr.Number),
					config.String("repo", payload.Repository.FullName),
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

	// Step 12.5: プラン構築処理（レビュー本文がある場合）
	var planResult *planCreationResult
	if !approvalDetected && reviewBody != "" && feedback != nil {
		// PullRequestReviewDepsからPullRequestReviewCommentDepsへの変換
		commentDeps := PullRequestReviewCommentDeps{
			Logger:                   deps.Logger,
			GitHubClient:             githubClient,
			PullRequestRepository:    deps.PullRequestRepository,
			ReviewFeedbackRepository: deps.ReviewFeedbackRepository,
			MergeConditionChecker:    deps.MergeConditionChecker,
			AutoMergeService:         deps.AutoMergeService,
			KubernetesJobService:     deps.KubernetesJobService,
			// AuthorizationServiceとCodexReviewServiceはnilで問題なし（startPlanCreationIfNeeded内で使用されない）
			AuthorizationService: nil,
			CodexReviewService:   nil,
		}

		var planErr error
		planResult, planErr = startPlanCreationIfNeeded(
			ctx,
			commentDeps,
			logger,
			pr,
			reviewBody,
			reviewID,
			payload.Review.User.Login,
			payload.Review.User.ID,
			deliveryID,
		)
		if planErr != nil {
			logger.Error("Failed to start plan creation from review body",
				config.Error(planErr),
				config.String("delivery_id", deliveryID),
				config.Int64("review_id", reviewID),
				config.Int("pr_number", payload.PullRequest.Number),
			)
			c.Error(planErr)
			return
		}

		if planResult != nil {
			logger.Info("Plan creation processing completed",
				config.String("status", planResult.Status),
				config.String("delivery_id", deliveryID),
				config.Int64("review_id", reviewID),
				config.Int("pr_number", payload.PullRequest.Number),
			)
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
	if planResult != nil {
		response["plan_creation_status"] = planResult.Status
		if planResult.ReviewFeedbackID != 0 {
			response["review_feedback_id"] = planResult.ReviewFeedbackID
		}
		if planResult.PlanAgentRunID != 0 {
			response["plan_agent_run_id"] = planResult.PlanAgentRunID
		}
		if planResult.PlanCreationState != "" {
			response["plan_creation_state"] = planResult.PlanCreationState
		}
	}

	c.JSON(http.StatusOK, response)
}
