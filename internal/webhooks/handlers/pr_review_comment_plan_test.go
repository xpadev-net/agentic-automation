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

func TestHandlePullRequestReviewComment_PlanCreationDeduplicationByCommentID(t *testing.T) {
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

	commentID := int64(3003)
	commentBody := "This is a detailed review comment that should trigger plan creation."

	// First invocation: should create a new ReviewFeedback and start plan creation
	payload1 := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   int(commentID),
			Body: commentBody,
			User: User{Login: "reviewer"},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w1 := invokeReviewCommentHandler(t, payload1, deps)
	require.Equal(t, http.StatusOK, w1.Code)

	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.Equal(t, "plan_creation_started", resp1["status"])
	require.True(t, jobService.called)
	require.NotZero(t, jobService.lastReviewFeedback)

	// Verify that a ReviewFeedback was created
	var feedback1 models.ReviewFeedback
	require.NoError(t, db.First(&feedback1, jobService.lastReviewFeedback).Error)
	require.Equal(t, commentID, *feedback1.GitHubCommentID)
	require.Equal(t, "creating", feedback1.PlanCreationStatus)

	// Reset the job service call counter
	jobService.called = false
	jobService.lastAgentRunID = 0
	jobService.lastReviewFeedback = 0

	// Second invocation with the same comment ID: should reuse existing ReviewFeedback and skip plan creation
	payload2 := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   int(commentID),
			Body: commentBody,
			User: User{Login: "reviewer"},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w2 := invokeReviewCommentHandler(t, payload2, deps)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.Equal(t, "no_trigger", resp2["status"])
	require.Equal(t, "skipped_plan_already_started", resp2["plan_creation_status"])
	require.False(t, jobService.called, "Plan creation job should not be called again for the same comment ID")

	// Verify that no new ReviewFeedback was created
	var feedbackCount int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&feedbackCount).Error)
	require.Equal(t, int64(1), feedbackCount, "Only one ReviewFeedback should exist")

	// Verify that the existing ReviewFeedback is still in "creating" status
	var feedback2 models.ReviewFeedback
	require.NoError(t, db.First(&feedback2, feedback1.ID).Error)
	require.Equal(t, feedback1.ID, feedback2.ID)
	require.Equal(t, "creating", feedback2.PlanCreationStatus)
}

func TestHandlePullRequestReviewComment_PlanCreationDeduplicationWithCompletedPlan(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	_, pr := setupPlanCreationFixtures(t, db)

	// Create an existing ReviewFeedback with plan creation already completed
	commentID := int64(4004)
	existingFeedback := &models.ReviewFeedback{
		PRID:               pr.ID,
		Source:             "Codex",
		Status:             "received",
		Content:            stringPtr("This is a review comment."),
		ApprovalDetected:   false,
		GitHubCommentID:    &commentID,
		PlanCreationStatus: "created",
	}
	require.NoError(t, db.Create(existingFeedback).Error)

	jobService := &recordingPlanJobService{}
	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		KubernetesJobService:     jobService,
	}

	// Reprocess the same comment: should skip plan creation since it's already completed
	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   int(commentID),
			Body: "This is a review comment.",
			User: User{Login: "reviewer"},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "no_trigger", resp["status"])
	require.Equal(t, "skipped_plan_already_started", resp["plan_creation_status"])
	require.Equal(t, "created", resp["plan_creation_state"])
	require.False(t, jobService.called, "Plan creation job should not be called for already completed plan")

	// Verify that no new ReviewFeedback was created
	var feedbackCount int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&feedbackCount).Error)
	require.Equal(t, int64(1), feedbackCount, "Only one ReviewFeedback should exist")
}

func stringPtr(s string) *string {
	return &s
}

