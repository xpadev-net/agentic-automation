package integration

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	tu "agentic-automation/tests/integration/testutils"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupDBCheckSuite creates in-memory SQLite database with required tables for check_suite tests
func setupDBCheckSuite(t *testing.T) *gorm.DB {
	dsn := "file::memory:?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// issues table
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo TEXT,
			number INTEGER,
			github_issue_id INTEGER,
			title TEXT,
			body TEXT,
			labels TEXT,
			state TEXT DEFAULT 'open',
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_repo_number ON issues(repo, number);
		CREATE INDEX IF NOT EXISTS idx_issues_github_issue_id ON issues(github_issue_id);
	`).Error)

	// pull_requests table
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS pull_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo TEXT,
			number INTEGER,
			issue_id INTEGER,
			branch TEXT,
			base_branch TEXT DEFAULT 'main',
			status TEXT DEFAULT 'open',
			mergeable BOOLEAN,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_pr_repo_number ON pull_requests(repo, number);
		CREATE INDEX IF NOT EXISTS idx_pull_requests_issue_id ON pull_requests(issue_id);
	`).Error)

	// agent_runs table
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT UNIQUE,
			issue_id INTEGER,
			pr_id INTEGER,
			state TEXT DEFAULT 'queued',
			agent_type TEXT DEFAULT 'claude-code',
		execution_mode TEXT DEFAULT 'normal',
		plan_content TEXT,
		review_feedback_id INTEGER,
			input TEXT,
			output TEXT,
			retry_count INTEGER DEFAULT 0,
			error_message TEXT,
			commit_sha TEXT,
			s3_session_key TEXT,
			session_saved_at DATETIME,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_idempotency_key ON agent_runs(idempotency_key);
		CREATE INDEX IF NOT EXISTS idx_agent_runs_issue_id ON agent_runs(issue_id);
		CREATE INDEX IF NOT EXISTS idx_agent_runs_pr_id ON agent_runs(pr_id);
		CREATE INDEX IF NOT EXISTS idx_agent_runs_state ON agent_runs(state);
	`).Error)

	// ci_status table
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS ci_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER,
			check_suite_id TEXT,
			check_run_id TEXT,
			name TEXT,
			status TEXT,
			conclusion TEXT,
			logs TEXT,
			logs_url TEXT,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE(pr_id, check_suite_id)
		);
		CREATE INDEX IF NOT EXISTS idx_ci_status_pr_id ON ci_status(pr_id);
		CREATE INDEX IF NOT EXISTS idx_ci_status_pr_status ON ci_status(pr_id, status);
	`).Error)

	// review_feedback table
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS review_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER,
			source TEXT DEFAULT 'Codex',
			content TEXT,
			status TEXT DEFAULT 'requested',
			approval_detected BOOLEAN DEFAULT FALSE,
			github_comment_id INTEGER,
		plan_creation_status TEXT DEFAULT 'pending',
		plan_content TEXT,
		plan_agent_run_id INTEGER,
		execution_agent_run_id INTEGER,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_review_feedback_pr_id ON review_feedback(pr_id);
	`).Error)

	return db
}

// teardownDBCheckSuite closes the database connection
func teardownDBCheckSuite(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

// setupRouterForCheckSuite constructs a minimal router for check_suite webhook tests
func setupRouterForCheckSuite(deps handlers.CheckSuiteDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/webhooks/github",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleCheckSuiteWithDeps(c, deps) },
	)
	return r
}

// createIssueWithPR creates a test Issue and associated PullRequest in the database
func createIssueWithPR(t *testing.T, db *gorm.DB) (*models.Issue, *models.PullRequest) {
	body := "Test body"
	issue := &models.Issue{
		Repo:          "test-org/test-repo",
		Number:        123,
		GitHubIssueID: 999123,
		Title:         "Test Issue",
		Body:          &body,
		Labels:        "[]",
		State:         "open",
	}
	require.NoError(t, db.Create(issue).Error)

	issueID := issue.ID
	pr := &models.PullRequest{
		Repo:       "test-org/test-repo",
		Number:     456,
		IssueID:    &issueID,
		Branch:     "feature/issue-123",
		BaseBranch: "main",
		Status:     "open",
		Mergeable:  boolPtr(true),
	}
	require.NoError(t, db.Create(pr).Error)

	return issue, pr
}

// boolPtr returns a pointer to the given bool
func boolPtr(b bool) *bool {
	return &b
}

// createStartedAgentRun creates a test AgentRun in "started" state
func createStartedAgentRun(t *testing.T, db *gorm.DB, issueID int, prID *int) *models.AgentRun {
	run := &models.AgentRun{
		IdempotencyKey: "test-delivery-" + uuid.NewString(),
		IssueID:        issueID,
		PRID:           prID,
		State:          "started",
		AgentType:      "claude-code",
		Input:          datatypes.JSON([]byte("{}")),
		Output:         datatypes.JSON([]byte("{}")),
		RetryCount:     0,
	}
	now := time.Now()
	run.StartedAt = &now
	require.NoError(t, db.Create(run).Error)
	return run
}

// buildCheckSuitePayload builds a check_suite webhook payload
func buildCheckSuitePayload(action string, conclusion *string, checkSuiteID int64) []byte {
	payload := handlers.CheckSuitePayload{
		Action: action,
		CheckSuite: handlers.CheckSuite{
			ID:         checkSuiteID,
			Status:     "completed",
			Conclusion: conclusion,
			HeadBranch: "feature/issue-123",
			HeadSHA:    "abc123def",
			PullRequests: []handlers.CheckSuitePullRequest{
				{Number: 456},
			},
		},
		Repository: handlers.CheckSuiteRepository{
			FullName: "test-org/test-repo",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal check_suite payload: %v", err))
	}
	return body
}

// Test_CheckSuite_CIFailure_TriggersRetry tests CI failure → retry trigger flow
func Test_CheckSuite_CIFailure_TriggersRetry(t *testing.T) {
	// Setup environment variables
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	os.Setenv("GITHUB_APP_TEST_MODE", "1")
	os.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
	os.Setenv("OPERATOR_SERVICE_PORT", "3000")
	os.Setenv("AGENT_RUNNER_IMAGE", "test/agent-runner:latest")
	os.Setenv("AGENT_RUNNER_TIMEOUT_MINUTES", "30")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		os.Unsetenv("GITHUB_APP_TEST_MODE")
		os.Unsetenv("OPERATOR_SERVICE_NAME")
		os.Unsetenv("OPERATOR_SERVICE_PORT")
		os.Unsetenv("AGENT_RUNNER_IMAGE")
		os.Unsetenv("AGENT_RUNNER_TIMEOUT_MINUTES")
	})

	// Setup logger and DB
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	config.SetLoggerForTesting(logger)

	db := setupDBCheckSuite(t)
	defer teardownDBCheckSuite(db)
	config.SetDBForTesting(db)

	// Create test data
	issue, pr := createIssueWithPR(t, db)
	agentRun := createStartedAgentRun(t, db, issue.ID, &pr.ID)

	// Setup stubs
	k8sJobService := &tu.StubKubernetesJobService{
		CreatedJobs: []tu.CreatedJobInfo{},
		Error:       nil,
	}

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciStatusRepo := repositories.NewCIStatusRepositoryWithDB(db)
	agentRunRepo := repositories.NewAgentRunRepository(db)
	issueRepo := repositories.NewIssueRepository()

	// Setup services
	// Create dummy GitHubClient for IssueContextService (it requires non-nil client)
	dummyGitHubClient := tu.NewDummyGitHubClient()
	issueContextService := services.NewIssueContextService(dummyGitHubClient, logger)
	retryOrchestrator := services.NewRetryOrchestrator(
		agentRunRepo,
		k8sJobService,
		issueContextService,
		logger,
	)

	feedbackAggregator := services.NewFeedbackAggregator(nil, logger)

	// Build dependencies
	// Inject stubbed CIFailureAnalyzer and dummy GitHub client so handler executes deterministically
	deps := handlers.CheckSuiteDeps{
		Logger:                    logger,
		GitHubClient:              dummyGitHubClient,
		PullRequestRepository:     prRepo,
		CIStatusRepository:        ciStatusRepo,
		CIFailureAnalyzer:         nil, // Handler will create analyzer using provided GitHubClient
		FeedbackAggregator:        feedbackAggregator,
		RetryOrchestrator:         retryOrchestrator,
		KubernetesJobService:      k8sJobService,
		GitHubNotificationService: nil, // Not needed for this test
		IssueContextService:       issueContextService,
		AgentRunRepository:        agentRunRepo,
		IssueRepository:           issueRepo,
	}

	// Setup router
	router := setupRouterForCheckSuite(deps)

	// Build webhook payload
	conclusion := "failure"
	payload := buildCheckSuitePayload("completed", &conclusion, 789)
	deliveryID := uuid.NewString()

	// Create request
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", payload))

	// Execute request
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify response
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "retry_triggered", resp["status"])
	assert.Equal(t, float64(1), resp["retry_count"])

	// Note: CIFailureAnalyzer is created by handler, so we can't easily verify stub calls
	// In a real scenario, we would need to make CIFailureAnalyzer an interface to stub it

	// Verify K8s Job was created
	assert.True(t, k8sJobService.CreateJobCalled)
	require.Len(t, k8sJobService.CreatedJobs, 1)
	assert.Equal(t, agentRun.ID, k8sJobService.CreatedJobs[0].AgentRunID)
	assert.Equal(t, 1, k8sJobService.CreatedJobs[0].RetryCount)
	assert.NotNil(t, k8sJobService.CreatedJobs[0].Feedback)

	// Verify AgentRun was updated
	var updatedRun models.AgentRun
	require.NoError(t, db.First(&updatedRun, agentRun.ID).Error)
	assert.Equal(t, 1, updatedRun.RetryCount)
	assert.Equal(t, "queued", updatedRun.State)

	// Verify CIStatus was created
	var ciStatus models.CIStatus
	require.NoError(t, db.Where("pr_id = ? AND check_suite_id = ?", pr.ID, "789").First(&ciStatus).Error)
	assert.Equal(t, "failure", *ciStatus.Conclusion)
}

// createAgentRunWithRetryCount creates an AgentRun with specified retry count
func createAgentRunWithRetryCount(t *testing.T, db *gorm.DB, issueID int, prID *int, retryCount int) *models.AgentRun {
	run := &models.AgentRun{
		IdempotencyKey: "test-delivery-" + uuid.NewString(),
		IssueID:        issueID,
		PRID:           prID,
		State:          "started",
		AgentType:      "claude-code",
		Input:          datatypes.JSON([]byte("{}")),
		Output:         datatypes.JSON([]byte("{}")),
		RetryCount:     retryCount,
	}
	now := time.Now()
	run.StartedAt = &now
	require.NoError(t, db.Create(run).Error)
	return run
}

// Test_CheckSuite_CIFailure_MaxRetriesExceeded tests max retries exceeded scenario
func Test_CheckSuite_CIFailure_MaxRetriesExceeded(t *testing.T) {
	// Setup environment variables
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	os.Setenv("GITHUB_APP_TEST_MODE", "1")
	os.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
	os.Setenv("OPERATOR_SERVICE_PORT", "3000")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		os.Unsetenv("GITHUB_APP_TEST_MODE")
		os.Unsetenv("OPERATOR_SERVICE_NAME")
		os.Unsetenv("OPERATOR_SERVICE_PORT")
	})

	// Setup logger and DB
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	config.SetLoggerForTesting(logger)

	db := setupDBCheckSuite(t)
	defer teardownDBCheckSuite(db)
	config.SetDBForTesting(db)

	// Create test data with retry_count=50
	issue, pr := createIssueWithPR(t, db)
	agentRun := createAgentRunWithRetryCount(t, db, issue.ID, &pr.ID, 50)

	// Setup stubs
	k8sJobService := &tu.StubKubernetesJobService{
		CreatedJobs: []tu.CreatedJobInfo{},
		Error:       nil,
	}

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciStatusRepo := repositories.NewCIStatusRepositoryWithDB(db)
	agentRunRepo := repositories.NewAgentRunRepository(db)
	issueRepo := repositories.NewIssueRepository()

	// Setup services
	// Create dummy GitHubClient for IssueContextService (it requires non-nil client)
	dummyGitHubClient := tu.NewDummyGitHubClient()
	issueContextService := services.NewIssueContextService(dummyGitHubClient, logger)
	retryOrchestrator := services.NewRetryOrchestrator(
		agentRunRepo,
		k8sJobService,
		issueContextService,
		logger,
	)

	feedbackAggregator := services.NewFeedbackAggregator(nil, logger)

	// Build dependencies
	// Inject stubbed CIFailureAnalyzer and dummy GitHub client
	deps := handlers.CheckSuiteDeps{
		Logger:                    logger,
		GitHubClient:              dummyGitHubClient, // Required for CIFailureAnalyzer creation
		PullRequestRepository:     prRepo,
		CIStatusRepository:        ciStatusRepo,
		CIFailureAnalyzer:         nil, // Handler will create analyzer using provided GitHubClient
		FeedbackAggregator:        feedbackAggregator,
		RetryOrchestrator:         retryOrchestrator,
		KubernetesJobService:      k8sJobService,
		GitHubNotificationService: nil,
		IssueContextService:       issueContextService,
		AgentRunRepository:        agentRunRepo,
		IssueRepository:           issueRepo,
	}

	// Setup router
	router := setupRouterForCheckSuite(deps)

	// Build webhook payload
	conclusion := "failure"
	payload := buildCheckSuitePayload("completed", &conclusion, 789)
	deliveryID := uuid.NewString()

	// Create request
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", payload))

	// Execute request
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify response
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "max_retries_exceeded", resp["status"])

	// Verify AgentRun was updated to failed
	var updatedRun models.AgentRun
	require.NoError(t, db.First(&updatedRun, agentRun.ID).Error)
	assert.Equal(t, "failed", updatedRun.State)
	assert.Equal(t, 50, updatedRun.RetryCount) // Should remain 50

	// Verify K8s Job was NOT created
	assert.False(t, k8sJobService.CreateJobCalled)
	assert.Len(t, k8sJobService.CreatedJobs, 0)

	// Verify CIStatus was still created (for record keeping)
	var ciStatus models.CIStatus
	require.NoError(t, db.Where("pr_id = ? AND check_suite_id = ?", pr.ID, "789").First(&ciStatus).Error)
	assert.Equal(t, "failure", *ciStatus.Conclusion)
}

// Test_CheckSuite_CISuccess_RecordsStatus tests CI success → status recording flow
func Test_CheckSuite_CISuccess_RecordsStatus(t *testing.T) {
	// Setup environment variables
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
	})

	// Setup logger and DB
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	config.SetLoggerForTesting(logger)

	db := setupDBCheckSuite(t)
	defer teardownDBCheckSuite(db)
	config.SetDBForTesting(db)

	// Create test data
	issue, pr := createIssueWithPR(t, db)
	agentRun := createStartedAgentRun(t, db, issue.ID, &pr.ID)

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciStatusRepo := repositories.NewCIStatusRepositoryWithDB(db)
	agentRunRepo := repositories.NewAgentRunRepository(db)
	issueRepo := repositories.NewIssueRepository()

	// Build dependencies (CI failure analyzer not needed for success)
	deps := handlers.CheckSuiteDeps{
		Logger:                    logger,
		GitHubClient:              nil,
		PullRequestRepository:     prRepo,
		CIStatusRepository:        ciStatusRepo,
		CIFailureAnalyzer:         nil,
		FeedbackAggregator:        nil,
		RetryOrchestrator:         nil,
		KubernetesJobService:      nil,
		GitHubNotificationService: nil,
		IssueContextService:       nil,
		AgentRunRepository:        agentRunRepo,
		IssueRepository:           issueRepo,
	}

	// Setup router
	router := setupRouterForCheckSuite(deps)

	// Build webhook payload with success conclusion
	conclusion := "success"
	payload := buildCheckSuitePayload("completed", &conclusion, 789)
	deliveryID := uuid.NewString()

	// Create request
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", payload))

	// Execute request
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify response
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "processed", resp["status"])
	assert.Equal(t, "success", resp["conclusion"])

	// Verify CIStatus was created with success
	var ciStatus models.CIStatus
	require.NoError(t, db.Where("pr_id = ? AND check_suite_id = ?", pr.ID, "789").First(&ciStatus).Error)
	assert.Equal(t, "success", *ciStatus.Conclusion)

	// Verify AgentRun state was NOT changed
	var updatedRun models.AgentRun
	require.NoError(t, db.First(&updatedRun, agentRun.ID).Error)
	assert.Equal(t, "started", updatedRun.State)
	assert.Equal(t, 0, updatedRun.RetryCount)
}

// Test_CheckSuite_CIFailure_Retry_Success_Flow tests the complete flow: CI failure → retry → success
func Test_CheckSuite_CIFailure_Retry_Success_Flow(t *testing.T) {
	// Setup environment variables
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	os.Setenv("GITHUB_APP_TEST_MODE", "1")
	os.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
	os.Setenv("OPERATOR_SERVICE_PORT", "3000")
	os.Setenv("AGENT_RUNNER_IMAGE", "test/agent-runner:latest")
	os.Setenv("AGENT_RUNNER_TIMEOUT_MINUTES", "30")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		os.Unsetenv("GITHUB_APP_TEST_MODE")
		os.Unsetenv("OPERATOR_SERVICE_NAME")
		os.Unsetenv("OPERATOR_SERVICE_PORT")
		os.Unsetenv("AGENT_RUNNER_IMAGE")
		os.Unsetenv("AGENT_RUNNER_TIMEOUT_MINUTES")
	})

	// Setup logger and DB
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	config.SetLoggerForTesting(logger)

	db := setupDBCheckSuite(t)
	defer teardownDBCheckSuite(db)
	config.SetDBForTesting(db)

	// Create test data
	issue, pr := createIssueWithPR(t, db)
	agentRun := createStartedAgentRun(t, db, issue.ID, &pr.ID)

	// Setup stubs
	k8sJobService := &tu.StubKubernetesJobService{
		CreatedJobs: []tu.CreatedJobInfo{},
		Error:       nil,
	}

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciStatusRepo := repositories.NewCIStatusRepositoryWithDB(db)
	agentRunRepo := repositories.NewAgentRunRepository(db)
	issueRepo := repositories.NewIssueRepository()

	// Setup services
	// Create dummy GitHubClient for IssueContextService (it requires non-nil client)
	dummyGitHubClient := tu.NewDummyGitHubClient()
	issueContextService := services.NewIssueContextService(dummyGitHubClient, logger)
	retryOrchestrator := services.NewRetryOrchestrator(
		agentRunRepo,
		k8sJobService,
		issueContextService,
		logger,
	)

	feedbackAggregator := services.NewFeedbackAggregator(nil, logger)

	// Build dependencies
	// Inject stubbed CIFailureAnalyzer and dummy GitHub client
	deps := handlers.CheckSuiteDeps{
		Logger:                    logger,
		GitHubClient:              dummyGitHubClient, // Required for CIFailureAnalyzer creation
		PullRequestRepository:     prRepo,
		CIStatusRepository:        ciStatusRepo,
		CIFailureAnalyzer:         nil, // Handler will create analyzer using provided GitHubClient
		FeedbackAggregator:        feedbackAggregator,
		RetryOrchestrator:         retryOrchestrator,
		KubernetesJobService:      k8sJobService,
		GitHubNotificationService: nil,
		IssueContextService:       issueContextService,
		AgentRunRepository:        agentRunRepo,
		IssueRepository:           issueRepo,
	}

	// Setup router
	router := setupRouterForCheckSuite(deps)

	// Step 1: Send CI failure webhook
	conclusionFailure := "failure"
	payloadFailure := buildCheckSuitePayload("completed", &conclusionFailure, 789)
	deliveryID1 := uuid.NewString()

	req1 := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadFailure))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-GitHub-Event", "check_suite")
	req1.Header.Set("X-GitHub-Delivery", deliveryID1)
	req1.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", payloadFailure))

	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusOK, w1.Code)
	var resp1 map[string]interface{}
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	assert.Equal(t, "retry_triggered", resp1["status"])
	assert.Equal(t, float64(1), resp1["retry_count"])

	// Verify retry was triggered
	var runAfterFailure models.AgentRun
	require.NoError(t, db.First(&runAfterFailure, agentRun.ID).Error)
	assert.Equal(t, 1, runAfterFailure.RetryCount)
	assert.Equal(t, "queued", runAfterFailure.State)

	// Verify K8s Job was created
	assert.True(t, k8sJobService.CreateJobCalled)
	require.Len(t, k8sJobService.CreatedJobs, 1)
	assert.Equal(t, agentRun.ID, k8sJobService.CreatedJobs[0].AgentRunID)
	assert.Equal(t, 1, k8sJobService.CreatedJobs[0].RetryCount)

	// Verify failure CIStatus was created
	var ciStatusFailure models.CIStatus
	require.NoError(t, db.Where("pr_id = ? AND check_suite_id = ?", pr.ID, "789").First(&ciStatusFailure).Error)
	assert.Equal(t, "failure", *ciStatusFailure.Conclusion)

	// Step 2: Send CI success webhook
	conclusionSuccess := "success"
	payloadSuccess := buildCheckSuitePayload("completed", &conclusionSuccess, 790)
	deliveryID2 := uuid.NewString()

	req2 := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadSuccess))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-GitHub-Event", "check_suite")
	req2.Header.Set("X-GitHub-Delivery", deliveryID2)
	req2.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", payloadSuccess))

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	var resp2 map[string]interface{}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.Equal(t, "processed", resp2["status"])
	assert.Equal(t, "success", resp2["conclusion"])

	// Verify success CIStatus was created
	var ciStatusSuccess models.CIStatus
	require.NoError(t, db.Where("pr_id = ? AND check_suite_id = ?", pr.ID, "790").First(&ciStatusSuccess).Error)
	assert.Equal(t, "success", *ciStatusSuccess.Conclusion)

	// Verify both CIStatus records exist
	var count int64
	db.Model(&models.CIStatus{}).Where("pr_id = ?", pr.ID).Count(&count)
	assert.Equal(t, int64(2), count) // Both failure and success records

	// Verify AgentRun state is still queued (retry in progress)
	var runAfterSuccess models.AgentRun
	require.NoError(t, db.First(&runAfterSuccess, agentRun.ID).Error)
	assert.Equal(t, "queued", runAfterSuccess.State)
	assert.Equal(t, 1, runAfterSuccess.RetryCount) // Still 1, not incremented by success
}
