package services

import (
	"context"
	"fmt"
	"strings"

	"agentic-automation/internal/clients"
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

// -----------------------------------------------------------------------------
// PR Created Notification (US2 T083)
// -----------------------------------------------------------------------------

const (
	prCreatedMarkerPrefix = "<!-- agent:pr-created:"
	prCreatedTemplate     = "PR created: #%d (%s) branch=%s sha=%s"
	maxCommentsToScan     = 30
)

// Retry Progress Notification (US3 T102)
const (
	retryProgressMarkerPrefix = "<!-- agent:retry-progress:"
	MaxRetryAttempts          = 50 // Exported for use in handlers
	errorReasonMaxLength      = 100
	progressBarLength         = 10 // 進捗バーの文字数
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

// hasCommentWithMarker checks if a recent comment contains the given marker.
func (s *GitHubNotificationService) hasCommentWithMarker(ctx context.Context, owner, repo string, number int, marker string) (bool, error) {
	commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, number, marker)
	if err != nil {
		return false, err
	}
	return found && commentID != nil, nil
}

// FindCommentWithMarker finds a recent comment with the given marker and returns its ID.
// It searches from newest to oldest within the scan window.
// Exported for testing purposes.
//
// Parameters:
//   - ctx: Context for cancellation
//   - owner: Repository owner
//   - repo: Repository name
//   - number: Issue or PR number
//   - marker: HTML comment marker to search for
//
// Returns:
//   - *int64: Comment ID if found, nil otherwise
//   - bool: true if found, false otherwise
//   - error: Error if comment listing failed
func (s *GitHubNotificationService) FindCommentWithMarker(ctx context.Context, owner, repo string, number int, marker string) (*int64, bool, error) {
	comments, err := s.githubClient.ListIssueComments(ctx, owner, repo, number)
	if err != nil {
		return nil, false, err
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
		if *c.Body != "" && marker != "" && contains(*c.Body, marker) {
			if c.ID != nil {
				return c.ID, true, nil
			}
		}
	}
	return nil, false, nil
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

// updateComment updates an existing comment with retry/backoff
func (s *GitHubNotificationService) updateComment(ctx context.Context, owner, repo string, commentID int64, body string) error {
	return utils.Retry(ctx, func() error {
		_, err := s.githubClient.UpdateIssueComment(ctx, owner, repo, commentID, body)
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
// Retry Progress Notification (US3 T102)
// -----------------------------------------------------------------------------

// TruncateErrorReason truncates an error message to the specified maximum length.
// If maxLength is 0 or negative, it defaults to errorReasonMaxLength (100).
// Exported for testing purposes.
//
// Parameters:
//   - errorReason: The error message to truncate
//   - maxLength: Maximum length (defaults to errorReasonMaxLength if <= 0)
//
// Returns:
//   - string: Truncated error message with "..." suffix if truncated
func TruncateErrorReason(errorReason string, maxLength int) string {
	if maxLength <= 0 {
		maxLength = errorReasonMaxLength
	}
	if len(errorReason) <= maxLength {
		return errorReason
	}
	return errorReason[:maxLength] + "..."
}

// FormatRetryProgressMessage formats a retry progress notification message.
// It includes a progress bar, percentage, and error reason summary.
// Exported for testing purposes.
//
// Parameters:
//   - retryCount: Current retry count (must be >= 0)
//   - maxRetries: Maximum retry attempts (must be > 0)
//   - errorReason: Error message to include (will be truncated)
//   - marker: HTML comment marker for idempotency
//
// Returns:
//   - string: Formatted markdown message
func FormatRetryProgressMessage(retryCount, maxRetries int, errorReason, marker string) string {
	// Calculate progress bar
	filled := (retryCount * progressBarLength) / maxRetries
	if filled > progressBarLength {
		filled = progressBarLength
	}
	if filled < 0 {
		filled = 0
	}

	// Build progress bar string
	progressBar := strings.Repeat("█", filled) + strings.Repeat("░", progressBarLength-filled)

	// Calculate percentage
	percentage := (retryCount * 100) / maxRetries
	if percentage > 100 {
		percentage = 100
	}
	if percentage < 0 {
		percentage = 0
	}

	// Truncate error reason
	errorReasonSummary := TruncateErrorReason(errorReason, errorReasonMaxLength)

	// Format message
	return fmt.Sprintf(`%s
🔄 Retry Progress

**Attempt**: %d/%d
**Progress**: [%s] %d%%

**Reason**: %s

Retrying with feedback...`, marker, retryCount, maxRetries, progressBar, percentage, errorReasonSummary)
}

// NotifyRetryProgress posts or updates a retry progress comment on both the Issue and PR.
// It is idempotent per idempotencyKey; if a marker is already present, the comment is updated.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner
//   - repo: Repository name
//   - issueNumber: Issue number (0 if not applicable)
//   - prNumber: PR number (0 if not applicable)
//   - retryCount: Current retry count (must be >= 0)
//   - maxRetries: Maximum retry attempts (must be > 0)
//   - errorReason: Error message to include (will be truncated)
//   - idempotencyKey: Idempotency key for marker-based deduplication
//
// Returns:
//   - error: Error if both Issue and PR notifications failed
func (s *GitHubNotificationService) NotifyRetryProgress(
	ctx context.Context,
	owner, repo string,
	issueNumber, prNumber int,
	retryCount, maxRetries int,
	errorReason, idempotencyKey string,
) error {
	// Input validation
	if retryCount < 0 {
		return fmt.Errorf("retryCount must be >= 0, got: %d", retryCount)
	}
	if maxRetries <= 0 {
		return fmt.Errorf("maxRetries must be > 0, got: %d", maxRetries)
	}
	if idempotencyKey == "" {
		return fmt.Errorf("idempotencyKey must not be empty")
	}

	s.logger.Info("Posting retry progress notifications",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
		zap.Int("retry_count", retryCount),
		zap.Int("max_retries", maxRetries),
		zap.String("idempotency_key", idempotencyKey),
	)

	// Generate marker
	marker := retryProgressMarkerPrefix + idempotencyKey + " -->"

	// Format message body
	body := FormatRetryProgressMessage(retryCount, maxRetries, errorReason, marker)

	var aggErr error

	// Post to Issue thread
	if issueNumber > 0 {
		commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, issueNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan issue comments for marker", zap.Error(err), zap.Int("issue_number", issueNumber))
			aggErr = err
		} else if found && commentID != nil {
			// Update existing comment
			if err := s.updateComment(ctx, owner, repo, *commentID, body); err != nil {
				s.logger.Warn("Failed to update issue comment for retry progress", zap.Error(err), zap.Int("issue_number", issueNumber), zap.Int64("comment_id", *commentID))
				aggErr = err
			} else {
				s.logger.Info("Updated issue comment for retry progress", zap.Int("issue_number", issueNumber), zap.Int64("comment_id", *commentID))
			}
		} else {
			// Create new comment
			if err := s.postComment(ctx, owner, repo, issueNumber, body); err != nil {
				s.logger.Warn("Failed to post issue comment for retry progress", zap.Error(err), zap.Int("issue_number", issueNumber))
				aggErr = err
			} else {
				s.logger.Info("Posted issue comment for retry progress", zap.Int("issue_number", issueNumber))
			}
		}
	}

	// Post to PR thread (PR number can be used with Issues API)
	if prNumber > 0 {
		commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, prNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan PR comments for marker", zap.Error(err), zap.Int("pr_number", prNumber))
			if aggErr == nil {
				aggErr = err
			}
		} else if found && commentID != nil {
			// Update existing comment
			if err := s.updateComment(ctx, owner, repo, *commentID, body); err != nil {
				s.logger.Warn("Failed to update PR comment for retry progress", zap.Error(err), zap.Int("pr_number", prNumber), zap.Int64("comment_id", *commentID))
				if aggErr == nil {
					aggErr = err
				}
			} else {
				s.logger.Info("Updated PR comment for retry progress", zap.Int("pr_number", prNumber), zap.Int64("comment_id", *commentID))
			}
		} else {
			// Create new comment
			if err := s.postComment(ctx, owner, repo, prNumber, body); err != nil {
				s.logger.Warn("Failed to post PR comment for retry progress", zap.Error(err), zap.Int("pr_number", prNumber))
				if aggErr == nil {
					aggErr = err
				}
			} else {
				s.logger.Info("Posted PR comment for retry progress", zap.Int("pr_number", prNumber))
			}
		}
	}

	if aggErr != nil {
		return aggErr
	}

	s.logger.Info("Retry progress notifications posted (or updated)",
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
	)
	return nil
}
