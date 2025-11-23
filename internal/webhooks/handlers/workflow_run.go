package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// deliveryHeader is defined in issue_comment.go, reuse it
// appGitHubClient is defined in issue_comment.go, reuse it

// WorkflowRunPayload represents the GitHub webhook payload for workflow_run events
type WorkflowRunPayload struct {
	Action      string                `json:"action"`
	WorkflowRun WorkflowRun           `json:"workflow_run"`
	Repository  WorkflowRunRepository `json:"repository"`
}

// WorkflowRun represents the WorkflowRun object in workflow_run webhook payload
type WorkflowRun struct {
	ID           int64                    `json:"id"`
	Status       string                   `json:"status"`
	Conclusion   *string                  `json:"conclusion"`
	HeadBranch   string                   `json:"head_branch"`
	HeadSHA      string                   `json:"head_sha"`
	PullRequests []WorkflowRunPullRequest `json:"pull_requests"`
}

// WorkflowRunPullRequest represents a PullRequest object in workflow_run webhook payload
type WorkflowRunPullRequest struct {
	Number int `json:"number"`
}

// WorkflowRunRepository represents the Repository object in workflow_run webhook payload
type WorkflowRunRepository struct {
	FullName string `json:"full_name"`
}

// WorkflowRunDeps represents injectable dependencies for HandleWorkflowRun
type WorkflowRunDeps struct {
	Logger                *config.AppLogger
	GitHubClient          *clients.Client
	PullRequestRepository *repositories.PullRequestRepository
	CIStatusRepository    *repositories.CIStatusRepository
	ReviewFeedbackRepo    *repositories.ReviewFeedbackRepository
	AutoMergeService      services.AutoMergeService
}

