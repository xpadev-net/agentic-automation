package services

import (
	"context"
	"errors"
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
//   - agentType: Agent type (e.g., "claude-code" or "cursor-agent")
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
	prCreatedMarkerPrefix   = "<!-- agent:pr-created:"
	maxRetriesMarkerPrefix  = "<!-- agent:max-retries:"
	mergeStatusMarkerPrefix = "<!-- agent:merge-status:"
	prCreatedTemplate       = "PR created: #%d (%s) branch=%s sha=%s"
	maxCommentsToScan       = 30
)

// Retry Progress Notification (US3 T102)
const (
	retryProgressMarkerPrefix = "<!-- agent:retry-progress:"
	MaxRetryAttempts          = 50 // Exported for use in handlers
	errorReasonMaxLength      = 100
	progressBarLength         = 10 // 進捗バーの文字数
)

// Dependency Violation Notification (US5 T129)
const (
	dependencyViolationMarkerPrefix = "<!-- agent:dependency-violation:"
	maxBlockedIssuesToShow          = 10
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

// makeMergeSuccessBody formats a message body for merge success notification.
// It includes an idempotency marker and the merge SHA (shortened).
func makeMergeSuccessBody(mergeSHA, idempotencyKey string) string {
	marker := mergeStatusMarkerPrefix + idempotencyKey + " -->"
	message := fmt.Sprintf("✅ Auto-merge succeeded\n\n**Merge SHA**: %s\n\nPR has been successfully merged.", shortSHA(mergeSHA))
	return marker + "\n" + message
}

// makeMergeFailureBody formats a message body for merge failure notification.
// It includes an idempotency marker, error classification, and the error message.
func makeMergeFailureBody(errorMessage, errorClassification, idempotencyKey string) string {
	marker := mergeStatusMarkerPrefix + idempotencyKey + " -->"

	content := "❌ Auto-merge failed\n\n"
	if errorClassification != "" {
		content += fmt.Sprintf("**Error Classification**: %s\n\n", errorClassification)
	}
	if em := getErrorMessageForNotification(&errorMessage); em != "" {
		content += fmt.Sprintf("**Error**: %s", em)
	}
	return marker + "\n" + content
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
//   - agentType: Agent type (e.g., "claude-code" or "cursor-agent")
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

// NotifyMergeStatus posts a status comment to both the Issue and the PR to report
// merge success or failure. It is idempotent per idempotencyKey; if a marker is
// already present, the existing comment is updated.
func (s *GitHubNotificationService) NotifyMergeStatus(
	ctx context.Context,
	owner, repo string,
	issueNumber, prNumber int,
	merged bool,
	mergeSHA string,
	errorMessage string,
	idempotencyKey string,
	errorType string, // Optional: pre-classified error type. If empty, will be classified from errorMessage.
) error {
	// Input validation
	if idempotencyKey == "" {
		return fmt.Errorf("idempotencyKey must not be empty")
	}

	s.logger.Info("Posting merge status notifications",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
		zap.Bool("merged", merged),
		zap.String("idempotency_key", idempotencyKey),
	)

	// Prepare body and marker
	marker := mergeStatusMarkerPrefix + idempotencyKey + " -->"
	var body string
	if merged {
		if mergeSHA == "" {
			s.logger.Warn("merge succeeded but mergeSHA is empty")
		}
		body = makeMergeSuccessBody(mergeSHA, idempotencyKey)
	} else {
		classification := errorType
		if classification == "" {
			classification = ClassifyMergeError(errors.New(errorMessage))
		}
		body = makeMergeFailureBody(errorMessage, classification, idempotencyKey)
	}

	var aggErr error

	// Post to Issue thread
	if issueNumber > 0 {
		commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, issueNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan issue comments for merge marker", zap.Error(err), zap.Int("issue_number", issueNumber))
			aggErr = err
		} else if found && commentID != nil {
			if err := s.updateComment(ctx, owner, repo, *commentID, body); err != nil {
				s.logger.Warn("Failed to update issue comment for merge status", zap.Error(err), zap.Int("issue_number", issueNumber), zap.Int64("comment_id", *commentID))
				aggErr = err
			} else {
				s.logger.Info("Updated issue comment for merge status", zap.Int("issue_number", issueNumber), zap.Int64("comment_id", *commentID))
			}
		} else {
			if err := s.postComment(ctx, owner, repo, issueNumber, body); err != nil {
				s.logger.Warn("Failed to post issue comment for merge status", zap.Error(err), zap.Int("issue_number", issueNumber))
				aggErr = err
			} else {
				s.logger.Info("Posted issue comment for merge status", zap.Int("issue_number", issueNumber))
			}
		}
	}

	// Post to PR thread
	if prNumber > 0 {
		commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, prNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan PR comments for merge marker", zap.Error(err), zap.Int("pr_number", prNumber))
			if aggErr == nil {
				aggErr = err
			}
		} else if found && commentID != nil {
			if err := s.updateComment(ctx, owner, repo, *commentID, body); err != nil {
				s.logger.Warn("Failed to update PR comment for merge status", zap.Error(err), zap.Int("pr_number", prNumber), zap.Int64("comment_id", *commentID))
				if aggErr == nil {
					aggErr = err
				}
			} else {
				s.logger.Info("Updated PR comment for merge status", zap.Int("pr_number", prNumber), zap.Int64("comment_id", *commentID))
			}
		} else {
			if err := s.postComment(ctx, owner, repo, prNumber, body); err != nil {
				s.logger.Warn("Failed to post PR comment for merge status", zap.Error(err), zap.Int("pr_number", prNumber))
				if aggErr == nil {
					aggErr = err
				}
			} else {
				s.logger.Info("Posted PR comment for merge status", zap.Int("pr_number", prNumber))
			}
		}
	}

	return aggErr
}

