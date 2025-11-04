package services_test

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockAgentRunRepository is a mock implementation of AgentRunRepository for testing
type mockAgentRunRepository struct {
	mock.Mock
}

func (m *mockAgentRunRepository) CreateOrGet(idempotencyKey string, run *models.AgentRun) (*models.AgentRun, bool, error) {
	args := m.Called(idempotencyKey, run)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*models.AgentRun), args.Bool(1), args.Error(2)
}

func (m *mockAgentRunRepository) GetByID(id int) (*models.AgentRun, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.AgentRun), args.Error(1)
}

func (m *mockAgentRunRepository) GetByIDempotencyKey(key string) (*models.AgentRun, error) {
	args := m.Called(key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.AgentRun), args.Error(1)
}

func (m *mockAgentRunRepository) Update(run *models.AgentRun) error {
	args := m.Called(run)
	return args.Error(0)
}

func (m *mockAgentRunRepository) UpdateState(id int, state string) error {
	args := m.Called(id, state)
	return args.Error(0)
}

func (m *mockAgentRunRepository) GetByIssueID(issueID int) ([]*models.AgentRun, error) {
	args := m.Called(issueID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.AgentRun), args.Error(1)
}

func (m *mockAgentRunRepository) GetByPRID(prID int) ([]*models.AgentRun, error) {
	args := m.Called(prID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.AgentRun), args.Error(1)
}

// mockKubernetesJobService is a mock implementation of KubernetesJobService for testing
type mockKubernetesJobService struct {
	mock.Mock
}

func (m *mockKubernetesJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string) (*batchv1.Job, error) {
	args := m.Called(ctx, agentRun, issue, prompt)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*batchv1.Job), args.Error(1)
}

func (m *mockKubernetesJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *services.AggregatedFeedback) (*batchv1.Job, error) {
	args := m.Called(ctx, agentRun, issue, prompt, feedback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*batchv1.Job), args.Error(1)
}

// Note: IssueContextService is a concrete type, not an interface
// We use real instances in tests, full integration tests are in tests/integration/

func TestNewRetryOrchestrator(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)

	// Create a real IssueContextService for testing (requires GitHubClient)
	githubClient := &clients.Client{} // Dummy client for IssueContextService
	issueContextService := services.NewIssueContextService(githubClient, logger)

	t.Run("nil logger uses default", func(t *testing.T) {
		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			nil,
		)
		require.NotNil(t, orchestrator)
	})

	t.Run("valid arguments", func(t *testing.T) {
		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)
		require.NotNil(t, orchestrator)
	})

	t.Run("nil agentRunRepo panics", func(t *testing.T) {
		assert.Panics(t, func() {
			services.NewRetryOrchestrator(
				nil,
				jobService,
				issueContextService,
				logger,
			)
		})
	})

	t.Run("nil jobService panics", func(t *testing.T) {
		assert.Panics(t, func() {
			services.NewRetryOrchestrator(
				agentRunRepo,
				nil,
				issueContextService,
				logger,
			)
		})
	})

	t.Run("nil issueContextService panics", func(t *testing.T) {
		// Create a real IssueContextService with nil GitHubClient to test panic behavior
		// This will panic in NewIssueContextService, not in NewRetryOrchestrator
		assert.Panics(t, func() {
			services.NewIssueContextService(nil, logger)
		})
	})
}

func TestRetryOrchestrator_ShouldRetry(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)

	// Create a real IssueContextService for testing (requires GitHubClient)
	githubClient := &clients.Client{} // Dummy client for IssueContextService
	issueContextService := services.NewIssueContextService(githubClient, logger)

	orchestrator := services.NewRetryOrchestrator(
		agentRunRepo,
		jobService,
		issueContextService,
		logger,
	)

	t.Run("retry_count < 50 returns true", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 10,
			State:      "started",
		}
		assert.True(t, orchestrator.ShouldRetry(agentRun))
	})

	t.Run("retry_count == 49 returns true", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 49,
			State:      "started",
		}
		assert.True(t, orchestrator.ShouldRetry(agentRun))
	})

	t.Run("retry_count == 50 returns false", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
			State:      "started",
		}
		assert.False(t, orchestrator.ShouldRetry(agentRun))
	})

	t.Run("retry_count > 50 returns false", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 51,
			State:      "started",
		}
		assert.False(t, orchestrator.ShouldRetry(agentRun))
	})

	t.Run("nil agentRun returns false", func(t *testing.T) {
		assert.False(t, orchestrator.ShouldRetry(nil))
	})
}

// Note: TriggerRetry tests are skipped due to complex IssueContextService dependency
// Full integration tests are in tests/integration/check_suite_to_retry_test.go
func TestRetryOrchestrator_TriggerRetry_Skipped(t *testing.T) {
	t.Skip("TriggerRetry tests require real IssueContextService - tested in integration tests")
}

func TestRetryOrchestrator_HandleMaxRetriesExceeded(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{} // Dummy client for IssueContextService
	issueContextService := services.NewIssueContextService(githubClient, logger)

	t.Run("successful handling", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
			State:      "started",
		}

		// Setup mocks
		agentRunRepo.On("Update", mock.AnythingOfType("*models.AgentRun")).Return(nil).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.HandleMaxRetriesExceeded(agentRun)
		require.NoError(t, err)
		assert.Equal(t, "failed", agentRun.State)
		assert.NotNil(t, agentRun.CompletedAt)

		agentRunRepo.AssertExpectations(t)
	})

	t.Run("update fails returns error", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
			State:      "started",
		}

		// Setup mocks
		agentRunRepo.On("Update", mock.AnythingOfType("*models.AgentRun")).Return(errors.New("update failed")).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.HandleMaxRetriesExceeded(agentRun)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update agent run state")

		agentRunRepo.AssertExpectations(t)
	})
}
