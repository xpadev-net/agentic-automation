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
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// deliveryHeader is defined in issue_comment.go, reuse it

// CheckSuitePayload represents the GitHub webhook payload for check_suite events
type CheckSuitePayload struct {
	Action     string               `json:"action"`
	CheckSuite CheckSuite           `json:"check_suite"`
	Repository CheckSuiteRepository `json:"repository"`
}

// CheckSuite represents the CheckSuite object in check_suite webhook payload
type CheckSuite struct {
	ID           int64                   `json:"id"`
	Status       string                  `json:"status"`
	Conclusion   *string                 `json:"conclusion"`
	HeadBranch   string                  `json:"head_branch"`
	HeadSHA      string                  `json:"head_sha"`
	PullRequests []CheckSuitePullRequest `json:"pull_requests"`
}

// CheckSuitePullRequest represents a PullRequest object in check_suite webhook payload
type CheckSuitePullRequest struct {
	Number int `json:"number"`
}

// CheckSuiteRepository represents the Repository object in check_suite webhook payload
type CheckSuiteRepository struct {
	FullName string `json:"full_name"`
}

// CheckSuiteDeps represents injectable dependencies for HandleCheckSuite
type CheckSuiteDeps struct {
	Logger                    *zap.Logger
	GitHubClient              *clients.Client
	PullRequestRepository     *repositories.PullRequestRepository
	CIStatusRepository        *repositories.CIStatusRepository
	CIStatusAggregator        services.CIStatusAggregator
	CIFailureAnalyzer         *services.CIFailureAnalyzer
	FeedbackAggregator        *services.FeedbackAggregator
	RetryOrchestrator         *services.RetryOrchestrator
	KubernetesJobService      services.KubernetesJobService
	GitHubNotificationService *services.GitHubNotificationService
	IssueContextService       *services.IssueContextService
	AgentRunRepository        repositories.AgentRunRepository
	IssueRepository           *repositories.IssueRepository
	AutoMergeService          services.AutoMergeService
}

