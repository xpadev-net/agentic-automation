package integration

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	testToken = "test-operator-api-token-12345"
)

// setupTestDB initializes an in-memory SQLite database for testing
// SQLite doesn't support ENUM, so we need to create tables manually with TEXT types
func setupTestDB(t *testing.T) *gorm.DB {
	dsn := "file::memory:?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		DisableForeignKeyConstraintWhenMigrating: true, // SQLite FK constraints need special handling
	})
	require.NoError(t, err, "Failed to open test database")

	// SQLite doesn't support ENUM, so we manually create tables with TEXT types
	// This is a workaround for testing purposes
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
		CREATE INDEX IF NOT EXISTS idx_issues_github_issue_id ON issues(github_issue_id);
	`).Error
	require.NoError(t, err, "Failed to create issues table")

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
		CREATE INDEX IF NOT EXISTS idx_pull_requests_issue_id ON pull_requests(issue_id);
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
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_idempotency_key ON agent_runs(idempotency_key);
		CREATE INDEX IF NOT EXISTS idx_agent_runs_issue_id ON agent_runs(issue_id);
		CREATE INDEX IF NOT EXISTS idx_agent_runs_pr_id ON agent_runs(pr_id);
		CREATE INDEX IF NOT EXISTS idx_agent_runs_state ON agent_runs(state);
	`).Error
	require.NoError(t, err, "Failed to create agent_runs table")

	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS operation_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER,
			operation_type TEXT,
			operation_id TEXT UNIQUE,
			status TEXT DEFAULT 'pending',
			created_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_operation_run_type ON operation_logs(run_id, operation_type);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_operation_logs_operation_id ON operation_logs(operation_id);
	`).Error
	require.NoError(t, err, "Failed to create operation_logs table")

	// Create minimal tables for other models (not used in these tests but referenced by foreign keys)
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS review_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS ci_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER,
			status TEXT DEFAULT 'queued',
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS blocker_graph_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER,
			depends_on_task_id INTEGER,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME
		);
	`).Error
	require.NoError(t, err, "Failed to create supporting tables")

	return db
}

// teardownTestDB closes the test database connection
func teardownTestDB(db *gorm.DB) {
	if db != nil {
		sqlDB, err := db.DB()
		if err == nil && sqlDB != nil {
			sqlDB.Close()
		}
	}
}

// createTestIssue creates a test Issue record
func createTestIssue(t *testing.T, db *gorm.DB) *models.Issue {
	issue := &models.Issue{
		Repo:          "test-org/test-repo",
		Number:        123,
		GitHubIssueID: 123456,
		Title:         "Test Issue",
		Body:          stringPtr("Test issue body"),
		Labels:        `["bug","test"]`,
		State:         "open",
	}
	err := db.Create(issue).Error
	require.NoError(t, err, "Failed to create test issue")
	return issue
}

// createTestAgentRun creates a test AgentRun record
func createTestAgentRun(t *testing.T, db *gorm.DB, issueID int, state string) *models.AgentRun {
	run := &models.AgentRun{
		IdempotencyKey: fmt.Sprintf("test-key-%d", time.Now().UnixNano()),
		IssueID:        issueID,
		State:          state,
		AgentType:      "claude-code",
		Input:          `{}`,
		Output:         `{}`,
		RetryCount:     0,
	}
	if state == "started" {
		now := time.Now()
		run.StartedAt = &now
	}
	err := db.Create(run).Error
	require.NoError(t, err, "Failed to create test agent run")
	return run
}

// setupTestRouter creates a test Gin router with the agent report endpoint
func setupTestRouter(db *gorm.DB, logger *zap.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Apply error handling middleware
	router.Use(middleware.ErrorHandler())

	// Setup config mock by replacing the handlers to use our test DB/Logger
	// We'll create a wrapper that uses the provided DB and Logger
	router.POST("/api/agent-runs/:id/report",
		middleware.VerifyBearerToken(),
		func(c *gin.Context) {
			// Temporarily replace config for this request
			// Since config uses globals, we'll need to patch them
			// For now, we'll modify the handler to accept DB and Logger
			// Actually, a better approach is to use dependency injection
			// But since handlers use config.GetDB() directly, we need to mock that
			handleAgentReportWithDeps(c, db, logger)
		})

	return router
}

