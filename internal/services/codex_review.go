package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v76/github"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/utils"
	"go.uber.org/zap"
)

const (
	codexReviewMarkerPrefix = "<!-- agent:codex-review-request:"
)

// CodexReviewService provides Codex review request functionality.
// It handles posting "@codex review" comments to GitHub PRs and creating
// ReviewFeedback records to track review requests.
type CodexReviewService struct {
	githubClient       *clients.Client
	reviewFeedbackRepo *repositories.ReviewFeedbackRepository
	logger             *zap.Logger
}

// makeCodexReviewComment creates a comment body with idempotency marker.
func makeCodexReviewComment(idempotencyKey string) string {
	marker := codexReviewMarkerPrefix + idempotencyKey + " -->"
	return marker + "\n" + utils.CodexReviewTrigger
}

// NewCodexReviewService creates a new CodexReviewService instance.
// It requires a GitHub client, review feedback repository, and logger as dependencies.
//
// Parameters:
//   - githubClient: GitHub API client (must not be nil, will panic if nil)
//   - reviewFeedbackRepo: ReviewFeedback repository for creating review records (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *CodexReviewService: Initialized service instance
func NewCodexReviewService(
	githubClient *clients.Client,
	reviewFeedbackRepo *repositories.ReviewFeedbackRepository,
	logger *zap.Logger,
) *CodexReviewService {
	if githubClient == nil {
		panic("githubClient is required for CodexReviewService")
	}

	if reviewFeedbackRepo == nil {
		panic("reviewFeedbackRepo is required for CodexReviewService")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &CodexReviewService{
		githubClient:       githubClient,
		reviewFeedbackRepo: reviewFeedbackRepo,
		logger:             logger,
	}
}

// hasCommentWithMarker checks if a recent comment contains the given marker.
// It scans only the last N comments to limit API/CPU usage.
func (s *CodexReviewService) hasCommentWithMarker(ctx context.Context, owner, repo string, number int, marker string) (bool, error) {
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
		if *c.Body != "" && marker != "" && strings.Contains(*c.Body, marker) {
			return true, nil
		}
	}
	return false, nil
}