// NotifyMergeFailure posts a status comment to Issue and PR when auto-merge fails.
// It is a convenience wrapper around NotifyMergeStatus for backward compatibility.
func (s *GitHubNotificationService) NotifyMergeFailure(
	ctx context.Context,
	owner, repo string,
	issueNumber, prNumber int,
	errorType, errorMessage, idempotencyKey string,
) error {
	return s.NotifyMergeStatus(ctx, owner, repo, issueNumber, prNumber, false, "", errorMessage, idempotencyKey, errorType)
}

// -----------------------------------------------------------------------------
// Dependency Violation Notification (US5 T129)
// -----------------------------------------------------------------------------

// FormatDependencyViolationBody formats a message body for dependency violation notification.
// It includes a marker, header, list of blocked issues, and guidance message.
// Exported for testing purposes.
//
// Parameters:
//   - blocked: List of blocking issues (must not be empty)
//   - marker: HTML comment marker for idempotency
//
// Returns:
//   - string: Formatted markdown message
func FormatDependencyViolationBody(blocked []models.Issue, marker string) string {
	message := marker + "\n❌ Dependency violation\n\n"
	message += "This issue cannot be executed because it depends on the following open issues:\n\n"

	// Show up to maxBlockedIssuesToShow issues
	showCount := len(blocked)
	if showCount > maxBlockedIssuesToShow {
		showCount = maxBlockedIssuesToShow
	}

	for i := 0; i < showCount; i++ {
		issue := blocked[i]
		message += fmt.Sprintf("- %s#%d [%s]\n", issue.Repo, issue.Number, issue.State)
	}

	// Add summary if there are more issues
	if len(blocked) > maxBlockedIssuesToShow {
		remaining := len(blocked) - maxBlockedIssuesToShow
		message += fmt.Sprintf("\n... and %d more\n", remaining)
	}

	message += "\nClose all blocking issues to proceed."
	return message
}

