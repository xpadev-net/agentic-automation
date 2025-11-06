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
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
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
// This interface will be implemented in T091
type CodexReviewService interface {
	RequestReview(ctx context.Context, owner, repo string, prNumber int) error
}

// PullRequestReviewCommentDeps represents injectable dependencies for HandlePullRequestReviewComment
type PullRequestReviewCommentDeps struct {
	Logger                   *zap.Logger
	GitHubClient             *clients.Client
	AuthorizationService     Authorization // Reuse from issue_comment.go
	CodexReviewService       CodexReviewService
	PullRequestRepository    *repositories.PullRequestRepository
	ReviewFeedbackRepository *repositories.ReviewFeedbackRepository
	// Optional DI for US4 merge re-evaluation
	MergeConditionChecker services.MergeConditionChecker
	AutoMergeService      services.AutoMergeService
}

// ciStatusProviderAdapter は PR に紐づく最新の aggregated CI 状態を返す軽量アダプタ
type ciStatusProviderAdapter struct {
	repo *repositories.CIStatusRepository
	prID int
}

func (a *ciStatusProviderAdapter) GetAggregatedState(_ context.Context, _ string, _ string, _ int) (services.CIState, error) {
	if a.repo == nil || a.prID == 0 {
		return services.CIStateUnknown, nil
	}
	statuses, err := a.repo.FindByPRID(a.prID)
	if err != nil {
		return services.CIStateUnknown, err
	}
	// 最新 aggregated を 1 件選定する
	var latest *models.CIStatus
	// helper: 比較関数（CompletedAt > UpdatedAt > CheckSuiteID 数値）
	isNewer := func(a, b *models.CIStatus) bool {
		// CompletedAt: nil は古い扱い
		if a.CompletedAt != nil || b.CompletedAt != nil {
			if a.CompletedAt == nil {
				return false
			}
			if b.CompletedAt == nil {
				return true
			}
			if a.CompletedAt.After(*b.CompletedAt) {
				return true
			}
			if b.CompletedAt.After(*a.CompletedAt) {
				return false
			}
		}
		// UpdatedAt
		if a.UpdatedAt.After(b.UpdatedAt) {
			return true
		}
		if b.UpdatedAt.After(a.UpdatedAt) {
			return false
		}
		// CheckSuiteID 数値比較（失敗時は同等扱い）
		var ai, bi int64
		if a.CheckSuiteID != "" {
			if v, err := strconv.ParseInt(a.CheckSuiteID, 10, 64); err == nil {
				ai = v
			}
		}
		if b.CheckSuiteID != "" {
			if v, err := strconv.ParseInt(b.CheckSuiteID, 10, 64); err == nil {
				bi = v
			}
		}
		return ai > bi
	}

	for i := range statuses {
		s := &statuses[i]
		if s.Name != "aggregated" {
			continue
		}
		if latest == nil || isNewer(s, latest) {
			latest = s
		}
	}

	if latest == nil {
		return services.CIStateUnknown, nil
	}

	// 最新 1 件のみから CIState を決定
	if latest.Conclusion != nil {
		switch *latest.Conclusion {
		case "failure":
			return services.CIStateFailed, nil
		case "success":
			return services.CIStateSuccess, nil
		default:
			return services.CIStatePending, nil
		}
	}
	// 結論未設定は進行中とみなす
	return services.CIStatePending, nil
}

// alwaysApprovedChecker は本イベントで承認検知済みのため常に true を返すアダプタ
type alwaysApprovedChecker struct{}

