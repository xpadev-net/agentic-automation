package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"

	"go.uber.org/zap"
)

const (
	// MaxRetryCount is the maximum number of retry attempts allowed for an AgentRun
	MaxRetryCount = 50
)

// RetryOrchestrator manages retry count and validation for AgentRun retries
type RetryOrchestrator struct {
	agentRunRepo repositories.AgentRunRepository
	logger       *zap.Logger
}

// NewRetryOrchestrator creates a new RetryOrchestrator instance.
// It requires an AgentRunRepository and an optional logger.
//
// Parameters:
//   - agentRunRepo: AgentRunRepository instance (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses config.GetLogger())
//
// Returns:
//   - *RetryOrchestrator: Initialized service instance
func NewRetryOrchestrator(agentRunRepo repositories.AgentRunRepository, logger *zap.Logger) *RetryOrchestrator {
	if agentRunRepo == nil {
		panic("agentRunRepo is required for RetryOrchestrator")
	}

	// Use config.GetLogger() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = config.GetLogger()
	}

	logger.Info("RetryOrchestrator initialized",
		zap.String("service", "retry_orchestrator"),
	)

	return &RetryOrchestrator{
		agentRunRepo: agentRunRepo,
		logger:       logger,
	}
}

// ShouldRetry checks if an AgentRun should be retried based on its retry count.
// Returns true if retry_count < MaxRetryCount, false otherwise.
//
// Parameters:
//   - agentRun: AgentRun instance to check (nil safe, returns false if nil)
//
// Returns:
//   - bool: true if retry is allowed, false otherwise
func (r *RetryOrchestrator) ShouldRetry(agentRun *models.AgentRun) bool {
	if agentRun == nil {
		return false
	}

	shouldRetry := agentRun.RetryCount < MaxRetryCount

	r.logger.Debug("Checking if retry is allowed",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
		zap.Int("max_retry_count", MaxRetryCount),
		zap.Bool("should_retry", shouldRetry),
		zap.String("service", "retry_orchestrator"),
	)

	return shouldRetry
}

// IncrementRetryCount increments the retry count for an AgentRun and saves it to the database.
// This method does NOT perform state transitions - it only updates the retry_count field.
//
// The method enforces the maximum retry limit (MaxRetryCount = 50). If the retry count
// has already reached or exceeded the maximum, this method returns an error and does
// not increment the count. This prevents retry_count from exceeding the configured maximum.
//
// This method uses an atomic UPDATE query at the repository level to prevent lost increments
// under concurrent retries. The atomic operation ensures that even if multiple calls happen
// simultaneously, each increment will be applied correctly without race conditions.
// The caller's agentRun parameter is updated with the latest data from the database upon return.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control (reserved for future use)
//   - agentRun: AgentRun instance to update (must not be nil, must have valid ID).
//     The struct will be updated with the latest data from the database upon return.
//
// Returns:
//   - error: Error if validation fails, max retries already reached, or database update fails
func (r *RetryOrchestrator) IncrementRetryCount(ctx context.Context, agentRun *models.AgentRun) error {
	// Parameter validation
	if agentRun == nil {
		r.logger.Error("agentRun must not be nil",
			zap.String("service", "retry_orchestrator"),
		)
		return errors.New("agentRun must not be nil")
	}

	if agentRun.ID == 0 {
		r.logger.Error("agentRun.ID must not be zero",
			zap.String("service", "retry_orchestrator"),
		)
		return errors.New("agentRun.ID must not be zero")
	}

	// Get current retry count for logging (before atomic increment)
	currentRetryCount := agentRun.RetryCount

	r.logger.Info("Incrementing retry count atomically",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("current_retry_count", currentRetryCount),
		zap.Int("max_retry_count", MaxRetryCount),
		zap.String("service", "retry_orchestrator"),
	)

	// Use atomic increment at the repository level to prevent lost increments under concurrent retries
	// This uses SQL: UPDATE ... SET retry_count = retry_count + 1 WHERE id = ? AND retry_count < ?
	// which ensures atomicity and prevents race conditions
	newRetryCount, err := r.agentRunRepo.IncrementRetryCount(agentRun.ID, MaxRetryCount)
	if err != nil {
		// Check if error is due to max retries already reached
		if strings.Contains(err.Error(), "already at or above maximum") {
			r.logger.Warn("Cannot increment retry count: max retries already reached",
				zap.Int("agent_run_id", agentRun.ID),
				zap.Int("retry_count", newRetryCount),
				zap.Int("max_retry_count", MaxRetryCount),
				zap.Error(err),
				zap.String("service", "retry_orchestrator"),
			)
			// Reload full record to update caller's agentRun with latest data
			latestAgentRun, reloadErr := r.agentRunRepo.GetByID(agentRun.ID)
			if reloadErr == nil {
				*agentRun = *latestAgentRun
			}
			return fmt.Errorf("maximum retry count (%d) already reached, cannot increment", MaxRetryCount)
		}

		r.logger.Error("Failed to increment retry count",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Error(err),
			zap.String("service", "retry_orchestrator"),
		)
		return fmt.Errorf("failed to increment retry count: %w", err)
	}

	// Reload the full AgentRun record to update caller's agentRun with all latest data
	latestAgentRun, err := r.agentRunRepo.GetByID(agentRun.ID)
	if err != nil {
		r.logger.Error("Failed to reload AgentRun after increment",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("new_retry_count", newRetryCount),
			zap.Error(err),
			zap.String("service", "retry_orchestrator"),
		)
		// Even if reload fails, update retry_count in the caller's struct
		agentRun.RetryCount = newRetryCount
		return fmt.Errorf("failed to reload AgentRun after increment: %w", err)
	}

	// Update the caller's agentRun with the latest data for consistency
	*agentRun = *latestAgentRun

	// Log successful update
	r.logger.Info("Retry count incremented successfully",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("old_retry_count", currentRetryCount),
		zap.Int("new_retry_count", newRetryCount),
		zap.String("service", "retry_orchestrator"),
	)

	return nil
}

