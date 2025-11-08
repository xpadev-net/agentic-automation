package utils

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"time"

	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"

	"agentic-automation/internal/clients"
)

// RetryConfig represents configuration for retry behavior
type RetryConfig struct {
	MaxAttempts   int           // Maximum number of retry attempts (default: 3)
	InitialDelay  time.Duration // Initial delay before first retry (default: 1 second)
	MaxDelay      time.Duration // Maximum delay between retries (default: 60 seconds)
	JitterPercent float64       // Jitter percentage ±N% (default: 0.25 = ±25%)
}

// DefaultRetryConfig returns a default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  1 * time.Second,
		MaxDelay:      60 * time.Second,
		JitterPercent: 0.25,
	}
}

// MaxRetriesExceededError indicates that the maximum number of retry attempts was exceeded
type MaxRetriesExceededError struct {
	MaxAttempts int
	LastError   error
}

func (e *MaxRetriesExceededError) Error() string {
	if e.LastError != nil {
		return fmt.Sprintf("max retries (%d) exceeded: %v", e.MaxAttempts, e.LastError)
	}
	return fmt.Sprintf("max retries (%d) exceeded", e.MaxAttempts)
}

func (e *MaxRetriesExceededError) Unwrap() error {
	return e.LastError
}

// ContextCancelledError indicates that the context was cancelled during retry
type ContextCancelledError struct {
	Cause error
}

func (e *ContextCancelledError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("context cancelled: %v", e.Cause)
	}
	return "context cancelled"
}

func (e *ContextCancelledError) Unwrap() error {
	return e.Cause
}

// IsRetryableError determines if an error should be retried
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context cancellation/deadline exceeded
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false // Context cancellation should not be retried
	}

	// Check for github.ErrorResponse (raw GitHub API errors)
	var ghErr *github.ErrorResponse
	if errors.As(err, &ghErr) {
		if ghErr.Response != nil {
			statusCode := ghErr.Response.StatusCode
			// Retry on rate limit (429) and server errors (5xx)
			return statusCode == http.StatusTooManyRequests || (statusCode >= 500 && statusCode < 600)
		}
	}

	// Check for clients.GitHubError (wrapped GitHub API errors)
	var ghClientErr *clients.GitHubError
	if errors.As(err, &ghClientErr) {
		if ghClientErr.ErrorResponse != nil && ghClientErr.ErrorResponse.Response != nil {
			statusCode := ghClientErr.ErrorResponse.Response.StatusCode
			// Retry on rate limit (429) and server errors (5xx)
			return statusCode == http.StatusTooManyRequests || (statusCode >= 500 && statusCode < 600)
		}
	}

	// Check for network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Retry on timeouts and temporary network errors
		// Note: net.Error.Temporary() is deprecated in Go 1.18+, but we check it for compatibility
		if netErr.Timeout() {
			return true
		}
		// Check Temporary() for older Go versions and compatibility
		if tempErr, ok := err.(interface {
			Temporary() bool
		}); ok {
			return tempErr.Temporary()
		}
	}

	// Check for HTTP response errors via interface
	if resp, ok := err.(interface {
		StatusCode() int
	}); ok {
		statusCode := resp.StatusCode()
		return statusCode == http.StatusTooManyRequests || (statusCode >= 500 && statusCode < 600)
	}

	// Default: don't retry unknown errors
	return false
}

// calculateBackoff calculates the backoff delay for a given attempt number
func calculateBackoff(attempt int, config *RetryConfig) time.Duration {
	if attempt <= 1 {
		return 0 // First attempt is immediate
	}

	// Exponential backoff: 2^(attempt-2) * InitialDelay
	// attempt=2: 2^0 * InitialDelay = 1 * InitialDelay
	// attempt=3: 2^1 * InitialDelay = 2 * InitialDelay
	// attempt=4: 2^2 * InitialDelay = 4 * InitialDelay
	// attempt=5: 2^3 * InitialDelay = 8 * InitialDelay
	backoff := time.Duration(math.Pow(2, float64(attempt-2))) * config.InitialDelay

	// Cap at MaxDelay
	if backoff > config.MaxDelay {
		backoff = config.MaxDelay
	}

	return backoff
}

