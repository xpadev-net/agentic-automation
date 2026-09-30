package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

const (
	// MaxRetryCount is the maximum number of retry attempts allowed for an AgentRun
	MaxRetryCount = 50
)

// RetryOrchestrator manages retry count and validation for AgentRun retries
type RetryOrchestrator struct {
	agentRunRepo        repositories.AgentRunRepository
	jobService          KubernetesJobService
	issueContextService *IssueContextService
	logger              *config.AppLogger
}

// NewRetryOrchestrator creates a new RetryOrchestrator instance.
// It requires an AgentRunRepository and an optional logger.
// If jobService and issueContextService are provided, TriggerRetry and HandleMaxRetriesExceeded can be used.
//
// Parameters:
//   - agentRunRepo: AgentRunRepository instance (must not be nil, will panic if nil)
//   - jobService: KubernetesJobService instance (optional, can be nil if only using basic retry count methods)
//   - issueContextService: IssueContextService instance (optional, can be nil if only using basic retry count methods)
//   - logger: Structured logger instance (if nil, uses config.GetLogger())
//
// Returns:
//   - *RetryOrchestrator: Initialized service instance
func NewRetryOrchestrator(
	agentRunRepo repositories.AgentRunRepository,
	jobService KubernetesJobService,
	issueContextService *IssueContextService,
	logger *config.AppLogger,
) *RetryOrchestrator {
	if agentRunRepo == nil {
		panic("agentRunRepo is required for RetryOrchestrator")
	}

	// Use config.GetLogger() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = config.GetLogger()
	}

	logger.Info("RetryOrchestrator initialized",
		config.String("service", "retry_orchestrator"),
	)

	return &RetryOrchestrator{
		agentRunRepo:        agentRunRepo,
		jobService:          jobService,
		issueContextService: issueContextService,
		logger:              logger,
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
		config.Int("agent_run_id", agentRun.ID),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("max_retry_count", MaxRetryCount),
		config.Bool("should_retry", shouldRetry),
		config.String("service", "retry_orchestrator"),
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
			config.String("service", "retry_orchestrator"),
		)
		return errors.New("agentRun must not be nil")
	}

	if agentRun.ID == 0 {
		r.logger.Error("agentRun.ID must not be zero",
			config.String("service", "retry_orchestrator"),
		)
		return errors.New("agentRun.ID must not be zero")
	}

	// Get current retry count for logging (before atomic increment)
	currentRetryCount := agentRun.RetryCount

	r.logger.Info("Incrementing retry count atomically",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("current_retry_count", currentRetryCount),
		config.Int("max_retry_count", MaxRetryCount),
		config.String("service", "retry_orchestrator"),
	)

	// Use atomic increment at the repository level to prevent lost increments under concurrent retries
	// This uses SQL: UPDATE ... SET retry_count = retry_count + 1 WHERE id = ? AND retry_count < ?
	// which ensures atomicity and prevents race conditions
	newRetryCount, err := r.agentRunRepo.IncrementRetryCount(agentRun.ID, MaxRetryCount)
	if err != nil {
		// Check if error is due to max retries already reached
		if strings.Contains(err.Error(), "already at or above maximum") {
			r.logger.Warn("Cannot increment retry count: max retries already reached",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("retry_count", newRetryCount),
				config.Int("max_retry_count", MaxRetryCount),
				config.Error(err),
				config.String("service", "retry_orchestrator"),
			)
			// Reload full record to update caller's agentRun with latest data
			latestAgentRun, reloadErr := r.agentRunRepo.GetByID(agentRun.ID)
			if reloadErr == nil {
				*agentRun = *latestAgentRun
			}
			return fmt.Errorf("maximum retry count (%d) already reached, cannot increment", MaxRetryCount)
		}

		r.logger.Error("Failed to increment retry count",
			config.Int("agent_run_id", agentRun.ID),
			config.Error(err),
			config.String("service", "retry_orchestrator"),
		)
		return fmt.Errorf("failed to increment retry count: %w", err)
	}

	// Reload the full AgentRun record to update caller's agentRun with all latest data
	latestAgentRun, err := r.agentRunRepo.GetByID(agentRun.ID)
	if err != nil {
		r.logger.Error("Failed to reload AgentRun after increment",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("new_retry_count", newRetryCount),
			config.Error(err),
			config.String("service", "retry_orchestrator"),
		)
		// Even if reload fails, update retry_count in the caller's struct
		agentRun.RetryCount = newRetryCount
		return fmt.Errorf("failed to reload AgentRun after increment: %w", err)
	}

	// Update the caller's agentRun with the latest data for consistency
	*agentRun = *latestAgentRun

	// Log successful update
	r.logger.Info("Retry count incremented successfully",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("old_retry_count", currentRetryCount),
		config.Int("new_retry_count", newRetryCount),
		config.String("service", "retry_orchestrator"),
	)

	return nil
}

