package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	tu "agentic-automation/tests/integration/testutils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Minimal in-memory DB setup (mirrors agent_report_test)
func setupDB(t *testing.T) *gorm.DB {
	dsn := "file::memory:?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	require.NoError(t, err)

	// Create minimal tables (subset required for this flow)
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

	return db
}

func setupRouterForTest(deps handlers.IssueCommentDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/webhooks/github",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleIssueCommentWithDeps(c, deps) },
	)
	return r
}

func Test_IssueComment_HappyPath_CreatesK8sJob(t *testing.T) {
	// Env & logger
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	os.Setenv("AGENT_RUNNER_IMAGE", "example/agent-runner:latest")
	os.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
	os.Setenv("OPERATOR_SERVICE_PORT", "3000")
	os.Setenv("AGENT_RUNNER_TIMEOUT_MINUTES", "30")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		os.Unsetenv("AGENT_RUNNER_IMAGE")
		os.Unsetenv("OPERATOR_SERVICE_NAME")
		os.Unsetenv("OPERATOR_SERVICE_PORT")
		os.Unsetenv("AGENT_RUNNER_TIMEOUT_MINUTES")
	})

	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)

	// DB
	db := setupDB(t)
	config.SetDBForTesting(db)

	// Seed Issue to be upserted by middleware or repository lookup
	issue := &models.Issue{Repo: "test-org/test-repo", Number: 123, GitHubIssueID: 555, Title: "Test", State: "open"}
	// Allow middleware to upsert if not found; no need to pre-insert
	_ = issue

	// Build fakes/stubs
	k8s := clients.NewKubernetesClientWithClientset(logger)
	auth := &tu.StubAuthorization{Allow: true}
	ic := &tu.StubIssueContext{Context: &services.IssueContext{
		Number: 123,
		Title:  "Test",
		Body:   "Body",
		Labels: []string{"agent:claude-code"},
	}}
	ghNotice := &tu.StubGitHubNotification{}

	deps := handlers.IssueCommentDeps{
		Logger:                    logger,
		KubernetesClient:          k8s,
		TriggerService:            services.NewTriggerDetectionService(logger),
		AuthorizationService:      auth,
		IssueContextService:       ic,
		AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
		StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(db), logger),
		GitHubNotificationService: ghNotice,
	}

	router := setupRouterForTest(deps)

	// Build webhook payload
	payload := map[string]interface{}{
		"action": "created",
		"issue": map[string]interface{}{
			"id":     555,
			"number": 123,
			"title":  "Test",
			"state":  "open",
		},
		"comment": map[string]interface{}{
			"id":         999,
			"body":       "/run-agent please",
			"user":       map[string]interface{}{"login": "alice"},
			"created_at": time.Now().UTC().Format(time.RFC3339),
		},
		"repository": map[string]interface{}{
			"full_name": "test-org/test-repo",
			"owner":     map[string]interface{}{"login": "test-org"},
			"name":      "test-repo",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	deliveryID := uuid.NewString()
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var resp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	// For issue-triggered plan creation, status is "plan_creation_started"
	assert.Equal(t, "plan_creation_started", resp["status"])

	// Verify plan creation AgentRun state
	arRepo := repositories.NewAgentRunRepository(db)
	planAgentRunID := int(resp["plan_agent_run_id"].(float64))
	run, err := arRepo.GetByID(planAgentRunID)
	require.NoError(t, err)
	assert.Equal(t, "started", run.State)

	// Verify K8s Job created with expected name
	expectedJobName := fmt.Sprintf("agent-runner-%d", run.ID)
	job, err := k8s.GetJob(req.Context(), expectedJobName)
	require.NoError(t, err)
	assert.Equal(t, expectedJobName, job.Name)
	assert.Equal(t, "default", job.Namespace)

	// Verify labels
	require.NotNil(t, job.Labels)
	assert.Equal(t, fmt.Sprintf("%d", run.ID), job.Labels["agent-run-id"])
	assert.Equal(t, "123", job.Labels["issue-id"])

	// Verify container image and args
	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	ctn := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, os.Getenv("AGENT_RUNNER_IMAGE"), ctn.Image)
	// args should include flags
	joined := strings.Join(ctn.Args, " ")
	assert.Contains(t, joined, "--issue-id=123")
	assert.Contains(t, joined, "--repo=test-org/test-repo")

	// Verify env contains required keys
	gotEnv := map[string]bool{}
	for _, e := range ctn.Env {
		gotEnv[e.Name] = true
	}
	assert.True(t, gotEnv["KUBERNETES_NAMESPACE"])
	assert.True(t, gotEnv["OPERATOR_SERVICE_NAME"])
	assert.True(t, gotEnv["OPERATOR_SERVICE_PORT"])
	assert.True(t, gotEnv["AGENT_RUN_ID"])
	assert.True(t, gotEnv["AGENT_TYPE"])

	// Verify resources are set
	res := ctn.Resources
	_, memReq := res.Requests["memory"]
	_, cpuReq := res.Requests["cpu"]
	_, memLim := res.Limits["memory"]
	_, cpuLim := res.Limits["cpu"]
	assert.True(t, memReq)
	assert.True(t, cpuReq)
	assert.True(t, memLim)
	assert.True(t, cpuLim)

	// Verify timeout applied
	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(30*60), *job.Spec.ActiveDeadlineSeconds)

	// GitHub notification called
	assert.True(t, ghNotice.Called)
}

