package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type recordingPlanJobService struct {
	called             bool
	lastAgentRunID     int
	lastReviewFeedback int
}

func (r *recordingPlanJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	return nil, fmt.Errorf("unexpected call")
}

func (r *recordingPlanJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *services.AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	return nil, fmt.Errorf("unexpected call")
}

func (r *recordingPlanJobService) CreateJobForPlanCreation(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, reviewFeedback *models.ReviewFeedback, branchName string) (*batchv1.Job, error) {
	r.called = true
	r.lastAgentRunID = agentRun.ID
	if reviewFeedback != nil {
		r.lastReviewFeedback = reviewFeedback.ID
	}
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("plan-job-%d", agentRun.ID)}}, nil
}

func (r *recordingPlanJobService) CreateJobForPlanExecution(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, planContent string, branchName string) (*batchv1.Job, error) {
	return nil, fmt.Errorf("unexpected call")
}

func setupPlanCreationDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	schema := []string{
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
		`CREATE TABLE IF NOT EXISTS agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT UNIQUE,
			issue_id INTEGER,
			pr_id INTEGER,
			state TEXT DEFAULT 'queued',
			agent_type TEXT,
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
	}

	for _, stmt := range schema {
		require.NoError(t, db.Exec(stmt).Error)
	}

	return db
}

func setupPlanCreationFixtures(t *testing.T, db *gorm.DB) (*models.Issue, *models.PullRequest) {
	issue := &models.Issue{Repo: "owner/repo", Number: 42}
	require.NoError(t, db.Create(issue).Error)

	pr := &models.PullRequest{Repo: "owner/repo", Number: 7, IssueID: &issue.ID, Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	return issue, pr
}

func invokeReviewCommentHandler(t *testing.T, payload *PullRequestReviewCommentPayload, deps PullRequestReviewCommentDeps) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payloadBytes))
	req.Header.Set("X-GitHub-Delivery", "delivery-test")

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	ctx.Set("webhook_payload", payloadBytes)

	HandlePullRequestReviewCommentWithDeps(ctx, deps)

	return w
}

func TestHandlePullRequestReviewComment_PlanCreationSkippedForShortComment(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	_, pr := setupPlanCreationFixtures(t, db)

	jobService := &recordingPlanJobService{}
	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		KubernetesJobService:     jobService,
	}

	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   1001,
			Body: "Looks good",
			User: User{Login: "alice"},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "no_trigger", resp["status"])
	require.Equal(t, "skipped_short_comment", resp["plan_creation_status"])
	require.False(t, jobService.called)

	var count int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&models.AgentRun{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestHandlePullRequestReviewComment_PlanCreationStarted(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	issue, pr := setupPlanCreationFixtures(t, db)

	jobService := &recordingPlanJobService{}
	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		KubernetesJobService:     jobService,
	}

	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   2002,
			Body: "This change breaks the API contract; please update the validation logic and add regression tests.",
			User: User{Login: "reviewer"},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "plan_creation_started", resp["status"])
	require.Equal(t, "started", resp["plan_creation_status"])
	require.True(t, jobService.called)
	require.NotZero(t, jobService.lastAgentRunID)
	require.NotZero(t, jobService.lastReviewFeedback)

	var feedback models.ReviewFeedback
	require.NoError(t, db.Last(&feedback).Error)
	require.Equal(t, pr.ID, feedback.PRID)
	require.Equal(t, "creating", feedback.PlanCreationStatus)
	require.NotNil(t, feedback.PlanAgentRunID)

	var run models.AgentRun
	require.NoError(t, db.First(&run, feedback.PlanAgentRunID).Error)
	require.Equal(t, "plan_creation", run.ExecutionMode)
	require.Equal(t, issue.ID, run.IssueID)
	require.NotNil(t, run.ReviewFeedbackID)
	require.Equal(t, feedback.ID, *run.ReviewFeedbackID)

	require.NotNil(t, resp["plan_agent_run_id"])
	require.Equal(t, float64(run.ID), resp["plan_agent_run_id"].(float64))
}
