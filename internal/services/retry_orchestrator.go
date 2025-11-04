package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"

	"go.uber.org/zap"
)

const (
	// MaxRetryCount is the maximum number of retries allowed for an AgentRun
	MaxRetryCount = 50
)

// RetryOrchestrator manages retry logic for AgentRun executions
type RetryOrchestrator struct {
	agentRunRepo        repositories.AgentRunRepository
	jobService          KubernetesJobService
	issueContextService *IssueContextService
	logger              *zap.Logger
}

// NewRetryOrchestrator creates a new RetryOrchestrator instance.
// It requires an AgentRunRepository, KubernetesJobService, IssueContextService, and logger as dependencies.
//
// Parameters:
//   - agentRunRepo: AgentRunRepository instance (must not be nil, will panic if nil)
//   - jobService: KubernetesJobService instance (must not be nil, will panic if nil)
//   - issueContextService: IssueContextService instance (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *RetryOrchestrator: Initialized orchestrator instance
func NewRetryOrchestrator(
	agentRunRepo repositories.AgentRunRepository,
	jobService KubernetesJobService,
	issueContextService *IssueContextService,
	logger *zap.Logger,
) *RetryOrchestrator {
	if agentRunRepo == nil {
		panic("agentRunRepo is required for RetryOrchestrator")
	}
	if jobService == nil {
		panic("jobService is required for RetryOrchestrator")
	}
	if issueContextService == nil {
		panic("issueContextService is required for RetryOrchestrator")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &RetryOrchestrator{
		agentRunRepo:        agentRunRepo,
		jobService:          jobService,
		issueContextService: issueContextService,
		logger:              logger,
	}
}

// ShouldRetry checks if an AgentRun should be retried based on retry count.
// Returns true if retry_count < MaxRetryCount, false otherwise.
//
// Parameters:
//   - agentRun: AgentRun record to check (must not be nil)
//
// Returns:
//   - bool: true if retry is allowed, false if max retries exceeded
func (r *RetryOrchestrator) ShouldRetry(agentRun *models.AgentRun) bool {
	if agentRun == nil {
		return false
	}

	return agentRun.RetryCount < MaxRetryCount
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
	if agentRun == nil {
		return fmt.Errorf("agentRun must not be nil")
	}
	if issue == nil {
		return fmt.Errorf("issue must not be nil")
	}

	r.logger.Info("Triggering retry for AgentRun",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("current_retry_count", agentRun.RetryCount),
		zap.Int("issue_id", issue.ID),
	)

	// Check if retry is allowed
	if !r.ShouldRetry(agentRun) {
		r.logger.Warn("Max retry count exceeded, cannot trigger retry",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("retry_count", agentRun.RetryCount),
			zap.Int("max_retry_count", MaxRetryCount),
		)
		return r.HandleMaxRetriesExceeded(agentRun)
	}

	// Increment retry count
	agentRun.RetryCount++
	agentRun.State = "queued" // Reset state to queued for retry

	// Update AgentRun in database
	if err := r.agentRunRepo.Update(agentRun); err != nil {
		r.logger.Error("Failed to update AgentRun for retry",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Error(err),
		)
		return fmt.Errorf("failed to update agent run: %w", err)
	}

	r.logger.Info("AgentRun retry count incremented",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("new_retry_count", agentRun.RetryCount),
	)

	// Check if max retries exceeded after increment
	if agentRun.RetryCount >= MaxRetryCount {
		r.logger.Warn("Max retry count reached after increment",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("retry_count", agentRun.RetryCount),
		)
		return r.HandleMaxRetriesExceeded(agentRun)
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
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("issue_id", issue.ID),
			zap.Error(err),
		)
		return fmt.Errorf("failed to collect issue context: %w", err)
	}

	// Format prompt
	prompt := r.issueContextService.FormatPrompt(issueContext)

	r.logger.Info("Creating retry Job for AgentRun",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
		zap.Bool("has_feedback", feedback != nil),
	)

	// Create new Kubernetes Job with feedback
	_, err = r.jobService.CreateJobForAgentRunWithFeedback(ctx, agentRun, issue, prompt, feedback)
	if err != nil {
		r.logger.Error("Failed to create retry Job",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("retry_count", agentRun.RetryCount),
			zap.Error(err),
		)
		return fmt.Errorf("failed to create retry job: %w", err)
	}

	r.logger.Info("Retry Job created successfully",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
	)

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
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
		zap.Int("max_retry_count", MaxRetryCount),
	)

	// Update state to failed
	agentRun.State = "failed"
	now := time.Now()
	agentRun.CompletedAt = &now

	// Update AgentRun in database
	if err := r.agentRunRepo.Update(agentRun); err != nil {
		r.logger.Error("Failed to update AgentRun state to failed",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Error(err),
		)
		return fmt.Errorf("failed to update agent run state: %w", err)
	}

	r.logger.Info("AgentRun state updated to failed due to max retries",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("retry_count", agentRun.RetryCount),
	)

	// TODO(T100, T101): Trigger GitHub and Discord notifications for max retries exceeded
	// This should be handled by the caller or via a separate notification service

	return nil
}

// splitRepo splits a repository full name (format: "owner/repo") into owner and repo parts.
func splitRepo(repo string) []string {
	return strings.SplitN(repo, "/", 2)
}
