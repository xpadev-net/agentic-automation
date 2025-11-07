package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	batchv1 "k8s.io/api/batch/v1"
)

type fakePlanJobService struct {
	capturedPlan string
}

func (f *fakePlanJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	return nil, nil
}

func (f *fakePlanJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *services.AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	return nil, nil
}

func (f *fakePlanJobService) CreateJobForPlanExecution(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, planContent string, branchName string) (*batchv1.Job, error) {
	f.capturedPlan = planContent
	return &batchv1.Job{}, nil
}

type planTestFixtures struct {
	db             *gorm.DB
	agentRun       *models.AgentRun
	reviewFeedback *models.ReviewFeedback
	issue          *models.Issue
}

func setupPlanTestFixtures(t *testing.T) *planTestFixtures {
	t.Helper()
	t.Setenv("AGENT_OUTPUT_DB_LIMIT_BYTES", "64")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	schemaStatements := []string{
		`CREATE TABLE IF NOT EXISTS agent_runs (
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
		)`,
		`CREATE TABLE IF NOT EXISTS issues (
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
		)`,
		`CREATE TABLE IF NOT EXISTS pull_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo TEXT NOT NULL,
			number INTEGER NOT NULL,
			issue_id INTEGER,
			branch TEXT,
			base_branch TEXT DEFAULT 'main',
			status TEXT DEFAULT 'open',
			mergeable BOOLEAN,
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE(repo, number)
		)`,
		`CREATE TABLE IF NOT EXISTS review_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER NOT NULL,
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
		)`,
	}

	for _, stmt := range schemaStatements {
		require.NoError(t, db.Exec(stmt).Error)
	}

	issue := &models.Issue{Repo: "owner/repo", Number: 1}
	require.NoError(t, db.Create(issue).Error)

	pr := &models.PullRequest{Repo: "owner/repo", Number: 1, IssueID: &issue.ID, Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	reviewFeedback := &models.ReviewFeedback{PRID: pr.ID, Status: "received", PlanCreationStatus: "pending"}
	require.NoError(t, db.Create(reviewFeedback).Error)

	agentRun := &models.AgentRun{
		IdempotencyKey:   "plan-run",
		IssueID:          issue.ID,
		State:            "queued",
		AgentType:        "claude-code",
		ExecutionMode:    "plan_creation",
		ReviewFeedbackID: &reviewFeedback.ID,
	}
	require.NoError(t, db.Create(agentRun).Error)

	return &planTestFixtures{
		db:             db,
		agentRun:       agentRun,
		reviewFeedback: reviewFeedback,
		issue:          issue,
	}
}

func TestHandlePlanReportPassesFullPlanToJob(t *testing.T) {
	fixtures := setupPlanTestFixtures(t)
	fakeJob := &fakePlanJobService{}

	origClientFactory := kubernetesClientFactory
	origJobFactory := kubernetesJobServiceFactory
	kubernetesClientFactory = func(logger *zap.Logger) (*clients.KubernetesClient, error) {
		return nil, nil
	}
	kubernetesJobServiceFactory = func(_ *clients.KubernetesClient, _ *zap.Logger) services.KubernetesJobService {
		return fakeJob
	}
	defer func() {
		kubernetesClientFactory = origClientFactory
		kubernetesJobServiceFactory = origJobFactory
	}()

	agentRunRepo := repositories.NewAgentRunRepository(fixtures.db)
	reviewFeedbackRepo := repositories.NewReviewFeedbackRepositoryWithDB(fixtures.db)

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest("POST", "/", nil)

	largePlan := strings.Repeat("Step detail line\n", 200)
	req := &PlanReportRequest{Status: "plan_created", AgentType: "claude-code", PlanContent: largePlan}

	handlePlanCreated(
		ginCtx,
		context.Background(),
		fixtures.agentRun.ID,
		fixtures.agentRun,
		fixtures.reviewFeedback,
		req,
		"",
		agentRunRepo,
		reviewFeedbackRepo,
		fixtures.db,
	)

	response := w.Result()
	require.Equal(t, 200, response.StatusCode)

	expectedPlan := utils.SanitizeUTF8(strings.TrimSpace(largePlan))
	require.Equal(t, expectedPlan, fakeJob.capturedPlan)

	var storedExecution models.AgentRun
	require.NoError(t, fixtures.db.Where("execution_mode = ?", "plan_execution").First(&storedExecution).Error)
	require.NotNil(t, storedExecution.PlanContent)
	require.Less(t, len(*storedExecution.PlanContent), len(largePlan))
	require.True(t, strings.HasSuffix(*storedExecution.PlanContent, "… [truncated]"))

	var storedCreation models.AgentRun
	require.NoError(t, fixtures.db.First(&storedCreation, fixtures.agentRun.ID).Error)
	require.NotNil(t, storedCreation.PlanContent)
	require.Less(t, len(*storedCreation.PlanContent), len(largePlan))

	require.NotEqual(t, len(expectedPlan), len(*storedCreation.PlanContent))
}

func TestHandleAgentReportDispatchesPlanReport(t *testing.T) {
	fixtures := setupPlanTestFixtures(t)
	fakeJob := &fakePlanJobService{}

	origClientFactory := kubernetesClientFactory
	origJobFactory := kubernetesJobServiceFactory
	kubernetesClientFactory = func(logger *zap.Logger) (*clients.KubernetesClient, error) {
		return nil, nil
	}
	kubernetesJobServiceFactory = func(_ *clients.KubernetesClient, _ *zap.Logger) services.KubernetesJobService {
		return fakeJob
	}
	defer func() {
		kubernetesClientFactory = origClientFactory
		kubernetesJobServiceFactory = origJobFactory
	}()

	largePlan := strings.Repeat("Step detail line\n", 150)
	payload := map[string]any{
		"status":       "plan_created",
		"agent_type":   "claude-code",
		"plan_content": largePlan,
		"logs":         "sample log",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	requestPath := "/api/agent-runs/" + strconv.Itoa(fixtures.agentRun.ID) + "/report"
	ginCtx.Request = httptest.NewRequest("POST", requestPath, bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	ginCtx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(fixtures.agentRun.ID)}}

	HandleAgentReport(ginCtx)

	response := w.Result()
	require.Equal(t, 200, response.StatusCode)

	expectedPlan := utils.SanitizeUTF8(strings.TrimSpace(largePlan))
	require.Equal(t, expectedPlan, fakeJob.capturedPlan)

	var executionRun models.AgentRun
	require.NoError(t, fixtures.db.Where("execution_mode = ?", "plan_execution").First(&executionRun).Error)
	require.NotNil(t, executionRun.PlanContent)
	require.True(t, strings.HasSuffix(*executionRun.PlanContent, "… [truncated]"))

	var reviewFeedback models.ReviewFeedback
	require.NoError(t, fixtures.db.First(&reviewFeedback, fixtures.reviewFeedback.ID).Error)
	require.Equal(t, "created", reviewFeedback.PlanCreationStatus)
	require.NotNil(t, reviewFeedback.ExecutionAgentRunID)
}

func TestHandlePlanReportIgnoresDuplicatePlanCreated(t *testing.T) {
	fixtures := setupPlanTestFixtures(t)
	fakeJob := &fakePlanJobService{}

	origClientFactory := kubernetesClientFactory
	origJobFactory := kubernetesJobServiceFactory
	kubernetesClientFactory = func(logger *zap.Logger) (*clients.KubernetesClient, error) {
		return nil, nil
	}
	kubernetesJobServiceFactory = func(_ *clients.KubernetesClient, _ *zap.Logger) services.KubernetesJobService {
		return fakeJob
	}
	defer func() {
		kubernetesClientFactory = origClientFactory
		kubernetesJobServiceFactory = origJobFactory
	}()

	agentRunRepo := repositories.NewAgentRunRepository(fixtures.db)
	planRepo := repositories.NewReviewFeedbackRepositoryWithDB(fixtures.db)

	// seed initial plan report handling
	firstReq := &PlanReportRequest{Status: "plan_created", AgentType: "claude-code", PlanContent: "Step A"}
	w1 := httptest.NewRecorder()
	ctx1, _ := gin.CreateTestContext(w1)
	ctx1.Request = httptest.NewRequest("POST", "/", nil)
	handlePlanCreated(
		ctx1,
		context.Background(),
		fixtures.agentRun.ID,
		fixtures.agentRun,
		fixtures.reviewFeedback,
		firstReq,
		"",
		agentRunRepo,
		planRepo,
		fixtures.db,
	)
	require.Equal(t, 200, w1.Code)
	require.Len(t, fakeJob.capturedPlan, len(utils.SanitizeUTF8("Step A")))

	// fetch updated records to assert preconditions
	var refreshedReview models.ReviewFeedback
	require.NoError(t, fixtures.db.First(&refreshedReview, fixtures.reviewFeedback.ID).Error)
	require.Equal(t, "created", refreshedReview.PlanCreationStatus)

	var executionRun models.AgentRun
	require.NoError(t, fixtures.db.Where("execution_mode = ?", "plan_execution").First(&executionRun).Error)
	originalExecutionID := executionRun.ID

	// prepare duplicate report payload
	payload := map[string]any{
		"status":       "plan_created",
		"agent_type":   "claude-code",
		"plan_content": "Step B",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w2 := httptest.NewRecorder()
	ctx2, _ := gin.CreateTestContext(w2)
	path := "/api/agent-runs/" + strconv.Itoa(fixtures.agentRun.ID) + "/report"
	ctx2.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx2.Request.Header.Set("Content-Type", "application/json")
	ctx2.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(fixtures.agentRun.ID)}}

	HandleAgentReport(ctx2)

	require.Equal(t, http.StatusOK, w2.Code)

	// ensure response signals duplicate processing
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	require.Equal(t, "Plan report already processed", resp["message"])
	require.Equal(t, "created", resp["plan_creation_status"])

	// ensure no new execution run/job was created
	var executionRuns []models.AgentRun
	require.NoError(t, fixtures.db.Where("execution_mode = ?", "plan_execution").Find(&executionRuns).Error)
	require.Len(t, executionRuns, 1)
	require.Equal(t, originalExecutionID, executionRuns[0].ID)

	// ensure job service did not receive a second plan
	require.Equal(t, utils.SanitizeUTF8("Step A"), fakeJob.capturedPlan)
}
