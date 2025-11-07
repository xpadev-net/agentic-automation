package webhooks

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Test DB setup (copied pattern from check_suite tests)
func setupTestDBForStatus(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Minimal tables for PR and CIStatus
	require.NoError(t, db.Exec(`
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
    `).Error)
	require.NoError(t, db.Exec(`
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
    `).Error)
	// Tables for idempotency middleware path
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
        )
    `).Error)
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
            created_at DATETIME NOT NULL,
            updated_at DATETIME NOT NULL
        )
    `).Error)
	return db
}

func setupTestRouterForStatus(db *gorm.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// logger & db
	logger := zap.NewNop()
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)

	// secret
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	// deps
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepository()
	deps := handlers.StatusDeps{
		PullRequestRepo: prRepo,
		CIStatusRepo:    ciRepo,
	}

	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, deps) },
	)

	return router
}

func generateHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestStatus_SignatureInvalid(t *testing.T) {
	db := setupTestDBForStatus(t)
	router := setupTestRouterForStatus(db)

	payload := map[string]any{
		"state":      "success",
		"sha":        "abc123",
		"context":    "ci/test",
		"repository": map[string]any{"full_name": "test/owner"},
	}
	b, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-GitHub-Delivery", "d1")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestStatus_Pending_DoesNotEvaluate(t *testing.T) {
	db := setupTestDBForStatus(t)
	router := setupTestRouterForStatus(db)

	// Create PR
	pr := &models.PullRequest{Repo: "test/owner", Number: 1, Status: "open", Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	payload := map[string]any{
		"state":      "pending",
		"sha":        "abc123",
		"context":    "ci/test",
		"repository": map[string]any{"full_name": "test/owner"},
		"branches":   []map[string]any{{"name": "feature/test"}},
	}
	b, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req.Header.Set("X-Hub-Signature-256", generateHMAC(b, "test-secret"))
	req.Header.Set("X-GitHub-Delivery", "d2")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

type fakeMergeChecker struct{ called bool }

func (f *fakeMergeChecker) Check(_ context.Context, _, _ string, _ int) (services.MergeConditionResult, error) {
	f.called = true
	return services.MergeConditionResult{Mergeable: false}, nil
}

func TestStatus_Success_EvaluatesAndPersists(t *testing.T) {
	db := setupTestDBForStatus(t)
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	pr := &models.PullRequest{Repo: "test/owner", Number: 1, Status: "open", Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	// deps with fake merge checker
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepository()
	fake := &fakeMergeChecker{}
	deps := handlers.StatusDeps{PullRequestRepo: prRepo, CIStatusRepo: ciRepo, MergeChecker: fake}

	router := gin.New()
	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, deps) },
	)

	payload := map[string]any{
		"state":      "success",
		"sha":        "abc123",
		"context":    "ci/test",
		"target_url": "https://ci.example/run/1",
		"repository": map[string]any{"full_name": "test/owner"},
		"branches":   []map[string]any{{"name": "feature/test"}},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req.Header.Set("X-Hub-Signature-256", generateHMAC(b, "test-secret"))
	req.Header.Set("X-GitHub-Delivery", "d3")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// CIStatus persisted
	var rows int64
	require.NoError(t, db.Model(&models.CIStatus{}).Where("pr_id = ? AND name = ?", pr.ID, "ci/test").Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
}

func TestStatus_Idempotency_SameDeliveryProcessedOnce(t *testing.T) {
	db := setupTestDBForStatus(t)
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	pr := &models.PullRequest{Repo: "test/owner", Number: 1, Status: "open", Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepository()
	deps := handlers.StatusDeps{PullRequestRepo: prRepo, CIStatusRepo: ciRepo}

	router := gin.New()
	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, deps) },
	)

	// Include an issue object to trigger idempotency middleware persistence path
	payload := map[string]any{
		"state":      "success",
		"sha":        "abc123",
		"context":    "ci/test",
		"repository": map[string]any{"full_name": "test/owner"},
		"issue":      map[string]any{"id": 123456, "number": 42, "title": "t", "state": "open"},
	}
	b, _ := json.Marshal(payload)

	req1 := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req1.Header.Set("X-Hub-Signature-256", generateHMAC(b, "test-secret"))
	req1.Header.Set("X-GitHub-Delivery", "same-delivery")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	req2 := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req2.Header.Set("X-Hub-Signature-256", generateHMAC(b, "test-secret"))
	req2.Header.Set("X-GitHub-Delivery", "same-delivery")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	// Expect middleware short-circuit
	assert.Equal(t, "already_processed", resp["status"])
}

type fakeAggregator struct{ called bool }

func (f *fakeAggregator) AggregateAndStore(ctx context.Context, owner, repo string, prID int, prNumber int, checkSuiteID int64, headSHA string) (*models.CIStatus, error) {
	return nil, nil
}
func (f *fakeAggregator) AddStatusSignal(ctx context.Context, prID int, name string, state string, targetURL string) error {
	f.called = true
	return nil
}

func TestStatus_AggregatorCalled_OnAnyState(t *testing.T) {
	db := setupTestDBForStatus(t)
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	pr := &models.PullRequest{Repo: "test/owner", Number: 1, Status: "open", Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	// deps with fake aggregator
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepository()
	agg := &fakeAggregator{}
	deps := handlers.StatusDeps{PullRequestRepo: prRepo, CIStatusRepo: ciRepo, Aggregator: agg}

	router := gin.New()
	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, deps) },
	)

	payload := map[string]any{
		"state":      "pending",
		"sha":        "abc123",
		"context":    "ci/test",
		"repository": map[string]any{"full_name": "test/owner"},
		"branches":   []map[string]any{{"name": "feature/test"}},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req.Header.Set("X-Hub-Signature-256", generateHMAC(b, "test-secret"))
	req.Header.Set("X-GitHub-Delivery", "d4")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, agg.called, "expected AddStatusSignal to be called")
}

type fakeMergeCheckerNoCall struct{ called bool }

func (f *fakeMergeCheckerNoCall) Check(_ context.Context, _, _ string, _ int) (services.MergeConditionResult, error) {
	f.called = true
	return services.MergeConditionResult{Mergeable: false}, nil
}

func TestStatus_Failure_DoesNotEvaluate(t *testing.T) {
	db := setupTestDBForStatus(t)
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	pr := &models.PullRequest{Repo: "test/owner", Number: 1, Status: "open", Branch: "feature/test"}
	require.NoError(t, db.Create(pr).Error)

	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepository()
	fake := &fakeMergeCheckerNoCall{}
	deps := handlers.StatusDeps{PullRequestRepo: prRepo, CIStatusRepo: ciRepo, MergeChecker: fake}

	router := gin.New()
	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, deps) },
	)

	payload := map[string]any{
		"state":      "failure",
		"sha":        "abc123",
		"context":    "ci/test",
		"repository": map[string]any{"full_name": "test/owner"},
		"branches":   []map[string]any{{"name": "feature/test"}},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b))
	req.Header.Set("X-Hub-Signature-256", generateHMAC(b, "test-secret"))
	req.Header.Set("X-GitHub-Delivery", "d5")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, fake.called, "merge checker should not be called on failure state")
}
