package integration

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"agentic-automation/tests/integration/testutils"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// minimal fake auto-merge service
type fakeAutoMerge struct{ called bool }

func (f *fakeAutoMerge) AttemptAutoMerge(_ context.Context, _, _ string, _ int) (*services.AutoMergeResult, error) {
	f.called = true
	return &services.AutoMergeResult{Merged: false}, nil
}

type fakeMergeCheckerInt struct{ called bool }

func (f *fakeMergeCheckerInt) Check(_ context.Context, _, _ string, _ int) (services.MergeConditionResult, error) {
	f.called = true
	return services.MergeConditionResult{Mergeable: false}, nil
}

func Test_CheckSuiteThenStatus_Success_TriggersEvaluationPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	logger := zaptest.NewLogger(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	// tables
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
        )`).Error)
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
            updated_at DATETIME
        )`).Error)

	// seed PR
	pr := &models.PullRequest{Repo: "test/owner", Number: 1, Status: "open"}
	require.NoError(t, db.Create(pr).Error)

	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepository()

	// Routers
	router := gin.New()

	// check_suite route
	csDeps := handlers.CheckSuiteDeps{Logger: logger, PullRequestRepository: prRepo, CIStatusRepository: ciRepo}
	router.POST("/webhooks/github",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleCheckSuiteWithDeps(c, csDeps) },
	)

	// status route with fake checker + auto merge
	fakeChecker := &fakeMergeCheckerInt{}
	fakeAM := &fakeAutoMerge{}
	stDeps := handlers.StatusDeps{Logger: logger, PullRequestRepo: prRepo, CIStatusRepo: ciRepo, MergeChecker: fakeChecker, AutoMergeService: fakeAM}
	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, stDeps) },
	)

	// send check_suite success
	csPayload := handlers.CheckSuitePayload{
		Action:     "completed",
		CheckSuite: handlers.CheckSuite{ID: 1001, Status: "completed", Conclusion: stringPtr("success"), HeadBranch: "feature/x", HeadSHA: "abc123", PullRequests: []handlers.CheckSuitePullRequest{{Number: 1}}},
		Repository: handlers.CheckSuiteRepository{FullName: "test/owner"},
	}
	b1, _ := json.Marshal(csPayload)
	req1 := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(b1))
	req1.Header.Set("X-GitHub-Event", "check_suite")
	req1.Header.Set("X-GitHub-Delivery", "int-1")
	req1.Header.Set("X-Hub-Signature-256", testutils.ComputeGitHubSignature("test-secret", b1))
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// send status success
	stPayload := map[string]any{"state": "success", "sha": "abc123", "context": "ci/test", "repository": map[string]any{"full_name": "test/owner"}}
	b2, _ := json.Marshal(stPayload)
	req2 := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(b2))
	req2.Header.Set("X-GitHub-Delivery", "int-2")
	req2.Header.Set("X-Hub-Signature-256", testutils.ComputeGitHubSignature("test-secret", b2))
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	// evaluation path touched
	assert.True(t, fakeChecker.called)
}

func stringPtr(s string) *string { return &s }
