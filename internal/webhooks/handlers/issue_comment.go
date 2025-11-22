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
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
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
	ID    int64  `json:"id"`
}

const deliveryHeader = "X-GitHub-Delivery"

// appGitHubClient holds a process-wide GitHub App client (DI from server)
var appGitHubClient *clients.GitHubClient

// ciStatusProviderAdapter は GitHub APIから直接CI状態を取得するアダプタ
type ciStatusProviderAdapter struct {
	githubClient *clients.Client
	owner        string
	repo         string
	prNumber     int
	logger       *config.AppLogger
}

func (a *ciStatusProviderAdapter) GetAggregatedState(ctx context.Context, owner, repo string, prNumber int) (services.CIState, error) {
	if a.githubClient == nil {
		if a.logger != nil {
			a.logger.Warn("GitHub client not available in ciStatusProviderAdapter")
		}
		return services.CIStateUnknown, nil
	}

	// Use the stored owner/repo/prNumber if available, otherwise use parameters
	actualOwner := a.owner
	actualRepo := a.repo
	actualPRNumber := a.prNumber
	if actualOwner == "" {
		actualOwner = owner
	}
	if actualRepo == "" {
		actualRepo = repo
	}
	if actualPRNumber == 0 {
		actualPRNumber = prNumber
	}

	// Get PR to retrieve head SHA
	pr, err := a.githubClient.GetPullRequest(ctx, actualOwner, actualRepo, actualPRNumber)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("Failed to get PR for CI status check",
				config.Error(err),
				config.String("owner", actualOwner),
				config.String("repo", actualRepo),
				config.Int("pr_number", actualPRNumber),
			)
		}
		return services.CIStateUnknown, err
	}

	if pr == nil || pr.Head == nil || pr.Head.SHA == nil {
		if a.logger != nil {
			a.logger.Warn("PR head SHA not available",
				config.String("owner", actualOwner),
				config.String("repo", actualRepo),
				config.Int("pr_number", actualPRNumber),
			)
		}
		return services.CIStateUnknown, nil
	}

	headSHA := *pr.Head.SHA

	// Get check runs for the head SHA
	checkRuns, err := a.githubClient.ListCheckRunsForRef(ctx, actualOwner, actualRepo, headSHA)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("Failed to get check runs for ref",
				config.Error(err),
				config.String("owner", actualOwner),
				config.String("repo", actualRepo),
				config.String("ref", headSHA),
			)
		}
		return services.CIStateUnknown, err
	}

	// Aggregate check runs using the existing service function
	agg := services.AggregateFromRuns(checkRuns)

	// Map aggregated result to CIState
	switch agg.Aggregated {
	case "success":
		return services.CIStateSuccess, nil
	case "failed":
		return services.CIStateFailed, nil
	case "pending":
		return services.CIStatePending, nil
	default:
		return services.CIStateUnknown, nil
	}
}

// alwaysApprovedChecker は本イベントで承認検知済みのため常に true を返すアダプタ
type alwaysApprovedChecker struct{}

func (a *alwaysApprovedChecker) IsApproved(_ context.Context, _ string, _ string, _ int) (bool, error) {
	return true, nil
}

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
			// テスト環境などで資格情報が無い場合でもここではエラーにせず後段で処理
			logger.Warn("GitHub App client not initialized", config.Error(err))
		} else {
			appGitHubClient = ghApp
		}
	}

	// Initialize Kubernetes client
	k8sClient, err := clients.NewKubernetesClient(logger)
	if err != nil {
		logger.Error("Failed to initialize Kubernetes client", config.Error(err))
		c.Error(err)
		return
	}
	deps.KubernetesClient = k8sClient

	// Initialize services that do not require GitHub client here
	agentRunRepo := repositories.NewAgentRunRepository(db)
	deps.TriggerService = services.NewTriggerDetectionService(logger)
	deps.AgentTypeDetectorService = services.NewAgentTypeDetectorService(logger)
	deps.StateMachine = services.NewAgentRunStateMachine(agentRunRepo, logger)
	// GitHub 依存サービスの生成は HandleIssueCommentWithDeps 内で
	// owner/repo 決定後に per-repo クライアントを用意してから行う

	HandleIssueCommentWithDeps(c, deps)
}