// HandleWorkflowRun handles GitHub workflow_run webhook events
// It processes workflow completion events and triggers auto-merge for approved PRs
func HandleWorkflowRun(c *gin.Context) {
	// Delegate to WithDeps using production dependencies
	logger := config.GetLogger()
	db := config.GetDB()

	// Build production dependencies
	deps := WorkflowRunDeps{
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

	// Initialize repositories
	deps.PullRequestRepository = repositories.NewPullRequestRepository(db)
	deps.CIStatusRepository = repositories.NewCIStatusRepository()
	deps.ReviewFeedbackRepo = repositories.NewReviewFeedbackRepository()

	HandleWorkflowRunWithDeps(c, deps)
}

// HandleWorkflowRunWithDeps handles workflow_run webhook with injected dependencies (for tests)
func HandleWorkflowRunWithDeps(c *gin.Context, deps WorkflowRunDeps) {
	// Step 1: Initialization
	logger := deps.Logger
	if logger == nil {
		logger = config.GetLogger()
	}

	// Create context
	var ctx context.Context = c.Request.Context()

	// Step 2: Delivery ID取得と検証
	deliveryID := c.GetHeader(deliveryHeader)
	if deliveryID == "" {
		logger.Warn("Missing X-GitHub-Delivery header",
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

	// Step 3: ペイロード取得とパース
	payloadData, exists := c.Get("webhook_payload")
	if !exists {
		logger.Error("Webhook payload not found in context",
			config.String("delivery_id", deliveryID),
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("webhook payload not found in context"))
		return
	}

	payloadBytes, ok := payloadData.([]byte)
	if !ok {
		logger.Error("Invalid webhook payload type",
			config.String("delivery_id", deliveryID),
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("invalid webhook payload type"))
		return
	}

	var payload WorkflowRunPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			config.Error(err),
			config.String("delivery_id", deliveryID),
			config.String("path", c.Request.URL.Path),
		)
		c.Error(err)
		return
	}

	logger.Info("Received workflow_run webhook",
		config.String("delivery_id", deliveryID),
		config.String("action", payload.Action),
		config.Int64("workflow_run_id", payload.WorkflowRun.ID),
		config.String("repo", payload.Repository.FullName),
	)

	// Step 4: アクション検証
	if payload.Action != models.WorkflowRunActionCompleted {
		logger.Info("Ignoring non-completed action",
			config.String("action", payload.Action),
			config.String("delivery_id", deliveryID),
			config.Int64("workflow_run_id", payload.WorkflowRun.ID),
			config.String("repo", payload.Repository.FullName),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "ignored",
			"action":      payload.Action,
			"delivery_id": deliveryID,
		})
		return
	}

	// Step 5: ステータスと結論の検証
	if payload.WorkflowRun.Status != "completed" {
		logger.Info("Ignoring non-completed workflow run",
			config.String("status", payload.WorkflowRun.Status),
			config.String("delivery_id", deliveryID),
			config.Int64("workflow_run_id", payload.WorkflowRun.ID),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":          "ignored",
			"workflow_status": payload.WorkflowRun.Status,
			"delivery_id":     deliveryID,
		})
		return
	}

	if payload.WorkflowRun.Conclusion == nil {
		logger.Warn("Workflow run conclusion is nil, skipping processing",
			config.String("delivery_id", deliveryID),
			config.Int64("workflow_run_id", payload.WorkflowRun.ID),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_conclusion",
			"delivery_id": deliveryID,
		})
		return
	}

	if *payload.WorkflowRun.Conclusion != "success" {
		logger.Info("Ignoring non-success workflow run",
			config.String("conclusion", *payload.WorkflowRun.Conclusion),
			config.String("delivery_id", deliveryID),
			config.Int64("workflow_run_id", payload.WorkflowRun.ID),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "ignored",
			"conclusion":  *payload.WorkflowRun.Conclusion,
			"delivery_id": deliveryID,
		})
		return
	}

	// Step 6: リポジトリ情報抽出
	repoParts := strings.Split(payload.Repository.FullName, "/")
	if len(repoParts) != 2 {
		logger.Error("Invalid repository full name format",
			config.String("full_name", payload.Repository.FullName),
			config.String("delivery_id", deliveryID),
		)
		c.Error(errors.New("invalid repository full name format"))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// Step 7: PullRequests取得
	if len(payload.WorkflowRun.PullRequests) == 0 {
		logger.Info("No pull requests in workflow_run, skipping",
			config.String("delivery_id", deliveryID),
			config.Int64("workflow_run_id", payload.WorkflowRun.ID),
			config.String("repo", payload.Repository.FullName),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_pr",
			"delivery_id": deliveryID,
		})
		return
	}

	// GitHub client初期化
	githubClient := deps.GitHubClient
	if githubClient == nil {
		if appGitHubClient == nil {
			logger.Error("GitHub App client not available",
				config.String("delivery_id", deliveryID),
			)
			c.Error(errors.New("github client not provided"))
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

	// AutoMergeService初期化
	autoMergeSvc := deps.AutoMergeService
	if autoMergeSvc == nil {
		if appGitHubClient == nil {
			logger.Info("Skipping auto-merge: GitHub App client unavailable",
				config.String("delivery_id", deliveryID),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "processed",
				"auto_merge":  "skipped",
				"reason":      "github_app_client_unavailable",
				"delivery_id": deliveryID,
			})
			return
		}
		autoMergeSvc = services.NewAutoMergeService(appGitHubClient, logger)
	}

	// Step 8: 各PRについて処理
	prRepo := deps.PullRequestRepository
	if prRepo == nil {
		prRepo = repositories.NewPullRequestRepository(config.GetDB())
	}
	reviewRepo := deps.ReviewFeedbackRepo
	if reviewRepo == nil {
		reviewRepo = repositories.NewReviewFeedbackRepository()
	}
	ciRepo := deps.CIStatusRepository
	if ciRepo == nil {
		ciRepo = repositories.NewCIStatusRepository()
	}

	processedPRs := []int{}
	for _, prRef := range payload.WorkflowRun.PullRequests {
		prNumber := prRef.Number

		// PR取得
		pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, prNumber)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warn("PullRequest not found",
					config.String("delivery_id", deliveryID),
					config.Int("pr_number", prNumber),
					config.String("repo", payload.Repository.FullName),
				)
				continue
			}
			logger.Error("Failed to get PullRequest",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
				config.String("repo", payload.Repository.FullName),
			)
			continue
		}

		logger.Info("Processing PR for workflow_run",
			config.Int("pr_id", pr.ID),
			config.Int("pr_number", pr.Number),
			config.String("repo", pr.Repo),
			config.String("delivery_id", deliveryID),
		)

		// codex:approvedラベルのチェック
		labels, err := githubClient.ListLabelsOnIssue(ctx, owner, repo, prNumber)
		if err != nil {
			logger.Warn("Failed to list labels on PR",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
		}

		hasCodexApprovedLabel := false
		for _, label := range labels {
			if label != nil && label.Name != nil && *label.Name == services.CodexApprovalLabel {
				hasCodexApprovedLabel = true
				break
			}
		}

		if !hasCodexApprovedLabel {
			logger.Info("PR does not have codex:approved label, skipping",
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
		}

		// Codex approvalチェック
		approvalChecker := services.NewCodexApprovalChecker(reviewRepo, prRepo, logger)
		codexApproved, err := approvalChecker.IsApproved(ctx, owner, repo, prNumber)
		if err != nil {
			logger.Warn("Failed to check Codex approval",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
		}

		if !codexApproved {
			logger.Info("PR is not Codex approved, skipping",
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
		}

		// マージ条件を再評価
		ciProvider := services.NewCIStatusProvider(ciRepo, prRepo, logger)
		conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
		checker := services.NewMergeConditionChecker(ciProvider, approvalChecker, conflictDetector, logger)

		result, err := checker.Check(ctx, owner, repo, prNumber)
		if err != nil {
			logger.Error("merge condition check failed",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
		}

		logger.Info("merge condition evaluated",
			config.String("ci_state", string(result.CIState)),
			config.Bool("codex_approved", result.CodexApproved),
			config.String("conflict", string(result.Conflict)),
			config.Bool("mergeable", result.Mergeable),
			config.String("delivery_id", deliveryID),
		)

		if !result.Mergeable {
			logger.Info("PR is not mergeable yet",
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
				config.Strings("reasons", result.Reasons),
			)
			continue
		}

		// 自動マージ実行
		mergeRes, mergeErr := autoMergeSvc.AttemptAutoMerge(ctx, owner, repo, prNumber)
		if mergeErr != nil {
			logger.Error("auto-merge service returned error",
				config.Error(mergeErr),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
		}

		if mergeRes != nil && !mergeRes.Merged {
			logger.Warn("auto-merge failed",
				config.String("error_type", mergeRes.ErrorType),
				config.String("error_message", mergeRes.ErrorMessage),
				config.String("delivery_id", deliveryID),
				config.Int("pr_number", prNumber),
			)
			continue
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
			config.Int("pr_number", prNumber),
		)

		processedPRs = append(processedPRs, prNumber)
	}

	c.JSON(http.StatusOK, gin.H{
		"status":        "processed",
		"delivery_id":   deliveryID,
		"processed_prs": processedPRs,
	})
}
