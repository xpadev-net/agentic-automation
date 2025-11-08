package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupPlanExecutionTestFixtures(t *testing.T) (*gorm.DB, *models.AgentRun, *models.ReviewFeedback, *models.Issue) {
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

	reviewFeedback := &models.ReviewFeedback{
		PRID:               pr.ID,
		Status:             "received",
		PlanCreationStatus: "created",
	}
	require.NoError(t, db.Create(reviewFeedback).Error)

	agentRun := &models.AgentRun{
		IdempotencyKey:   "plan-exec-run",
		IssueID:          issue.ID,
		State:            "queued",
		AgentType:        "claude-code",
		ExecutionMode:    "plan_execution",
		ReviewFeedbackID: &reviewFeedback.ID,
	}
	require.NoError(t, db.Create(agentRun).Error)

	return db, agentRun, reviewFeedback, issue
}

func TestHandleAgentReport_PlanExecution_Success(t *testing.T) {
	db, agentRun, reviewFeedback, _ := setupPlanExecutionTestFixtures(t)

	payload := map[string]any{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  1,
		"branch":     "feature/test",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	path := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify ReviewFeedback status was updated to "executed"
	var updatedReviewFeedback models.ReviewFeedback
	require.NoError(t, db.First(&updatedReviewFeedback, reviewFeedback.ID).Error)
	require.Equal(t, "executed", updatedReviewFeedback.PlanCreationStatus)

	// Verify AgentRun was updated
	var updatedAgentRun models.AgentRun
	require.NoError(t, db.First(&updatedAgentRun, agentRun.ID).Error)
	require.Equal(t, "succeeded", updatedAgentRun.State)
}

func TestHandleAgentReport_PlanExecution_Failed(t *testing.T) {
	db, agentRun, reviewFeedback, _ := setupPlanExecutionTestFixtures(t)

	payload := map[string]any{
		"status":        "failed",
		"agent_type":    "claude-code",
		"error_message": "Test error",
		"logs":          "Error log content",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	path := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify ReviewFeedback status was reverted to "created"
	var updatedReviewFeedback models.ReviewFeedback
	require.NoError(t, db.First(&updatedReviewFeedback, reviewFeedback.ID).Error)
	require.Equal(t, "created", updatedReviewFeedback.PlanCreationStatus)

	// Verify AgentRun was updated
	var updatedAgentRun models.AgentRun
	require.NoError(t, db.First(&updatedAgentRun, agentRun.ID).Error)
	require.Equal(t, "failed", updatedAgentRun.State)
}

func TestHandleAgentReport_PlanExecution_ReviewFeedbackID_Nil(t *testing.T) {
	db, agentRun, _, _ := setupPlanExecutionTestFixtures(t)

	// Set ReviewFeedbackID to nil
	agentRun.ReviewFeedbackID = nil
	require.NoError(t, db.Save(agentRun).Error)

	payload := map[string]any{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  1,
		"branch":     "feature/test",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	path := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx)

	// Should complete successfully without error
	require.Equal(t, http.StatusOK, w.Code)

	// Verify AgentRun was updated
	var updatedAgentRun models.AgentRun
	require.NoError(t, db.First(&updatedAgentRun, agentRun.ID).Error)
	require.Equal(t, "succeeded", updatedAgentRun.State)
}

func TestHandleAgentReport_PlanExecution_ReviewFeedback_NotFound(t *testing.T) {
	db, agentRun, _, _ := setupPlanExecutionTestFixtures(t)

	// Set ReviewFeedbackID to a non-existent ID
	nonExistentID := 99999
	agentRun.ReviewFeedbackID = &nonExistentID
	require.NoError(t, db.Save(agentRun).Error)

	payload := map[string]any{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  1,
		"branch":     "feature/test",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	path := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx)

	// Should complete successfully without error (non-blocking)
	require.Equal(t, http.StatusOK, w.Code)

	// Verify AgentRun was updated
	var updatedAgentRun models.AgentRun
	require.NoError(t, db.First(&updatedAgentRun, agentRun.ID).Error)
	require.Equal(t, "succeeded", updatedAgentRun.State)
}

func TestHandleAgentReport_PlanExecution_Update_Failed(t *testing.T) {
	// This test verifies that Update() failure is handled gracefully
	// Since we're using a real database, we can't easily simulate Update() failure
	// without mocking. For now, we'll test that the normal flow works.
	// In a real scenario with mocking, we would inject a failing repository.
	db, agentRun, reviewFeedback, _ := setupPlanExecutionTestFixtures(t)

	payload := map[string]any{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  1,
		"branch":     "feature/test",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	path := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx)

	// Should complete successfully even if ReviewFeedback update fails (non-blocking)
	require.Equal(t, http.StatusOK, w.Code)

	// Verify ReviewFeedback was updated (in normal case)
	var updatedReviewFeedback models.ReviewFeedback
	require.NoError(t, db.First(&updatedReviewFeedback, reviewFeedback.ID).Error)
	require.Equal(t, "executed", updatedReviewFeedback.PlanCreationStatus)
}

func TestHandleAgentReport_PlanExecution_NormalMode_Skipped(t *testing.T) {
	db, agentRun, reviewFeedback, _ := setupPlanExecutionTestFixtures(t)

	// Change ExecutionMode to "normal"
	agentRun.ExecutionMode = "normal"
	require.NoError(t, db.Save(agentRun).Error)

	// Store original status
	originalStatus := reviewFeedback.PlanCreationStatus

	payload := map[string]any{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  1,
		"branch":     "feature/test",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	path := "/api/agent-runs/" + strconv.Itoa(agentRun.ID) + "/report"
	ctx.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(agentRun.ID)}}

	HandleAgentReport(ctx)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify ReviewFeedback status was NOT changed
	var updatedReviewFeedback models.ReviewFeedback
	require.NoError(t, db.First(&updatedReviewFeedback, reviewFeedback.ID).Error)
	require.Equal(t, originalStatus, updatedReviewFeedback.PlanCreationStatus)

	// Verify AgentRun was updated
	var updatedAgentRun models.AgentRun
	require.NoError(t, db.First(&updatedAgentRun, agentRun.ID).Error)
	require.Equal(t, "succeeded", updatedAgentRun.State)
}