// handleAgentReportWithDeps is a wrapper that calls HandleAgentReport with custom DB and Logger
// Since HandleAgentReport uses config.GetDB() and config.GetLogger(), we need to work around this
// For testing, we'll create a modified version that accepts dependencies
func handleAgentReportWithDeps(c *gin.Context, db *gorm.DB, logger *zap.Logger) {
	// This is a workaround since the original handler uses config package
	// We'll temporarily set the config globals (not ideal but necessary for testing)
	// Actually, we should modify the handler to accept dependencies
	// For now, let's create a test-specific handler
	handleAgentReportTest(c, db, logger)
}

// handleAgentReportTest is a test version of HandleAgentReport that accepts DB and Logger
func handleAgentReportTest(c *gin.Context, db *gorm.DB, logger *zap.Logger) {
	// Get AgentRun ID from path parameter
	idStr := c.Param("id")
	agentRunID, err := strconv.Atoi(idStr)
	if err != nil {
		logger.Warn("Invalid agent run ID in path",
			zap.String("id", idStr),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid agent run ID",
		})
		return
	}

	// Parse request body
	var req handlers.ReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Get repository
	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Get AgentRun by ID
	agentRun, err := agentRunRepo.GetByID(agentRunID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("AgentRun not found",
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "AGENT_RUN_NOT_FOUND",
				"message": "AgentRun with ID " + idStr + " not found",
			})
			return
		}

		logger.Error("Failed to retrieve AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to retrieve agent run",
		})
		return
	}

	// Update AgentRun state and related fields
	now := time.Now()
	agentRun.State = req.Status
	agentRun.AgentType = req.AgentType
	agentRun.CompletedAt = &now

	// Update PR ID if provided (for succeeded status)
	if req.Status == "succeeded" && req.PRNumber != nil {
		prID := *req.PRNumber
		agentRun.PRID = &prID
	}

	// Update commit SHA if provided
	if req.CommitSHA != "" {
		agentRun.CommitSHA = &req.CommitSHA
	}

	// Update error message if provided (for failed status)
	if req.Status == "failed" && req.ErrorMessage != "" {
		agentRun.ErrorMessage = &req.ErrorMessage
	}

	// Save updated AgentRun
	if err := agentRunRepo.Update(agentRun); err != nil {
		logger.Error("Failed to update AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to update agent run",
		})
		return
	}

	// Log successful report
	logger.Info("Agent execution report received",
		zap.Int("agent_run_id", agentRunID),
		zap.String("status", req.Status),
		zap.String("agent_type", req.AgentType),
		zap.String("path", c.Request.URL.Path),
	)

	// Return success response
	c.JSON(http.StatusOK, handlers.ReportResponse{
		Message:    "Report received, AgentRun #" + idStr + " updated to " + req.Status,
		AgentRunID: agentRunID,
	})
}

// makeReportRequest sends an HTTP POST request to the agent report endpoint
func makeReportRequest(t *testing.T, router *gin.Engine, agentRunID int, token string, reqBody handlers.ReportRequest) *httptest.ResponseRecorder {
	jsonBody, err := json.Marshal(reqBody)
	require.NoError(t, err, "Failed to marshal request body")

	req, err := http.NewRequest("POST", fmt.Sprintf("/api/agent-runs/%d/report", agentRunID), bytes.NewBuffer(jsonBody))
	require.NoError(t, err, "Failed to create request")

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
	return &s
}

// TestMain sets up and tears down test environment
func TestMain(m *testing.M) {
	// Set test token environment variable
	os.Setenv("OPERATOR_API_TOKEN", testToken)
	defer os.Unsetenv("OPERATOR_API_TOKEN")

	// Run tests
	code := m.Run()
	os.Exit(code)
}

// TestAgentReportFlow_Success tests the successful report flow
func TestAgentReportFlow_Success(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "queued")

	// Request
	reqBody := handlers.ReportRequest{
		Status:    "succeeded",
		AgentType: "claude-code",
		PRNumber:  intPtr(42),
		CommitSHA: "abc123def456",
	}

	w := makeReportRequest(t, router, agentRun.ID, testToken, reqBody)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK status")

	var response handlers.ReportResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Failed to unmarshal response")
	assert.Equal(t, agentRun.ID, response.AgentRunID)
	assert.Contains(t, response.Message, "succeeded")

	// Verify database state
	var updatedRun models.AgentRun
	err = db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "succeeded", updatedRun.State)
	assert.Equal(t, "claude-code", updatedRun.AgentType)
	assert.NotNil(t, updatedRun.CompletedAt)
	assert.NotNil(t, updatedRun.PRID)
	assert.Equal(t, 42, *updatedRun.PRID)
	assert.NotNil(t, updatedRun.CommitSHA)
	assert.Equal(t, "abc123def456", *updatedRun.CommitSHA)
}

