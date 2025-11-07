package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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

// Test constants for owner/repo and issue numbers
const (
	testOwner    = "o"
	testRepo     = "r"
	testRepoFull = "o/r"
	issueANumber = 1
	issueBNumber = 2
)

// Minimal in-memory DB setup including blocker_graph_edges
func setupDBForUS5(t *testing.T) *gorm.DB {
	dsn := "file::memory:?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)

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

	err = db.Exec(`
        CREATE TABLE IF NOT EXISTS blocker_graph_edges (
            task_id INTEGER NOT NULL,
            depends_on_task_id INTEGER NOT NULL,
            created_at DATETIME,
            PRIMARY KEY(task_id, depends_on_task_id)
        );
        CREATE INDEX IF NOT EXISTS idx_blocker_graph_edges_depends_on ON blocker_graph_edges(depends_on_task_id);
    `).Error
	require.NoError(t, err)

	return db
}

// Router for issues webhook with injected BlockedTaskResolver only
func setupIssuesRouterForTest(deps handlers.IssuesDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/webhooks/github",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleIssuesWithDeps(c, deps) },
	)
	return r
}

// testBlockedTaskResolver implements handlers.BlockedTaskResolver and
// delegates to services.BlockedTaskResolver + TriggerJobs with stubs
type testBlockedTaskResolver struct {
	svcResolver services.BlockedTaskResolver
	issuesRepo  *repositories.IssueRepository
	agentsRepo  repositories.AgentRunRepository
	jobSvc      services.KubernetesJobService
}

func (tbr *testBlockedTaskResolver) ResolveAndMaybeTrigger(ctx context.Context, owner, repo string, issueNumber int) error {
	// Map to DB issue ID
	issue, err := tbr.issuesRepo.FindByRepoAndNumber(owner+"/"+repo, issueNumber)
	if err != nil {
		return err
	}
	// Use dummy GitHub client for context service
	gh := tu.NewDummyGitHubClient()
	logger := config.GetLogger()
	issueCtxSvc := services.NewIssueContextService(gh, logger)
	// State machine
	sm := services.NewAgentRunStateMachine(tbr.agentsRepo, logger)
	// Trigger
	return services.TriggerJobsForUnblockedTasks(
		ctx,
		tbr.svcResolver,
		tbr.issuesRepo,
		tbr.agentsRepo,
		tbr.jobSvc,
		sm,
		issueCtxSvc,
		gh,
		owner, repo,
		int64(issue.ID),
	)
}

func Test_US5_Unblock_AutoTrigger(t *testing.T) {
	// Secrets
	os.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
	t.Cleanup(func() { os.Unsetenv("GITHUB_WEBHOOK_SECRET") })

	// Logger & DB
	logger, _ := zap.NewDevelopment()
	config.SetLoggerForTesting(logger)
	db := setupDBForUS5(t)
	config.SetDBForTesting(db)

	// Seed issues: A(closed), B(open)
	issueRepo := repositories.NewIssueRepository()
	err := issueRepo.Create(&models.Issue{Repo: testRepoFull, Number: issueANumber, Title: "A", State: "closed"})
	require.NoError(t, err)
	err = issueRepo.Create(&models.Issue{Repo: testRepoFull, Number: issueBNumber, Title: "B", State: "open"})
	require.NoError(t, err)

	// Fetch IDs to create dependency: B depends on A
	a, _ := issueRepo.FindByRepoAndNumber(testRepoFull, issueANumber)
	b, _ := issueRepo.FindByRepoAndNumber(testRepoFull, issueBNumber)
	edgesRepo := repositories.NewBlockerGraphRepository()
	err = edgesRepo.CreateEdge(b.ID, a.ID)
	require.NoError(t, err)

	// Build services
	blockedSvc := services.NewBlockedTaskResolver(issueRepo, edgesRepo, repositories.NewAgentRunRepository(db))
	stubJob := &tu.StubKubernetesJobService{}
	testResolver := &testBlockedTaskResolver{
		svcResolver: blockedSvc,
		issuesRepo:  issueRepo,
		agentsRepo:  repositories.NewAgentRunRepository(db),
		jobSvc:      stubJob,
	}

	// Setup GitHub client and authorization service for testing
	ghClient := tu.NewDummyGitHubClient()
	authService := &tu.StubAuthorization{Allow: true} // Allow all requests in test

	deps := handlers.IssuesDeps{
		Logger:               logger,
		GitHubClient:         ghClient,
		AuthorizationService: authService,
		BlockedTaskResolver:  testResolver,
	}
	router := setupIssuesRouterForTest(deps)

	// Build webhook payload (issues: closed for A)
	payload := map[string]any{
		"action": "closed",
		"issue": map[string]any{
			"number": issueANumber,
			"state":  "closed",
		},
		"repository": map[string]any{
			"full_name": testRepoFull,
		},
		"sender": map[string]any{
			"login": "alice",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	delivery := uuid.NewString()
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature("secret123", body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Assert K8s Job requested once via stub and AgentRun created for B
	require.True(t, stubJob.CreateJobCalled)
	require.Len(t, stubJob.CreatedJobs, 1)

	// AgentRun for B should be started
	arRepo := repositories.NewAgentRunRepository(db)
	runs, err := arRepo.GetByIssueID(b.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "started", runs[0].State)
}
