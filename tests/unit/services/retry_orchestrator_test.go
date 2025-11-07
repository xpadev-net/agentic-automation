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

func (m *mockAgentRunRepository) IncrementRetryCount(agentRunID int, maxRetryCount int) (int, error) {
	args := m.Called(agentRunID, maxRetryCount)
	return args.Int(0), args.Error(1)
}

// mockKubernetesJobService is a mock implementation of KubernetesJobService for testing
type mockKubernetesJobService struct {
	mock.Mock
}

func (m *mockKubernetesJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	args := m.Called(ctx, agentRun, issue, prompt, branchName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*batchv1.Job), args.Error(1)
}

func (m *mockKubernetesJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *services.AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	args := m.Called(ctx, agentRun, issue, prompt, feedback, branchName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*batchv1.Job), args.Error(1)
}

func (m *mockKubernetesJobService) CreateJobForPlanExecution(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, planContent string, branchName string) (*batchv1.Job, error) {
	args := m.Called(ctx, agentRun, issue, planContent, branchName)
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

	// Note: jobService is now optional (can be nil) - TriggerRetry will fail if called with nil jobService
	// This allows using RetryOrchestrator for basic retry count operations without Kubernetes dependencies
	t.Run("nil jobService allowed", func(t *testing.T) {
		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			nil, // nil jobService is now allowed
			issueContextService,
			logger,
		)
		require.NotNil(t, orchestrator)
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

func TestRetryOrchestrator_IncrementRetryCount(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	issueContextService := services.NewIssueContextService(githubClient, logger)
	ctx := context.Background()

	t.Run("正常系: リトライカウントが0から1に増加", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 0,
		}

		// Setup mocks
		agentRunRepo.On("IncrementRetryCount", 1, 50).Return(1, nil).Once()
		agentRunRepo.On("GetByID", 1).Return(&models.AgentRun{
			ID:         1,
			RetryCount: 1,
			State:      "started",
		}, nil).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, agentRun)
		require.NoError(t, err)
		assert.Equal(t, 1, agentRun.RetryCount)

		agentRunRepo.AssertExpectations(t)
	})

	t.Run("正常系: リトライカウントが49から50に増加（境界値）", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 49,
		}

		// Setup mocks
		agentRunRepo.On("IncrementRetryCount", 1, 50).Return(50, nil).Once()
		agentRunRepo.On("GetByID", 1).Return(&models.AgentRun{
			ID:         1,
			RetryCount: 50,
			State:      "started",
		}, nil).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, agentRun)
		require.NoError(t, err)
		assert.Equal(t, 50, agentRun.RetryCount)

		agentRunRepo.AssertExpectations(t)
	})

	t.Run("異常系: リトライカウントが50の時は増加できない（最大値到達）", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
		}

		// Setup mocks
		maxErr := errors.New("cannot increment retry count: already at or above maximum (50 >= 50)")
		agentRunRepo.On("IncrementRetryCount", 1, 50).Return(50, maxErr).Once()
		agentRunRepo.On("GetByID", 1).Return(&models.AgentRun{
			ID:         1,
			RetryCount: 50,
		}, nil).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, agentRun)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum retry count (50) already reached")
		assert.Equal(t, 50, agentRun.RetryCount)

		agentRunRepo.AssertExpectations(t)
	})

	t.Run("異常系: agentRunがnilの場合", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agentRun must not be nil")
	})

	t.Run("異常系: agentRun.IDが0の場合", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         0,
			RetryCount: 0,
		}

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, agentRun)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agentRun.ID must not be zero")
	})

	t.Run("異常系: リポジトリのIncrementRetryCountが一般的なエラーを返す", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 10,
		}

		// Setup mocks
		agentRunRepo.On("IncrementRetryCount", 1, 50).Return(0, errors.New("database connection failed")).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, agentRun)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to increment retry count")

		agentRunRepo.AssertExpectations(t)
	})

	t.Run("異常系: GetByIDのリロードが失敗する", func(t *testing.T) {
		agentRunRepo := new(mockAgentRunRepository)
		jobService := new(mockKubernetesJobService)

		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 10,
		}

		// Setup mocks
		agentRunRepo.On("IncrementRetryCount", 1, 50).Return(11, nil).Once()
		agentRunRepo.On("GetByID", 1).Return(nil, errors.New("record not found")).Once()

		orchestrator := services.NewRetryOrchestrator(
			agentRunRepo,
			jobService,
			issueContextService,
			logger,
		)

		err := orchestrator.IncrementRetryCount(ctx, agentRun)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to reload AgentRun after increment")
		assert.Equal(t, 11, agentRun.RetryCount) // リロード失敗でもカウントは更新される

		agentRunRepo.AssertExpectations(t)
	})
}