// applyJitter applies random jitter to a delay duration
func applyJitter(delay time.Duration, jitterPercent float64) time.Duration {
	if delay == 0 || jitterPercent <= 0 {
		return delay
	}

	// Generate random value in range [-jitterPercent, +jitterPercent]
	jitter := (rand.Float64()*2 - 1) * jitterPercent // Range: [-1, 1] * jitterPercent

	// Apply jitter: delay * (1 + jitter)
	// Example: delay=1s, jitterPercent=0.25, jitter=-0.1 -> 1s * (1 - 0.1) = 0.9s
	// Example: delay=1s, jitterPercent=0.25, jitter=+0.15 -> 1s * (1 + 0.15) = 1.15s
	jitteredDelay := float64(delay) * (1 + jitter)

	// Ensure minimum delay of 0
	if jitteredDelay < 0 {
		jitteredDelay = 0
	}

	return time.Duration(jitteredDelay)
}

// Retry executes a function with retry logic using exponential backoff and jitter
func Retry(ctx context.Context, fn func() error, config *RetryConfig, logger *zap.Logger) error {
	if config == nil {
		config = DefaultRetryConfig()
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	var lastErr error

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Check context cancellation before each attempt
		select {
		case <-ctx.Done():
			return &ContextCancelledError{Cause: ctx.Err()}
		default:
		}

		// Execute the function
		err := fn()
		if err == nil {
			// Success - no retry needed
			if attempt > 1 {
				logger.Info("Retry succeeded",
					zap.Int("attempt", attempt),
					zap.Int("max_attempts", config.MaxAttempts))
			}
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !IsRetryableError(err) {
			logger.Warn("Error is not retryable, stopping retry",
				zap.Int("attempt", attempt),
				zap.Error(err))
			return err
		}

		// Don't wait after the last attempt
		if attempt >= config.MaxAttempts {
			break
		}

		// Calculate backoff with jitter
		backoff := calculateBackoff(attempt+1, config) // +1 because we're calculating for the next attempt
		jitteredBackoff := applyJitter(backoff, config.JitterPercent)

		logger.Warn("Retry attempt failed, waiting before next attempt",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", config.MaxAttempts),
			zap.Duration("backoff", jitteredBackoff),
			zap.Error(err))

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return &ContextCancelledError{Cause: ctx.Err()}
		case <-time.After(jitteredBackoff):
			// Continue to next attempt
		}
	}

	// All retries exhausted
	logger.Error("All retry attempts exhausted",
		zap.Int("max_attempts", config.MaxAttempts),
		zap.Error(lastErr))

	return &MaxRetriesExceededError{
		MaxAttempts: config.MaxAttempts,
		LastError:   lastErr,
	}
}

// RetryWithResult executes a function that returns a result and error with retry logic
func RetryWithResult[T any](ctx context.Context, fn func() (T, error), config *RetryConfig, logger *zap.Logger) (T, error) {
	var zero T

	if config == nil {
		config = DefaultRetryConfig()
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	var lastErr error

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Check context cancellation before each attempt
		select {
		case <-ctx.Done():
			return zero, &ContextCancelledError{Cause: ctx.Err()}
		default:
		}

		// Execute the function
		result, err := fn()
		if err == nil {
			// Success - no retry needed
			if attempt > 1 {
				logger.Info("Retry succeeded",
					zap.Int("attempt", attempt),
					zap.Int("max_attempts", config.MaxAttempts))
			}
			return result, nil
		}

		lastErr = err

		// Check if error is retryable
		if !IsRetryableError(err) {
			logger.Warn("Error is not retryable, stopping retry",
				zap.Int("attempt", attempt),
				zap.Error(err))
			return zero, err
		}

		// Don't wait after the last attempt
		if attempt >= config.MaxAttempts {
			break
		}

		// Calculate backoff with jitter
		backoff := calculateBackoff(attempt+1, config) // +1 because we're calculating for the next attempt
		jitteredBackoff := applyJitter(backoff, config.JitterPercent)

		logger.Warn("Retry attempt failed, waiting before next attempt",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", config.MaxAttempts),
			zap.Duration("backoff", jitteredBackoff),
			zap.Error(err))

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return zero, &ContextCancelledError{Cause: ctx.Err()}
		case <-time.After(jitteredBackoff):
			// Continue to next attempt
		}
	}

	// All retries exhausted
	logger.Error("All retry attempts exhausted",
		zap.Int("max_attempts", config.MaxAttempts),
		zap.Error(lastErr))

	return zero, &MaxRetriesExceededError{
		MaxAttempts: config.MaxAttempts,
		LastError:   lastErr,
	}
}