// TriggerRetry triggers a retry for an AgentRun by incrementing retry count,
// updating the state, and creating a new Kubernetes Job with aggregated feedback.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - agentRun: AgentRun record to retry (must not be nil)
//   - issue: Issue record associated with the AgentRun (must not be nil)
//   - feedback: AggregatedFeedback containing review feedback and CI logs (may be nil)
//
// Returns:
//   - error: Error if retry cannot be triggered (e.g., max retries exceeded, Job creation failed)
func (r *RetryOrchestrator) TriggerRetry(
	ctx context.Context,
	agentRun *models.AgentRun,
	issue *models.Issue,
	feedback *AggregatedFeedback,
) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if r.jobService == nil {
		return fmt.Errorf("jobService is required for TriggerRetry")
	}
	if r.issueContextService == nil {
		return fmt.Errorf("issueContextService is required for TriggerRetry")
	}

	if agentRun == nil {
		return fmt.Errorf("agentRun must not be nil")
	}
	if issue == nil {
		return fmt.Errorf("issue must not be nil")
	}

	r.logger.Info("Triggering retry for AgentRun",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("current_retry_count", agentRun.RetryCount),
		config.Int("issue_id", issue.ID),
	)

	// Check if retry is allowed
	if !r.ShouldRetry(agentRun) {
		r.logger.Warn("Max retry count exceeded, cannot trigger retry",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("retry_count", agentRun.RetryCount),
			config.Int("max_retry_count", MaxRetryCount),
		)
		return r.HandleMaxRetriesExceeded(agentRun)
	}

	if atomic, ok := r.agentRunRepo.(repositories.AtomicRetryAdmission); ok {
		admitted, err := atomic.BeginRetry(agentRun, MaxRetryCount)
		if err != nil {
			return fmt.Errorf("failed to admit retry: %w", err)
		}
		*agentRun = *admitted
		if agentRun.RetryCount >= MaxRetryCount {
			return r.HandleMaxRetriesExceeded(agentRun)
		}
	} else {
		// Increment retry count atomically
		if err := r.IncrementRetryCount(ctx, agentRun); err != nil {
			return fmt.Errorf("failed to increment retry count: %w", err)
		}

		// Check if max retries exceeded after increment
		if agentRun.RetryCount >= MaxRetryCount {
			r.logger.Warn("Max retry count reached after increment",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("retry_count", agentRun.RetryCount),
			)
			return r.HandleMaxRetriesExceeded(agentRun)
		}

		// Update state to queued for retry
		agentRun.State = "queued"
		agentRun.StartedAt = nil
		agentRun.CompletedAt = nil
		agentRun.ErrorMessage = nil
		agentRun.Output = nil
		name := agentRun.AttemptJobName()
		agentRun.JobName = &name
		if err := r.agentRunRepo.Update(agentRun); err != nil {
			r.logger.Error("Failed to update AgentRun state for retry",
				config.Int("agent_run_id", agentRun.ID),
				config.Error(err),
			)
			return fmt.Errorf("failed to update agent run state: %w", err)
		}

	}

	// Extract repository info from issue.Repo (format: "owner/repo")
	repoParts := splitRepo(issue.Repo)
	if len(repoParts) != 2 {
		return fmt.Errorf("invalid repo format: %s", issue.Repo)
	}
	owner := repoParts[0]
	repo := repoParts[1]

	// Re-collect Issue context to get updated prompt
	issueContext, err := r.issueContextService.CollectIssueContext(ctx, owner, repo, issue.Number)
	if err != nil {
		r.logger.Error("Failed to collect Issue context for retry",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("issue_id", issue.ID),
			config.Error(err),
		)
		return fmt.Errorf("failed to collect issue context: %w", err)
	}

	// Format prompt (no user instruction for retry)
	prompt := r.issueContextService.FormatPrompt(issueContext, "")

	// Get existing branch name from PR associated with agent run (for continuing work on existing PR)
	var existingBranchName string
	prRepo := repositories.NewPullRequestRepository(config.GetDB())
	// Prefer PR associated with this agent run if available
	if agentRun.PRID != nil {
		pr, prErr := prRepo.FindByID(*agentRun.PRID)
		if prErr == nil && pr != nil && pr.Status == "open" {
			existingBranchName = pr.Branch
			r.logger.Info("Found PR associated with agent run, will use existing branch for retry",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("pr_id", *agentRun.PRID),
				config.Int("pr_number", pr.Number),
				config.String("branch", existingBranchName),
			)
		} else if prErr != nil {
			r.logger.Warn("Failed to find PR associated with agent run, falling back to issue PRs",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("pr_id", *agentRun.PRID),
				config.Error(prErr),
			)
		} else if pr != nil && pr.Status != "open" {
			r.logger.Info("PR associated with agent run is not open, falling back to issue PRs",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("pr_id", *agentRun.PRID),
				config.String("pr_status", pr.Status),
			)
		}
	}

	// Fallback: scan all PRs for the issue if no branch found from agent run's PR
	if existingBranchName == "" {
		prs, prErr := prRepo.FindByIssueID(issue.ID)
		if prErr == nil && len(prs) > 0 {
			// Use the first open PR if multiple exist
			for _, pr := range prs {
				if pr.Status == "open" {
					existingBranchName = pr.Branch
					r.logger.Info("Found existing PR for issue, will use existing branch for retry",
						config.Int("agent_run_id", agentRun.ID),
						config.Int("issue_id", issue.ID),
						config.Int("pr_number", pr.Number),
						config.String("branch", existingBranchName),
					)
					break
				}
			}
		}
	}

	r.logger.Info("Creating retry Job for AgentRun",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("retry_count", agentRun.RetryCount),
		config.Bool("has_feedback", feedback != nil),
		config.String("branch_name", existingBranchName),
	)

	// Record dispatch state before the API call so a lost response is observable.
	if atomic, ok := r.agentRunRepo.(repositories.AtomicAgentRunLifecycle); ok {
		if err := atomic.TransitionLifecycle(agentRun.ID, "started", nil, nil, nil); err != nil {
			return err
		}
		if latest, err := r.agentRunRepo.GetByID(agentRun.ID); err == nil {
			*agentRun = *latest
		} else {
			return err
		}
	}

	// Create new Kubernetes Job with feedback
	_, err = r.jobService.CreateJobForAgentRunWithFeedback(ctx, agentRun, issue, prompt, feedback, existingBranchName)
	if err != nil {
		r.logger.Error("Failed to create retry Job",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("retry_count", agentRun.RetryCount),
			config.Error(err),
		)
		return fmt.Errorf("failed to create retry job: %w", err)
	}

	r.logger.Info("Retry Job created successfully",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("retry_count", agentRun.RetryCount),
	)

	// Update AgentRun with job name for cleanup
	if err := r.agentRunRepo.Update(agentRun); err != nil {
		r.logger.Warn("Failed to update AgentRun with job name",
			config.Error(err),
			config.Int("agent_run_id", agentRun.ID),
		)
		// Non-blocking: continue even if update fails
	}

	return nil
}

