package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	capturedPlan  string
	planErr       error
	planCallCount int
}

func (f *fakePlanJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	return &batchv1.Job{}, nil
}

func (f *fakePlanJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *services.AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	return nil, nil
}

func (f *fakePlanJobService) CreateJobForPlanCreation(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, reviewFeedback *models.ReviewFeedback, branchName string) (*batchv1.Job, error) {
	return &batchv1.Job{}, nil
}

func (f *fakePlanJobService) CreateJobForPlanExecution(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, planContent string, branchName string) (*batchv1.Job, error) {
	f.planCallCount++
	f.capturedPlan = planContent
	if f.planErr != nil {
		return nil, f.planErr
	}
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
			plan_agent_run_id INTEGER,
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
		fixtures.agentRun.State,
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

func TestHandlePlanCreatedRollsBackWhenJobCreationFails(t *testing.T) {
	fixtures := setupPlanTestFixtures(t)
	fakeJob := &fakePlanJobService{planErr: fmt.Errorf("boom")}

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

	req := &PlanReportRequest{Status: "plan_created", AgentType: "claude-code", PlanContent: "Step detail line"}

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
		fixtures.agentRun.State,
	)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 1, fakeJob.planCallCount)

	var storedFeedback models.ReviewFeedback
	require.NoError(t, fixtures.db.First(&storedFeedback, fixtures.reviewFeedback.ID).Error)
	require.Equal(t, "pending", storedFeedback.PlanCreationStatus)
	require.Nil(t, storedFeedback.PlanContent)
	require.Nil(t, storedFeedback.PlanAgentRunID)
	require.Nil(t, storedFeedback.ExecutionAgentRunID)

	var storedRun models.AgentRun
	require.NoError(t, fixtures.db.First(&storedRun, fixtures.agentRun.ID).Error)
	require.Equal(t, "queued", storedRun.State)
	require.Nil(t, storedRun.PlanContent)

	var executionRuns int64
	require.NoError(t, fixtures.db.Model(&models.AgentRun{}).Where("execution_mode = ?", "plan_execution").Count(&executionRuns).Error)
	require.Equal(t, int64(0), executionRuns)
}

func TestHandlePlanRejectedRollsBackWhenCommentFails(t *testing.T) {
	fixtures := setupPlanTestFixtures(t)
	originalPost := postPlanRejectionComment
	defer func() { postPlanRejectionComment = originalPost }()

	callCount := 0
	postPlanRejectionComment = func(ctx context.Context, logger *zap.Logger, db *gorm.DB, reviewFeedback *models.ReviewFeedback, sanitizedReason string) error {
		callCount++
		require.Equal(t, fixtures.reviewFeedback.ID, reviewFeedback.ID)
		require.Equal(t, utils.TruncateWithSuffix(utils.SanitizeUTF8("Needs more detail"), utils.GetDBOutputLimitBytes(), "… [truncated]"), sanitizedReason)
		return &planRejectionHTTPError{
			status:  http.StatusInternalServerError,
			code:    "GITHUB_COMMENT_ERROR",
			message: "Failed to post plan rejection comment",
			err:     errors.New("comment failure"),
		}
	}

	agentRunRepo := repositories.NewAgentRunRepository(fixtures.db)
	reviewFeedbackRepo := repositories.NewReviewFeedbackRepositoryWithDB(fixtures.db)

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest("POST", "/", nil)

	req := &PlanReportRequest{Status: "plan_rejected", AgentType: "claude-code", RejectionReason: "Needs more detail"}

	handlePlanRejected(
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

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 1, callCount)

	var refreshedFeedback models.ReviewFeedback
	require.NoError(t, fixtures.db.First(&refreshedFeedback, fixtures.reviewFeedback.ID).Error)
	require.Equal(t, "pending", refreshedFeedback.PlanCreationStatus)
	require.Nil(t, refreshedFeedback.PlanAgentRunID)
	require.Nil(t, refreshedFeedback.PlanContent)
	require.Nil(t, refreshedFeedback.ExecutionAgentRunID)

	var refreshedRun models.AgentRun
	require.NoError(t, fixtures.db.First(&refreshedRun, fixtures.agentRun.ID).Error)
	require.Equal(t, "queued", refreshedRun.State)
	require.Nil(t, refreshedRun.PlanContent)
	require.Nil(t, refreshedRun.ErrorMessage)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "GITHUB_COMMENT_ERROR", resp["error"])
}