func Test_IssueComment_NoTrigger(t *testing.T) {
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	t.Cleanup(func() { os.Unsetenv("GITHUB_WEBHOOK_SECRET") })
	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)
	db := setupDB(t)
	config.SetDBForTesting(db)

	k8s := clients.NewKubernetesClientWithClientset(logger)
	deps := handlers.IssueCommentDeps{
		Logger:                    logger,
		KubernetesClient:          k8s,
		TriggerService:            services.NewTriggerDetectionService(logger),
		AuthorizationService:      &tu.StubAuthorization{Allow: true},
		IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{Number: 123}},
		AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
		StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(db), logger),
		GitHubNotificationService: &tu.StubGitHubNotification{},
	}
	router := setupRouterForTest(deps)

	payload := map[string]interface{}{
		"action":     "created",
		"issue":      map[string]interface{}{"id": 1, "number": 123, "title": "T", "state": "open"},
		"comment":    map[string]interface{}{"id": 2, "body": "hello", "user": map[string]interface{}{"login": "alice"}, "created_at": time.Now().UTC().Format(time.RFC3339)},
		"repository": map[string]interface{}{"full_name": "o/r"},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", uuid.NewString())
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "no_trigger", resp["status"])
}

func Test_IssueComment_PermissionDenied(t *testing.T) {
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	t.Cleanup(func() { os.Unsetenv("GITHUB_WEBHOOK_SECRET") })
	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)
	db := setupDB(t)
	config.SetDBForTesting(db)

	k8s := clients.NewKubernetesClientWithClientset(logger)
	deps := handlers.IssueCommentDeps{
		Logger:                    logger,
		KubernetesClient:          k8s,
		TriggerService:            services.NewTriggerDetectionService(logger),
		AuthorizationService:      &tu.StubAuthorization{Allow: false},
		IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{Number: 123}},
		AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
		StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(db), logger),
		GitHubNotificationService: &tu.StubGitHubNotification{},
	}
	router := setupRouterForTest(deps)

	payload := map[string]interface{}{
		"action":     "created",
		"issue":      map[string]interface{}{"id": 1, "number": 123, "title": "T", "state": "open"},
		"comment":    map[string]interface{}{"id": 2, "body": "/run-agent", "user": map[string]interface{}{"login": "alice"}, "created_at": time.Now().UTC().Format(time.RFC3339)},
		"repository": map[string]interface{}{"full_name": "o/r"},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", uuid.NewString())
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "permission_denied", resp["status"])
}

func Test_IssueComment_ClosedIssue(t *testing.T) {
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	t.Cleanup(func() { os.Unsetenv("GITHUB_WEBHOOK_SECRET") })
	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)
	db := setupDB(t)
	config.SetDBForTesting(db)

	k8s := clients.NewKubernetesClientWithClientset(logger)
	deps := handlers.IssueCommentDeps{
		Logger:                    logger,
		KubernetesClient:          k8s,
		TriggerService:            services.NewTriggerDetectionService(logger),
		AuthorizationService:      &tu.StubAuthorization{Allow: true},
		IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{Number: 123}},
		AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
		StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(db), logger),
		GitHubNotificationService: &tu.StubGitHubNotification{},
	}
	router := setupRouterForTest(deps)

	payload := map[string]interface{}{
		"action":     "created",
		"issue":      map[string]interface{}{"id": 1, "number": 123, "title": "T", "state": "closed"},
		"comment":    map[string]interface{}{"id": 2, "body": "/run-agent", "user": map[string]interface{}{"login": "alice"}, "created_at": time.Now().UTC().Format(time.RFC3339)},
		"repository": map[string]interface{}{"full_name": "o/r"},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", uuid.NewString())
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "rejected", resp["status"])
	assert.Equal(t, "issue_not_open", resp["reason"])
}