// HandleMaxRetriesExceeded handles the case when max retries have been exceeded.
// It updates the AgentRun state to "failed" and triggers failure notifications.
//
// Parameters:
//   - agentRun: AgentRun record that exceeded max retries (must not be nil)
//
// Returns:
//   - error: Error if state update fails
//
// Note: GitHub and Discord notifications should be triggered by the caller or via
// a separate notification service (to be implemented in T100, T101).
func (r *RetryOrchestrator) HandleMaxRetriesExceeded(agentRun *models.AgentRun) error {
	if agentRun == nil {
		return fmt.Errorf("agentRun must not be nil")
	}

	r.logger.Warn("Handling max retries exceeded",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("max_retry_count", MaxRetryCount),
	)

	// Update state to failed
	agentRun.State = "failed"
	now := time.Now()
	agentRun.CompletedAt = &now

	// Update AgentRun in database
	if err := r.agentRunRepo.Update(agentRun); err != nil {
		r.logger.Error("Failed to update AgentRun state to failed",
			config.Int("agent_run_id", agentRun.ID),
			config.Error(err),
		)
		return fmt.Errorf("failed to update agent run state: %w", err)
	}

	r.logger.Info("AgentRun state updated to failed due to max retries",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("retry_count", agentRun.RetryCount),
	)

	// TODO(T100, T101): Trigger GitHub and Discord notifications for max retries exceeded
	// This should be handled by the caller or via a separate notification service

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
		config.Int("agent_run_id", agentRun.ID),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("max_retry_count", MaxRetryCount),
		config.Bool("max_reached", maxReached),
		config.String("service", "retry_orchestrator"),
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
			config.String("service", "retry_orchestrator"),
		)
		return errors.New("agentRun must not be nil")
	}

	// Validate retry_count is non-negative
	if agentRun.RetryCount < 0 {
		err := fmt.Errorf("retry_count must be non-negative, got: %d", agentRun.RetryCount)
		r.logger.Error("Retry count validation failed",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("retry_count", agentRun.RetryCount),
			config.Error(err),
			config.String("service", "retry_orchestrator"),
		)
		return err
	}

	// Warn if retry_count exceeds maximum (but don't return error - allow existing exceeded counts)
	if agentRun.RetryCount > MaxRetryCount {
		r.logger.Warn("Retry count exceeds maximum",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("retry_count", agentRun.RetryCount),
			config.Int("max_retry_count", MaxRetryCount),
			config.String("service", "retry_orchestrator"),
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

// splitRepo splits a repository full name (format: "owner/repo") into owner and repo parts.
func splitRepo(repo string) []string {
	return strings.SplitN(repo, "/", 2)
}