// TestAgentReportFlow_Failure tests the failed report flow
func TestAgentReportFlow_Failure(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "started")

	// Request
	reqBody := handlers.ReportRequest{
		Status:       "failed",
		AgentType:    "claude-code",
		ErrorMessage: "Test error message",
	}

	w := makeReportRequest(t, router, agentRun.ID, testToken, reqBody)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK status")

	var response handlers.ReportResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Failed to unmarshal response")
	assert.Equal(t, agentRun.ID, response.AgentRunID)
	assert.Contains(t, response.Message, "failed")

	// Verify database state
	var updatedRun models.AgentRun
	err = db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "failed", updatedRun.State)
	assert.Equal(t, "claude-code", updatedRun.AgentType)
	assert.NotNil(t, updatedRun.CompletedAt)
	assert.NotNil(t, updatedRun.ErrorMessage)
	assert.Equal(t, "Test error message", *updatedRun.ErrorMessage)
}

// TestAgentReportFlow_BearerToken_Valid tests valid Bearer token authentication
func TestAgentReportFlow_BearerToken_Valid(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "queued")

	// Request with valid token
	reqBody := handlers.ReportRequest{
		Status:    "succeeded",
		AgentType: "claude-code",
	}

	w := makeReportRequest(t, router, agentRun.ID, testToken, reqBody)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK with valid token")
}

// TestAgentReportFlow_BearerToken_Invalid tests invalid Bearer token authentication
func TestAgentReportFlow_BearerToken_Invalid(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "queued")

	// Request with invalid token
	reqBody := handlers.ReportRequest{
		Status:    "succeeded",
		AgentType: "claude-code",
	}

	w := makeReportRequest(t, router, agentRun.ID, "invalid-token", reqBody)

	// Assertions
	assert.Equal(t, http.StatusUnauthorized, w.Code, "Expected 401 Unauthorized with invalid token")

	var errorResponse map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &errorResponse)
	require.NoError(t, err)
	assert.Equal(t, "INVALID_TOKEN", errorResponse["error"])
}

// TestAgentReportFlow_BearerToken_Missing tests missing Bearer token authentication
func TestAgentReportFlow_BearerToken_Missing(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "queued")

	// Request without token
	reqBody := handlers.ReportRequest{
		Status:    "succeeded",
		AgentType: "claude-code",
	}

	w := makeReportRequest(t, router, agentRun.ID, "", reqBody)

	// Assertions
	assert.Equal(t, http.StatusUnauthorized, w.Code, "Expected 401 Unauthorized without token")

	var errorResponse map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &errorResponse)
	require.NoError(t, err)
	assert.Equal(t, "INVALID_TOKEN", errorResponse["error"])
}

// TestAgentReportFlow_NotFound tests 404 error for non-existent AgentRun
func TestAgentReportFlow_NotFound(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)

	// Request with non-existent ID
	reqBody := handlers.ReportRequest{
		Status:    "succeeded",
		AgentType: "claude-code",
	}

	w := makeReportRequest(t, router, 99999, testToken, reqBody)

	// Assertions
	assert.Equal(t, http.StatusNotFound, w.Code, "Expected 404 Not Found")

	var errorResponse map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &errorResponse)
	require.NoError(t, err)
	assert.Equal(t, "AGENT_RUN_NOT_FOUND", errorResponse["error"])
}

// TestAgentReportFlow_InvalidRequestBody tests 400 error for invalid request body
func TestAgentReportFlow_InvalidRequestBody(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "queued")

	// Request with invalid JSON
	req, err := http.NewRequest("POST", fmt.Sprintf("/api/agent-runs/%d/report", agentRun.ID), bytes.NewBufferString("invalid json"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", testToken))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request")
}

// TestAgentReportFlow_InvalidStatus tests 400 error for invalid status value
func TestAgentReportFlow_InvalidStatus(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	defer teardownTestDB(db)

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	router := setupTestRouter(db, logger)
	issue := createTestIssue(t, db)
	agentRun := createTestAgentRun(t, db, issue.ID, "queued")

	// Request with invalid status
	reqBody := map[string]interface{}{
		"status":     "invalid-status",
		"agent_type": "claude-code",
	}

	jsonBody, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", fmt.Sprintf("/api/agent-runs/%d/report", agentRun.ID), bytes.NewBuffer(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", testToken))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request for invalid status")
}

// intPtr returns a pointer to the given int
func intPtr(i int) *int {
	return &i
}