// RequestReview posts a "@codex review" comment to the specified GitHub PR
// and creates a ReviewFeedback record with status "requested".
// It is idempotent per idempotencyKey; if a request already exists, it returns
// the existing ReviewFeedback record without posting a new comment.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - prNumber: GitHub PR number (must be > 0)
//   - prID: Database PullRequest ID (must be > 0)
//   - idempotencyKey: Unique key for idempotency (must not be empty)
//
// Returns:
//   - *models.ReviewFeedback: Created or existing ReviewFeedback record (on success)
//   - error: Error if comment posting or record creation failed
func (s *CodexReviewService) RequestReview(
	ctx context.Context,
	owner, repo string,
	prNumber, prID int,
	idempotencyKey string,
) (*models.ReviewFeedback, error) {
	// Validate input parameters
	if prNumber <= 0 {
		return nil, fmt.Errorf("prNumber must be greater than 0, got: %d", prNumber)
	}
	if prID <= 0 {
		return nil, fmt.Errorf("prID must be greater than 0, got: %d", prID)
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotencyKey must not be empty")
	}

	s.logger.Info("Requesting Codex review",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int("pr_id", prID),
		zap.String("idempotency_key", idempotencyKey),
	)

	// Check for existing ReviewFeedback record with status="requested"
	existingFeedbacks, err := s.reviewFeedbackRepo.FindByPRIDAndStatus(prID, "requested")
	if err != nil {
		s.logger.Warn("Failed to check for existing ReviewFeedback records",
			zap.Error(err),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("pr_number", prNumber),
			zap.Int("pr_id", prID),
			zap.String("idempotency_key", idempotencyKey),
		)
		// Continue processing even if check fails (best effort)
	} else if len(existingFeedbacks) > 0 {
		// Existing request found - return the most recent one
		existingFeedback := existingFeedbacks[0] // Already ordered by created_at DESC
		s.logger.Info("Existing Codex review request found, returning existing record",
			zap.Int("feedback_id", existingFeedback.ID),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("pr_number", prNumber),
			zap.Int("pr_id", prID),
			zap.String("idempotency_key", idempotencyKey),
		)
		return existingFeedback, nil
	}

	// Check for existing comment with marker
	marker := codexReviewMarkerPrefix + idempotencyKey + " -->"
	hasMarker, err := s.hasCommentWithMarker(ctx, owner, repo, prNumber, marker)
	if err != nil {
		s.logger.Warn("Failed to check for existing comment marker",
			zap.Error(err),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("pr_number", prNumber),
			zap.Int("pr_id", prID),
			zap.String("idempotency_key", idempotencyKey),
		)
		// Continue processing even if check fails (best effort)
	} else if hasMarker {
		// Comment with marker already exists - check if we have a ReviewFeedback record
		// If not, create one to maintain consistency
		existingFeedbacks, err := s.reviewFeedbackRepo.FindByPRIDAndStatus(prID, "requested")
		if err == nil && len(existingFeedbacks) > 0 {
			existingFeedback := existingFeedbacks[0]
			s.logger.Info("Existing Codex review comment found, returning existing record",
				zap.Int("feedback_id", existingFeedback.ID),
				zap.String("owner", owner),
				zap.String("repo", repo),
				zap.Int("pr_number", prNumber),
				zap.Int("pr_id", prID),
				zap.String("idempotency_key", idempotencyKey),
			)
			return existingFeedback, nil
		}
		// Comment exists but no ReviewFeedback record - continue to create one
	}

	// Post comment with retry logic
	commentBody := makeCodexReviewComment(idempotencyKey)
	var comment *github.IssueComment
	err = utils.Retry(ctx, func() error {
		var retryErr error
		comment, retryErr = s.githubClient.CreateIssueComment(ctx, owner, repo, prNumber, commentBody)
		return retryErr
	}, nil, s.logger)
	if err != nil {
		s.logger.Error("Failed to post Codex review request comment",
			zap.Error(err),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("pr_number", prNumber),
			zap.Int("pr_id", prID),
			zap.String("idempotency_key", idempotencyKey),
		)
		return nil, fmt.Errorf("failed to post Codex review request comment: %w", err)
	}

	logFields := []zap.Field{
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int("pr_id", prID),
		zap.String("idempotency_key", idempotencyKey),
	}
	if comment != nil && comment.ID != nil {
		logFields = append(logFields, zap.Int64("comment_id", *comment.ID))
	}
	s.logger.Info("Codex review request comment posted successfully", logFields...)

	// Create ReviewFeedback record
	feedback := &models.ReviewFeedback{
		PRID:             prID,
		Source:           "Codex",
		Status:           "requested",
		ApprovalDetected: false,
		Content:          nil,
	}
	if comment != nil && comment.ID != nil {
		feedback.GitHubCommentID = comment.ID
	}

	if err := s.reviewFeedbackRepo.Create(feedback); err != nil {
		warnFields := []zap.Field{
			zap.Error(err),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("pr_number", prNumber),
			zap.Int("pr_id", prID),
			zap.String("idempotency_key", idempotencyKey),
		}
		if comment != nil && comment.ID != nil {
			warnFields = append(warnFields, zap.Int64("comment_id", *comment.ID))
		}
		s.logger.Warn("Failed to create ReviewFeedback record after posting comment", warnFields...)
		return nil, fmt.Errorf("failed to create ReviewFeedback record: %w", err)
	}

	successFields := []zap.Field{
		zap.Int("feedback_id", feedback.ID),
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int("pr_id", prID),
		zap.String("idempotency_key", idempotencyKey),
	}
	if comment != nil && comment.ID != nil {
		successFields = append(successFields, zap.Int64("comment_id", *comment.ID))
	}
	s.logger.Info("ReviewFeedback record created successfully", successFields...)

	return feedback, nil
}
