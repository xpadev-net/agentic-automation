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
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// testConfig holds test configuration and dependencies
type testConfig struct {
	db     *gorm.DB
	router *gin.Engine
	token  string
}

const testAPIToken = "test-api-token-12345"

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "Failed to create test database")

	// Create AgentRun table (SQLite doesn't support ENUM, so we use TEXT)
	err = db.Exec(`
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
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`).Error
	require.NoError(t, err, "Failed to create agent_runs table")

	// Create Issues table (minimal columns used by handler/tests)
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

	// Create PullRequests table (minimal columns used by handler/tests)
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

	// Create ReviewFeedback table (required for US3 T103)
	err = db.Exec(`
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
		)
	`).Error
	require.NoError(t, err, "Failed to create review_feedback table")

	return db
}

// setupTestRouter creates a Gin router with Bearer token middleware
func setupTestRouter(db *gorm.DB, logger *zap.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Set up database and logger in config (for handler access)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)

	// Apply Bearer token middleware
	router.POST("/api/agent-runs/:id/report",
		middleware.VerifyBearerToken(),
		handlers.HandleAgentReport)

	return router
}

// setupTestEnv sets up test environment variables
func setupTestEnv(t *testing.T) {
	os.Setenv("OPERATOR_API_TOKEN", testAPIToken)
	t.Cleanup(func() {
		os.Unsetenv("OPERATOR_API_TOKEN")
	})
}

// createTestAgentRun creates a test AgentRun in the database
func createTestAgentRun(t *testing.T, db *gorm.DB, state string) *models.AgentRun {
	agentRun := &models.AgentRun{
		IdempotencyKey: "test-key-" + time.Now().Format(time.RFC3339Nano),
		IssueID:        1,
		State:          state,
		AgentType:      "claude-code",
		Input:          datatypes.JSON([]byte(`{"test": "data"}`)),
	}

	err := db.Create(agentRun).Error
	require.NoError(t, err, "Failed to create test AgentRun")
	return agentRun
}

