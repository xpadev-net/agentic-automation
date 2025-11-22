package integration

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// test constants
const (
	testOpToken = "test-operator-api-token-12345"
)

// setupDB creates in-memory SQLite and required tables for this test
func setupDBFailureRetry(t *testing.T) *gorm.DB {
	dsn := "file::memory:?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// Minimal schema used by the handler
	err = db.Exec(`
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
    `).Error
	require.NoError(t, err)

	err = db.Exec(`
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
    `).Error
	require.NoError(t, err)

	err = db.Exec(`
        CREATE TABLE IF NOT EXISTS agent_runs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            idempotency_key TEXT UNIQUE,
            issue_id INTEGER,
            pr_id INTEGER,
            state TEXT DEFAULT 'queued',
            agent_type TEXT DEFAULT 'claude-code',
            execution_mode TEXT DEFAULT 'normal',
            job_name TEXT,
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
        );
        CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_idempotency_key ON agent_runs(idempotency_key);
        CREATE INDEX IF NOT EXISTS idx_agent_runs_issue_id ON agent_runs(issue_id);
        CREATE INDEX IF NOT EXISTS idx_agent_runs_state ON agent_runs(state);
    `).Error
	require.NoError(t, err)

	// Audit logs table (for failure summary recording)
	err = db.Exec(`
        CREATE TABLE IF NOT EXISTS audit_logs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            event_type TEXT,
            actor TEXT,
            resource_type TEXT,
            resource_id INTEGER,
            payload TEXT,
            idempotency_key TEXT,
            ip_address TEXT,
            user_agent TEXT,
            created_at DATETIME
        );
        CREATE INDEX IF NOT EXISTS idx_audit_event_created ON audit_logs(event_type, created_at);
        CREATE INDEX IF NOT EXISTS idx_audit_resource ON audit_logs(resource_type, resource_id);
    `).Error
	require.NoError(t, err)

	return db
}

func teardownDBFailureRetry(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
		sqlDB.Close()
	}
}

func createIssue(t *testing.T, db *gorm.DB) *models.Issue {
	issue := &models.Issue{
		Repo:          "test-org/test-repo",
		Number:        777,
		GitHubIssueID: 999777,
		Title:         "Failure path test issue",
		Body:          strPtr("Body"),
		Labels:        `[]`,
		State:         "open",
	}
	require.NoError(t, db.Create(issue).Error)
	return issue
}

func createStartedRun(t *testing.T, db *gorm.DB, issueID int) *models.AgentRun {
	run := &models.AgentRun{
		IdempotencyKey: "idem-failure-1",
		IssueID:        issueID,
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

func buildRealRouter(db *gorm.DB, logger *config.AppLogger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	// Inject test DB/logger into config used by real handler
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)

	// Set OPERATOR_API_TOKEN for bearer middleware
	os.Setenv("OPERATOR_API_TOKEN", testOpToken)

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/api/agent-runs/:id/report", middleware.VerifyBearerToken(), handlers.HandleAgentReport)
	return r
}

func Test_AgentRunnerFailure_Report_RecordsAuditAndNoRetry(t *testing.T) {
	db := setupDBFailureRetry(t)
	defer teardownDBFailureRetry(db)

	logger := config.NewNopLogger()

	router := buildRealRouter(db, logger)
	issue := createIssue(t, db)
	run := createStartedRun(t, db, issue.ID)

	// Build failed report body (with logs to exercise summary/excerpt path)
	reqBody := handlers.ReportRequest{
		Status:       "failed",
		AgentType:    "claude-code",
		ErrorMessage: "Unit tests failed",
		Logs:         "FAIL: parser_test.go:42 expected 'foo', got 'bar'\nmore lines...\n",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/agent-runs/"+itoa(run.ID)+"/report", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testOpToken)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp handlers.ReportResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, run.ID, resp.AgentRunID)
	assert.Contains(t, resp.Message, "failed")

	// Verify AgentRun updated
	var updated models.AgentRun
	require.NoError(t, db.First(&updated, run.ID).Error)
	assert.Equal(t, "failed", updated.State)
	assert.Equal(t, "claude-code", updated.AgentType)
	if assert.NotNil(t, updated.CompletedAt) {
		assert.WithinDuration(t, time.Now(), *updated.CompletedAt, time.Minute)
	}
	// Retry count should remain 0 (US2では自動再試行しない)
	assert.Equal(t, 0, updated.RetryCount)

	// Verify audit log recorded with event_type=agent-run.failed
	type row struct{ Count int }
	var c row
	require.NoError(t, db.Raw("SELECT COUNT(1) AS count FROM audit_logs WHERE event_type = ? AND resource_type = ? AND resource_id = ?", "agent-run.failed", "AgentRun", run.ID).Scan(&c).Error)
	assert.Equal(t, 1, c.Count)
}

// helpers
func strPtr(s string) *string { return &s }

func itoa(i int) string { return strconv.Itoa(i) }
