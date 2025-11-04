package services

import (
	"context"
	"fmt"
	"strings"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"agentic-automation/internal/utils"
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

// getErrorMessageForNotification returns the error message for notification,
// or a default message if the error message is nil or empty.
//
// Parameters:
//   - errorMessage: Pointer to error message string (can be nil)
//
// Returns:
//   - string: Error message or "No error details available" if nil/empty
func getErrorMessageForNotification(errorMessage *string) string {
	if errorMessage == nil || *errorMessage == "" {
		return "No error details available"
	}
	return *errorMessage
}

// -----------------------------------------------------------------------------
// PR Created Notification (US2 T083)
// -----------------------------------------------------------------------------

const (
	prCreatedMarkerPrefix  = "<!-- agent:pr-created:"
	maxRetriesMarkerPrefix = "<!-- agent:max-retries:"
	prCreatedTemplate      = "PR created: #%d (%s) branch=%s sha=%s"
	maxCommentsToScan      = 30
)

func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

func makePRCreatedBody(prNumber int, prURL, branch, sha, idempotencyKey string) string {
	marker := prCreatedMarkerPrefix + idempotencyKey + " -->"
	return marker + "\n" + fmt.Sprintf(prCreatedTemplate, prNumber, prURL, branch, shortSHA(sha))
}

// makeMaxRetriesBody formats a message body for max retries exceeded notification.
// It includes an idempotency marker and formatted message with error details.
//
// Parameters:
//   - agentRun: AgentRun instance with retry count, error message, and other details
//   - idempotencyKey: Idempotency key for deduplication
//
// Returns:
//   - string: Formatted message body with marker and notification content
func makeMaxRetriesBody(agentRun *models.AgentRun, idempotencyKey string) string {
	marker := maxRetriesMarkerPrefix + idempotencyKey + " -->"

	message := fmt.Sprintf(`❌ Maximum retry limit (50) reached. Manual intervention required.

**Total Attempts**: %d
**Agent Type**: %s
**Agent Run ID**: %d`, agentRun.RetryCount, agentRun.AgentType, agentRun.ID)

	errorMsg := getErrorMessageForNotification(agentRun.ErrorMessage)
	if errorMsg != "No error details available" {
		message += fmt.Sprintf("\n\n**Last Error**:\n%s", errorMsg)
	}

	return marker + "\n" + message
}

// hasCommentWithMarker checks if a recent comment contains the given marker.
func (s *GitHubNotificationService) hasCommentWithMarker(ctx context.Context, owner, repo string, number int, marker string) (bool, error) {
	comments, err := s.githubClient.ListIssueComments(ctx, owner, repo, number)
	if err != nil {
		return false, err
	}
	// Scan only the last N comments to limit API/CPU
	start := 0
	if len(comments) > maxCommentsToScan {
		start = len(comments) - maxCommentsToScan
	}
	for i := len(comments) - 1; i >= start; i-- { // newest first within the window
		c := comments[i]
		if c == nil || c.Body == nil {
			continue
		}
		if *c.Body != "" && marker != "" && (contains(*c.Body, marker)) {
			return true, nil
		}
	}
	return false, nil
}

// contains is a small wrapper to avoid importing strings repeatedly in call sites
func contains(s, substr string) bool { return strings.Contains(s, substr) }

// postComment posts a comment with retry/backoff
func (s *GitHubNotificationService) postComment(ctx context.Context, owner, repo string, number int, body string) error {
	return utils.Retry(ctx, func() error {
		_, err := s.githubClient.CreateIssueComment(ctx, owner, repo, number, body)
		return err
	}, nil, s.logger)
}

// NotifyPRCreated posts a status comment to both the originating Issue and the PR.
// It is idempotent per idempotencyKey; if a marker is already present, posting is skipped.
func (s *GitHubNotificationService) NotifyPRCreated(
	ctx context.Context,
	owner, repo string,
	issueNumber, prNumber int,
	prURL, branch, sha, idempotencyKey string,
) error {
	s.logger.Info("Posting PR created notifications",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
		zap.String("pr_url", prURL),
		zap.String("branch", branch),
		zap.String("sha", sha),
		zap.String("idempotency_key", idempotencyKey),
	)

	if prNumber <= 0 || prURL == "" {
		s.logger.Warn("Missing PR info; skipping PR created notifications",
			zap.Int("pr_number", prNumber),
			zap.String("pr_url", prURL),
		)
		return nil
	}

	body := makePRCreatedBody(prNumber, prURL, branch, sha, idempotencyKey)
	marker := prCreatedMarkerPrefix + idempotencyKey + " -->"

	var aggErr error
	// Post to Issue thread
	if issueNumber > 0 {
		exists, err := s.hasCommentWithMarker(ctx, owner, repo, issueNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan issue comments for marker", zap.Error(err), zap.Int("issue_number", issueNumber))
			aggErr = err
		} else if !exists {
			if err := s.postComment(ctx, owner, repo, issueNumber, body); err != nil {
				s.logger.Warn("Failed to post issue comment for PR created", zap.Error(err), zap.Int("issue_number", issueNumber))
				aggErr = err
			}
		}
	}

	// Post to PR thread (PR number can be used with Issues API)
	if prNumber > 0 {
		exists, err := s.hasCommentWithMarker(ctx, owner, repo, prNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan PR comments for marker", zap.Error(err), zap.Int("pr_number", prNumber))
			if aggErr == nil {
				aggErr = err
			}
		} else if !exists {
			if err := s.postComment(ctx, owner, repo, prNumber, body); err != nil {
				s.logger.Warn("Failed to post PR comment for PR created", zap.Error(err), zap.Int("pr_number", prNumber))
				if aggErr == nil {
					aggErr = err
				}
			}
		}
	}

	if aggErr != nil {
		return aggErr
	}

	s.logger.Info("PR created notifications posted (or already present)",
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
	)
	return nil
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

// -----------------------------------------------------------------------------
// Max Retries Exceeded Notification (US3 T100)
// -----------------------------------------------------------------------------

// NotifyMaxRetriesExceeded posts a notification comment to a GitHub Issue when
// the maximum retry limit (50) has been reached. It is idempotent per idempotencyKey;
// if a marker is already present, posting is skipped.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - agentRun: AgentRun instance with retry count, error message, and idempotency key
//   - issue: Issue instance with repository information and issue number
//
// Returns:
//   - error: Error if comment posting failed (GitHub API error, network error, etc.)
func (s *GitHubNotificationService) NotifyMaxRetriesExceeded(
	ctx context.Context,
	agentRun *models.AgentRun,
	issue *models.Issue,
) error {
	// Parameter validation
	if agentRun == nil {
		s.logger.Error("agentRun is required for NotifyMaxRetriesExceeded")
		return fmt.Errorf("agentRun is required")
	}
	if issue == nil {
		s.logger.Error("issue is required for NotifyMaxRetriesExceeded")
		return fmt.Errorf("issue is required")
	}

	// Check if IdempotencyKey is empty
	if agentRun.IdempotencyKey == "" {
		s.logger.Warn("IdempotencyKey is empty for max retries notification",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("issue_id", issue.ID),
		)
	}

	// Split Issue.Repo into owner and repo
	parts := strings.SplitN(issue.Repo, "/", 2)
	if len(parts) != 2 {
		s.logger.Error("Invalid repo format",
			zap.String("repo", issue.Repo),
			zap.Int("issue_id", issue.ID),
		)
		return fmt.Errorf("invalid repo format: %s", issue.Repo)
	}
	owner := parts[0]
	repo := parts[1]

	// Log processing start
	s.logger.Info("Posting max retries exceeded notification",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issue.Number),
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
		zap.String("idempotency_key", agentRun.IdempotencyKey),
	)

	// Generate idempotency marker
	marker := maxRetriesMarkerPrefix + agentRun.IdempotencyKey + " -->"

	// Check for existing comment with marker
	exists, err := s.hasCommentWithMarker(ctx, owner, repo, issue.Number, marker)
	if err != nil {
		s.logger.Warn("Failed to scan issue comments for marker",
			zap.Error(err),
			zap.Int("issue_number", issue.Number),
		)
		// Continue processing even if scan fails
	} else if exists {
		s.logger.Info("Max retries notification already exists, skipping",
			zap.Int("issue_number", issue.Number),
		)
		return nil
	}

	// Generate comment body
	body := makeMaxRetriesBody(agentRun, agentRun.IdempotencyKey)

	// Post comment
	if err := s.postComment(ctx, owner, repo, issue.Number, body); err != nil {
		s.logger.Error("Failed to post max retries notification",
			zap.Error(err),
			zap.Int("issue_number", issue.Number),
		)
		return err
	}

	// Log success
	s.logger.Info("Max retries notification posted successfully",
		zap.Int("issue_number", issue.Number),
		zap.Int("agent_run_id", agentRun.ID),
	)

	return nil
}
