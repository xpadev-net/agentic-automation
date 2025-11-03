package services

import (
	"context"
	"fmt"

	"agentic-automation/internal/clients"
	"go.uber.org/zap"
)

// GitHubNotificationService provides GitHub notification functionality
// It wraps the GitHubClient to provide a service-layer abstraction
// for GitHub notifications required by webhook handlers.
type GitHubNotificationService struct {
	githubClient *clients.Client
	logger       *zap.Logger
}

// NewGitHubNotificationService creates a new GitHubNotificationService instance.
// It requires a GitHub client and logger as dependencies.
//
// Parameters:
//   - githubClient: GitHub API client (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *GitHubNotificationService: Initialized service instance
func NewGitHubNotificationService(githubClient *clients.Client, logger *zap.Logger) *GitHubNotificationService {
	if githubClient == nil {
		panic("githubClient is required for GitHubNotificationService")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &GitHubNotificationService{
		githubClient: githubClient,
		logger:       logger,
	}
}

// formatExecutionStartMessage formats a message for agent execution start notification.
// It creates a markdown-formatted message with agent type and run ID.
//
// Parameters:
//   - agentType: Agent type (e.g., "claude-code" or "cursor-agents")
//   - agentRunID: AgentRun ID (integer)
//
// Returns:
//   - string: Formatted message string
func formatExecutionStartMessage(agentType string, agentRunID int) string {
	return fmt.Sprintf(`🤖 Agent execution started

**Agent Type**: %s
**Run ID**: %d

Processing your request...`, agentType, agentRunID)
}

// PostExecutionStartComment posts a comment on a GitHub Issue to notify that
// agent execution has started. This is a blocking method - errors are returned
// to the caller for handling.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - issueNumber: Issue number (integer)
//   - agentType: Agent type (e.g., "claude-code" or "cursor-agents")
//   - agentRunID: AgentRun ID (integer)
//
// Returns:
//   - error: Error if comment posting failed (GitHub API error, network error, etc.)
func (s *GitHubNotificationService) PostExecutionStartComment(ctx context.Context, owner, repo string, issueNumber int, agentType string, agentRunID int) error {
	s.logger.Info("Posting GitHub execution start comment",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.String("agent_type", agentType),
		zap.Int("agent_run_id", agentRunID),
	)

	// Format the message
	message := formatExecutionStartMessage(agentType, agentRunID)

	// Post comment via GitHub API
	_, err := s.githubClient.CreateIssueComment(ctx, owner, repo, issueNumber, message)
	if err != nil {
		s.logger.Error("Failed to post GitHub execution start comment",
			zap.Error(err),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
			zap.String("agent_type", agentType),
			zap.Int("agent_run_id", agentRunID),
		)
		return err
	}

	s.logger.Info("GitHub execution start comment posted successfully",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.String("agent_type", agentType),
		zap.Int("agent_run_id", agentRunID),
	)

	return nil
}