// IssueCommentDeps represents injectable dependencies for HandleIssueComment
// Narrow interfaces for dependency injection (allow fakes in tests)
type Authorization interface {
	CheckPermission(ctx context.Context, owner, repo, username string) (bool, error)
}

type IssueContext interface {
	CollectIssueContext(ctx context.Context, owner, repo string, issueNumber int) (*services.IssueContext, error)
	FormatPrompt(issueCtx *services.IssueContext, userInstruction string) string
}

type GitHubNotification interface {
	PostExecutionStartComment(ctx context.Context, owner, repo string, issueNumber int, agentType string, agentRunID int) error
	NotifyDependencyViolation(ctx context.Context, owner, repo string, issueNumber, prNumber int, blocked []models.Issue, idempotencyKey string) error
}

type IssueCommentDeps struct {
	Logger                    *config.AppLogger
	GitHubClient              *clients.Client
	KubernetesClient          *clients.KubernetesClient
	TriggerService            *services.TriggerDetectionService
	AuthorizationService      Authorization
	IssueContextService       IssueContext
	AgentTypeDetectorService  *services.AgentTypeDetectorService
	StateMachine              services.AgentRunStateMachine
	GitHubNotificationService GitHubNotification
	AutoMergeService          services.AutoMergeService // Optional: for testing
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
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "missing X-GitHub-Delivery header", nil))
		return
	}

	// Create context
	ctx := c.Request.Context()

	// Step 2: Payload parsing
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

	var payload IssueCommentPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.String("path", c.Request.URL.Path),
		)
		c.Error(err)
		return
	}

	// Only process "created" actions (ignore edited/deleted)
	if payload.Action != models.IssueCommentActionCreated {
		logger.Info("Ignoring non-created action",
			config.String("action", payload.Action),
			config.String("delivery_id", deliveryID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
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
			config.String("delivery_id", deliveryID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
			config.String("issue_state", payload.Issue.State),
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
	// GitHub 依存サービスは owner/repo 決定後に設定する（まず注入済みを反映）
	authorizationService := deps.AuthorizationService
	issueContextService := deps.IssueContextService
	agentTypeDetectorService := deps.AgentTypeDetectorService
	if agentTypeDetectorService == nil {
		agentTypeDetectorService = services.NewAgentTypeDetectorService(logger)
	}
	stateMachine := deps.StateMachine
	if stateMachine == nil {
		stateMachine = services.NewAgentRunStateMachine(agentRunRepo, logger)
	}
	githubNotificationService := deps.GitHubNotificationService

	// Initialize Kubernetes job service
	var jobService services.KubernetesJobService
	if deps.KubernetesClient != nil {
		jobService = services.NewKubernetesJobService(deps.KubernetesClient, logger)
	} else {
		k8sClient, err := clients.NewKubernetesClient(logger)
		if err != nil {
			logger.Error("Failed to initialize Kubernetes client",
				config.Error(err),
				config.String("delivery_id", deliveryID),
			)
			c.Error(err)
			return
		}
		jobService = services.NewKubernetesJobService(k8sClient, logger)
	}

	// Step 4.5: Codex approve検出（PRに関連するIssueコメントの場合のみ）
	// 注意: Codex bot判定は`issue_comment`（PRに関連する通常のコメント）でのみ行う
	// IssueがPRに関連しているかチェック
	prRepo := repositories.NewPullRequestRepository(config.GetDB())
	pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.Issue.Number)
	if err == nil && pr != nil {
		// PRに関連するIssueコメントの場合、approveを検出
		// DetectApprovalはCodex bot判定を内部で行うため、外側での事前チェックは不要
		detector := services.NewCodexApprovalDetector(logger)
		if detector.DetectApproval(payload.Comment.Body, payload.Comment.User.Login, payload.Comment.User.ID) {
			logger.Info("Codex approval detected in issue comment; re-evaluating merge conditions",
				config.String("delivery_id", deliveryID),
				config.Int("issue_number", payload.Issue.Number),
				config.String("repo", payload.Repository.FullName),
			)

			// リポジトリ情報抽出
			repoParts := strings.Split(payload.Repository.FullName, "/")
			if len(repoParts) != 2 {
				logger.Error("Invalid repository full name format",
					config.String("full_name", payload.Repository.FullName),
					config.String("delivery_id", deliveryID),
				)
				c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_INVALID_REPO_FORMAT, "invalid repository full name format", nil))
				return
			}
			owner := repoParts[0]
			repo := repoParts[1]

			// GitHub クライアント初期化（repo 単位）
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

			// ReviewFeedbackレコード作成（承認検出時）
			reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
			commentID := int64(payload.Comment.ID)
			content := payload.Comment.Body

			// 既存の'requested'があれば'received'へ更新、無ければ'received'を新規作成
			if list, err := reviewFeedbackRepo.FindByPRIDAndStatus(pr.ID, "requested"); err == nil && len(list) > 0 {
				latest := list[0]
				if uerr := reviewFeedbackRepo.UpdateToReceived(latest.ID, content, true, &commentID); uerr != nil {
					logger.Warn("Failed to update ReviewFeedback to received",
						config.Error(uerr),
						config.String("delivery_id", deliveryID),
						config.Int("pr_id", pr.ID),
					)
				}
			} else {
				if _, cerr := reviewFeedbackRepo.CreateReceivedReview(pr.ID, content, true, &commentID); cerr != nil {
					logger.Warn("Failed to create received ReviewFeedback",
						config.Error(cerr),
						config.String("delivery_id", deliveryID),
						config.Int("pr_id", pr.ID),
					)
				}
			}

			// CIStatusProvider/CodexApprovalCheckerのアダプタを作成
			ciProvider := &ciStatusProviderAdapter{
				githubClient: githubClient,
				owner:        owner,
				repo:         repo,
				prNumber:     pr.Number,
				logger:       logger,
			}
			conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
			checker := services.NewMergeConditionChecker(ciProvider, &alwaysApprovedChecker{}, conflictDetector, logger)

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
					// 予期しないエラー（通常はAutoMergeResultで返却される）
					logger.Warn("auto-merge attempt returned error",
						config.Error(mergeErr),
						config.String("delivery_id", deliveryID),
					)
					// Discord: notify merge failure (best-effort)
					func() {
						discordClient := clients.NewDiscordClient("", logger)
						if discordClient == nil {
							return
						}
						discordSvc := services.NewDiscordNotificationService(discordClient, logger)
						var prModel *models.PullRequest
						prModel, _ = prRepo.FindByRepoAndNumber(owner+"/"+repo, pr.Number)
						_ = discordSvc.NotifyMergeFailure(ctx, prModel, nil, mergeErr.Error(), services.ClassifyMergeError(mergeErr))
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
						var prModel *models.PullRequest
						prModel, _ = prRepo.FindByRepoAndNumber(owner+"/"+repo, pr.Number)
						_ = discordSvc.NotifyMergeFailure(ctx, prModel, issue, mergeRes.ErrorMessage, mergeRes.ErrorType)
					}()

					logger.Warn("auto-merge failed",
						config.String("error_type", mergeRes.ErrorType),
						config.String("error_message", mergeRes.ErrorMessage),
						config.String("delivery_id", deliveryID),
					)
					// 任意通知（軽量）：PR に結果コメントを投稿（ベストエフォート）
					_, _ = githubClient.CreateIssueComment(ctx, owner, repo, pr.Number, "⚠️ Auto-merge attempt failed after Codex approval. Please check CI/conflicts.")
					c.JSON(http.StatusOK, gin.H{
						"status":      "merge_attempt_failed",
						"delivery_id": deliveryID,
					})
					return
				}

				logger.Info("auto-merge succeeded",
					config.Bool("merged", mergeRes != nil && mergeRes.Merged),
					config.String("merge_sha", func() string {
						if mergeRes != nil {
							return mergeRes.MergeSHA
						}
						return ""
					}()),
					config.String("delivery_id", deliveryID),
				)
				// Discord: notify merge success (best-effort)
				func() {
					discordClient := clients.NewDiscordClient("", logger)
					if discordClient == nil {
						return
					}
					discordSvc := services.NewDiscordNotificationService(discordClient, logger)
					var prModel *models.PullRequest
					prModel, _ = prRepo.FindByRepoAndNumber(owner+"/"+repo, pr.Number)
					_ = discordSvc.NotifyMergeSuccess(ctx, prModel, nil, 0)
				}()
				// 任意通知（軽量）
				_, _ = githubClient.CreateIssueComment(ctx, owner, repo, pr.Number, "✅ Auto-merged after Codex approval.")
				c.JSON(http.StatusOK, gin.H{
					"status":      "merged_or_initiated",
					"delivery_id": deliveryID,
				})
				return
			}

			// 条件未成立（任意通知：理由を簡易表示）
			_, _ = githubClient.CreateIssueComment(ctx, owner, repo, pr.Number, "ℹ️ Merge re-evaluated after Codex approval: not mergeable yet.")
			c.JSON(http.StatusOK, gin.H{
				"status":      "re_evaluated_not_mergeable",
				"delivery_id": deliveryID,
				"ci_state":    string(res.CIState),
				"conflict":    string(res.Conflict),
			})
			return
		}
	}

	// Step 5: Trigger detection
	logger.Info("Checking for trigger in comment",
		config.String("delivery_id", deliveryID),
		config.Int("issue_number", payload.Issue.Number),
		config.String("repo", payload.Repository.FullName),
		config.String("comment_user", payload.Comment.User.Login),
	)

	triggerDetected := triggerService.DetectRunAgentTrigger(payload.Comment.Body)
	if !triggerDetected {
		// Create comment body preview (first 100 characters for security)
		commentBodyPreview := payload.Comment.Body
		if len(commentBodyPreview) > 100 {
			commentBodyPreview = commentBodyPreview[:100]
		}

		logger.Info("No trigger detected in comment",
			config.String("delivery_id", deliveryID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
			config.Int("comment_id", payload.Comment.ID),
			config.String("comment_created_at", payload.Comment.CreatedAt),
			config.String("comment_body_preview", commentBodyPreview),
			config.String("comment_user", payload.Comment.User.Login),
			config.String("trigger_string", utils.RunAgentTrigger),
			config.String("detection_reason", "trigger string '/run-agent' not found in comment body"),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_trigger",
			"delivery_id": deliveryID,
		})
		return
	}

	logger.Info("Trigger detected in comment",
		config.Bool("trigger_detected", true),
		config.String("delivery_id", deliveryID),
		config.Int("issue_number", payload.Issue.Number),
		config.String("repo", payload.Repository.FullName),
	)

	// Step 6: Permission check
	repoParts := strings.Split(payload.Repository.FullName, "/")
	if len(repoParts) != 2 {
		logger.Error("Invalid repository full name format",
			config.String("full_name", payload.Repository.FullName),
			config.String("delivery_id", deliveryID),
		)
		c.Error(errors.NewCodedError(errors.ERR_WEBHOOK_INVALID_REPO_FORMAT, "invalid repository full name format", nil))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// 依存が未注入の場合のみ、GitHub クライアントとサービスを構築
	if authorizationService == nil || issueContextService == nil || githubNotificationService == nil {
		if deps.GitHubClient == nil {
			if appGitHubClient == nil {
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
			deps.GitHubClient = clients.NewFromGitHub(rawClient, logger)
		}
		if authorizationService == nil {
			authorizationService = services.NewAuthorizationService(deps.GitHubClient, logger)
		}
		if issueContextService == nil {
			issueContextService = services.NewIssueContextService(deps.GitHubClient, logger)
		}
		if githubNotificationService == nil {
			githubNotificationService = services.NewGitHubNotificationService(deps.GitHubClient, logger)
		}
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

		logFields := []config.Field{
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.String("user", payload.Comment.User.Login),
			config.String("repo", payload.Repository.FullName),
			config.String("api_method", "GetPermissionLevel"),
			config.String("error_type", errorType),
		}
		if httpStatusCode > 0 {
			logFields = append(logFields, config.Int("http_status_code", httpStatusCode))
		}

		logger.Error("Failed to check user permission",
			logFields...,
		)
		c.Error(err)
		return
	}

	if !hasPermission {
		logger.Warn("User lacks permission to trigger agent",
			config.String("delivery_id", deliveryID),
			config.String("user", payload.Comment.User.Login),
			config.String("repo", payload.Repository.FullName),
			config.Int("issue_number", payload.Issue.Number),
			config.String("authorization_required_level", "write/maintain/admin"),
			config.String("authorization_policy", "FR-018: Collaborator+ permission required"),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "permission_denied",
			"delivery_id": deliveryID,
		})
		return
	}

	logger.Info("User permission verified",
		config.Bool("authorized", true),
		config.String("user", payload.Comment.User.Login),
		config.String("repo", payload.Repository.FullName),
		config.String("delivery_id", deliveryID),
	)

	// Step 7: Get AgentRun (created by idempotency middleware)
	agentRun, err := repositories.NewAgentRunRepository(db).GetByIDempotencyKey(deliveryID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("AgentRun not found for delivery ID (should be created by middleware)",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("issue_number", payload.Issue.Number),
				config.String("repo", payload.Repository.FullName),
			)
			c.Error(errors.NewCodedError(errors.ERR_AGENT_RUN_NOT_FOUND, "agent run not found for delivery ID", nil))
			return
		}
		logger.Error("Failed to get AgentRun by idempotency key",
			config.Error(err),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	logger.Info("AgentRun retrieved",
		config.Int("agent_run_id", agentRun.ID),
		config.String("state", agentRun.State),
		config.String("delivery_id", deliveryID),
	)

	// Check if AgentRun is already processed
	if agentRun.State != "queued" {
		logger.Info("AgentRun already processed",
			config.Int("agent_run_id", agentRun.ID),
			config.String("state", agentRun.State),
			config.String("delivery_id", deliveryID),
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
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Issue not found (should be created by middleware)",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("issue_number", payload.Issue.Number),
				config.String("repo", payload.Repository.FullName),
			)
			c.Error(errors.NewCodedError(errors.ERR_DB_RECORD_NOT_FOUND, "issue not found", nil))
			return
		}
		logger.Error("Failed to get Issue",
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}

	logger.Info("Issue retrieved",
		config.Int("issue_id", issue.ID),
		config.Int("issue_number", issue.Number),
		config.String("repo", issue.Repo),
		config.String("delivery_id", deliveryID),
	)

	// Step 9: Collect Issue context
	issueContext, err := issueContextService.CollectIssueContext(ctx, owner, repo, payload.Issue.Number)
	if err != nil {
		logger.Error("Failed to collect Issue context",
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
		)
		c.Error(err)
		return
	}

	logger.Info("Issue context collected",
		config.Int("comments_count", len(issueContext.Comments)),
		config.Int("labels_count", len(issueContext.Labels)),
		config.Bool("has_body", issueContext.Body != ""),
		config.String("delivery_id", deliveryID),
	)

	// Step 9.5: Check for existing PR and extract user instruction
	var existingBranchName string
	userInstruction := utils.ExtractInstructionFromComment(payload.Comment.Body)

	pullRequestRepo := repositories.NewPullRequestRepository(db)
	// Prefer PR associated with this agent run if available
	if agentRun.PRID != nil {
		pr, prErr := pullRequestRepo.FindByID(*agentRun.PRID)
		if prErr == nil && pr != nil && pr.Status == "open" {
			existingBranchName = pr.Branch
			logger.Info("Found PR associated with agent run, will checkout existing branch",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("pr_id", *agentRun.PRID),
				config.Int("pr_number", pr.Number),
				config.String("branch", existingBranchName),
				config.String("delivery_id", deliveryID),
			)
		} else if prErr != nil {
			logger.Warn("Failed to find PR associated with agent run, falling back to issue PRs",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("pr_id", *agentRun.PRID),
				config.Error(prErr),
				config.String("delivery_id", deliveryID),
			)
		} else if pr != nil && pr.Status != "open" {
			logger.Info("PR associated with agent run is not open, falling back to issue PRs",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("pr_id", *agentRun.PRID),
				config.String("pr_status", pr.Status),
				config.String("delivery_id", deliveryID),
			)
		}
	}

	// Fallback: scan all PRs for the issue if no branch found from agent run's PR
	if existingBranchName == "" {
		prs, prErr := pullRequestRepo.FindByIssueID(issue.ID)
		if prErr == nil && len(prs) > 0 {
			// Use the first open PR if multiple exist
			for _, pr := range prs {
				if pr.Status == "open" {
					existingBranchName = pr.Branch
					logger.Info("Found existing PR for issue, will checkout existing branch",
						config.Int("issue_id", issue.ID),
						config.Int("pr_number", pr.Number),
						config.String("branch", existingBranchName),
						config.String("delivery_id", deliveryID),
					)
					break
				}
			}
		}
	}

	if userInstruction != "" {
		logger.Info("Extracted user instruction from comment",
			config.String("instruction", userInstruction),
			config.String("delivery_id", deliveryID),
		)
	}

	// Step 10: Format prompt with user instruction
	prompt := issueContextService.FormatPrompt(issueContext, userInstruction)

	// Step 10.5: Set labels from issueContext for agent type detection
	if len(issueContext.Labels) > 0 {
		labelsJSON, err := json.Marshal(issueContext.Labels)
		if err != nil {
			logger.Warn("Failed to marshal issue labels",
				config.Error(err),
				config.Int("issue_id", issue.ID),
				config.String("delivery_id", deliveryID),
			)
		} else {
			issue.Labels = string(labelsJSON)
			if err := issueRepo.Update(issue); err != nil {
				logger.Warn("Failed to update issue labels",
					config.Error(err),
					config.Int("issue_id", issue.ID),
					config.String("delivery_id", deliveryID),
				)
			} else {
				logger.Info("Issue labels updated from GitHub",
					config.Int("labels_count", len(issueContext.Labels)),
					config.Int("issue_id", issue.ID),
					config.String("delivery_id", deliveryID),
				)
			}
		}
	}

	// Step 11: Detect agent type
	agentType := agentTypeDetectorService.DetectAgentType(issue)

	logger.Info("Agent type detected",
		config.String("agent_type", agentType),
		config.Int("issue_id", issue.ID),
		config.String("delivery_id", deliveryID),
	)

	// Step 12: Reuse middleware-created AgentRun for plan creation
	// For /run-agent from issue, we first create a plan, then execute it
	// Reuse the agentRun created by idempotency middleware to avoid leaving orphaned queued records
	planAgentRun := agentRun

	// Reload agentRun from database to ensure we have the latest state
	reloadedForConfig, err := agentRunRepo.GetByID(planAgentRun.ID)
	if err != nil {
		logger.Error("Failed to reload AgentRun for plan creation",
			config.Error(err),
			config.Int("agent_run_id", planAgentRun.ID),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Configure agentRun for plan creation mode
	reloadedForConfig.ExecutionMode = "plan_creation"
	reloadedForConfig.AgentType = agentType
	reloadedForConfig.ReviewFeedbackID = nil // No review feedback for issue-triggered plan creation

	// Update agentRun with plan creation configuration
	if err := agentRunRepo.Update(reloadedForConfig); err != nil {
		logger.Error("Failed to update AgentRun for plan creation",
			config.Error(err),
			config.Int("agent_run_id", reloadedForConfig.ID),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}
	planAgentRun = reloadedForConfig

	logger.Info("Reusing middleware-created AgentRun for plan creation",
		config.Int("plan_agent_run_id", planAgentRun.ID),
		config.String("idempotency_key", planAgentRun.IdempotencyKey),
		config.String("delivery_id", deliveryID),
	)

	// Determine branch name to store in Input
	// Use existingBranchName if resolved, otherwise use default format
	branchNameForInput := existingBranchName
	if branchNameForInput == "" {
		branchNameForInput = fmt.Sprintf("feature/issue-%d", issue.Number)
	}

	// Build structured input JSON for plan creation (schema v1)
	inputPayload := map[string]any{
		"schema_version": "1",
		"prompt":         prompt,
		"agent_type":     agentType,
		"branch_name":    branchNameForInput,
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
		logger.Warn("Failed to marshal structured input payload", config.Error(marshalErr))
		// Fallback to minimal JSON with prompt and branch name
		inputBytes, _ = json.Marshal(map[string]any{
			"schema_version": "1",
			"prompt":         prompt,
			"agent_type":     agentType,
			"branch_name":    branchNameForInput,
		})
	}

	// Reload agentRun from database to ensure we have the latest state
	reloadedForInput, err := agentRunRepo.GetByID(planAgentRun.ID)
	if err != nil {
		logger.Error("Failed to reload AgentRun before updating input",
			config.Error(err),
			config.Int("agent_run_id", planAgentRun.ID),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Assign JSON to plan creation AgentRun.Input
	reloadedForInput.Input = datatypes.JSON(inputBytes)
	if err := agentRunRepo.Update(reloadedForInput); err != nil {
		logger.Error("Failed to update plan creation AgentRun",
			config.Error(err),
			config.Int("plan_agent_run_id", reloadedForInput.ID),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}
	planAgentRun = reloadedForInput

	// Step 12.5: Dependency validation (US5 T127)
	// ジョブ開始前に依存が全てクローズ済みかを検証する。
	// GitHubクライアント未注入のテスト/環境では安全にスキップする。
	if deps.GitHubClient != nil {
		edgesRepo := repositories.NewBlockerGraphRepository()
		fetcher := services.NewIssueDependencyFetcher(deps.GitHubClient, logger)
		builder := services.NewBlockerGraphBuilder(fetcher, issueRepo, edgesRepo, logger)
		validator := services.NewDependencyValidator(builder, issueRepo, edgesRepo, logger)

		vr, verr := validator.ValidateUnblocked(ctx, owner, repo, payload.Issue.Number)
		if verr != nil {
			if stderrors.Is(verr, services.ErrBlockedDependencies) {
				logger.Info("Execution blocked due to dependencies",
					config.Int("agent_run_id", agentRun.ID),
					config.Int("issue_number", payload.Issue.Number),
					config.String("repo", payload.Repository.FullName),
					config.Int("blocked_count", len(vr.BlockedDeps)),
					config.Strings("blocked_deps", summarizeIssuesForLog(vr.BlockedDeps)),
				)

				// Post dependency violation notification (US5 T129)
				if githubNotificationService != nil {
					// Get idempotency key
					idempotencyKey := agentRun.IdempotencyKey
					if idempotencyKey == "" {
						// Fallback if idempotency key is empty
						idempotencyKey = fmt.Sprintf("%s#%d:%d", payload.Repository.FullName, payload.Issue.Number, len(vr.BlockedDeps))
					}

					// Get PR number if exists
					prNumber := 0
					pullRequestRepo := repositories.NewPullRequestRepository(db)
					prs, prErr := pullRequestRepo.FindByIssueID(issue.ID)
					if prErr == nil && len(prs) > 0 {
						// Use the first PR if multiple exist
						prNumber = prs[0].Number
					}

					// Post notification (errors are logged but don't block the error return)
					if notifyErr := githubNotificationService.NotifyDependencyViolation(
						ctx,
						owner,
						repo,
						payload.Issue.Number,
						prNumber,
						vr.BlockedDeps,
						idempotencyKey,
					); notifyErr != nil {
						logger.Warn("Failed to post dependency violation notification",
							config.Error(notifyErr),
							config.Int("issue_number", payload.Issue.Number),
							config.String("repo", payload.Repository.FullName),
						)
					}
				}

				c.Error(verr)
				return
			}
			c.Error(verr)
			return
		}
		// Defensive: if no error but blocked deps exist, treat as violation and notify
		if vr != nil && len(vr.BlockedDeps) > 0 {
			if githubNotificationService != nil {
				idempotencyKey := agentRun.IdempotencyKey
				if idempotencyKey == "" {
					idempotencyKey = fmt.Sprintf("%s#%d:%d", payload.Repository.FullName, payload.Issue.Number, len(vr.BlockedDeps))
				}
				prNumber := 0
				pullRequestRepo := repositories.NewPullRequestRepository(db)
				prs, prErr := pullRequestRepo.FindByIssueID(issue.ID)
				if prErr == nil && len(prs) > 0 {
					prNumber = prs[0].Number
				}
				if notifyErr := githubNotificationService.NotifyDependencyViolation(
					ctx, owner, repo, payload.Issue.Number, prNumber, vr.BlockedDeps, idempotencyKey,
				); notifyErr != nil {
					logger.Warn("Failed to post dependency violation notification (defensive path)",
						config.Error(notifyErr),
						config.Int("issue_number", payload.Issue.Number),
						config.String("repo", payload.Repository.FullName),
					)
				}
			}
			c.Error(services.ErrBlockedDependencies)
			return
		}
	} else {
		logger.Info("Skipping dependency validation: GitHub client not provided",
			config.String("delivery_id", deliveryID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
		)
	}

	// Step 13: State transition for plan creation AgentRun (queued -> started)
	if err := stateMachine.TransitionToStarted(planAgentRun.ID); err != nil {
		logger.Error("Failed to transition plan creation AgentRun to started state",
			config.Error(err),
			config.Int("plan_agent_run_id", planAgentRun.ID),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	logger.Info("Plan creation AgentRun state transitioned to started",
		config.Int("plan_agent_run_id", planAgentRun.ID),
		config.String("delivery_id", deliveryID),
	)

	// Update planAgentRun state in memory to match database
	planAgentRun.State = "started"

	// Step 14: Create Kubernetes Job for plan creation
	// Note: reviewFeedback is nil for issue-triggered plan creation
	job, err := jobService.CreateJobForPlanCreation(ctx, planAgentRun, issue, nil, existingBranchName)
	if err != nil {
		logger.Error("Failed to create plan creation Kubernetes Job, rolling back state",
			config.Error(err),
			config.Int("plan_agent_run_id", planAgentRun.ID),
			config.String("delivery_id", deliveryID),
		)
		// Rollback state to queued for retry
		if rollbackErr := stateMachine.TransitionToQueued(planAgentRun.ID); rollbackErr != nil {
			logger.Error("Failed to rollback plan creation AgentRun state",
				config.Error(rollbackErr),
				config.Int("plan_agent_run_id", planAgentRun.ID),
				config.String("delivery_id", deliveryID),
			)
		} else {
			logger.Info("Plan creation AgentRun state rolled back to queued for retry",
				config.Int("plan_agent_run_id", planAgentRun.ID),
				config.String("delivery_id", deliveryID),
			)
		}
		c.Error(err)
		return
	}

	logger.Info("Plan creation Kubernetes Job created successfully",
		config.Int("plan_agent_run_id", planAgentRun.ID),
		config.String("job_name", job.Name),
		config.String("job_uid", string(job.UID)),
		config.String("namespace", job.Namespace),
		config.String("delivery_id", deliveryID),
	)

	// Reload agentRun from database to preserve started_at and other fields
	// that were set by TransitionToStarted
	reloadedForJobName, err := agentRunRepo.GetByID(planAgentRun.ID)
	if err != nil {
		logger.Warn("Failed to reload AgentRun before updating job name",
			config.Error(err),
			config.Int("plan_agent_run_id", planAgentRun.ID),
			config.String("delivery_id", deliveryID),
		)
		// Non-blocking: continue even if reload fails
	} else {
		// Update AgentRun with job name for cleanup
		reloadedForJobName.JobName = &job.Name
		if err := agentRunRepo.Update(reloadedForJobName); err != nil {
			logger.Warn("Failed to update AgentRun with job name",
				config.Error(err),
				config.Int("plan_agent_run_id", reloadedForJobName.ID),
				config.String("delivery_id", deliveryID),
			)
			// Non-blocking: continue even if update fails
		}
	}

	// Post GitHub status comment for plan creation
	if err := githubNotificationService.PostExecutionStartComment(
		ctx,
		owner,
		repo,
		payload.Issue.Number,
		planAgentRun.AgentType,
		planAgentRun.ID,
	); err != nil {
		// Non-blocking: log error but don't fail the webhook processing
		logger.Warn("Failed to post GitHub plan creation start comment",
			config.Error(err),
			config.Int("plan_agent_run_id", planAgentRun.ID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
			config.String("delivery_id", deliveryID),
		)
	} else {
		logger.Info("GitHub plan creation start comment posted successfully",
			config.Int("plan_agent_run_id", planAgentRun.ID),
			config.Int("issue_number", payload.Issue.Number),
			config.String("repo", payload.Repository.FullName),
			config.String("delivery_id", deliveryID),
		)
	}

	// Step 15: Return success response
	// Note: Normal execution AgentRun will be created after plan creation completes in agent_report.go
	c.JSON(http.StatusOK, gin.H{
		"status":            "plan_creation_started",
		"plan_agent_run_id": planAgentRun.ID,
		"job_name":          job.Name,
		"delivery_id":       deliveryID,
	})
}

// summarizeIssuesForLog は repo#number 形式で配列化してログ出力向けに整形する。
func summarizeIssuesForLog(issues []models.Issue) []string {
	if len(issues) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.Repo+"#"+strconv.Itoa(is.Number))
	}
	return out
}