func (a *alwaysApprovedChecker) IsApproved(_ context.Context, _ string, _ string, _ int) (bool, error) {
	return true, nil
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
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

	// Step 3: Payload parsing
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

	// Step 4.5: Codex 承認コメント検知 → マージ条件再評価（@codex review トリガーと独立に実行）
	detector := services.NewCodexApprovalDetector(logger)
	if detector.DetectApproval(payload.Comment.Body, payload.Comment.User.Login) {
		logger.Info("Codex approval detected; re-evaluating merge conditions",
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", payload.PullRequest.Number),
			zap.String("repo", payload.Repository.FullName),
		)

		// リポジトリ情報抽出
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

		// GitHub クライアント初期化（repo 単位）
		githubClient := deps.GitHubClient
		if githubClient == nil {
			if appGitHubClient == nil {
				logger.Error("GitHub App client not available",
					zap.String("delivery_id", deliveryID),
				)
				c.Error(errors.New("github client not provided"))
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

		// PR 取得
		prRepo := deps.PullRequestRepository
		if prRepo == nil {
			prRepo = repositories.NewPullRequestRepository(config.GetDB())
		}
		pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.PullRequest.Number)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Error("PullRequest not found for merge re-evaluation",
					zap.Error(err),
					zap.String("delivery_id", deliveryID),
					zap.Int("pr_number", payload.PullRequest.Number),
					zap.String("repo", payload.Repository.FullName),
				)
				c.Error(errors.New("pull request not found"))
				return
			}
			logger.Error("Failed to get PullRequest for merge re-evaluation",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", payload.PullRequest.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.Error(err)
			return
		}

		// ReviewFeedbackレコード作成（承認検出時）
		reviewFeedbackRepo := deps.ReviewFeedbackRepository
		if reviewFeedbackRepo != nil {
			commentID := int64(payload.Comment.ID)
			content := payload.Comment.Body
			feedback := &models.ReviewFeedback{
				PRID:             pr.ID,
				Source:           "Codex",
				Content:          &content,
				Status:           "completed",
				ApprovalDetected: true,
				GitHubCommentID:  &commentID,
			}
			if err := reviewFeedbackRepo.Create(feedback); err != nil {
				logger.Warn("Failed to create ReviewFeedback record for approval",
					zap.Error(err),
					zap.String("delivery_id", deliveryID),
					zap.Int("pr_id", pr.ID),
				)
				// エラーは無視して続行（既に存在する可能性があるため）
			}
		}

		// CIStatusProvider/CodexApprovalChecker はファイル先頭のアダプタを利用

		var checker services.MergeConditionChecker
		if deps.MergeConditionChecker != nil {
			checker = deps.MergeConditionChecker
		} else {
			ciRepo := repositories.NewCIStatusRepository()
			ciProvider := &ciStatusProviderAdapter{repo: ciRepo, prID: pr.ID}
			conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
			checker = services.NewMergeConditionChecker(ciProvider, &alwaysApprovedChecker{}, conflictDetector, logger)
		}

		res, err := checker.Check(ctx, owner, repo, payload.PullRequest.Number)
		if err != nil {
			logger.Error("merge condition check failed",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", payload.PullRequest.Number),
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
			am := deps.AutoMergeService
			if am == nil {
				// GitHub App クライアント未設定時はフォールバック生成をスキップして安全に抜ける
				if appGitHubClient == nil {
					logger.Warn("auto-merge skipped: GitHub App client not available",
						zap.String("delivery_id", deliveryID),
					)
					// 任意通知（軽量）
					_, _ = githubClient.CreateIssueComment(ctx, owner, repo, payload.PullRequest.Number, "ℹ️ Mergeable, but auto-merge skipped (app client unavailable).")
					c.JSON(http.StatusOK, gin.H{
						"status":      "merge_skipped_no_app_client",
						"delivery_id": deliveryID,
					})
					return
				}
				am = services.NewAutoMergeService(appGitHubClient, logger)
			}
			mergeRes, mergeErr := am.AttemptAutoMerge(ctx, owner, repo, payload.PullRequest.Number)
			if mergeErr != nil {
				// 予期しないエラー（通常はAutoMergeResultで返却される）
				logger.Warn("auto-merge attempt returned error",
					zap.Error(mergeErr),
					zap.String("delivery_id", deliveryID),
				)
				// Discord: notify merge failure (best-effort)
				func() {
					discordClient := clients.NewDiscordClient("", logger)
					if discordClient == nil {
						return
					}
					discordSvc := services.NewDiscordNotificationService(discordClient, logger)
					var prModel *models.PullRequest
					if deps.PullRequestRepository != nil {
						prModel, _ = deps.PullRequestRepository.FindByRepoAndNumber(owner+"/"+repo, payload.PullRequest.Number)
					}
					_ = discordSvc.NotifyMergeFailure(ctx, prModel, nil, mergeErr.Error(), services.ClassifyMergeError(mergeErr))
				}()
				// 成否に関わらず 200 を返す（再試行は他イベントで行われ得る）
				// 任意通知（軽量）：PR に結果コメントを投稿（ベストエフォート）
				_, _ = githubClient.CreateIssueComment(ctx, owner, repo, payload.PullRequest.Number, "⚠️ Auto-merge attempt failed after Codex approval. Please check CI/conflicts.")
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
					var prModel *models.PullRequest
					if deps.PullRequestRepository != nil {
						prModel, _ = deps.PullRequestRepository.FindByRepoAndNumber(owner+"/"+repo, payload.PullRequest.Number)
					}
					_ = discordSvc.NotifyMergeFailure(ctx, prModel, issue, mergeRes.ErrorMessage, mergeRes.ErrorType)
				}()

				logger.Warn("auto-merge failed",
					zap.String("error_type", mergeRes.ErrorType),
					zap.String("error_message", mergeRes.ErrorMessage),
					zap.String("delivery_id", deliveryID),
				)
				// 成否に関わらず 200 を返す（再試行は他イベントで行われ得る）
				// 任意通知（軽量）：PR に結果コメントを投稿（ベストエフォート）
				_, _ = githubClient.CreateIssueComment(ctx, owner, repo, payload.PullRequest.Number, "⚠️ Auto-merge attempt failed after Codex approval. Please check CI/conflicts.")
				c.JSON(http.StatusOK, gin.H{
					"status":      "merge_attempt_failed",
					"delivery_id": deliveryID,
				})
				return
			}

			logger.Info("auto-merge succeeded",
				zap.Bool("merged", mergeRes != nil && mergeRes.Merged),
				zap.String("merge_sha", func() string {
					if mergeRes != nil {
						return mergeRes.MergeSHA
					}
					return ""
				}()),
				zap.String("delivery_id", deliveryID),
			)
			// Discord: notify merge success (best-effort)
			func() {
				discordClient := clients.NewDiscordClient("", logger)
				if discordClient == nil {
					return
				}
				discordSvc := services.NewDiscordNotificationService(discordClient, logger)
				var prModel *models.PullRequest
				if deps.PullRequestRepository != nil {
					prModel, _ = deps.PullRequestRepository.FindByRepoAndNumber(owner+"/"+repo, payload.PullRequest.Number)
				}
				_ = discordSvc.NotifyMergeSuccess(ctx, prModel, nil, 0)
			}()
			// 任意通知（軽量）
			_, _ = githubClient.CreateIssueComment(ctx, owner, repo, payload.PullRequest.Number, "✅ Auto-merged after Codex approval.")
			c.JSON(http.StatusOK, gin.H{
				"status":      "merged_or_initiated",
				"delivery_id": deliveryID,
			})
			return
		}

		// 条件未成立（任意通知：理由を簡易表示）
		_, _ = githubClient.CreateIssueComment(ctx, owner, repo, payload.PullRequest.Number, "ℹ️ Merge re-evaluated after Codex approval: not mergeable yet.")
		c.JSON(http.StatusOK, gin.H{
			"status":      "re_evaluated_not_mergeable",
			"delivery_id": deliveryID,
			"ci_state":    string(res.CIState),
			"conflict":    string(res.Conflict),
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

	triggerDetected := utils.ContainsCodexReviewTrigger(payload.Comment.Body)
	if !triggerDetected {
		// Create comment body preview (first 100 characters for security)
		commentBodyPreview := payload.Comment.Body
		if len(commentBodyPreview) > 100 {
			commentBodyPreview = commentBodyPreview[:100]
		}

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
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_trigger",
			"delivery_id": deliveryID,
		})
		return
	}

	logger.Info("Trigger detected in comment",
		zap.Bool("trigger_detected", true),
		zap.String("delivery_id", deliveryID),
		zap.Int("pr_number", payload.PullRequest.Number),
		zap.String("repo", payload.Repository.FullName),
	)

	// Step 6: リポジトリ情報抽出
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

	// Step 7: 権限チェック（サービス初期化含む）
	// GitHub クライアント初期化（deps が nil の場合）
	githubClient := deps.GitHubClient
	if githubClient == nil {
		if appGitHubClient == nil {
			logger.Error("GitHub App client not available",
				zap.String("delivery_id", deliveryID),
			)
			c.Error(errors.New("github client not provided"))
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
	// T091 で実装予定のため、実装が存在する場合のみ初期化
	codexReviewService := deps.CodexReviewService
	if codexReviewService == nil {
		// Note: services.NewCodexReviewService will be implemented in T091
		// For now, we check if it exists and initialize if available
		// If not implemented yet, codexReviewService will remain nil
		// and Step 9 will handle it gracefully
		// TODO: Uncomment when T091 is implemented
		// codexReviewService = services.NewCodexReviewService(githubClient, logger)
	}

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

	// Step 8: PullRequest 取得
	prRepo := deps.PullRequestRepository
	if prRepo == nil {
		prRepo = repositories.NewPullRequestRepository(config.GetDB())
	}

	pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("PullRequest not found",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", payload.PullRequest.Number),
				zap.String("repo", payload.Repository.FullName),
			)
			c.Error(errors.New("pull request not found"))
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

	err = codexReviewService.RequestReview(ctx, owner, repo, payload.PullRequest.Number)
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

	// Step 10: ReviewFeedback レコード作成
	reviewFeedbackRepo := deps.ReviewFeedbackRepository
	if reviewFeedbackRepo == nil {
		reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
	}

	// Convert comment ID to int64
	commentID := int64(payload.Comment.ID)
	feedback := &models.ReviewFeedback{
		PRID:             pr.ID,
		Source:           "Codex",
		Status:           "requested",
		ApprovalDetected: false,
		GitHubCommentID:  &commentID,
	}

	err = reviewFeedbackRepo.Create(feedback)
	if err != nil {
		logger.Error("Failed to create ReviewFeedback record",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_id", pr.ID),
			zap.Int("pr_number", payload.PullRequest.Number),
		)
		c.Error(err)
		return
	}

	logger.Info("ReviewFeedback record created",
		zap.Int("feedback_id", feedback.ID),
		zap.Int("pr_id", pr.ID),
		zap.String("delivery_id", deliveryID),
	)

	// Step 11: 成功レスポンス返却
	c.JSON(http.StatusOK, gin.H{
		"status":      "processed",
		"delivery_id": deliveryID,
		"pr_number":   payload.PullRequest.Number,
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