// createTestIssue inserts a minimal Issue row used by handler for repo lookup
func createTestIssue(t *testing.T, db *gorm.DB, id int, repo string, number int) {
	// Insert deterministic issue id by specifying id explicitly if possible
	err := db.Exec(`INSERT INTO issues (id, repo, number, state, created_at, updated_at) VALUES (?, ?, ?, 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, repo, number).Error
	require.NoError(t, err, "Failed to create test Issue")
}

// makeRequest creates and executes an HTTP request
func makeRequest(t *testing.T, router *gin.Engine, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		require.NoError(t, err)
	}

	req, err := http.NewRequest(method, path, bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

// assertJSONResponse asserts the response status and JSON structure
func assertJSONResponse(t *testing.T, recorder *httptest.ResponseRecorder, expectedStatus int, expectedFields map[string]interface{}) {
	assert.Equal(t, expectedStatus, recorder.Code, "Unexpected status code")

	var response map[string]interface{}
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err, "Failed to parse response JSON")

	for key, expectedValue := range expectedFields {
		actualValue, exists := response[key]
		assert.True(t, exists, "Response missing field: %s", key)
		assert.Equal(t, expectedValue, actualValue, "Field %s mismatch", key)
	}
}

// Test Helper: Setup test environment
func setupTest(t *testing.T) (*gorm.DB, *gin.Engine) {
	setupTestEnv(t)

	db := setupTestDB(t)
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})

	// Clean up database before each test
	db.Exec("DELETE FROM agent_runs")

	// Initialize logger for testing
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	// Setup router with middleware
	router := setupTestRouter(db, logger)

	return db, router
}

// ============================================================================
// Bearer Token Validation Tests
// ============================================================================

func TestHandleAgentReport_ValidBearerToken(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	// This test validates token handling only; avoid success path requirements
	reqBody := map[string]interface{}{
		"status":        "failed",
		"agent_type":    "claude-code",
		"error_message": "just testing token",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.ReportResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, agentRun.ID, response.AgentRunID)
	assert.Contains(t, response.Message, "failed")
}

func TestHandleAgentReport_MissingAuthHeader(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONResponse(t, w, http.StatusUnauthorized, map[string]interface{}{
		"error":   "INVALID_TOKEN",
		"message": "Invalid or missing Bearer token",
	})
}

func TestHandleAgentReport_InvalidBearerPrefix(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
	}

	// Create request without "Bearer " prefix
	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Authorization", testAPIToken) // Missing "Bearer " prefix
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONResponse(t, w, http.StatusUnauthorized, map[string]interface{}{
		"error":   "INVALID_TOKEN",
		"message": "Invalid or missing Bearer token",
	})
}

func TestHandleAgentReport_EmptyBearerToken(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
	}

	// Create request with empty token
	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Authorization", "Bearer ") // Empty token
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONResponse(t, w, http.StatusUnauthorized, map[string]interface{}{
		"error":   "INVALID_TOKEN",
		"message": "Invalid or missing Bearer token",
	})
}

func TestHandleAgentReport_InvalidBearerToken(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, "invalid-token")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONResponse(t, w, http.StatusUnauthorized, map[string]interface{}{
		"error":   "INVALID_TOKEN",
		"message": "Invalid or missing Bearer token",
	})
}

// ============================================================================
// Handler Success Tests
// ============================================================================

func TestHandleAgentReport_Success_Succeeded(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	// Seed Issue required for PR URL generation
	createTestIssue(t, db, agentRun.IssueID, "owner/repo", 1)

	prNumber := 123
	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  prNumber,
		"commit_sha": "abc123def456",
		"branch":     "feature/test",
		"logs":       "Test logs",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.ReportResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, agentRun.ID, response.AgentRunID)
	assert.Contains(t, response.Message, "succeeded")

	// Verify database update
	var updatedRun models.AgentRun
	err = db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "succeeded", updatedRun.State)
	require.NotNil(t, updatedRun.PRID)
	// Verify PR foreign key points to a pull_requests row with expected number
	type prRow struct {
		ID     int
		Repo   string
		Number int
	}
	var row prRow
	err = db.Raw("SELECT id, repo, number FROM pull_requests WHERE id = ?", *updatedRun.PRID).Scan(&row).Error
	require.NoError(t, err)
	assert.Equal(t, prNumber, row.Number)
	assert.NotNil(t, updatedRun.CommitSHA)
	assert.Equal(t, "abc123def456", *updatedRun.CommitSHA)
	assert.NotNil(t, updatedRun.CompletedAt)
}

func TestHandleAgentReport_Success_Failed(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	errorMsg := "Test error message"
	reqBody := map[string]interface{}{
		"status":        "failed",
		"agent_type":    "cursor-agent",
		"error_message": errorMsg,
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.ReportResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, agentRun.ID, response.AgentRunID)
	assert.Contains(t, response.Message, "failed")

	// Verify database update
	var updatedRun models.AgentRun
	err = db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "failed", updatedRun.State)
	assert.Equal(t, "cursor-agent", updatedRun.AgentType)
	assert.NotNil(t, updatedRun.ErrorMessage)
	assert.Equal(t, errorMsg, *updatedRun.ErrorMessage)
	assert.NotNil(t, updatedRun.CompletedAt)
}

func TestHandleAgentReport_WithCommitSHA(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	// Seed Issue for success path
	createTestIssue(t, db, agentRun.IssueID, "owner/repo", 1)

	commitSHA := "def456ghi789"
	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"commit_sha": commitSHA,
		"pr_number":  111,
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify database update
	var updatedRun models.AgentRun
	err := db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	assert.NotNil(t, updatedRun.CommitSHA)
	assert.Equal(t, commitSHA, *updatedRun.CommitSHA)
}

// ============================================================================
// Handler Error Tests
// ============================================================================

func TestHandleAgentReport_NotFound(t *testing.T) {
	_, router := setupTest(t)

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/99999/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assertJSONResponse(t, w, http.StatusNotFound, map[string]interface{}{
		"error":   "AGENT_RUN_NOT_FOUND",
		"message": "AgentRun with ID 99999 not found",
	})
}

func TestHandleAgentReport_InvalidID(t *testing.T) {
	_, router := setupTest(t)

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/invalid-id/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONResponse(t, w, http.StatusBadRequest, map[string]interface{}{
		"error":   "INVALID_REQUEST",
		"message": "Invalid agent run ID",
	})
}

func TestHandleAgentReport_InvalidRequestBody(t *testing.T) {
	_, router := setupTest(t)

	// Missing required fields
	reqBody := map[string]interface{}{
		"status": "succeeded",
		// agent_type is missing
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/1/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONResponse(t, w, http.StatusBadRequest, map[string]interface{}{
		"error": "INVALID_REQUEST",
	})
	// Check that error message contains reference to AgentType field
	assert.Contains(t, w.Body.String(), "AgentType", "Error message should mention AgentType field")
}

func TestHandleAgentReport_InvalidStatusEnum(t *testing.T) {
	_, router := setupTest(t)

	reqBody := map[string]interface{}{
		"status":     "invalid_status",
		"agent_type": "claude-code",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/1/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONResponse(t, w, http.StatusBadRequest, map[string]interface{}{
		"error": "INVALID_REQUEST",
	})
	// Check that error message contains reference to Status field
	assert.Contains(t, w.Body.String(), "Status", "Error message should mention Status field")
}

func TestHandleAgentReport_InvalidAgentTypeEnum(t *testing.T) {
	_, router := setupTest(t)

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "invalid_agent_type",
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/1/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONResponse(t, w, http.StatusBadRequest, map[string]interface{}{
		"error": "INVALID_REQUEST",
	})
	// Check that error message contains reference to AgentType field
	assert.Contains(t, w.Body.String(), "AgentType", "Error message should mention AgentType field")
}

// ============================================================================
// Request Validation Detail Tests
// ============================================================================

func TestHandleAgentReport_PRNumberOnlyOnSuccess(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	// Seed Issue for success path
	createTestIssue(t, db, agentRun.IssueID, "owner/repo", 1)

	prNumber := 456
	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"pr_number":  prNumber,
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify PR link is set
	var updatedRun models.AgentRun
	err := db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	require.NotNil(t, updatedRun.PRID)
	// Check that linked PullRequest has the provided number
	type prRow2 struct{ Number int }
	var row2 prRow2
	err = db.Raw("SELECT number FROM pull_requests WHERE id = ?", *updatedRun.PRID).Scan(&row2).Error
	require.NoError(t, err)
	assert.Equal(t, prNumber, row2.Number)

	// Test that PR number is not set for failed status
	agentRun2 := createTestAgentRun(t, db, "started")
	reqBody2 := map[string]interface{}{
		"status":     "failed",
		"agent_type": "claude-code",
		"pr_number":  789, // Should be ignored
	}

	w2 := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun2.ID)+"/report", reqBody2, testAPIToken)
	assert.Equal(t, http.StatusOK, w2.Code)

	var updatedRun2 models.AgentRun
	err = db.First(&updatedRun2, agentRun2.ID).Error
	require.NoError(t, err)
	assert.Nil(t, updatedRun2.PRID, "PR number should not be set for failed status")
}

func TestHandleAgentReport_ErrorMessageOnlyOnFailed(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	errorMsg := "Something went wrong"
	reqBody := map[string]interface{}{
		"status":        "failed",
		"agent_type":    "claude-code",
		"error_message": errorMsg,
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify error message is set
	var updatedRun models.AgentRun
	err := db.First(&updatedRun, agentRun.ID).Error
	require.NoError(t, err)
	assert.NotNil(t, updatedRun.ErrorMessage)
	assert.Equal(t, errorMsg, *updatedRun.ErrorMessage)

	// Test that error message is not set for succeeded status
	agentRun2 := createTestAgentRun(t, db, "started")
	// Seed Issue and include pr_number to satisfy success path requirements
	createTestIssue(t, db, agentRun2.IssueID, "owner/repo", 1)
	reqBody2 := map[string]interface{}{
		"status":        "succeeded",
		"agent_type":    "claude-code",
		"error_message": "This should be ignored",
		"pr_number":     333,
	}

	w2 := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun2.ID)+"/report", reqBody2, testAPIToken)
	assert.Equal(t, http.StatusOK, w2.Code)

	var updatedRun2 models.AgentRun
	err = db.First(&updatedRun2, agentRun2.ID).Error
	require.NoError(t, err)
	assert.Nil(t, updatedRun2.ErrorMessage, "Error message should not be set for succeeded status")
}

func TestHandleAgentReport_OptionalFields(t *testing.T) {
	db, router := setupTest(t)
	agentRun := createTestAgentRun(t, db, "started")

	// Seed Issue for success path
	createTestIssue(t, db, agentRun.IssueID, "owner/repo", 1)

	reqBody := map[string]interface{}{
		"status":     "succeeded",
		"agent_type": "claude-code",
		"branch":     "feature/optional-fields",
		"logs":       "Optional log data",
		"pr_number":  222,
	}

	w := makeRequest(t, router, "POST", "/api/agent-runs/"+strconv.Itoa(agentRun.ID)+"/report", reqBody, testAPIToken)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify request was processed successfully (optional fields don't cause errors)
	var response handlers.ReportResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, agentRun.ID, response.AgentRunID)
}
