package services_test

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// mockReviewFeedbackRepository is a mock implementation of ReviewFeedbackRepository for testing
type mockReviewFeedbackRepository struct {
	feedbacks []*models.ReviewFeedback
	err       error
}

func newMockReviewFeedbackRepository() *mockReviewFeedbackRepository {
	return &mockReviewFeedbackRepository{
		feedbacks: make([]*models.ReviewFeedback, 0),
	}
}

func (m *mockReviewFeedbackRepository) SetFeedbacks(feedbacks []*models.ReviewFeedback) {
	m.feedbacks = feedbacks
}

func (m *mockReviewFeedbackRepository) SetError(err error) {
	m.err = err
}

func (m *mockReviewFeedbackRepository) FindByPRID(prID int) ([]*models.ReviewFeedback, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.feedbacks, nil
}

// TestNewFeedbackAggregator tests the constructor
func TestNewFeedbackAggregator(t *testing.T) {
	t.Run("nil logger uses default", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		aggregator := services.NewFeedbackAggregator(repo, nil)
		require.NotNil(t, aggregator)
	})

	t.Run("valid arguments", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		repo := newMockReviewFeedbackRepository()
		aggregator := services.NewFeedbackAggregator(repo, logger)
		require.NotNil(t, aggregator)
	})
}

// TestFeedbackAggregator_AggregateFeedback tests the main aggregation method
func TestFeedbackAggregator_AggregateFeedback(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("both review and CI failure exist", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment1 := "Missing semicolon at line 42"
		comment2 := "Missing type annotation"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment1,
				Status:           "received",
				ApprovalDetected: false,
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &comment2,
				Status:           "commented",
				ApprovalDetected: false,
			},
		})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed: expected 'foo', got 'bar'",
			Excerpt: "Error at line 42: Expected ';'\nTests failed: parser_test.ts:12",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.HasReviewFeedback)
		assert.True(t, result.HasCIFailure)
		assert.NotEmpty(t, result.CILogs)
		assert.Contains(t, result.PreviousAttemptsJSON, "retry_count")
		assert.Contains(t, result.PreviousAttemptsJSON, "Review feedback")
		assert.Contains(t, result.PreviousAttemptsJSON, "CI Failure")

		// Verify JSON structure
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, float64(1), attempts[0]["retry_count"])
		assert.Contains(t, attempts[0]["error"].(string), "Review feedback")
		assert.Contains(t, attempts[0]["error"].(string), "CI Failure")
		assert.Equal(t, ciResult.Excerpt, attempts[0]["ci_logs"])
	})

	t.Run("review only exists", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Missing semicolon at line 42"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 2)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.HasReviewFeedback)
		assert.False(t, result.HasCIFailure)
		assert.Empty(t, result.CILogs)
		assert.Contains(t, result.PreviousAttemptsJSON, "Review feedback")
		assert.NotContains(t, result.PreviousAttemptsJSON, "CI Failure")
	})

	t.Run("CI failure only exists", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed: expected 'foo', got 'bar'",
			Excerpt: "Error at line 42: Expected ';'",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 3)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.HasReviewFeedback)
		assert.True(t, result.HasCIFailure)
		assert.NotEmpty(t, result.CILogs)
		assert.Contains(t, result.PreviousAttemptsJSON, "CI Failure")
		assert.NotContains(t, result.PreviousAttemptsJSON, "Review feedback")
	})

	t.Run("neither exists", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 0)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.HasReviewFeedback)
		assert.False(t, result.HasCIFailure)
		assert.Empty(t, result.CILogs)
		assert.Equal(t, "[]", result.PreviousAttemptsJSON)
	})

	t.Run("approved reviews are excluded", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment1 := "Approved comment"
		comment2 := "Not approved comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment1,
				Status:           "received",
				ApprovalDetected: true, // Approved - should be excluded
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &comment2,
				Status:           "received",
				ApprovalDetected: false, // Not approved - should be included
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.HasReviewFeedback)
		assert.Contains(t, result.PreviousAttemptsJSON, comment2)
		assert.NotContains(t, result.PreviousAttemptsJSON, comment1)
	})

	t.Run("status filtering", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment1 := "Requested comment"
		comment2 := "Received comment"
		comment3 := "Commented comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment1,
				Status:           "requested", // Should be excluded
				ApprovalDetected: false,
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &comment2,
				Status:           "received", // Should be included
				ApprovalDetected: false,
			},
			{
				ID:               3,
				PRID:             100,
				Content:          &comment3,
				Status:           "commented", // Should be included
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.HasReviewFeedback)
		assert.Contains(t, result.PreviousAttemptsJSON, comment2)
		assert.Contains(t, result.PreviousAttemptsJSON, comment3)
		assert.NotContains(t, result.PreviousAttemptsJSON, comment1)
	})

	t.Run("nil Content is skipped", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Valid comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          nil, // Should be skipped
				Status:           "received",
				ApprovalDetected: false,
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.HasReviewFeedback)
		assert.Contains(t, result.PreviousAttemptsJSON, comment)
	})

	t.Run("empty Content is skipped", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		emptyComment := ""
		comment := "Valid comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &emptyComment, // Should be skipped
				Status:           "received",
				ApprovalDetected: false,
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.HasReviewFeedback)
		assert.Contains(t, result.PreviousAttemptsJSON, comment)
		// Empty comment should not appear in the JSON (it's filtered out)
		// We verify this by checking that only the valid comment appears
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		errorMsg := attempts[0]["error"].(string)
		// Verify that only the valid comment is in the error message
		// The empty comment should be filtered out, so the error message should only contain "Valid comment"
		// Verify the error message structure: it should be "Review feedback:\nValid comment"
		expectedMsg := "Review feedback:\n" + comment
		assert.Equal(t, expectedMsg, errorMsg, "Error message should contain only the valid comment, empty comment should be filtered out")
	})

	t.Run("input validation errors", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		aggregator := services.NewFeedbackAggregator(repo, logger)

		t.Run("prID <= 0", func(t *testing.T) {
			result, err := aggregator.AggregateFeedback(ctx, 0, nil, 1)
			assert.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), "prID must be positive")
		})

		t.Run("retryCount < 0", func(t *testing.T) {
			result, err := aggregator.AggregateFeedback(ctx, 100, nil, -1)
			assert.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), "retryCount must be non-negative")
		})
	})

	t.Run("JSON serialization validation", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Test comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed",
			Excerpt: "Error log",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 5)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify JSON is valid and can be unmarshaled
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, float64(5), attempts[0]["retry_count"])
		assert.IsType(t, "", attempts[0]["error"])
		assert.IsType(t, "", attempts[0]["ci_logs"])
	})

	t.Run("repository error", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetError(assert.AnError)

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to get review feedbacks")
	})
}

