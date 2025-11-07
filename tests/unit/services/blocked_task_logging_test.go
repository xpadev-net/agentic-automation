package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"testing"

	"agentic-automation/internal/clients"
	appcfg "agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	svc "agentic-automation/internal/services"

	gh "github.com/google/go-github/v76/github"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeResolver returns a fixed list of issues
type fakeResolver struct{ issues []models.Issue }

func (f *fakeResolver) FindUnblockedTasks(ctx context.Context, eventIssueID int64) ([]models.Issue, error) {
	return f.issues, nil
}

// fakeJobService implements KubernetesJobService and returns a dummy job
type fakeJobService struct{}

func (f *fakeJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job-1"}}, nil
}

func (f *fakeJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *svc.AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job-1"}}, nil
}

// minimal ObjectMeta shim to avoid importing k8s meta in test package usage
// We alias to services.ObjectMeta which delegates to k8s meta types where needed.

func setupSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	// SQLite は enum 型をサポートしないため、最低限のカラムで手動定義する
	// issues
	if err := db.Exec(`
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
);`).Error; err != nil {
		t.Fatalf("failed to create issues table: %v", err)
	}
	// agent_runs
	if err := db.Exec(`
CREATE TABLE IF NOT EXISTS agent_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  idempotency_key TEXT UNIQUE,
  issue_id INTEGER,
  pr_id INTEGER,
  state TEXT DEFAULT 'queued',
  agent_type TEXT,
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
);`).Error; err != nil {
		t.Fatalf("failed to create agent_runs table: %v", err)
	}
	return db
}

func newObserverLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return zap.New(core), logs
}

// Test that end-to-end happy path emits key logs without external dependencies
func TestTriggerJobsForUnblockedTasks_EmitsLogs(t *testing.T) {
	// Logger (observer)
	logger, logs := newObserverLogger()
	appcfg.SetLoggerForTesting(logger)
	t.Cleanup(func() { appcfg.ResetLoggerForTesting() })

	// SQLite test DB
	db := setupSQLite(t)
	appcfg.SetDBForTesting(db)
	t.Cleanup(func() { appcfg.ResetDBForTesting() })

	// Pre-create issue in DB
	iss := &models.Issue{Repo: "o/r", Number: 1, Title: "t", State: "open", Labels: "[]"}
	if err := db.Create(iss).Error; err != nil {
		t.Fatalf("failed to seed issue: %v", err)
	}

	// Resolver returns this issue as unblocked
	resolver := &fakeResolver{issues: []models.Issue{*iss}}

	// Real repositories on sqlite
	issueRepo := repositories.NewIssueRepository()
	agentRepo := repositories.NewAgentRunRepository(db)

	// Minimal GitHub API stub server for Issue and Comments
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 1,
			"title":  "t",
			"body":   "",
			"labels": []map[string]any{},
		})
	})
	mux.HandleFunc("/repos/o/r/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Create a github.Client pointing to our server
	httpClient := server.Client()
	githubClient := gh.NewClient(httpClient)
	baseURL := server.URL + "/"
	parsed, _ := neturl.Parse(baseURL)
	githubClient.BaseURL = parsed
	// Wrap into our clients.Client and create IssueContextService
	ghWrap := clients.NewFromGitHub(githubClient, logger)
	issueCtxSvc := svc.NewIssueContextService(ghWrap, logger)

	// Job service and state machine
	jobSvc := &fakeJobService{}
	stateMachine := svc.NewAgentRunStateMachine(agentRepo, logger)

	// Execute
	ctx := context.Background()
	if err := svc.TriggerJobsForUnblockedTasks(ctx, resolver, issueRepo, agentRepo, jobSvc, stateMachine, issueCtxSvc, ghWrap, "o", "r", int64(999)); err != nil {
		t.Fatalf("TriggerJobsForUnblockedTasks returned error: %v", err)
	}

	// Assertions: presence of key events
	wantKeys := []string{
		"blocked_task.resume_evaluation_started",
		"blocked_task.issue_context_collected",
		"blocked_task.agent_run_upserted",
		"blocked_task.run_transition_started",
		"blocked_task.job_created",
		"blocked_task.resume_evaluation_completed",
	}
	for _, k := range wantKeys {
		if logs.FilterMessage(k).Len() == 0 {
			t.Fatalf("expected log %q not found", k)
		}
	}
}
