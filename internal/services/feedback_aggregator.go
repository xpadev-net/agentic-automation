package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// previousAttempt represents a previous retry attempt in agent-runner format
// Note: This matches agent-runner/pkg/context/issue.go:PreviousAttempt structure
type previousAttempt struct {
	RetryCount int    `json:"retry_count"`
	Error      string `json:"error"`
	CILogs     string `json:"ci_logs,omitempty"`
}

// AggregatedFeedback represents the aggregated feedback from reviews and CI failures
type AggregatedFeedback struct {
	PreviousAttemptsJSON string // JSON string of []previousAttempt (for agent-runner)
	CILogs               string // CI log excerpt (for JobConfig.CILogs)
	HasReviewFeedback    bool   // Whether review feedback exists
	HasCIFailure         bool   // Whether CI failure exists
}

// ReviewFeedbackRepositoryInterface defines the interface for review feedback repository operations
// This allows for dependency injection and testing with mocks
type ReviewFeedbackRepositoryInterface interface {
	FindByPRID(prID int) ([]*models.ReviewFeedback, error)
}

// FeedbackAggregator aggregates review feedback and CI failure results
type FeedbackAggregator struct {
	reviewFeedbackRepo ReviewFeedbackRepositoryInterface
	logger             *config.AppLogger
}

// NewFeedbackAggregator creates a new FeedbackAggregator instance
// Parameters:
//   - reviewFeedbackRepo: ReviewFeedbackRepositoryInterface instance (nil可、内部でNewReviewFeedbackRepository()を呼ぶ)
//   - logger: Logger instance (nil可、config.GetLogger()を使用)
//
// Returns:
//   - *FeedbackAggregator: Initialized aggregator
func NewFeedbackAggregator(reviewFeedbackRepo ReviewFeedbackRepositoryInterface, logger *config.AppLogger) *FeedbackAggregator {
	if reviewFeedbackRepo == nil {
		reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
	}

	if logger == nil {
		logger = config.GetLogger()
	}

	return &FeedbackAggregator{
		reviewFeedbackRepo: reviewFeedbackRepo,
		logger:             logger,
	}
}

// combineReviewComments combines multiple ReviewFeedback.Content fields
// Parameters:
//   - feedbacks: Slice of ReviewFeedback (filtered by approval_detected=false)
//
// Returns:
//   - string: Combined comments (newline-separated)
func combineReviewComments(feedbacks []*models.ReviewFeedback) string {
	if len(feedbacks) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, fb := range feedbacks {
		if fb == nil || fb.Content == nil {
			continue
		}

		content := strings.TrimSpace(*fb.Content)
		if content == "" {
			continue
		}

		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(content)
	}

	return builder.String()
}

// buildErrorMessage combines review comments and CI failure into a single error message
// Parameters:
//   - reviewComments: Combined review comments (空文字列可)
//   - ciResult: CI failure result (nil可)
//
// Returns:
//   - string: Combined error message
func buildErrorMessage(reviewComments string, ciResult *CIFailureResult) string {
	hasReview := strings.TrimSpace(reviewComments) != ""
	hasCI := ciResult != nil && strings.TrimSpace(ciResult.Summary) != ""

	if !hasReview && !hasCI {
		return ""
	}

	var builder strings.Builder

	if hasReview {
		builder.WriteString("Review feedback:\n")
		builder.WriteString(reviewComments)
	}

	if hasReview && hasCI {
		builder.WriteString("\n\n")
	}

	if hasCI {
		builder.WriteString("CI Failure: ")
		builder.WriteString(ciResult.Summary)
	}

	return builder.String()
}

