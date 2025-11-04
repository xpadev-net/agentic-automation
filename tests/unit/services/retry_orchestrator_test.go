package services_test

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"context"
	"errors"
	"testing"
	"time"

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

// mockIssueContextService is a mock implementation of IssueContextService for testing
type mockIssueContextService struct {
	mock.Mock
}

func (m *mockIssueContextService) CollectIssueContext(ctx context.Context, owner, repo string, issueNumber int) (*services.IssueContext, error) {
	args := m.Called(ctx, owner, repo, issueNumber)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.IssueContext), args.Error(1)
}

func (m *mockIssueContextService) FormatPrompt(issueCtx *services.IssueContext) string {
	args := m.Called(issueCtx)
	return args.String(0)
}

func TestNewRetryOrchestrator(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)
	issueContextService := new(mockIssueContextService)

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
		assert.Panics(t, func() {
			services.NewRetryOrchestrator(
				agentRunRepo,
				jobService,
				nil,
				logger,
			)
		})
	})
}

func TestRetryOrchestrator_ShouldRetry(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)
	issueContextService := new(mockIssueContextService)

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

func TestRetryOrchestrator_TriggerRetry(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("successful retry", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)
		issueContextService := new(mockIssueContextService)

		agentRun := &models.AgentRun{
			ID:         1,
			IssueID:    100,
			RetryCount: 5,
			State:      "started",
		}
		issue := &models.Issue{
			ID:     100,
			Repo:   "test/owner",
			Number: 1,
		}
		feedback := &services.AggregatedFeedback{
			PreviousAttemptsJSON: `[{"retry_count":5,"error":"test error"}]`,
			CILogs:               "test logs",
			HasReviewFeedback:    true,
			HasCIFailure:         true,
		}

		issueContext := &services.IssueContext{
			Number: 1,
			Title:  "Test Issue",
			Body:   "Test body",
		}

		// Setup mocks
		agentRunRepo.On("Update", mock.AnythingOfType("*models.AgentRun")).Return(nil).Once()
		issueContextService.On("CollectIssueContext", ctx, "test", "owner", 1).Return(issueContext, nil).Once()
		issueContextService.On("FormatPrompt", issueContext).Return("Test prompt").Once()
		jobService.On("CreateJobForAgentRunWithFeedback", ctx, mock.AnythingOfType("*models.AgentRun"), issue, "Test prompt", feedback).Return(&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-job",
			},
		}, nil).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.TriggerRetry(ctx, agentRun, issue, feedback)
		require.NoError(t, err)
		assert.Equal(t, 6, agentRun.RetryCount)
		assert.Equal(t, "queued", agentRun.State)

		agentRunRepo.AssertExpectations(t)
		jobService.AssertExpectations(t)
		issueContextService.AssertExpectations(t)
	})

	t.Run("max retries exceeded calls HandleMaxRetriesExceeded", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)
		issueContextService := new(mockIssueContextService)

		agentRun := &models.AgentRun{
			ID:         1,
			IssueID:    100,
			RetryCount: 49, // Will be incremented to 50
			State:      "started",
		}
		issue := &models.Issue{
			ID:     100,
			Repo:   "test/owner",
			Number: 1,
		}

		// Setup mocks
		agentRunRepo.On("Update", mock.AnythingOfType("*models.AgentRun")).Return(nil).Times(2) // Once for retry, once for failed state

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.TriggerRetry(ctx, agentRun, issue, nil)
		require.NoError(t, err)
		assert.Equal(t, 50, agentRun.RetryCount)
		assert.Equal(t, "failed", agentRun.State)

		agentRunRepo.AssertExpectations(t)
	})

	t.Run("update fails returns error", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)
		issueContextService := new(mockIssueContextService)

		agentRun := &models.AgentRun{
			ID:         1,
			IssueID:    100,
			RetryCount: 5,
			State:      "started",
		}
		issue := &models.Issue{
			ID:     100,
			Repo:   "test/owner",
			Number: 1,
		}

		// Setup mocks
		agentRunRepo.On("Update", mock.AnythingOfType("*models.AgentRun")).Return(errors.New("update failed")).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.TriggerRetry(ctx, agentRun, issue, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update agent run")

		agentRunRepo.AssertExpectations(t)
	})
}

func TestRetryOrchestrator_HandleMaxRetriesExceeded(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("successful handling", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)
		issueContextService := new(mockIssueContextService)

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
		issueContextService := new(mockIssueContextService)

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
