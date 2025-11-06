package webhooks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// transportDepsOpen returns dependencies with one open issue for the blocked_by endpoint.
type transportDepsOpen struct{}

func (t *transportDepsOpen) RoundTrip(r *http.Request) (*http.Response, error) {
	path := r.URL.Path
	var body string
	switch {
	case strings.Contains(path, "/dependencies/blocked_by"):
		body = `[{"number":11,"title":"dep","state":"open","repository":{"full_name":"a/b"}}]`
	case strings.HasSuffix(path, "/comments"):
		body = `[]`
	default:
		body = `{}`
	}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

func setupIssueCommentDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Minimal tables used by handler/idempotency
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
    `).Error)
	require.NoError(t, db.Exec(`
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
        );
    `).Error)
	require.NoError(t, db.Exec(`
        CREATE TABLE IF NOT EXISTS blocker_graph_edges (
            task_id INTEGER,
            depends_on_task_id INTEGER
        );
    `).Error)

	return db
}

type allowAuth struct{}

func (a *allowAuth) CheckPermission(_ context.Context, _, _ string, _ string) (bool, error) {
	return true, nil
}

type simpleIssueCtx struct{}

func (s *simpleIssueCtx) CollectIssueContext(_ context.Context, _, _ string, issueNumber int) (*services.IssueContext, error) {
	return &services.IssueContext{Number: issueNumber}, nil
}
func (s *simpleIssueCtx) FormatPrompt(ic *services.IssueContext) string { return "p" }

func TestIssueComment_BlockedDependencies_PreventsStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupIssueCommentDB(t)
	logger := zap.NewNop()
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(logger)
	os.Setenv("GITHUB_WEBHOOK_SECRET", "sec")

	// Seed idempotency middleware expectations (middleware will create rows on request)

	// Prepare deps
	deps := handlers.IssueCommentDeps{
		Logger:                   logger,
		AuthorizationService:     &allowAuth{},
		IssueContextService:      &simpleIssueCtx{},
		AgentTypeDetectorService: services.NewAgentTypeDetectorService(logger),
	}

	// GitHub client with custom transport for dependencies endpoint
	httpClient := &http.Client{Transport: &transportDepsOpen{}}
	gh := github.NewClient(httpClient)
	deps.GitHubClient = clients.NewFromGitHub(gh, logger)

	// Router
	router := gin.New()
	router.POST("/webhooks/issue_comment",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleIssueCommentWithDeps(c, deps) },
	)

	// Construct payload
	payload := map[string]any{
		"action": "created",
		"issue": map[string]any{
			"id": 1001, "number": 10, "title": "t", "state": "open",
		},
		"comment":    map[string]any{"id": 9001, "body": "/run-agent", "user": map[string]any{"login": "u"}, "created_at": "2025-01-01T00:00:00Z"},
		"repository": map[string]any{"full_name": "a/b", "owner": map[string]any{"login": "o"}, "name": "b"},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/webhooks/issue_comment", bytes.NewBuffer(b))
	req.Header.Set("X-GitHub-Delivery", "dep-block-1")
	// HMAC
	mac := middleware.GenerateHMACForTesting(b, "sec")
	req.Header.Set("X-Hub-Signature-256", mac)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assert: AgentRun remains queued (not transitioned to started)
	var run models.AgentRun
	require.NoError(t, db.Where("idempotency_key = ?", "dep-block-1").First(&run).Error)
	assert.Equal(t, "queued", run.State)
}