// NotifyDependencyViolation posts a status comment to both the Issue and the PR (if exists)
// to report dependency violations. It is idempotent per idempotencyKey; if a marker is
// already present, the existing comment is updated.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner
//   - repo: Repository name
//   - issueNumber: Issue number
//   - prNumber: PR number (0 if not applicable)
//   - blocked: List of blocking issues (must not be empty)
//   - idempotencyKey: Idempotency key for marker-based deduplication
//
// Returns:
//   - error: Error if both Issue and PR notifications failed
func (s *GitHubNotificationService) NotifyDependencyViolation(
	ctx context.Context,
	owner, repo string,
	issueNumber, prNumber int,
	blocked []models.Issue,
	idempotencyKey string,
) error {
	// Input validation
	if len(blocked) == 0 {
		return fmt.Errorf("blocked issues list must not be empty")
	}
	if idempotencyKey == "" {
		return fmt.Errorf("idempotencyKey must not be empty")
	}

	s.logger.Info("Posting dependency violation notifications",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
		zap.Int("blocked_count", len(blocked)),
		zap.String("idempotency_key", idempotencyKey),
	)

	// Generate marker
	marker := dependencyViolationMarkerPrefix + idempotencyKey + " -->"

	// Format message body
	body := FormatDependencyViolationBody(blocked, marker)

	var aggErr error

	// Post to Issue thread
	if issueNumber > 0 {
		commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, issueNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan issue comments for dependency violation marker",
				zap.Error(err),
				zap.Int("issue_number", issueNumber),
			)
			aggErr = err
		} else if found && commentID != nil {
			// Update existing comment
			if err := s.updateComment(ctx, owner, repo, *commentID, body); err != nil {
				s.logger.Warn("Failed to update issue comment for dependency violation",
					zap.Error(err),
					zap.Int("issue_number", issueNumber),
					zap.Int64("comment_id", *commentID),
				)
				aggErr = err
			} else {
				s.logger.Info("Updated issue comment for dependency violation",
					zap.Int("issue_number", issueNumber),
					zap.Int64("comment_id", *commentID),
				)
			}
		} else {
			// Create new comment
			if err := s.postComment(ctx, owner, repo, issueNumber, body); err != nil {
				s.logger.Warn("Failed to post issue comment for dependency violation",
					zap.Error(err),
					zap.Int("issue_number", issueNumber),
				)
				aggErr = err
			} else {
				s.logger.Info("Posted issue comment for dependency violation",
					zap.Int("issue_number", issueNumber),
				)
			}
		}
	}

	// Post to PR thread (PR number can be used with Issues API)
	if prNumber > 0 {
		commentID, found, err := s.FindCommentWithMarker(ctx, owner, repo, prNumber, marker)
		if err != nil {
			s.logger.Warn("Failed to scan PR comments for dependency violation marker",
				zap.Error(err),
				zap.Int("pr_number", prNumber),
			)
			if aggErr == nil {
				aggErr = err
			}
		} else if found && commentID != nil {
			// Update existing comment
			if err := s.updateComment(ctx, owner, repo, *commentID, body); err != nil {
				s.logger.Warn("Failed to update PR comment for dependency violation",
					zap.Error(err),
					zap.Int("pr_number", prNumber),
					zap.Int64("comment_id", *commentID),
				)
				if aggErr == nil {
					aggErr = err
				}
			} else {
				s.logger.Info("Updated PR comment for dependency violation",
					zap.Int("pr_number", prNumber),
					zap.Int64("comment_id", *commentID),
				)
			}
		} else {
			// Create new comment
			if err := s.postComment(ctx, owner, repo, prNumber, body); err != nil {
				s.logger.Warn("Failed to post PR comment for dependency violation",
					zap.Error(err),
					zap.Int("pr_number", prNumber),
				)
				if aggErr == nil {
					aggErr = err
				}
			} else {
				s.logger.Info("Posted PR comment for dependency violation",
					zap.Int("pr_number", prNumber),
				)
			}
		}
	}

	if aggErr != nil {
		return aggErr
	}

	s.logger.Info("Dependency violation notifications posted (or updated)",
		zap.Int("issue_number", issueNumber),
		zap.Int("pr_number", prNumber),
	)
	return nil
}