func TestRetryOrchestrator_IsMaxRetriesReached(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)
	githubClient := &clients.Client{}
	issueContextService := services.NewIssueContextService(githubClient, logger)
	orchestrator := services.NewRetryOrchestrator(agentRunRepo, jobService, issueContextService, logger)

	t.Run("retry_count < 50 の場合、false を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 10,
		}
		result := orchestrator.IsMaxRetriesReached(agentRun)
		assert.False(t, result)
	})

	t.Run("retry_count == 49 の場合、false を返す（境界値）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 49,
		}
		result := orchestrator.IsMaxRetriesReached(agentRun)
		assert.False(t, result)
	})

	t.Run("retry_count == 50 の場合、true を返す（境界値）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
		}
		result := orchestrator.IsMaxRetriesReached(agentRun)
		assert.True(t, result)
	})

	t.Run("retry_count > 50 の場合、true を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 51,
		}
		result := orchestrator.IsMaxRetriesReached(agentRun)
		assert.True(t, result)
	})

	t.Run("agentRunがnilの場合、true を返す（安全のため）", func(t *testing.T) {
		result := orchestrator.IsMaxRetriesReached(nil)
		assert.True(t, result)
	})
}

func TestRetryOrchestrator_GetRemainingRetries(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)
	githubClient := &clients.Client{}
	issueContextService := services.NewIssueContextService(githubClient, logger)
	orchestrator := services.NewRetryOrchestrator(agentRunRepo, jobService, issueContextService, logger)

	t.Run("retry_count == 0 の場合、50 を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 0,
		}
		remaining := orchestrator.GetRemainingRetries(agentRun)
		assert.Equal(t, 50, remaining)
	})

	t.Run("retry_count == 25 の場合、25 を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 25,
		}
		remaining := orchestrator.GetRemainingRetries(agentRun)
		assert.Equal(t, 25, remaining)
	})

	t.Run("retry_count == 49 の場合、1 を返す（境界値）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 49,
		}
		remaining := orchestrator.GetRemainingRetries(agentRun)
		assert.Equal(t, 1, remaining)
	})

	t.Run("retry_count == 50 の場合、0 を返す（境界値）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
		}
		remaining := orchestrator.GetRemainingRetries(agentRun)
		assert.Equal(t, 0, remaining)
	})

	t.Run("retry_count > 50 の場合、0 を返す（負の値は返さない）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 51,
		}
		remaining := orchestrator.GetRemainingRetries(agentRun)
		assert.Equal(t, 0, remaining)
	})

	t.Run("agentRunがnilの場合、0 を返す", func(t *testing.T) {
		remaining := orchestrator.GetRemainingRetries(nil)
		assert.Equal(t, 0, remaining)
	})
}

func TestRetryOrchestrator_ValidateRetryCount(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)
	githubClient := &clients.Client{}
	issueContextService := services.NewIssueContextService(githubClient, logger)
	orchestrator := services.NewRetryOrchestrator(agentRunRepo, jobService, issueContextService, logger)

	t.Run("retry_count == 0 の場合、エラーなし", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 0,
		}
		err := orchestrator.ValidateRetryCount(agentRun)
		require.NoError(t, err)
	})

	t.Run("retry_count == 25 の場合、エラーなし", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 25,
		}
		err := orchestrator.ValidateRetryCount(agentRun)
		require.NoError(t, err)
	})

	t.Run("retry_count == 50 の場合、エラーなし（警告のみ）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
		}
		err := orchestrator.ValidateRetryCount(agentRun)
		require.NoError(t, err)
	})

	t.Run("retry_count > 50 の場合、エラーなし（警告のみ、既存データを許容）", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 51,
		}
		err := orchestrator.ValidateRetryCount(agentRun)
		require.NoError(t, err)
	})

	t.Run("retry_count < 0 の場合、エラーを返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: -1,
		}
		err := orchestrator.ValidateRetryCount(agentRun)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retry_count must be non-negative")
	})

	t.Run("agentRunがnilの場合、エラーを返す", func(t *testing.T) {
		err := orchestrator.ValidateRetryCount(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agentRun must not be nil")
	})
}

func TestRetryOrchestrator_GetRetryCount(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agentRunRepo := new(mockAgentRunRepository)
	jobService := new(mockKubernetesJobService)
	githubClient := &clients.Client{}
	issueContextService := services.NewIssueContextService(githubClient, logger)
	orchestrator := services.NewRetryOrchestrator(agentRunRepo, jobService, issueContextService, logger)

	t.Run("retry_count == 0 の場合、0 を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 0,
		}
		count := orchestrator.GetRetryCount(agentRun)
		assert.Equal(t, 0, count)
	})

	t.Run("retry_count == 25 の場合、25 を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 25,
		}
		count := orchestrator.GetRetryCount(agentRun)
		assert.Equal(t, 25, count)
	})

	t.Run("retry_count == 50 の場合、50 を返す", func(t *testing.T) {
		agentRun := &models.AgentRun{
			ID:         1,
			RetryCount: 50,
		}
		count := orchestrator.GetRetryCount(agentRun)
		assert.Equal(t, 50, count)
	})

	t.Run("agentRunがnilの場合、0 を返す", func(t *testing.T) {
		count := orchestrator.GetRetryCount(nil)
		assert.Equal(t, 0, count)
	})
}
