package services

import (
	"context"
	"fmt"

	"github.com/google/go-github/v76/github"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/utils"
	"go.uber.org/zap"
)

// CodexReviewService provides Codex review request functionality.
// It handles posting "@codex review" comments to GitHub PRs and creating
// ReviewFeedback records to track review requests.
type CodexReviewService struct {
	githubClient       *clients.Client
	reviewFeedbackRepo *repositories.ReviewFeedbackRepository
	logger             *zap.Logger
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

// RequestReview posts a "@codex review" comment to the specified GitHub PR
// and creates a ReviewFeedback record with status "requested".
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - prNumber: GitHub PR number (must be > 0)
//   - prID: Database PullRequest ID (must be > 0)
//
// Returns:
//   - *models.ReviewFeedback: Created ReviewFeedback record (on success)
//   - error: Error if comment posting or record creation failed
func (s *CodexReviewService) RequestReview(
	ctx context.Context,
	owner, repo string,
	prNumber, prID int,
) (*models.ReviewFeedback, error) {
	// Validate input parameters
	if prNumber <= 0 {
		return nil, fmt.Errorf("prNumber must be greater than 0, got: %d", prNumber)
	}
	if prID <= 0 {
		return nil, fmt.Errorf("prID must be greater than 0, got: %d", prID)
	}

	s.logger.Info("Requesting Codex review",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int("pr_id", prID),
	)

	// Post comment with retry logic
	var comment *github.IssueComment
	err := utils.Retry(ctx, func() error {
		var retryErr error
		comment, retryErr = s.githubClient.CreateIssueComment(ctx, owner, repo, prNumber, utils.CodexReviewTrigger)
		return retryErr
	}, nil, s.logger)
	if err != nil {
		s.logger.Error("Failed to post Codex review request comment",
			zap.Error(err),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("pr_number", prNumber),
			zap.Int("pr_id", prID),
		)
		return nil, fmt.Errorf("failed to post Codex review request comment: %w", err)
	}

	logFields := []zap.Field{
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int("pr_id", prID),
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
	}
	if comment != nil && comment.ID != nil {
		successFields = append(successFields, zap.Int64("comment_id", *comment.ID))
	}
	s.logger.Info("ReviewFeedback record created successfully", successFields...)

	return feedback, nil
}
