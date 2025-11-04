package webhooks

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDBForCheckSuite creates an in-memory SQLite database for check_suite testing
func setupTestDBForCheckSuite(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "Failed to create test database")

	// Create tables
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
		)
	`).Error
	require.NoError(t, err, "Failed to create issues table")

	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS pull_requests (
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
		)
	`).Error
	require.NoError(t, err, "Failed to create pull_requests table")

	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT UNIQUE,
			issue_id INTEGER,
			pr_id INTEGER,
			state TEXT DEFAULT 'queued',
			agent_type TEXT DEFAULT 'claude-code',
			input TEXT,
			output TEXT,
			retry_count INTEGER DEFAULT 0,
			error_message TEXT,
			commit_sha TEXT,
			s3_session_key TEXT,
			session_saved_at DATETIME,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`).Error
	require.NoError(t, err, "Failed to create agent_runs table")

	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS ci_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER,
			check_suite_id TEXT,
			check_run_id TEXT,
			name TEXT,
			status TEXT DEFAULT 'queued',
			conclusion TEXT,
			logs TEXT,
			logs_url TEXT,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE(check_suite_id, pr_id)
		)
	`).Error
	require.NoError(t, err, "Failed to create ci_status table")

	return db
}

// setupTestRouterForCheckSuite creates a Gin router with signature verification middleware
func setupTestRouterForCheckSuite(db *gorm.DB, logger *zap.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Set up database and logger in config
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)

	// Apply signature verification and idempotency middleware
	router.POST("/webhooks/github",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		handlers.HandleCheckSuite)

	return router
}

// createTestCheckSuitePayload creates a test check_suite payload
func createTestCheckSuitePayload(action string, conclusion *string, prNumber int) handlers.CheckSuitePayload {
	return handlers.CheckSuitePayload{
		Action: action,
		CheckSuite: handlers.CheckSuite{
			ID:         12345,
			Status:     "completed",
			Conclusion: conclusion,
			HeadBranch: "feature/test",
			HeadSHA:    "abc123",
			PullRequests: []handlers.CheckSuitePullRequest{
				{Number: prNumber},
			},
		},
		Repository: handlers.CheckSuiteRepository{
			FullName: "test/owner",
		},
	}
}

func TestCheckSuite_HandleCheckSuite_NonCompletedAction(t *testing.T) {
	db := setupTestDBForCheckSuite(t)
	logger := zaptest.NewLogger(t)
	router := setupTestRouterForCheckSuite(db, logger)

	payload := createTestCheckSuitePayload("requested", nil, 1)
	payloadBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadBytes))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-123")
	req.Header.Set("X-Hub-Signature-256", "sha256=test-signature")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "ignored", response["status"])
}

func TestCheckSuite_HandleCheckSuite_NoPullRequests(t *testing.T) {
	db := setupTestDBForCheckSuite(t)
	logger := zaptest.NewLogger(t)
	router := setupTestRouterForCheckSuite(db, logger)

	payload := handlers.CheckSuitePayload{
		Action: "completed",
		CheckSuite: handlers.CheckSuite{
			ID:           12345,
			Status:       "completed",
			Conclusion:   stringPtr("success"),
			HeadBranch:   "feature/test",
			HeadSHA:      "abc123",
			PullRequests: []handlers.CheckSuitePullRequest{},
		},
		Repository: handlers.CheckSuiteRepository{
			FullName: "test/owner",
		},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadBytes))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-123")
	req.Header.Set("X-Hub-Signature-256", "sha256=test-signature")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "no_pr", response["status"])
}

func TestCheckSuite_HandleCheckSuite_PRNotFound(t *testing.T) {
	db := setupTestDBForCheckSuite(t)
	logger := zaptest.NewLogger(t)
	router := setupTestRouterForCheckSuite(db, logger)

	payload := createTestCheckSuitePayload("completed", stringPtr("success"), 999)
	payloadBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadBytes))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-123")
	req.Header.Set("X-Hub-Signature-256", "sha256=test-signature")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "pr_not_found", response["status"])
}

func TestCheckSuite_HandleCheckSuite_CISuccess(t *testing.T) {
	db := setupTestDBForCheckSuite(t)
	logger := zaptest.NewLogger(t)
	router := setupTestRouterForCheckSuite(db, logger)

	// Create test PR
	pr := &models.PullRequest{
		Repo:   "test/owner",
		Number: 1,
		Status: "open",
	}
	db.Create(pr)

	payload := createTestCheckSuitePayload("completed", stringPtr("success"), 1)
	payloadBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadBytes))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-123")
	req.Header.Set("X-Hub-Signature-256", "sha256=test-signature")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "processed", response["status"])
	assert.Equal(t, "success", response["conclusion"])

	// Verify CIStatus was created
	var ciStatus models.CIStatus
	err := db.Where("check_suite_id = ? AND pr_id = ?", "12345", pr.ID).First(&ciStatus).Error
	require.NoError(t, err)
	assert.Equal(t, "completed", ciStatus.Status)
	assert.NotNil(t, ciStatus.Conclusion)
	assert.Equal(t, "success", *ciStatus.Conclusion)
}

func TestCheckSuite_HandleCheckSuite_NoConclusion(t *testing.T) {
	db := setupTestDBForCheckSuite(t)
	logger := zaptest.NewLogger(t)
	router := setupTestRouterForCheckSuite(db, logger)

	// Create test PR
	pr := &models.PullRequest{
		Repo:   "test/owner",
		Number: 1,
		Status: "open",
	}
	db.Create(pr)

	payload := createTestCheckSuitePayload("completed", nil, 1)
	payloadBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(payloadBytes))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-123")
	req.Header.Set("X-Hub-Signature-256", "sha256=test-signature")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "no_conclusion", response["status"])
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}
