package handlers

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const issuesDeliveryHeader = "X-GitHub-Delivery"

// Interfaces for dependency injection (kept minimal for testability)
type IssuesAuthorization interface {
	CheckPermission(ctx context.Context, owner, repo, username string) (bool, error)
}

type IssueDependencyFetcher interface {
	Fetch(ctx context.Context, owner, repo string, issueNumber int) error
}

type BlockerGraphBuilder interface {
	UpdateFromIssueEvent(ctx context.Context, owner, repo string, issueNumber int, action string) error
}

type BlockedTaskResolver interface {
	ResolveAndMaybeTrigger(ctx context.Context, owner, repo string, issueNumber int) error
}

// IssuesDeps represents injectable dependencies for issues webhook handler
type IssuesDeps struct {
	Logger               *zap.Logger
	GitHubClient         *clients.Client
	AuthorizationService IssuesAuthorization
	DependencyFetcher    IssueDependencyFetcher
	GraphBuilder         BlockerGraphBuilder
	BlockedTaskResolver  BlockedTaskResolver
}

// IssuesPayload represents the GitHub webhook payload for issues events
type IssuesPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// HandleIssues is the production entrypoint for issues webhook
func HandleIssues(c *gin.Context) {
	logger := config.GetLogger()

	deps := IssuesDeps{
		Logger: logger,
	}

	// Defer GitHub client and services wiring to WithDeps after owner/repo resolution
	HandleIssuesWithDeps(c, deps)
}

// HandleIssuesWithDeps handles issues webhook with injected dependencies
func HandleIssuesWithDeps(c *gin.Context, deps IssuesDeps) {
	logger := deps.Logger
	if logger == nil {
		logger = config.GetLogger()
	}

	// Delivery ID (idempotency key)
	deliveryID := c.GetHeader(issuesDeliveryHeader)
	if deliveryID == "" {
		logger.Warn("Missing X-GitHub-Delivery header",
			zap.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

	ctx := c.Request.Context()

	// Parse payload
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

	var payload IssuesPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse issues webhook payload",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Filter actions: only closed/reopened
	if payload.Action != models.IssuesActionClosed && payload.Action != models.IssuesActionReopened {
		logger.Info("Ignoring issues action",
			zap.String("action", payload.Action),
			zap.String("delivery_id", deliveryID),
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "ignored",
			"action":      payload.Action,
			"delivery_id": deliveryID,
		})
		return
	}

	// Extract owner/repo
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

	logger.Info("Issues event received",
		zap.String("delivery_id", deliveryID),
		zap.String("action", payload.Action),
		zap.String("repo", payload.Repository.FullName),
		zap.Int("issue_number", payload.Issue.Number),
		zap.String("sender", payload.Sender.Login),
	)

	// Ensure GitHub client and authorization service
	authorizationService := deps.AuthorizationService
	if deps.GitHubClient == nil || authorizationService == nil {
		if appGitHubClient == nil {
			// Attempt lazy init (mirror of issue_comment handler behavior)
			ghApp, err := clients.NewGitHubAppClient(logger)
			if err == nil {
				appGitHubClient = ghApp
			} else {
				logger.Warn("GitHub App client not initialized", zap.Error(err))
			}
		}
		if appGitHubClient != nil && deps.GitHubClient == nil {
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
		if authorizationService == nil && deps.GitHubClient != nil {
			authorizationService = services.NewAuthorizationService(deps.GitHubClient, logger)
		}
	}

	// Permission check (Collaborator+ for sender)
	if authorizationService != nil {
		hasPermission, err := authorizationService.CheckPermission(ctx, owner, repo, payload.Sender.Login)
		if err != nil {
			logger.Error("Failed to check user permission",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
				zap.String("user", payload.Sender.Login),
				zap.String("repo", payload.Repository.FullName),
			)
			c.Error(err)
			return
		}
		if !hasPermission {
			logger.Warn("User lacks permission for issues event processing",
				zap.String("delivery_id", deliveryID),
				zap.String("user", payload.Sender.Login),
				zap.String("repo", payload.Repository.FullName),
				zap.Int("issue_number", payload.Issue.Number),
			)
			c.JSON(http.StatusOK, gin.H{
				"status":      "permission_denied",
				"delivery_id": deliveryID,
			})
			return
		}
	}

	// Call downstream services if provided
	if deps.DependencyFetcher != nil {
		if err := deps.DependencyFetcher.Fetch(ctx, owner, repo, payload.Issue.Number); err != nil {
			logger.Error("Dependency fetch failed",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
			)
			c.JSON(http.StatusAccepted, gin.H{
				"status":      "accepted_with_errors",
				"delivery_id": deliveryID,
				"stage":       "dependency_fetch",
			})
			return
		}
	}

	if deps.GraphBuilder != nil {
		if err := deps.GraphBuilder.UpdateFromIssueEvent(ctx, owner, repo, payload.Issue.Number, payload.Action); err != nil {
			logger.Error("Blocker graph update failed",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
			)
			c.JSON(http.StatusAccepted, gin.H{
				"status":      "accepted_with_errors",
				"delivery_id": deliveryID,
				"stage":       "graph_update",
			})
			return
		}
	}

	if deps.BlockedTaskResolver != nil {
		if err := deps.BlockedTaskResolver.ResolveAndMaybeTrigger(ctx, owner, repo, payload.Issue.Number); err != nil {
			logger.Error("Blocked task resolve/trigger failed",
				zap.Error(err),
				zap.String("delivery_id", deliveryID),
			)
			c.JSON(http.StatusAccepted, gin.H{
				"status":      "accepted_with_errors",
				"delivery_id": deliveryID,
				"stage":       "resolve_and_trigger",
			})
			return
		}
	}

	logger.Info("Issues event processed",
		zap.String("delivery_id", deliveryID),
		zap.String("action", payload.Action),
		zap.String("repo", payload.Repository.FullName),
		zap.Int("issue_number", payload.Issue.Number),
	)

	c.JSON(http.StatusOK, gin.H{
		"status":      "processed",
		"delivery_id": deliveryID,
	})
}