// TestCombineReviewComments tests the internal helper function
// Note: This tests the function indirectly through AggregateFeedback
// Since the function is not exported, we test it through the public API
func TestCombineReviewComments_Indirect(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("multiple reviews are combined", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment1 := "First comment"
		comment2 := "Second comment"
		comment3 := "Third comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment1,
				Status:           "received",
				ApprovalDetected: false,
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &comment2,
				Status:           "commented",
				ApprovalDetected: false,
			},
			{
				ID:               3,
				PRID:             100,
				Content:          &comment3,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.PreviousAttemptsJSON, comment1)
		assert.Contains(t, result.PreviousAttemptsJSON, comment2)
		assert.Contains(t, result.PreviousAttemptsJSON, comment3)
	})

	t.Run("nil and empty Content are skipped", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Valid comment"
		emptyComment := ""
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          nil,
				Status:           "received",
				ApprovalDetected: false,
			},
			{
				ID:               2,
				PRID:             100,
				Content:          &emptyComment,
				Status:           "received",
				ApprovalDetected: false,
			},
			{
				ID:               3,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.PreviousAttemptsJSON, comment)
	})

	t.Run("zero reviews", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.HasReviewFeedback)
	})
}

// TestBuildErrorMessage tests the internal helper function
// Note: This tests the function indirectly through AggregateFeedback
func TestBuildErrorMessage_Indirect(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("review only", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Review comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.PreviousAttemptsJSON, "Review feedback")
		assert.NotContains(t, result.PreviousAttemptsJSON, "CI Failure")
	})

	t.Run("CI failure only", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed",
			Excerpt: "Error log",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.PreviousAttemptsJSON, "CI Failure")
		assert.NotContains(t, result.PreviousAttemptsJSON, "Review feedback")
	})

	t.Run("both exist", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Review comment"
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &comment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed",
			Excerpt: "Error log",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.PreviousAttemptsJSON, "Review feedback")
		assert.Contains(t, result.PreviousAttemptsJSON, "CI Failure")
	})

	t.Run("neither exists", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "[]", result.PreviousAttemptsJSON)
	})
}
