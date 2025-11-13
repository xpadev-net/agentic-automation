package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockCodexReviewService is a mock implementation of CodexReviewService for testing
type mockCodexReviewService struct {
	requestReviewCalled bool
	requestReviewArgs   struct {
		ctx            context.Context
		owner          string
		repo           string
		prNumber       int
		prID           int
		idempotencyKey string
	}
	requestReviewResult *models.ReviewFeedback
	requestReviewErr    error
}

func (m *mockCodexReviewService) RequestReview(ctx context.Context, owner, repo string, prNumber, prID int, idempotencyKey string) (*models.ReviewFeedback, error) {
	m.requestReviewCalled = true
	m.requestReviewArgs.ctx = ctx
	m.requestReviewArgs.owner = owner
	m.requestReviewArgs.repo = repo
	m.requestReviewArgs.prNumber = prNumber
	m.requestReviewArgs.prID = prID
	m.requestReviewArgs.idempotencyKey = idempotencyKey
	return m.requestReviewResult, m.requestReviewErr
}

type stubAuthorizationService struct {
	allowed      bool
	err          error
	lastOwner    string
	lastRepo     string
	lastUsername string
}

func (s *stubAuthorizationService) CheckPermission(ctx context.Context, owner, repo, username string) (bool, error) {
	s.lastOwner = owner
	s.lastRepo = repo
	s.lastUsername = username
	if s.err != nil {
		return false, s.err
	}
	return s.allowed, nil
}

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

func TestHandlePullRequestReviewComment_TriggerRequestsCodexReview(t *testing.T) {
	db := setupPlanCreationDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	_, pr := setupPlanCreationFixtures(t, db)

	mockReviewService := &mockCodexReviewService{}
	authStub := &stubAuthorizationService{allowed: true}
	githubClient := clients.NewFromGitHub(github.NewClient(nil), zap.NewNop())

	deps := PullRequestReviewCommentDeps{
		Logger:                   zap.NewNop(),
		GitHubClient:             githubClient,
		AuthorizationService:     authStub,
		CodexReviewService:       mockReviewService,
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
	}

	payload := &PullRequestReviewCommentPayload{
		Action: models.PullRequestReviewCommentActionCreated,
		Comment: PullRequestReviewCommentComment{
			ID:   9001,
			Body: "Please @codex review the latest changes.",
			User: User{Login: "trusted-reviewer"},
		},
		PullRequest: PullRequestReviewCommentPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewCommentRepository{FullName: pr.Repo},
	}

	w := invokeReviewCommentHandler(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "processed", resp["status"])
	require.Equal(t, float64(pr.Number), resp["pr_number"])

	require.True(t, mockReviewService.requestReviewCalled, "Codex review should be requested when trigger is present")
	require.Equal(t, "owner", mockReviewService.requestReviewArgs.owner)
	require.Equal(t, "repo", mockReviewService.requestReviewArgs.repo)
	require.Equal(t, pr.Number, mockReviewService.requestReviewArgs.prNumber)
	require.Equal(t, pr.ID, mockReviewService.requestReviewArgs.prID)
	require.Contains(t, mockReviewService.requestReviewArgs.idempotencyKey, fmt.Sprintf(":%d", payload.Comment.ID))

	require.Equal(t, "owner", authStub.lastOwner)
	require.Equal(t, "repo", authStub.lastRepo)
	require.Equal(t, "trusted-reviewer", authStub.lastUsername)
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
	// plan作成はpull_request_review_commentイベントでは実行されない
	require.NotContains(t, resp, "plan_creation_status")
	require.False(t, jobService.called)

	var count int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&models.AgentRun{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestHandlePullRequestReviewComment_PlanCreationNotStarted(t *testing.T) {
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
	// plan作成はpull_request_review_commentイベントでは実行されない
	require.Equal(t, "no_trigger", resp["status"])
	require.NotContains(t, resp, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation should not be called for pull_request_review_comment event")

	// ReviewFeedbackやAgentRunは作成されない
	var count int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&models.AgentRun{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestHandlePullRequestReviewComment_PlanCreationNotExecutedForCommentID(t *testing.T) {
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
	commentBody := "This is a detailed review comment that should not trigger plan creation."

	// First invocation: plan creation should not be executed
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
	require.Equal(t, "no_trigger", resp1["status"])
	require.NotContains(t, resp1, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation should not be called for pull_request_review_comment event")

	// Verify that no ReviewFeedback was created
	var feedbackCount int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&feedbackCount).Error)
	require.Zero(t, feedbackCount, "No ReviewFeedback should be created")

	// Second invocation with the same comment ID: plan creation should still not be executed
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
	require.NotContains(t, resp2, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation job should not be called for pull_request_review_comment event")

	// Verify that still no ReviewFeedback was created
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Count(&feedbackCount).Error)
	require.Zero(t, feedbackCount, "No ReviewFeedback should be created")
}

func TestHandlePullRequestReviewComment_PlanCreationNotExecutedEvenWithExistingFeedback(t *testing.T) {
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

	// Reprocess the same comment: plan creation should not be executed
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
	require.NotContains(t, resp, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation job should not be called for pull_request_review_comment event")

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
	// plan作成はpull_request_review_commentイベントでは実行されない
	require.Equal(t, "no_trigger", resp["status"])
	require.NotContains(t, resp, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation should not be called for pull_request_review_comment event")

	// Verify that the Codex requested record is still in "requested" status
	var codexFeedback models.ReviewFeedback
	require.NoError(t, db.First(&codexFeedback, codexRequestedFeedback.ID).Error)
	require.Equal(t, "requested", codexFeedback.Status, "Codex requested record should remain in requested status")
	require.Equal(t, codexCommentID, *codexFeedback.GitHubCommentID)

	// Verify that no new received record was created for the human comment
	var allFeedbacks []models.ReviewFeedback
	require.NoError(t, db.Where("pr_id = ?", pr.ID).Find(&allFeedbacks).Error)
	require.Len(t, allFeedbacks, 1, "Should have only 1 feedback record (the existing Codex requested record)")
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
	// plan作成はpull_request_review_commentイベントでは実行されない
	require.Equal(t, "no_trigger", resp["status"])
	require.NotContains(t, resp, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation should not be called for pull_request_review_comment event")

	// Verify that the Codex requested record is still in "requested" status (not updated)
	var updatedFeedback models.ReviewFeedback
	require.NoError(t, db.First(&updatedFeedback, codexRequestedFeedback.ID).Error)
	require.Equal(t, "requested", updatedFeedback.Status, "Codex requested record should remain in requested status")
	require.Equal(t, codexCommentID, *updatedFeedback.GitHubCommentID)

	// Verify that no new record was created
	var feedbackCount int64
	require.NoError(t, db.Model(&models.ReviewFeedback{}).Where("pr_id = ?", pr.ID).Count(&feedbackCount).Error)
	require.Equal(t, int64(1), feedbackCount, "Should have only 1 feedback record (the existing Codex requested record)")
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
	require.NotContains(t, resp, "plan_creation_status")
	require.False(t, jobService.called, "Plan creation job should not be called when PR is not found")
}