func TestHandlePullRequestReviewComment_HumanCommentDoesNotUpdateCodexRequested(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	_, pr := setupPlanCreationFixtures(t, db)

	// Create an existing Codex review request
	codexCommentID := int64(5001)
	codexRequestedFeedback := &models.ReviewFeedback{
		PRID:             pr.ID,
		Source:           "Codex",
		Status:           "requested",
		Content:          nil,
		ApprovalDetected: false,
		GitHubCommentID:  &codexCommentID,
	}
	require.NoError(t, db.Create(codexRequestedFeedback).Error)

	jobService := &recordingPlanJobService{}
	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		KubernetesJobService:     jobService,
	}

	// Human reviewer's comment (different comment ID)
	humanCommentID := int64(5002)
	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   int(humanCommentID),
			Body: "This is a detailed review comment from a human reviewer that should trigger plan creation.",
			User: User{Login: "human-reviewer", ID: 12345},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "plan_creation_started", resp["status"])

	// Verify that the Codex requested record is still in "requested" status
	var codexFeedback models.ReviewFeedback
	require.NoError(t, db.First(&codexFeedback, codexRequestedFeedback.ID).Error)
	require.Equal(t, "requested", codexFeedback.Status, "Codex requested record should remain in requested status")
	require.Equal(t, codexCommentID, *codexFeedback.GitHubCommentID)

	// Verify that a new received record was created for the human comment
	var allFeedbacks []models.ReviewFeedback
	require.NoError(t, db.Where("pr_id = ?", pr.ID).Find(&allFeedbacks).Error)
	require.Len(t, allFeedbacks, 2, "Should have 2 feedback records: one requested (Codex) and one received (human)")

	// Find the human comment's feedback record
	var humanFeedback *models.ReviewFeedback
	for i := range allFeedbacks {
		if allFeedbacks[i].ID != codexFeedback.ID {
			humanFeedback = &allFeedbacks[i]
			break
		}
	}
	require.NotNil(t, humanFeedback, "Human comment feedback record should exist")
	require.Equal(t, "received", humanFeedback.Status)
	require.Equal(t, humanCommentID, *humanFeedback.GitHubCommentID)
}

func TestHandlePullRequestReviewComment_CodexCommentUpdatesMatchingRequested(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	_, pr := setupPlanCreationFixtures(t, db)

	// Create an existing Codex review request with a specific comment ID
	codexCommentID := int64(6001)
	codexRequestedFeedback := &models.ReviewFeedback{
		PRID:             pr.ID,
		Source:           "Codex",
		Status:           "requested",
		Content:          nil,
		ApprovalDetected: false,
		GitHubCommentID:  &codexCommentID,
	}
	require.NoError(t, db.Create(codexRequestedFeedback).Error)

	jobService := &recordingPlanJobService{}
	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		KubernetesJobService:     jobService,
	}

	// Codex bot's comment with matching comment ID
	codexBotUsername := "chatgpt-codex-connector[bot]"
	codexBotUserID := int64(199175422)
	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   int(codexCommentID),
			Body: "This is Codex's review response that matches the requested review.",
			User: User{Login: codexBotUsername, ID: codexBotUserID},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "plan_creation_started", resp["status"])

	// Verify that the Codex requested record was updated to "received"
	var updatedFeedback models.ReviewFeedback
	require.NoError(t, db.First(&updatedFeedback, codexRequestedFeedback.ID).Error)
	require.Equal(t, "received", updatedFeedback.Status, "Codex requested record should be updated to received")
	require.Equal(t, codexCommentID, *updatedFeedback.GitHubCommentID)
	require.NotNil(t, updatedFeedback.Content)
	require.Contains(t, *updatedFeedback.Content, "This is Codex's review response")

	// Verify that no new record was created
	var feedbackCount int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Where("pr_id = ?", pr.ID).Count(&feedbackCount).Error)
	require.Equal(t, int64(1), feedbackCount, "Should have only 1 feedback record (updated from requested to received)")
}

func TestHandlePullRequestReviewComment_MissingPRReturns200(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	jobService := &recordingPlanJobService{}
	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		KubernetesJobService:     jobService,
	}

	// Comment for a PR that doesn't exist in the database
	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   7001,
			Body: "This is a review comment for a PR that doesn't exist in our database.",
			User: User{Login: "reviewer", ID: 12345},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: 999},
		Repository:  PullRequestReviewCommentRepository{FullName: "owner/repo"},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code, "Should return 200 even when PR is not found")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "no_trigger", resp["status"])
	require.Equal(t, "pr_not_found", resp["reason"])
	require.False(t, jobService.called, "Plan creation job should not be called when PR is not found")
}