// HandleCheckSuite handles GitHub check_suite webhook events
// It processes CI completion events and triggers retries on failure
func HandleCheckSuite(c *gin.Context) {
	// Delegate to WithDeps using production dependencies
	logger := config.GetLogger()
	db := config.GetDB()

	// Build production dependencies
	deps := CheckSuiteDeps{
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

	// Initialize repositories
	deps.PullRequestRepository = repositories.NewPullRequestRepository(db)
	deps.CIStatusRepository = repositories.NewCIStatusRepository()
	deps.AgentRunRepository = repositories.NewAgentRunRepository(db)
	deps.IssueRepository = repositories.NewIssueRepository()

	// Initialize Kubernetes client
	k8sClient, err := clients.NewKubernetesClient(logger)
	if err != nil {
		logger.Error("Failed to initialize Kubernetes client", zap.Error(err))
		c.Error(err)
		return
	}
	deps.KubernetesJobService = services.NewKubernetesJobService(k8sClient, logger)

	HandleCheckSuiteWithDeps(c, deps)
}

// HandleCheckSuiteWithDeps handles check_suite webhook with injected dependencies (for tests)
func HandleCheckSuiteWithDeps(c *gin.Context, deps CheckSuiteDeps) {
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
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

	// Step 3: ペイロード取得とパース
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

	var payload CheckSuitePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse webhook payload",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(err)
		return
	}

	logger.Info("Received check_suite webhook",
		zap.String("delivery_id", deliveryID),
		zap.String("action", payload.Action),
		zap.Int64("check_suite_id", payload.CheckSuite.ID),
		zap.String("repo", payload.Repository.FullName),
	)

	// Step 4: アクション検証
	if payload.Action != models.CheckSuiteActionCompleted {
		logger.Info("Ignoring non-completed action",
			zap.String("action", payload.Action),
			zap.String("delivery_id", deliveryID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
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
		c.Error(errors.New("invalid repository full name format"))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// Step 6: PullRequest取得
	if len(payload.CheckSuite.PullRequests) == 0 {
		logger.Info("No pull requests in check_suite, skipping",
			zap.String("delivery_id", deliveryID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
			zap.String("repo", payload.Repository.FullName),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_pr",
			"delivery_id": deliveryID,
		})
		return
	}

	prNumber := payload.CheckSuite.PullRequests[0].Number

	prRepo := deps.PullRequestRepository
	if prRepo == nil {
		prRepo = repositories.NewPullRequestRepository(config.GetDB())
	}

	pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, prNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("PullRequest not found",
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_number", prNumber),
				zap.String("repo", payload.Repository.FullName),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "pr_not_found",
				"delivery_id": deliveryID,
				"pr_number":   prNumber,
			})
			return
		}
		logger.Error("Failed to get PullRequest",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", prNumber),
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

	// Prepare repositories/services
	ciStatusRepo := deps.CIStatusRepository
	if ciStatusRepo == nil {
		ciStatusRepo = repositories.NewCIStatusRepository()
	}

	// Prepare optional GitHub client (used by aggregator/analyzer). If not available, we will skip aggregation.
	githubClient := deps.GitHubClient
	if githubClient == nil && appGitHubClient != nil {
		if rawClient, err := appGitHubClient.ForRepo(ctx, owner, repo); err == nil {
			githubClient = clients.NewFromGitHub(rawClient, logger)
		}
	}

	// Step 7: CIStatus更新

	checkSuiteIDStr := strconv.FormatInt(payload.CheckSuite.ID, 10)
	conclusion := payload.CheckSuite.Conclusion
	now := time.Now()

	ciStatus := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: checkSuiteIDStr,
		Status:       "completed",
		Conclusion:   conclusion,
		CompletedAt:  &now,
	}

	// Note: HeadSHA is not stored in CIStatus model, but we can store it in Logs field if needed
	// For now, we'll skip storing it as it's not in the model schema

	if err := ciStatusRepo.CreateOrUpdate(ciStatus); err != nil {
		logger.Error("Failed to create or update CIStatus",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_id", pr.ID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
		)
		// Continue processing even if CIStatus update fails
	} else {
		logger.Info("CIStatus created or updated",
			zap.Int("ci_status_id", ciStatus.ID),
			zap.Int("pr_id", pr.ID),
			zap.String("delivery_id", deliveryID),
		)
	}

	// Step 7b: 集計（PR単位: 最新HEAD SHA）を保存
	if githubClient != nil {
		aggregator := deps.CIStatusAggregator
		if aggregator == nil {
			aggregator = services.NewCIStatusAggregator(githubClient, ciStatusRepo, logger)
		}
		if _, err := aggregator.AggregateAndStore(ctx, owner, repo, pr.ID, pr.Number, payload.CheckSuite.ID, payload.CheckSuite.HeadSHA); err != nil {
			logger.Warn("Failed to aggregate CI status",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
				zap.Int64("check_suite_id", payload.CheckSuite.ID),
			)
			// non-fatal
		}
	} else {
		logger.Info("Skipping CI aggregation (GitHub client unavailable)",
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_id", pr.ID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
		)
	}

	// Step 8: 結論判定
	if conclusion == nil {
		logger.Warn("Check suite conclusion is nil, skipping processing",
			zap.String("delivery_id", deliveryID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_conclusion",
			"delivery_id": deliveryID,
		})
		return
	}

	// Step 8a: CI失敗時の処理
	if *conclusion == models.CheckSuiteConclusionFailure {
		logger.Info("CI failure detected, processing retry",
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_id", pr.ID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
		)

		// Initialize GitHub client if needed
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

		// Initialize CIFailureAnalyzer
		ciAnalyzer := deps.CIFailureAnalyzer
		if ciAnalyzer == nil {
			ciAnalyzer = services.NewCIFailureAnalyzer(githubClient, logger)
		}

		// Analyze CI failure
		ciResult, err := ciAnalyzer.AnalyzeCIFailure(ctx, owner, repo, payload.CheckSuite.ID)
		if err != nil {
			logger.Error("Failed to analyze CI failure",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int64("check_suite_id", payload.CheckSuite.ID),
			)
			// Continue with retry even if analysis fails
			ciResult = nil
		}

		// Get AgentRun for this PR
		agentRunRepo := deps.AgentRunRepository
		if agentRunRepo == nil {
			agentRunRepo = repositories.NewAgentRunRepository(config.GetDB())
		}

		agentRuns, err := agentRunRepo.GetByPRID(pr.ID)
		if err != nil {
			logger.Error("Failed to get AgentRuns for PR",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
			)
			c.Error(err)
			return
		}

		// Find the latest started or succeeded AgentRun
		var agentRun *models.AgentRun
		for _, ar := range agentRuns {
			if ar.State == "started" || ar.State == "succeeded" {
				if agentRun == nil || ar.UpdatedAt.After(agentRun.UpdatedAt) {
					agentRun = ar
				}
			}
		}

		if agentRun == nil {
			logger.Warn("No suitable AgentRun found for retry",
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "no_agent_run",
				"delivery_id": deliveryID,
			})
			return
		}

		logger.Info("AgentRun found for retry",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("retry_count", agentRun.RetryCount),
			zap.String("state", agentRun.State),
			zap.String("delivery_id", deliveryID),
		)

		// Initialize FeedbackAggregator
		feedbackAggregator := deps.FeedbackAggregator
		if feedbackAggregator == nil {
			feedbackAggregator = services.NewFeedbackAggregator(nil, logger)
		}

		// Aggregate feedback
		aggregatedFeedback, err := feedbackAggregator.AggregateFeedback(ctx, pr.ID, ciResult, agentRun.RetryCount)
		if err != nil {
			logger.Error("Failed to aggregate feedback",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
			)
			c.Error(err)
			return
		}

		// Initialize RetryOrchestrator
		retryOrchestrator := deps.RetryOrchestrator
		if retryOrchestrator == nil {
			issueContextService := deps.IssueContextService
			if issueContextService == nil {
				issueContextService = services.NewIssueContextService(githubClient, logger)
			}
			retryOrchestrator = services.NewRetryOrchestrator(
				agentRunRepo,
				deps.KubernetesJobService,
				issueContextService,
				logger,
			)
		}

		// Check if retry is allowed
		if !retryOrchestrator.ShouldRetry(agentRun) {
			logger.Warn("Max retry count exceeded",
				zap.Int("agent_run_id", agentRun.ID),
				zap.Int("retry_count", agentRun.RetryCount),
				zap.String("delivery_id", deliveryID),
			)
			if err := retryOrchestrator.HandleMaxRetriesExceeded(agentRun); err != nil {
				logger.Error("Failed to handle max retries exceeded",
					zap.Error(err),
					zap.Int("agent_run_id", agentRun.ID),
					zap.String("delivery_id", deliveryID),
				)
				c.Error(err)
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"status":       "max_retries_exceeded",
				"delivery_id":  deliveryID,
				"agent_run_id": agentRun.ID,
			})
			return
		}

		// Get Issue for retry
		issueRepo := deps.IssueRepository
		if issueRepo == nil {
			issueRepo = repositories.NewIssueRepository()
		}

		issue, err := issueRepo.FindByID(agentRun.IssueID)
		if err != nil {
			logger.Error("Failed to get Issue for retry",
				zap.Error(err),
				zap.Int("agent_run_id", agentRun.ID),
				zap.Int("issue_id", agentRun.IssueID),
				zap.String("delivery_id", deliveryID),
			)
			c.Error(err)
			return
		}

		// Trigger retry
		if err := retryOrchestrator.TriggerRetry(ctx, agentRun, issue, aggregatedFeedback); err != nil {
			logger.Error("Failed to trigger retry",
				zap.Error(err),
				zap.Int("agent_run_id", agentRun.ID),
				zap.String("delivery_id", deliveryID),
			)
			c.Error(err)
			return
		}

		logger.Info("Retry triggered successfully",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("new_retry_count", agentRun.RetryCount),
			zap.String("delivery_id", deliveryID),
		)

		c.JSON(http.StatusOK, gin.H{
			"status":       "retry_triggered",
			"delivery_id":  deliveryID,
			"agent_run_id": agentRun.ID,
			"retry_count":  agentRun.RetryCount,
		})
		return
	}

	// Step 8b: CI成功時の処理
	if *conclusion == models.CheckSuiteConclusionSuccess {
		logger.Info("CI success detected",
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_id", pr.ID),
			zap.Int64("check_suite_id", payload.CheckSuite.ID),
		)

		// Initialize GitHub client if needed (for conflict detection and auto-merge)
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

		// Initialize repositories for providers
		prRepo := deps.PullRequestRepository
		if prRepo == nil {
			prRepo = repositories.NewPullRequestRepository(config.GetDB())
		}
		ciRepo := deps.CIStatusRepository
		if ciRepo == nil {
			ciRepo = repositories.NewCIStatusRepository()
		}
		reviewRepo := repositories.NewReviewFeedbackRepository()

		// Build providers and checker
		ciProvider := services.NewCIStatusProvider(ciRepo, prRepo, logger)
		approvalChecker := services.NewCodexApprovalChecker(reviewRepo, prRepo, logger)
		conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
		checker := services.NewMergeConditionChecker(ciProvider, approvalChecker, conflictDetector, logger)

		// Re-evaluate merge conditions
		result, err := checker.Check(ctx, owner, repo, pr.Number)
		if err != nil {
			logger.Error("merge condition check failed",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
			)
			c.Error(err)
			return
		}

		logger.Info("merge condition evaluated",
			zap.String("ci_state", string(result.CIState)),
			zap.Bool("codex_approved", result.CodexApproved),
			zap.String("conflict", string(result.Conflict)),
			zap.Bool("mergeable", result.Mergeable),
			zap.String("delivery_id", deliveryID),
		)

		if !result.Mergeable {
			c.JSON(http.StatusOK, gin.H{
				"status":      "processed",
				"action":      "re_eval",
				"mergeable":   false,
				"delivery_id": deliveryID,
				"pr_number":   prNumber,
				"ci_state":    string(result.CIState),
				"conclusion":  *conclusion,
				"reasons":     result.Reasons,
			})
			return
		}

		// Attempt auto-merge
		autoMergeSvc := deps.AutoMergeService
		if autoMergeSvc == nil {
			autoMergeSvc = services.NewAutoMergeService(appGitHubClient, logger)
		}
		mergeRes, mergeErr := autoMergeSvc.AttemptAutoMerge(ctx, owner, repo, pr.Number)
		if mergeErr != nil {
			logger.Error("auto-merge failed",
				zap.Error(mergeErr),
				zap.String("delivery_id", deliveryID),
				zap.Int("pr_id", pr.ID),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "processed",
				"action":      "re_eval",
				"auto_merge":  "failed",
				"delivery_id": deliveryID,
				"pr_number":   prNumber,
				"error":       mergeErr.Error(),
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
		c.JSON(http.StatusOK, gin.H{
			"status":     "processed",
			"action":     "re_eval",
			"auto_merge": "succeeded",
			"merge_sha": func() string {
				if mergeRes != nil {
					return mergeRes.MergeSHA
				}
				return ""
			}(),
			"delivery_id": deliveryID,
			"pr_number":   prNumber,
		})
		return
	}

	// Other conclusions (cancelled, skipped, neutral) - just acknowledge
	logger.Info("Check suite completed with other conclusion",
		zap.String("delivery_id", deliveryID),
		zap.String("conclusion", *conclusion),
		zap.Int64("check_suite_id", payload.CheckSuite.ID),
	)

	c.JSON(http.StatusOK, gin.H{
		"status":      "processed",
		"delivery_id": deliveryID,
		"pr_number":   prNumber,
		"conclusion":  *conclusion,
	})
}