// GetRetryCount returns the current retry count for an AgentRun.
// This is a helper method for convenience.
//
// Parameters:
//   - agentRun: AgentRun instance (nil safe, returns 0 if nil)
//
// Returns:
//   - int: Current retry count, or 0 if agentRun is nil
func (r *RetryOrchestrator) GetRetryCount(agentRun *models.AgentRun) int {
	if agentRun == nil {
		return 0
	}
	return agentRun.RetryCount
}

// IsMaxRetriesReached checks if the maximum retry count has been reached for an AgentRun.
// Returns true if retry_count >= MaxRetryCount, false otherwise.
//
// Parameters:
//   - agentRun: AgentRun instance to check (nil safe, returns true if nil to indicate limit reached)
//
// Returns:
//   - bool: true if max retries reached, false otherwise
func (r *RetryOrchestrator) IsMaxRetriesReached(agentRun *models.AgentRun) bool {
	if agentRun == nil {
		return true
	}

	maxReached := agentRun.RetryCount >= MaxRetryCount

	r.logger.Debug("Checking if max retries reached",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
		zap.Int("max_retry_count", MaxRetryCount),
		zap.Bool("max_reached", maxReached),
		zap.String("service", "retry_orchestrator"),
	)

	return maxReached
}

// ValidateRetryCount validates that the retry count is within acceptable bounds.
// Returns an error if retry_count < 0. Logs a warning if retry_count > MaxRetryCount
// but does not return an error (to allow existing exceeded counts).
//
// Parameters:
//   - agentRun: AgentRun instance to validate (must not be nil)
//
// Returns:
//   - error: Error if validation fails (retry_count < 0), nil otherwise
func (r *RetryOrchestrator) ValidateRetryCount(agentRun *models.AgentRun) error {
	// Parameter validation
	if agentRun == nil {
		r.logger.Error("agentRun must not be nil for validation",
			zap.String("service", "retry_orchestrator"),
		)
		return errors.New("agentRun must not be nil")
	}

	// Validate retry_count is non-negative
	if agentRun.RetryCount < 0 {
		err := fmt.Errorf("retry_count must be non-negative, got: %d", agentRun.RetryCount)
		r.logger.Error("Retry count validation failed",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("retry_count", agentRun.RetryCount),
			zap.Error(err),
			zap.String("service", "retry_orchestrator"),
		)
		return err
	}

	// Warn if retry_count exceeds maximum (but don't return error - allow existing exceeded counts)
	if agentRun.RetryCount > MaxRetryCount {
		r.logger.Warn("Retry count exceeds maximum",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("retry_count", agentRun.RetryCount),
			zap.Int("max_retry_count", MaxRetryCount),
			zap.String("service", "retry_orchestrator"),
		)
	}

	return nil
}

// GetRemainingRetries calculates and returns the number of remaining retry attempts.
// Returns 0 if retry_count >= MaxRetryCount or if agentRun is nil.
//
// Parameters:
//   - agentRun: AgentRun instance (nil safe, returns 0 if nil)
//
// Returns:
//   - int: Number of remaining retries, or 0 if agentRun is nil or limit exceeded
func (r *RetryOrchestrator) GetRemainingRetries(agentRun *models.AgentRun) int {
	if agentRun == nil {
		return 0
	}

	remaining := MaxRetryCount - agentRun.RetryCount

	// If remaining is negative (exceeded limit), return 0
	if remaining < 0 {
		return 0
	}

	return remaining
}