func Test_IssueComment_K8sFailure_RollbackQueued(t *testing.T) {
	// Missing AGENT_RUNNER_IMAGE to force job creation error
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	os.Unsetenv("AGENT_RUNNER_IMAGE")
	os.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
	os.Setenv("OPERATOR_SERVICE_PORT", "3000")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		os.Unsetenv("OPERATOR_SERVICE_NAME")
		os.Unsetenv("OPERATOR_SERVICE_PORT")
	})
	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)
	db := setupDB(t)
	config.SetDBForTesting(db)

	k8s := clients.NewKubernetesClientWithClientset(logger)
	deps := handlers.IssueCommentDeps{
		Logger:                    logger,
		KubernetesClient:          k8s,
		TriggerService:            services.NewTriggerDetectionService(logger),
		AuthorizationService:      &tu.StubAuthorization{Allow: true},
		IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{Number: 123}},
		AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
		StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(db), logger),
		GitHubNotificationService: &tu.StubGitHubNotification{},
	}
	router := setupRouterForTest(deps)

	payload := map[string]interface{}{
		"action":     "created",
		"issue":      map[string]interface{}{"id": 1, "number": 123, "title": "T", "state": "open"},
		"comment":    map[string]interface{}{"id": 2, "body": "/run-agent", "user": map[string]interface{}{"login": "alice"}, "created_at": time.Now().UTC().Format(time.RFC3339)},
		"repository": map[string]interface{}{"full_name": "o/r"},
	}
	body, _ := json.Marshal(payload)
	delivery := uuid.NewString()
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Expect error handled by middleware as 200 with error body, or bubbled
	// Key assertion: AgentRun rolled back to queued
	arRepo := repositories.NewAgentRunRepository(db)
	run, err := arRepo.GetByIDempotencyKey(delivery)
	require.NoError(t, err)
	assert.Equal(t, "queued", run.State)
}

func Test_IssueComment_Idempotency_SecondIsNoop(t *testing.T) {
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	os.Setenv("AGENT_RUNNER_IMAGE", "example/agent-runner:latest")
	os.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
	os.Setenv("OPERATOR_SERVICE_PORT", "3000")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		os.Unsetenv("AGENT_RUNNER_IMAGE")
		os.Unsetenv("OPERATOR_SERVICE_NAME")
		os.Unsetenv("OPERATOR_SERVICE_PORT")
	})
	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)
	db := setupDB(t)
	config.SetDBForTesting(db)

	k8s := clients.NewKubernetesClientWithClientset(logger)
	deps := handlers.IssueCommentDeps{
		Logger:                    logger,
		KubernetesClient:          k8s,
		TriggerService:            services.NewTriggerDetectionService(logger),
		AuthorizationService:      &tu.StubAuthorization{Allow: true},
		IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{Number: 123}},
		AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
		StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(db), logger),
		GitHubNotificationService: &tu.StubGitHubNotification{},
	}
	router := setupRouterForTest(deps)

	payload := map[string]interface{}{
		"action":     "created",
		"issue":      map[string]interface{}{"id": 1, "number": 123, "title": "T", "state": "open"},
		"comment":    map[string]interface{}{"id": 2, "body": "/run-agent", "user": map[string]interface{}{"login": "alice"}, "created_at": time.Now().UTC().Format(time.RFC3339)},
		"repository": map[string]interface{}{"full_name": "o/r"},
	}
	body, _ := json.Marshal(payload)
	delivery := uuid.NewString()

	// First
	req1 := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req1.Header.Set("X-GitHub-Event", "issue_comment")
	req1.Header.Set("X-GitHub-Delivery", delivery)
	req1.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second (same delivery)
	req2 := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req2.Header.Set("X-GitHub-Event", "issue_comment")
	req2.Header.Set("X-GitHub-Delivery", delivery)
	req2.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, "already_processed", resp["status"])
}
