package integration

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	us2TestToken = "test-operator-api-token-12345"
)

// setOperatorToken sets OPERATOR_API_TOKEN for the duration of the test
func setOperatorToken(t *testing.T) {
	prev, had := os.LookupEnv("OPERATOR_API_TOKEN")
	_ = os.Setenv("OPERATOR_API_TOKEN", us2TestToken)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("OPERATOR_API_TOKEN", prev)
		} else {
			_ = os.Unsetenv("OPERATOR_API_TOKEN")
		}
	})
}

// setupRouter constructs a minimal router mounting the real handler
func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/api/agent-runs/:id/report",
		middleware.VerifyBearerToken(),
		handlers.HandleAgentReport,
	)
	return r
}

// helper to POST report
func postReport(t *testing.T, router *gin.Engine, id int, token string, body handlers.ReportRequest) *httptest.ResponseRecorder {
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", fmt.Sprintf("/api/agent-runs/%d/report", id), bytes.NewBuffer(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// configure test logger/db placeholders (DB will be set in following tests)
func mustTestLogger(t *testing.T) *zap.Logger {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	config.SetLoggerForTesting(logger)
	return logger
}

// --- SQLite test DB setup (in-memory) ---
// We mirror the minimal schema used by the handler's repositories.
// Note: SQLite lacks native ENUM; use TEXT.

func setupUS2TestDB(t *testing.T) *gorm.DB {
	dsn := "file::memory:?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// issues
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

	// pull_requests
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

	// agent_runs
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

	// review_feedback
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS review_feedback (
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
		);
		CREATE INDEX IF NOT EXISTS idx_review_feedback_pr_id ON review_feedback(pr_id);
	`).Error)

	// minimal supporting tables referenced elsewhere
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS operation_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER,
			operation_type TEXT,
			operation_id TEXT UNIQUE,
			status TEXT DEFAULT 'pending',
			created_at DATETIME
		);
	`).Error)

	return db
}

func teardownUS2TestDB(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

// --- Fixtures helpers ---

type issueRow struct {
	ID            int
	Repo          string
	Number        int
	GitHubIssueID int
}

type agentRunRow struct {
	ID             int
	IdempotencyKey string
	IssueID        int
}

func createIssueUS2(t *testing.T, db *gorm.DB) *issueRow {
	row := &issueRow{Repo: "test-org/us2-pr-repo", Number: 2001, GitHubIssueID: 987654321}
	require.NoError(t, db.Exec(`INSERT INTO issues (repo, number, github_issue_id, title, body, labels, state, created_at, updated_at)
		VALUES (?, ?, ?, 't', 'b', '[]', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, row.Repo, row.Number, row.GitHubIssueID).Error)
	require.NoError(t, db.Raw("SELECT id FROM issues WHERE repo = ? AND number = ?", row.Repo, row.Number).Scan(&row.ID).Error)
	return row
}

func createAgentRun(t *testing.T, db *gorm.DB, issueID int) *agentRunRow {
	row := &agentRunRow{IssueID: issueID, IdempotencyKey: fmt.Sprintf("test-key-%d", time.Now().UnixNano())}
	require.NoError(t, db.Exec(`INSERT INTO agent_runs (idempotency_key, issue_id, state, agent_type, created_at, updated_at)
		VALUES (?, ?, 'queued', 'claude-code', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, row.IdempotencyKey, row.IssueID).Error)
	require.NoError(t, db.Raw("SELECT id FROM agent_runs WHERE idempotency_key = ?", row.IdempotencyKey).Scan(&row.ID).Error)
	return row
}

// --- Main success test ---

func TestUS2_PRCreation_Success(t *testing.T) {
	setOperatorToken(t)
	logger := mustTestLogger(t)
	db := setupUS2TestDB(t)
	defer teardownUS2TestDB(db)

	// Inject config deps for real handler
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)

	router := setupRouter()

	iss := createIssueUS2(t, db)
	run := createAgentRun(t, db, iss.ID)

	reqBody := handlers.ReportRequest{
		Status:    "succeeded",
		AgentType: "claude-code",
		PRNumber:  intPtrUS2(101),
		Branch:    "feature/t088",
		CommitSHA: "deadbeef",
	}

	w := postReport(t, router, run.ID, us2TestToken, reqBody)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Message    string `json:"message"`
		AgentRunID int    `json:"agent_run_id"`
		PRURL      string `json:"pr_url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, run.ID, resp.AgentRunID)
	require.Contains(t, resp.PRURL, "https://github.com/test-org/us2-pr-repo/pull/101")
	require.Contains(t, resp.Message, "succeeded")

	// Verify PR upserted
	var pr struct {
		ID, Number           int
		Repo, Branch, Status string
	}
	require.NoError(t, db.Raw("SELECT id, repo, number, branch, status FROM pull_requests WHERE repo = ? AND number = ?", iss.Repo, 101).Scan(&pr).Error)
	require.NotZero(t, pr.ID)
	require.Equal(t, "test-org/us2-pr-repo", pr.Repo)
	require.Equal(t, 101, pr.Number)
	require.Equal(t, "feature/t088", pr.Branch)
	require.Equal(t, "open", pr.Status)

	// Verify AgentRun linked and fields updated
	var linked struct {
		PRID        *int
		CommitSHA   *string
		CompletedAt *string
	}
	require.NoError(t, db.Raw("SELECT pr_id, commit_sha, completed_at FROM agent_runs WHERE id = ?", run.ID).Scan(&linked).Error)
	require.NotNil(t, linked.PRID)
	require.Equal(t, pr.ID, *linked.PRID)
	require.NotNil(t, linked.CommitSHA)
	require.Equal(t, "deadbeef", *linked.CommitSHA)
	require.NotNil(t, linked.CompletedAt)
}

func intPtrUS2(i int) *int { return &i }
