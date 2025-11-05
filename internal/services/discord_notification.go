package services

import (
	"context"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"go.uber.org/zap"
)

// DiscordNotificationService provides Discord notification functionality
// It wraps the DiscordClient to provide a service-layer abstraction
// for Discord notifications required by webhook handlers.
type DiscordNotificationService struct {
	discordClient *clients.DiscordClient
	logger        *zap.Logger
}

// NewDiscordNotificationService creates a new DiscordNotificationService instance.
// It requires a Discord client and logger as dependencies.
//
// Parameters:
//   - discordClient: Discord webhook client (can be nil - notifications will be disabled)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *DiscordNotificationService: Initialized service instance
func NewDiscordNotificationService(discordClient *clients.DiscordClient, logger *zap.Logger) *DiscordNotificationService {
	// Warn if discordClient is nil (non-blocking)
	if discordClient == nil {
		if logger != nil {
			logger.Warn("Discord client is nil. Discord notifications will be disabled.")
		}
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &DiscordNotificationService{
		discordClient: discordClient,
		logger:        logger,
	}
}

// NotifyPRCreated sends a Discord notification when a PR is successfully created.
// This is a non-blocking method - errors are logged but do not interrupt the flow.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - pr: PullRequest model (must not be nil)
//   - issue: Issue model (can be nil)
//   - agentRun: AgentRun model (must not be nil, AgentType will be used)
//
// Returns:
//   - error: Error if notification failed (should be logged but not block execution)
func (s *DiscordNotificationService) NotifyPRCreated(ctx context.Context, pr *models.PullRequest, issue *models.Issue, agentRun *models.AgentRun) error {
	// Check if discord client is disabled (non-blocking)
	if s.discordClient == nil {
		return nil
	}

	// Send notification
	err := s.discordClient.SendPRCreatedNotification(ctx, pr, issue, agentRun.AgentType)
	if err != nil {
		s.logger.Error("Failed to send Discord PR created notification",
			zap.Error(err),
			zap.Int("pr_number", pr.Number),
			zap.Int("agent_run_id", agentRun.ID))
		return err
	}

	return nil
}

// NotifyMaxRetries sends a Discord notification when max retries (50) is exceeded.
// This is a non-blocking method - errors are logged but do not interrupt the flow.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - agentRun: AgentRun model (must not be nil, contains RetryCount and ErrorMessage)
//   - issue: Issue model (must not be nil, contains Issue number and title)
//
// Returns:
//   - error: Error if notification failed (should be logged but not block execution)
func (s *DiscordNotificationService) NotifyMaxRetries(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue) error {
	// Check if discord client is disabled (non-blocking)
	if s.discordClient == nil {
		return nil
	}

	// Send notification
	err := s.discordClient.SendMaxRetriesNotification(ctx, agentRun, issue)
	if err != nil {
		s.logger.Error("Failed to send Discord max retries notification",
			zap.Error(err),
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("issue_number", issue.Number))
		return err
	}

	return nil
}

// NotifyMergeSuccess sends a Discord notification when a PR is auto-merged successfully.
// This is non-blocking; if the client is nil or sending fails, it only logs.
func (s *DiscordNotificationService) NotifyMergeSuccess(ctx context.Context, pr *models.PullRequest, issue *models.Issue, retryCount int) error {
	if s.discordClient == nil {
		return nil
	}
	if err := s.discordClient.SendPRMergedNotification(ctx, pr, issue, retryCount); err != nil {
		s.logger.Warn("Failed to send Discord PR merged notification",
			zap.Error(err),
			zap.Int("pr_number", pr.Number),
		)
		return err
	}
	return nil
}

// NotifyMergeFailure sends a Discord notification when a PR auto-merge fails.
// This is non-blocking; if the client is nil or sending fails, it only logs.
func (s *DiscordNotificationService) NotifyMergeFailure(ctx context.Context, pr *models.PullRequest, issue *models.Issue, errorMessage string, errorCode string) error {
	if s.discordClient == nil {
		return nil
	}
	if err := s.discordClient.SendPRMergeFailureNotification(ctx, pr, issue, errorMessage, errorCode); err != nil {
		s.logger.Warn("Failed to send Discord PR merge failure notification",
			zap.Error(err),
			zap.Int("pr_number", pr.Number),
			zap.String("error_code", errorCode),
		)
		return err
	}
	return nil
}
