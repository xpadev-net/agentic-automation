package services_test

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

	// CI結果の部分的なデータ欠損ケース
	t.Run("CI result with empty Summary but Excerpt exists", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		ciResult := &services.CIFailureResult{
			Summary: "", // Empty Summary
			Excerpt: "Error at line 42: Expected ';'\nTests failed: parser_test.ts:12",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// HasCIFailure is true because ciResult != nil (implementation checks if ciResult is not nil)
		assert.True(t, result.HasCIFailure)
		// CILogs should contain Excerpt value
		assert.Equal(t, ciResult.Excerpt, result.CILogs)
		// PreviousAttemptsJSON should contain ci_logs field with value
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, ciResult.Excerpt, attempts[0]["ci_logs"])
		// Error message should not contain "CI Failure:" because buildErrorMessage checks Summary
		assert.NotContains(t, attempts[0]["error"].(string), "CI Failure:")
	})

	t.Run("CI result with empty Excerpt but Summary exists", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed: expected 'foo', got 'bar'",
			Excerpt: "", // Empty Excerpt
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// HasCIFailure should be true because ciResult != nil
		assert.True(t, result.HasCIFailure)
		// CILogs should be empty string
		assert.Empty(t, result.CILogs)
		// Error message should contain "CI Failure: Test failed: expected 'foo', got 'bar'"
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Contains(t, attempts[0]["error"].(string), "CI Failure: Test failed: expected 'foo', got 'bar'")
		// ci_logs field should be omitted from JSON due to omitempty tag when empty
		_, exists := attempts[0]["ci_logs"]
		assert.False(t, exists, "ci_logs field should be omitted when empty due to omitempty tag")
	})

	t.Run("CI result with both Summary and Excerpt empty", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		ciResult := &services.CIFailureResult{
			Summary: "", // Empty Summary
			Excerpt: "", // Empty Excerpt
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// HasCIFailure is true because ciResult != nil (implementation checks if ciResult is not nil)
		assert.True(t, result.HasCIFailure)
		// CILogs should be empty string
		assert.Empty(t, result.CILogs)
		// Error message should not contain CI information because Summary is empty
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		// Should be empty array because no error message (Summary is empty) and no ci_logs (Excerpt is empty)
		assert.Equal(t, "[]", result.PreviousAttemptsJSON)
	})

	// 特殊文字・長文の処理
	t.Run("review comments with newlines", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment1 := "Line 1\nLine 2\nLine 3"
		comment2 := "Another comment\nWith multiple lines"
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

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Verify comments are separated by newline
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		errorMsg := attempts[0]["error"].(string)
		// Verify newlines are preserved in error message
		assert.Contains(t, errorMsg, "Line 1\nLine 2\nLine 3")
		assert.Contains(t, errorMsg, "Another comment\nWith multiple lines")
		// Verify JSON serialization properly escaped newlines
		// The JSON string should contain \n escape sequences
		assert.Contains(t, result.PreviousAttemptsJSON, "\\n")
		// Verify we can unmarshal and get the correct content
		assert.Contains(t, errorMsg, "\n")
	})

	t.Run("CI logs with newlines", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		repo.SetFeedbacks([]*models.ReviewFeedback{})

		ciResult := &services.CIFailureResult{
			Summary: "Test failed",
			Excerpt: "Error at line 42: Expected ';'\nTests failed: parser_test.ts:12\nMore details here",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// CILogs should contain newlines
		assert.Contains(t, result.CILogs, "\n")
		// Verify JSON serialization properly escaped newlines
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		// Verify newlines are preserved after unmarshaling
		assert.Contains(t, attempts[0]["ci_logs"].(string), "\n")
		// Verify the JSON string contains escape sequences
		assert.Contains(t, result.PreviousAttemptsJSON, "\\n")
	})

	t.Run("JSON special characters in review comments and CI logs", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		comment := "Comment with \"quotes\" and \\backslash and\ttab"
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
			Summary: "Error with \"quotes\"",
			Excerpt: "Log with \\backslash and\ttab and\nnewline",
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Verify JSON serialization properly escapes special characters
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		// Verify we can unmarshal and get the correct content
		errorMsg := attempts[0]["error"].(string)
		assert.Contains(t, errorMsg, "Comment with \"quotes\"")
		assert.Contains(t, errorMsg, "\\backslash")
		assert.Contains(t, attempts[0]["ci_logs"].(string), "\\backslash")
		// Verify JSON string contains escape sequences
		assert.Contains(t, result.PreviousAttemptsJSON, "\\\"")
		assert.Contains(t, result.PreviousAttemptsJSON, "\\\\")
	})

	t.Run("very long strings", func(t *testing.T) {
		repo := newMockReviewFeedbackRepository()
		// Create a very long comment (1000+ characters)
		longComment := "This is a very long comment. " + strings.Repeat("A", 1000)
		repo.SetFeedbacks([]*models.ReviewFeedback{
			{
				ID:               1,
				PRID:             100,
				Content:          &longComment,
				Status:           "received",
				ApprovalDetected: false,
			},
		})

		// Create a very long CI log (1000+ characters)
		longExcerpt := "This is a very long CI log. " + strings.Repeat("B", 1000)
		ciResult := &services.CIFailureResult{
			Summary: "Test failed",
			Excerpt: longExcerpt,
		}

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Verify JSON serialization completes successfully
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		// Verify long strings are preserved
		errorMsg := attempts[0]["error"].(string)
		assert.Contains(t, errorMsg, longComment)
		assert.Contains(t, attempts[0]["ci_logs"].(string), longExcerpt)
		// Verify error message is correctly constructed
		assert.Contains(t, errorMsg, "Review feedback")
		assert.Contains(t, errorMsg, "CI Failure")
	})

	// 複数リトライカウントのJSON構造検証
	t.Run("retryCount=0", func(t *testing.T) {
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

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 0)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Verify retry_count field is 0
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, float64(0), attempts[0]["retry_count"])
	})

	t.Run("retryCount=50 (max value)", func(t *testing.T) {
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

		aggregator := services.NewFeedbackAggregator(repo, logger)
		result, err := aggregator.AggregateFeedback(ctx, 100, nil, 50)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Verify retry_count field is 50
		var attempts []map[string]interface{}
		err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, float64(50), attempts[0]["retry_count"])
	})

	t.Run("various retryCount values consistency", func(t *testing.T) {
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

		retryCounts := []int{1, 5, 10, 25, 49}
		aggregator := services.NewFeedbackAggregator(repo, logger)

		for _, retryCount := range retryCounts {
			t.Run(fmt.Sprintf("retryCount=%d", retryCount), func(t *testing.T) {
				result, err := aggregator.AggregateFeedback(ctx, 100, nil, retryCount)
				require.NoError(t, err)
				require.NotNil(t, result)

				// Verify PreviousAttemptsJSON is valid JSON
				var attempts []map[string]interface{}
				err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
				require.NoError(t, err, "PreviousAttemptsJSON should be valid JSON for retryCount=%d", retryCount)
				// Verify array length is always 1
				require.Len(t, attempts, 1, "Array length should be 1 for retryCount=%d", retryCount)
				// Verify retry_count has correct value
				assert.Equal(t, float64(retryCount), attempts[0]["retry_count"], "retry_count should be %d", retryCount)
			})
		}
	})

	// レビューとCIの組み合わせの詳細検証
	t.Run("error message format accuracy", func(t *testing.T) {
		// Test case: both review and CI exist
		t.Run("both review and CI exist", func(t *testing.T) {
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
				Summary: "CI failed",
				Excerpt: "CI log",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			errorMsg := attempts[0]["error"].(string)
			// Verify format: "Review feedback:\n{comments}\n\nCI Failure: {summary}"
			assert.Contains(t, errorMsg, "Review feedback:\n")
			assert.Contains(t, errorMsg, "\n\nCI Failure: ")
			assert.Contains(t, errorMsg, comment)
			assert.Contains(t, errorMsg, ciResult.Summary)
			// Verify separator \n\n is correctly inserted
			expectedFormat := "Review feedback:\n" + comment + "\n\nCI Failure: " + ciResult.Summary
			assert.Equal(t, expectedFormat, errorMsg)
		})

		// Test case: review only
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
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			errorMsg := attempts[0]["error"].(string)
			// Verify format: "Review feedback:\n{comments}"
			expectedFormat := "Review feedback:\n" + comment
			assert.Equal(t, expectedFormat, errorMsg)
		})

		// Test case: CI only
		t.Run("CI only", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			ciResult := &services.CIFailureResult{
				Summary: "CI failed",
				Excerpt: "CI log",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			errorMsg := attempts[0]["error"].(string)
			// Verify format: "CI Failure: {summary}"
			expectedFormat := "CI Failure: " + ciResult.Summary
			assert.Equal(t, expectedFormat, errorMsg)
		})
	})

	t.Run("flag accuracy verification", func(t *testing.T) {
		// Test case: both true
		t.Run("both HasReviewFeedback and HasCIFailure true", func(t *testing.T) {
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
				Summary: "CI failed",
				Excerpt: "CI log",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, result.HasReviewFeedback)
			assert.True(t, result.HasCIFailure)
		})

		// Test case: HasReviewFeedback=true, HasCIFailure=false
		t.Run("HasReviewFeedback=true, HasCIFailure=false", func(t *testing.T) {
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
			assert.True(t, result.HasReviewFeedback)
			assert.False(t, result.HasCIFailure)
		})

		// Test case: HasReviewFeedback=false, HasCIFailure=true
		t.Run("HasReviewFeedback=false, HasCIFailure=true", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			ciResult := &services.CIFailureResult{
				Summary: "CI failed",
				Excerpt: "CI log",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.False(t, result.HasReviewFeedback)
			assert.True(t, result.HasCIFailure)
		})

		// Test case: both false
		t.Run("both HasReviewFeedback and HasCIFailure false", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.False(t, result.HasReviewFeedback)
			assert.False(t, result.HasCIFailure)
		})

		// Test case: filtered reviews count is 0, HasReviewFeedback should be false
		t.Run("filtered reviews count is 0", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			// All reviews are approved or have wrong status
			comment1 := "Approved comment"
			comment2 := "Requested comment"
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
					Status:           "requested", // Wrong status - should be excluded
					ApprovalDetected: false,
				},
			})

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			// HasReviewFeedback should be false because all reviews are filtered out
			assert.False(t, result.HasReviewFeedback)
			assert.False(t, result.HasCIFailure)
		})
	})

	t.Run("CILogs field setting verification", func(t *testing.T) {
		// Test case: CILogs should always be ciResult.Excerpt when ciResult is not nil
		t.Run("CILogs equals ciResult.Excerpt when ciResult is not nil", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			ciResult := &services.CIFailureResult{
				Summary: "CI failed",
				Excerpt: "Detailed CI log here",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, ciResult.Excerpt, result.CILogs)
			// Verify PreviousAttemptsJSON ci_logs field matches CILogs
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			assert.Equal(t, result.CILogs, attempts[0]["ci_logs"])
		})

		// Test case: ciResult is nil, CILogs should be empty string
		t.Run("CILogs is empty when ciResult is nil", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Empty(t, result.CILogs)
		})

		// Test case: ciResult.Excerpt is empty string, CILogs should be empty string
		t.Run("CILogs is empty when ciResult.Excerpt is empty", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			ciResult := &services.CIFailureResult{
				Summary: "CI failed",
				Excerpt: "", // Empty Excerpt
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Empty(t, result.CILogs)
			// Verify PreviousAttemptsJSON ci_logs field is omitted due to omitempty tag
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			_, exists := attempts[0]["ci_logs"]
			assert.False(t, exists, "ci_logs field should be omitted when empty due to omitempty tag")
		})
	})

	t.Run("PreviousAttemptsJSON structure verification", func(t *testing.T) {
		// Test case: both review and CI exist
		t.Run("both review and CI exist", func(t *testing.T) {
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
				Summary: "CI failed",
				Excerpt: "CI log",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			// Verify retry_count, error, ci_logs fields exist
			assert.Contains(t, attempts[0], "retry_count")
			assert.Contains(t, attempts[0], "error")
			assert.Contains(t, attempts[0], "ci_logs")
			// Verify error field contains both review and CI
			errorMsg := attempts[0]["error"].(string)
			assert.Contains(t, errorMsg, "Review feedback")
			assert.Contains(t, errorMsg, "CI Failure")
		})

		// Test case: review only
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
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			// Verify retry_count and error fields exist
			assert.Contains(t, attempts[0], "retry_count")
			assert.Contains(t, attempts[0], "error")
			// ci_logs field should be omitted due to omitempty tag when empty
			_, exists := attempts[0]["ci_logs"]
			assert.False(t, exists, "ci_logs field should be omitted when empty due to omitempty tag")
		})

		// Test case: CI only
		t.Run("CI only", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			ciResult := &services.CIFailureResult{
				Summary: "CI failed",
				Excerpt: "CI log",
			}

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, ciResult, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			var attempts []map[string]interface{}
			err = json.Unmarshal([]byte(result.PreviousAttemptsJSON), &attempts)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			// Verify retry_count, error, ci_logs fields exist
			assert.Contains(t, attempts[0], "retry_count")
			assert.Contains(t, attempts[0], "error")
			assert.Contains(t, attempts[0], "ci_logs")
		})

		// Test case: neither exists
		t.Run("neither exists", func(t *testing.T) {
			repo := newMockReviewFeedbackRepository()
			repo.SetFeedbacks([]*models.ReviewFeedback{})

			aggregator := services.NewFeedbackAggregator(repo, logger)
			result, err := aggregator.AggregateFeedback(ctx, 100, nil, 1)

			require.NoError(t, err)
			require.NotNil(t, result)
			// Should be empty array
			assert.Equal(t, "[]", result.PreviousAttemptsJSON)
		})
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