// AggregateFeedback aggregates review feedback and CI failure results for a PR
// Parameters:
//   - ctx: Context for cancellation
//   - prID: PullRequest ID (must be > 0)
//   - ciResult: CI failure result (nil可、CI失敗がない場合)
//   - retryCount: Current retry count (must be >= 0)
//
// Returns:
//   - *AggregatedFeedback: Aggregated feedback, or nil on error
//   - error: Error if aggregation fails
func (a *FeedbackAggregator) AggregateFeedback(ctx context.Context, prID int, ciResult *CIFailureResult, retryCount int) (*AggregatedFeedback, error) {
	// Input validation
	if prID <= 0 {
		return nil, fmt.Errorf("prID must be positive, got: %d", prID)
	}

	if retryCount < 0 {
		return nil, fmt.Errorf("retryCount must be non-negative, got: %d", retryCount)
	}

	// Log aggregation start
	a.logger.Info("Starting feedback aggregation",
		config.Int("pr_id", prID),
		config.Int("retry_count", retryCount),
		config.Bool("has_ci_failure", ciResult != nil),
		config.String("service", "feedback_aggregator"),
	)

	// Get all review feedbacks for PR
	allFeedbacks, err := a.reviewFeedbackRepo.FindByPRID(prID)
	if err != nil {
		a.logger.Error("Failed to aggregate feedback",
			config.Int("pr_id", prID),
			config.Error(err),
			config.String("service", "feedback_aggregator"),
		)
		return nil, fmt.Errorf("failed to get review feedbacks: %w", err)
	}

	// Filter: approval_detected=false AND status IN ('received', 'commented') AND content IS NOT NULL AND content != ''
	filteredFeedbacks := make([]*models.ReviewFeedback, 0)
	for _, fb := range allFeedbacks {
		if fb == nil {
			continue
		}

		if !fb.ApprovalDetected &&
			(fb.Status == "received" || fb.Status == "commented") &&
			fb.Content != nil && strings.TrimSpace(*fb.Content) != "" {
			filteredFeedbacks = append(filteredFeedbacks, fb)
		}
	}

	// Log feedback retrieval
	a.logger.Debug("Review feedbacks retrieved",
		config.Int("pr_id", prID),
		config.Int("total_feedbacks", len(allFeedbacks)),
		config.Int("filtered_feedbacks", len(filteredFeedbacks)),
		config.String("service", "feedback_aggregator"),
	)

	// Combine review comments
	reviewComments := combineReviewComments(filteredFeedbacks)
	hasReviewFeedback := len(filteredFeedbacks) > 0

	// Extract CI failure information
	hasCIFailure := ciResult != nil
	ciLogs := ""
	if ciResult != nil {
		ciLogs = ciResult.Excerpt
	}

	// Build error message
	errorMessage := buildErrorMessage(reviewComments, ciResult)

	// Build previousAttempt structure
	var attempts []previousAttempt
	if errorMessage != "" || ciLogs != "" {
		attempt := previousAttempt{
			RetryCount: retryCount,
			Error:      errorMessage,
			CILogs:     ciLogs,
		}
		attempts = []previousAttempt{attempt}
	} else {
		// Empty array if no feedback
		attempts = []previousAttempt{}
	}

	// JSON serialization
	previousAttemptsJSON, err := json.Marshal(attempts)
	if err != nil {
		a.logger.Error("Failed to aggregate feedback",
			config.Int("pr_id", prID),
			config.Error(err),
			config.String("service", "feedback_aggregator"),
		)
		return nil, fmt.Errorf("failed to marshal previous attempts: %w", err)
	}

	result := &AggregatedFeedback{
		PreviousAttemptsJSON: string(previousAttemptsJSON),
		CILogs:               ciLogs,
		HasReviewFeedback:    hasReviewFeedback,
		HasCIFailure:         hasCIFailure,
	}

	// Log aggregation completion
	a.logger.Info("Feedback aggregation completed",
		config.Int("pr_id", prID),
		config.Bool("has_review_feedback", result.HasReviewFeedback),
		config.Bool("has_ci_failure", result.HasCIFailure),
		config.Int("previous_attempts_length", len(previousAttemptsJSON)),
		config.Int("ci_logs_length", len(result.CILogs)),
		config.String("service", "feedback_aggregator"),
	)

	return result, nil
}