func TestHandlePlanRejectedUpdatesStatusAfterComment(t *testing.T) {
	fixtures := setupPlanTestFixtures(t)
	originalPost := postPlanRejectionComment
	defer func() { postPlanRejectionComment = originalPost }()

	callCount := 0
	postPlanRejectionComment = func(ctx context.Context, logger *zap.Logger, db *gorm.DB, reviewFeedback *models.ReviewFeedback, sanitizedReason string) error {
		callCount++
		require.Equal(t, fixtures.reviewFeedback.ID, reviewFeedback.ID)
		require.Equal(t, utils.TruncateWithSuffix(utils.SanitizeUTF8("Missing acceptance tests"), utils.GetDBOutputLimitBytes(), "… [truncated]"), sanitizedReason)
		return nil
	}

	agentRunRepo := repositories.NewAgentRunRepository(fixtures.db)
	reviewFeedbackRepo := repositories.NewReviewFeedbackRepositoryWithDB(fixtures.db)

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest("POST", "/", nil)

	req := &PlanReportRequest{Status: "plan_rejected", AgentType: "claude-code", RejectionReason: "Missing acceptance tests"}

	handlePlanRejected(
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

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, callCount)

	var refreshedFeedback models.ReviewFeedback
	require.NoError(t, fixtures.db.First(&refreshedFeedback, fixtures.reviewFeedback.ID).Error)
	require.Equal(t, "rejected", refreshedFeedback.PlanCreationStatus)
	require.NotNil(t, refreshedFeedback.PlanAgentRunID)
	require.Equal(t, fixtures.agentRun.ID, *refreshedFeedback.PlanAgentRunID)
	require.Nil(t, refreshedFeedback.PlanContent)
	require.Nil(t, refreshedFeedback.ExecutionAgentRunID)

	var refreshedRun models.AgentRun
	require.NoError(t, fixtures.db.First(&refreshedRun, fixtures.agentRun.ID).Error)
	require.Equal(t, "failed", refreshedRun.State)
	require.Nil(t, refreshedRun.PlanContent)
	require.NotNil(t, refreshedRun.ErrorMessage)
	require.Contains(t, *refreshedRun.ErrorMessage, "Missing acceptance tests")
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
		fixtures.agentRun.State,
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

func TestHandlePlanReportIgnoresDuplicatePlanCreated_IssueTriggered(t *testing.T) {
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
			plan_agent_run_id INTEGER,
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
	}

	for _, stmt := range schemaStatements {
		require.NoError(t, db.Exec(stmt).Error)
	}

	issue := &models.Issue{Repo: "owner/repo", Number: 1}
	require.NoError(t, db.Create(issue).Error)

	// Create issue-triggered plan creation AgentRun (ReviewFeedbackID is nil)
	// State must be "started" because UpdateState only allows transition from "started" to "succeeded"
	agentRun := &models.AgentRun{
		IdempotencyKey:   "issue-plan-run",
		IssueID:          issue.ID,
		State:            "started",
		AgentType:        "claude-code",
		ExecutionMode:    "plan_creation",
		ReviewFeedbackID: nil, // Issue-triggered
	}
	require.NoError(t, db.Create(agentRun).Error)

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

	// First plan report: should succeed
	firstPayload := map[string]any{
		"status":       "plan_created",
		"agent_type":   "claude-code",
		"plan_content": "Step A",
	}
	firstBody, err := json.Marshal(firstPayload)
	require.NoError(t, err)

	w1 := httptest.NewRecorder()
	ctx1, _ := gin.CreateTestContext(w1)
	path1 := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx1.Request = httptest.NewRequest("POST", path1, bytes.NewReader(firstBody))
	ctx1.Request.Header.Set("Content-Type", "application/json")
	ctx1.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx1)

	require.Equal(t, http.StatusOK, w1.Code)

	// Verify first plan report was processed
	var refreshedAgentRun models.AgentRun
	require.NoError(t, db.First(&refreshedAgentRun, agentRun.ID).Error)
	require.Equal(t, "succeeded", refreshedAgentRun.State)
	require.NotNil(t, refreshedAgentRun.PlanContent)

	// Verify execution AgentRun was created
	var executionRuns []models.AgentRun
	require.NoError(t, db.Where("issue_id = ? AND execution_mode = ? AND plan_content IS NOT NULL",
		issue.ID, "plan_execution").Find(&executionRuns).Error)
	require.Len(t, executionRuns, 1)
	originalExecutionID := executionRuns[0].ID
	originalPlanCallCount := fakeJob.planCallCount

	// Second plan report: should be ignored as duplicate
	secondPayload := map[string]any{
		"status":       "plan_created",
		"agent_type":   "claude-code",
		"plan_content": "Step B",
	}
	secondBody, err := json.Marshal(secondPayload)
	require.NoError(t, err)

	w2 := httptest.NewRecorder()
	ctx2, _ := gin.CreateTestContext(w2)
	path2 := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx2.Request = httptest.NewRequest("POST", path2, bytes.NewReader(secondBody))
	ctx2.Request.Header.Set("Content-Type", "application/json")
	ctx2.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx2)

	require.Equal(t, http.StatusOK, w2.Code)

	// Verify response signals duplicate processing
	// Note: When state is already "succeeded", UpdateState fails and the duplicate check
	// should catch it. However, if the state check happens after UpdateState fails,
	// the response may indicate that execution is already in progress.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	// Accept either message as both indicate duplicate processing
	message := resp["message"].(string)
	require.True(t, message == "Plan report already processed" || message == "Plan content updated, execution already in progress" || message == "Plan created, execution already in progress",
		"Expected duplicate processing message, got: %s", message)
	// State field may not be present in all response types
	if state, ok := resp["state"]; ok {
		require.Equal(t, "succeeded", state)
	}
	require.Equal(t, float64(agentRun.ID), resp["plan_agent_run_id"])
	require.Equal(t, float64(originalExecutionID), resp["execution_agent_run_id"])

	// Verify no new execution run was created
	var allExecutionRuns []models.AgentRun
	require.NoError(t, db.Where("issue_id = ? AND execution_mode = ? AND plan_content IS NOT NULL",
		issue.ID, "plan_execution").Find(&allExecutionRuns).Error)
	require.Len(t, allExecutionRuns, 1)
	require.Equal(t, originalExecutionID, allExecutionRuns[0].ID)

	// Verify job service was not called again
	require.Equal(t, originalPlanCallCount, fakeJob.planCallCount)
}
