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

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
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

// AssignmentRunner starts the existing issue-comment execution flow. It is a
// function type so assignment handling can be tested without starting a
// Kubernetes client/job.
type AssignmentRunner func(*gin.Context)

// IssuesDeps represents injectable dependencies for issues webhook handler
type IssuesDeps struct {
	Logger               *config.AppLogger
	GitHubClient         *clients.Client
	AuthorizationService IssuesAuthorization
	DependencyFetcher    IssueDependencyFetcher
	GraphBuilder         BlockerGraphBuilder
	BlockedTaskResolver  BlockedTaskResolver
	AssignmentRunner     AssignmentRunner
}

// IssuesPayload represents the GitHub webhook payload for issues events
type IssuesPayload struct {
	Action string `json:"action"`
	Issue  struct {
		ID     int64   `json:"id"`
		Number int     `json:"number"`
		Title  string  `json:"title"`
		Body   *string `json:"body"`
		State  string  `json:"state"`
	} `json:"issue"`
	Assignee *struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"assignee"`
	Assignees []struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"assignees"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// assignmentBotConfigured reports whether an assignee is the configured agent bot.
// AGENT_ASSIGNMENT_BOT_* is intentionally separate from CODEX_BOT_* so deployments
// can use assignment-triggered automation with a bot other than Codex.
func assignmentBotConfigured(login string, userID int64) bool {
	configuredLogin := config.GetEnv("AGENT_ASSIGNMENT_BOT_USERNAME", config.GetEnv("CODEX_BOT_USERNAME", "codex-bot"))
	configuredID, _ := strconv.ParseInt(config.GetEnv("AGENT_ASSIGNMENT_BOT_USER_ID", config.GetEnv("CODEX_BOT_USER_ID", "0")), 10, 64)

	if configuredID != 0 && userID != 0 && configuredID == userID {
		return true
	}

	normalize := func(value string) string {
		return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), "[bot]")
	}
	return configuredLogin != "" && normalize(login) == normalize(configuredLogin)
}

func assignmentRepositoryAllowed(fullName string) bool {
	configuredRepo := strings.TrimSpace(config.GetEnv("AGENT_ASSIGNMENT_REPOSITORY", ""))
	return configuredRepo == "" || strings.EqualFold(configuredRepo, fullName)
}

func assignmentTarget(payload IssuesPayload) (string, int64, bool) {
	if payload.Assignee != nil && assignmentBotConfigured(payload.Assignee.Login, payload.Assignee.ID) {
		return payload.Assignee.Login, payload.Assignee.ID, true
	}
	for _, assignee := range payload.Assignees {
		if assignmentBotConfigured(assignee.Login, assignee.ID) {
			return assignee.Login, assignee.ID, true
		}
	}
	return "", 0, false
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
			config.String("path", c.Request.URL.Path),
		)
		c.Error(errors.New("missing X-GitHub-Delivery header"))
		return
	}

	ctx := c.Request.Context()

	// Parse payload
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

	var payload IssuesPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		logger.Error("Failed to parse issues webhook payload",
			config.Error(err),
			config.String("delivery_id", deliveryID),
		)
		c.Error(err)
		return
	}

	// Assignment of the configured bot is an automatic equivalent of /run-agent.
	// Keep it on the same handler path so context collection, state transitions,
	// job creation, notifications, and failure handling remain identical.
	if payload.Action == models.IssuesActionAssigned {
		if payload.Issue.State != "open" || !assignmentRepositoryAllowed(payload.Repository.FullName) {
			logger.Info("Ignoring assignment event", config.String("delivery_id", deliveryID), config.String("repo", payload.Repository.FullName), config.String("issue_state", payload.Issue.State))
			c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "assignment_not eligible", "delivery_id": deliveryID})
			return
		}

		login, userID, matched := assignmentTarget(payload)
		if !matched {
			logger.Info("Ignoring assignment to non-agent user", config.String("delivery_id", deliveryID), config.Int("issue_number", payload.Issue.Number))
			c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "assignee_not_agent_bot", "delivery_id": deliveryID})
			return
		}

		issueRepo := repositories.NewIssueRepository()
		runRepo := repositories.NewAgentRunRepository(config.GetDB())
		// The idempotency middleware deliberately does not reserve AgentRuns for
		// issues events. Upsert the issue here, after eligibility checks, so a
		// non-bot assignment cannot affect execution state.
		if err := issueRepo.UpsertSelective(payload.Repository.FullName, payload.Issue.Number, map[string]interface{}{
			"title": payload.Issue.Title,
			"state": payload.Issue.State,
		}); err != nil {
			logger.Error("Failed to upsert assignment Issue", config.Error(err), config.String("delivery_id", deliveryID))
			c.Error(err)
			return
		}
		issue, err := issueRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.Issue.Number)
		if err != nil {
			logger.Error("Failed to load assignment Issue", config.Error(err), config.String("delivery_id", deliveryID))
			c.Error(err)
			return
		}

		activeRuns, err := runRepo.GetByIssueID(issue.ID)
		if err != nil {
			logger.Error("Failed to check active AgentRuns for assignment", config.Error(err), config.Int("issue_id", issue.ID))
			c.Error(err)
			return
		}
		for _, run := range activeRuns {
			if run.ID != currentRun.ID && (run.State == "queued" || run.State == "started") {
				logger.Info("Skipping assignment because an AgentRun is already active", config.Int("issue_id", issue.ID), config.Int("agent_run_id", run.ID), config.String("delivery_id", deliveryID))
				c.JSON(http.StatusOK, gin.H{"status": "already_running", "agent_run_id": run.ID, "delivery_id": deliveryID})
				return
			}
		}

		currentRun, isNew, err := runRepo.CreateOrGet(deliveryID, &models.AgentRun{
			IssueID: issue.ID,
			State:   "queued",
			Input:   datatypes.JSON([]byte("{}")),
			Output:  datatypes.JSON([]byte("{}")),
		})
		if err != nil {
			logger.Error("Failed to reserve assignment AgentRun", config.Error(err), config.String("delivery_id", deliveryID))
			c.Error(err)
			return
		}
		if !isNew {
			logger.Info("Assignment webhook already processed", config.String("delivery_id", deliveryID), config.Int("agent_run_id", currentRun.ID))
			c.JSON(http.StatusOK, gin.H{"status": "already_processed", "agent_run_id": currentRun.ID, "delivery_id": deliveryID})
			return
		}

		synthetic := IssueCommentPayload{
			Action:     models.IssueCommentActionCreated,
			Issue:      IssueCommentIssue{ID: int(payload.Issue.ID), Number: payload.Issue.Number, Title: payload.Issue.Title, Body: payload.Issue.Body, State: payload.Issue.State},
			Comment:    IssueCommentComment{Body: "/run-agent", User: User{Login: login, ID: userID}},
			Repository: IssueCommentRepository{FullName: payload.Repository.FullName},
		}
		// Preserve the human actor for the existing permission check. The bot is
		// the trigger target, while the person who assigned it is the requester.
		synthetic.Comment.User = User{Login: payload.Sender.Login}
		syntheticBytes, err := json.Marshal(synthetic)
		if err != nil {
			c.Error(err)
			return
		}
		c.Set("webhook_payload", syntheticBytes)
		logger.Info("Starting agent from bot assignment", config.String("delivery_id", deliveryID), config.String("assignee", login), config.Int64("assignee_id", userID), config.Int("issue_number", payload.Issue.Number))
		runner := deps.AssignmentRunner
		if runner == nil {
			runner = HandleIssueComment
		}
		runner(c)
		return
	}

	// Filter actions: only closed/reopened
	if payload.Action != models.IssuesActionClosed && payload.Action != models.IssuesActionReopened {
		logger.Info("Ignoring issues action",
			config.String("action", payload.Action),
			config.String("delivery_id", deliveryID),
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
			config.String("full_name", payload.Repository.FullName),
			config.String("delivery_id", deliveryID),
		)
		c.Error(errors.New("invalid repository full name format"))
		return
	}
	owner := repoParts[0]
	repo := repoParts[1]

	logger.Info("Issues event received",
		config.String("delivery_id", deliveryID),
		config.String("action", payload.Action),
		config.String("repo", payload.Repository.FullName),
		config.Int("issue_number", payload.Issue.Number),
		config.String("sender", payload.Sender.Login),
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
				logger.Warn("GitHub App client not initialized", config.Error(err))
			}
		}
		if appGitHubClient != nil && deps.GitHubClient == nil {
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
		if authorizationService == nil && deps.GitHubClient != nil {
			authorizationService = services.NewAuthorizationService(deps.GitHubClient, logger)
		}
	}

	// Permission check (Collaborator+ for sender)
	if authorizationService != nil {
		hasPermission, err := authorizationService.CheckPermission(ctx, owner, repo, payload.Sender.Login)
		if err != nil {
			logger.Error("Failed to check user permission",
				config.Error(err),
				config.String("delivery_id", deliveryID),
				config.String("user", payload.Sender.Login),
				config.String("repo", payload.Repository.FullName),
			)
			c.Error(err)
			return
		}
		if !hasPermission {
			logger.Warn("User lacks permission for issues event processing",
				config.String("delivery_id", deliveryID),
				config.String("user", payload.Sender.Login),
				config.String("repo", payload.Repository.FullName),
				config.Int("issue_number", payload.Issue.Number),
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
				config.Error(err),
				config.String("delivery_id", deliveryID),
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
				config.Error(err),
				config.String("delivery_id", deliveryID),
			)
			c.JSON(http.StatusAccepted, gin.H{
				"status":      "accepted_with_errors",
				"delivery_id": deliveryID,
				"stage":       "graph_update",
			})
			return
		}
	}

	// T128: ブロック解除タスクの自動起動
	if deps.BlockedTaskResolver == nil {
		// Wire default resolver via adapter
		issueRepo := repositories.NewIssueRepository()
		edgesRepo := repositories.NewBlockerGraphRepository()
		agentRunRepo := repositories.NewAgentRunRepository(config.GetDB())
		serviceResolver := services.NewBlockedTaskResolver(issueRepo, edgesRepo, agentRunRepo)
		deps.BlockedTaskResolver = NewBlockedTaskResolverAdapter(serviceResolver, issueRepo)
	}
	if deps.BlockedTaskResolver != nil {
		if err := deps.BlockedTaskResolver.ResolveAndMaybeTrigger(ctx, owner, repo, payload.Issue.Number); err != nil {
			logger.Error("Blocked task resolve/trigger failed",
				config.Error(err),
				config.String("delivery_id", deliveryID),
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
		config.String("delivery_id", deliveryID),
		config.String("action", payload.Action),
		config.String("repo", payload.Repository.FullName),
		config.Int("issue_number", payload.Issue.Number),
	)

	c.JSON(http.StatusOK, gin.H{
		"status":      "processed",
		"delivery_id": deliveryID,
	})
}
