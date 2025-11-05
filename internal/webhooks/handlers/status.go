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
	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
)

// StatusPayload represents GitHub status event payload (subset used by our handler)
type StatusPayload struct {
	State      string `json:"state"`
	Sha        string `json:"sha"`
	Context    string `json:"context"`
	TargetURL  string `json:"target_url"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Branches []struct {
		Name string `json:"name"`
	} `json:"branches"`
}

// StatusDeps contains injectable dependencies for Status handler
type StatusDeps struct {
	Logger           *zap.Logger
	GitHubAppClient  *clients.GitHubClient
	PullRequestRepo  *repositories.PullRequestRepository
	CIStatusRepo     *repositories.CIStatusRepository
	Aggregator       services.CIStatusAggregator
	MergeChecker     services.MergeConditionChecker
	AutoMergeService services.AutoMergeService
}

// HandleStatus handles GitHub status webhook events
func HandleStatus(c *gin.Context) {
	deps := StatusDeps{Logger: config.GetLogger()}
	HandleStatusWithDeps(c, deps)
}

// HandleStatusWithDeps handles status webhook with injected dependencies
func HandleStatusWithDeps(c *gin.Context, deps StatusDeps) {
	logger := deps.Logger
	if logger == nil {
		logger = config.GetLogger()
	}

	var ctx context.Context = c.Request.Context()

	deliveryID := c.GetHeader(deliveryHeader)
	if deliveryID == "" {
		logger.Warn("Missing X-GitHub-Delivery header",
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

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

	var payload StatusPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse status payload",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	logger.Info("Received status webhook",
		zap.String("delivery_id", deliveryID),
		zap.String("state", payload.State),
		zap.String("context", payload.Context),
		zap.String("sha", payload.Sha),
		zap.String("repo", payload.Repository.FullName),
	)

	// Validate minimal fields
	if payload.State == "" || payload.Sha == "" || payload.Repository.FullName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_payload",
			"message": "missing required fields: state/sha/repository.full_name",
		})
		return
	}

	repoParts := strings.Split(payload.Repository.FullName, "/")
	if len(repoParts) != 2 {
		c.Error(errors.New("invalid repository full name format"))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// Repos & services
	db := config.GetDB()
	prRepo := deps.PullRequestRepo
	if prRepo == nil {
		prRepo = repositories.NewPullRequestRepository(db)
	}
	ciRepo := deps.CIStatusRepo
	if ciRepo == nil {
		ciRepo = repositories.NewCIStatusRepository()
	}

	// Prepare GitHub client for repo
	ghApp := deps.GitHubAppClient
	if ghApp == nil {
		if appGitHubClient == nil {
			if g, err := clients.NewGitHubAppClient(logger); err == nil {
				appGitHubClient = g
			}
		}
		ghApp = appGitHubClient
	}
	var gh *github.Client
	if ghApp != nil {
		if g, err := ghApp.ForRepo(ctx, owner, repo); err == nil {
			gh = g
		} else {
			logger.Warn("Failed to init repo GitHub client", zap.Error(err))
		}
	}

	// Resolve PRs by commit SHA using GitHub API (fallback: by branches head if provided)
	var prNumber int
	if gh != nil {
		prs, resp, err := gh.Repositories.ListPullRequestsWithCommit(ctx, owner, repo, payload.Sha, &github.ListOptions{PerPage: 50})
		if err == nil && len(prs) > 0 {
			prNumber = prs[0].GetNumber()
			config.GetLogger().Debug("PR resolved from commit",
				zap.Int("pr_number", prNumber),
				zap.String("sha", payload.Sha),
			)
		} else {
			if resp != nil {
				config.GetLogger().Debug("No PR found for commit", zap.String("sha", payload.Sha))
			}
		}
	}

	// If PR not found via API, try local DB using branches info (best-effort)
	if prNumber == 0 && len(payload.Branches) > 0 {
		// Find the first branch name; repository stores PRs with Repo full name
		branch := payload.Branches[0].Name
		if branch != "" {
			if pr, err := prRepo.FindOpenByRepoAndBranch(payload.Repository.FullName, branch); err == nil && pr != nil {
				prNumber = pr.Number
			}
		}
	}

	if prNumber == 0 {
		// Not strictly an error; status events may target branches without PRs
		c.JSON(http.StatusOK, gin.H{
			"status":      "no_pr_for_commit",
			"delivery_id": deliveryID,
		})
		return
	}

	pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, prNumber)
	if err != nil {
		logger.Error("Failed to load PR from repository",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
			zap.Int("pr_number", prNumber),
		)
		c.Error(err)
		return
	}

	// Supplementary CI signal persistence (status as supplementary)
	// Map state to CIStatus fields
	status := "in_progress"
	var conclusion *string
	if payload.State == models.StatusStateSuccess {
		status = "completed"
		v := "success"
		conclusion = &v
	} else if payload.State == models.StatusStateFailure || payload.State == models.StatusStateError {
		status = "completed"
		v := "failure"
		conclusion = &v
	}

	ci := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "", // not a check_suite; leave empty
		CheckRunID:   nil,
		Name:         payload.Context,
		Status:       status,
		Conclusion:   conclusion,
		LogsURL:      nil, // not available in status; TargetURL may point to details page
	}
	if payload.TargetURL != "" {
		u := payload.TargetURL
		ci.LogsURL = &u
	}
	if err := ciRepo.CreateOrUpdate(ci); err != nil {
		logger.Warn("Failed to upsert supplementary CI status",
			zap.Error(err),
			zap.Int("pr_id", pr.ID),
			zap.String("context", payload.Context),
			zap.String("delivery_id", deliveryID),
		)
		// non-fatal
	}

	// Aggregate supplementary signal (optional): expose via aggregator method when available
	aggregator := deps.Aggregator
	if aggregator != nil {
		if err := aggregator.AddStatusSignal(ctx, pr.ID, payload.Context, payload.State, payload.TargetURL); err != nil {
			logger.Debug("AddStatusSignal failed", zap.Error(err))
		}
	}

	// On success state, re-evaluate merge conditions and attempt auto-merge
	if payload.State == models.StatusStateSuccess {
		mergeChecker := deps.MergeChecker
		if mergeChecker != nil {
			result, err := mergeChecker.Check(ctx, owner, repo, pr.Number)
			if err != nil {
				logger.Warn("Merge condition evaluation failed",
					zap.Error(err),
					zap.String("delivery_id", deliveryID),
					zap.Int("pr_number", pr.Number),
				)
			} else if result.Mergeable {
				autoMerge := deps.AutoMergeService
				if autoMerge != nil {
					if _, err := autoMerge.AttemptAutoMerge(ctx, owner, repo, pr.Number); err != nil {
						logger.Warn("Auto-merge attempt failed",
							zap.Error(err),
							zap.String("delivery_id", deliveryID),
							zap.Int("pr_number", pr.Number),
						)
					} else {
						logger.Info("Auto-merge attempted",
							zap.Int("pr_number", pr.Number),
							zap.String("delivery_id", deliveryID),
						)
					}
				} else {
					logger.Info("Auto-merge service not configured; skipping merge attempt",
						zap.Int("pr_number", pr.Number),
						zap.String("delivery_id", deliveryID),
					)
				}
			}
		} else {
			logger.Info("Merge condition checker not configured; skipping evaluation",
				zap.Int("pr_number", pr.Number),
				zap.String("delivery_id", deliveryID),
			)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      "processed",
		"delivery_id": deliveryID,
		"pr_number":   pr.Number,
		"state":       payload.State,
	})
}
