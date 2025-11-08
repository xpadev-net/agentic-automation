package integration

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	tu "agentic-automation/tests/integration/testutils"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupDBForUS4 creates in-memory SQLite database with required tables for US4 auto-merge tests
func setupDBForUS4(t *testing.T) *gorm.DB {
	// Use :memory: (not shared) to avoid conflicts when tests run in parallel
	dsn := ":memory:"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// Restrict to single connection to ensure all queries use the same :memory: database
	// Without this, GORM's connection pool can create multiple connections, each getting
	// a fresh empty database instance, causing "no such table" errors.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // Critical: prevents multiple connections to :memory:

	// issues table
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

	// pull_requests table
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

	// agent_runs table
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

	// ci_status table
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
			UNIQUE(pr_id, check_suite_id)
		);
		CREATE INDEX IF NOT EXISTS idx_ci_status_pr_id ON ci_status(pr_id);
		CREATE INDEX IF NOT EXISTS idx_ci_status_pr_status ON ci_status(pr_id, status);
	`).Error)

	// review_feedback table
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS review_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER,
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

	return db
}

// mockGitHubClient implements services.GitHubChecks interface for testing
type mockGitHubClient struct {
	headSHA        string
	mergeable      *bool
	mergeableState *string
	checkRuns      []*github.CheckRun
	mergeCalled    bool
	mergeError     error
	isMerged       bool
	prsForCommit   []*github.PullRequest
}

// GetPullRequest returns a mock PR with the configured head SHA, mergeable, and mergeableState
func (m *mockGitHubClient) GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*github.PullRequest, error) {
	sha := m.headSHA
	if sha == "" {
		sha = "abc123def"
	}
	mergeable := true
	if m.mergeable != nil {
		mergeable = *m.mergeable
	}
	mergeableState := "clean"
	if m.mergeableState != nil {
		mergeableState = *m.mergeableState
	}
	return &github.PullRequest{
		Head: &github.PullRequestBranch{
			SHA: &sha,
		},
		Mergeable:      &mergeable,
		MergeableState: &mergeableState,
	}, nil
}

// ListCheckRunsForCheckSuite returns the configured check runs or a default success run
func (m *mockGitHubClient) ListCheckRunsForCheckSuite(ctx context.Context, owner, repo string, checkSuiteID int64) ([]*github.CheckRun, error) {
	if m.checkRuns != nil {
		return m.checkRuns, nil
	}
	// default: one success run
	status := "completed"
	conclusion := "success"
	return []*github.CheckRun{
		{
			Status:     &status,
			Conclusion: &conclusion,
		},
	}, nil
}

// newCheckRun creates a new CheckRun with the given status and conclusion
func newCheckRun(status, conclusion string) *github.CheckRun {
	s := status
	c := conclusion
	return &github.CheckRun{
		Status:     &s,
		Conclusion: &c,
	}
}

// verifyingAutoMergeService implements services.AutoMergeService interface for testing
type verifyingAutoMergeService struct {
	Called       bool
	CallCount    int
	LastOwner    string
	LastRepo     string
	LastPRNumber int
	ReturnResult *services.AutoMergeResult
	ReturnError  error
}

// AttemptAutoMerge records the call and returns the configured result
func (v *verifyingAutoMergeService) AttemptAutoMerge(ctx context.Context, owner, repo string, prNumber int) (*services.AutoMergeResult, error) {
	v.Called = true
	v.CallCount++
	v.LastOwner = owner
	v.LastRepo = repo
	v.LastPRNumber = prNumber

	if v.ReturnError != nil {
		return nil, v.ReturnError
	}

	if v.ReturnResult != nil {
		return v.ReturnResult, nil
	}

	// Default: return success
	return &services.AutoMergeResult{
		Merged:  true,
		Message: "merged",
	}, nil
}

// createPRWithIssue creates a test Issue and associated PullRequest in the database
// If issueNumber is 0, a unique number will be generated based on current timestamp
func createPRWithIssue(t *testing.T, db *gorm.DB, repo, branch string, mergeable bool, issueNumber int) (*models.Issue, *models.PullRequest) {
	body := "Test body"
	if issueNumber == 0 {
		// Generate unique issue number based on timestamp to avoid conflicts
		issueNumber = int(time.Now().UnixNano() % 1000000)
	}
	issue := &models.Issue{
		Repo:          repo,
		Number:        issueNumber,
		GitHubIssueID: uint64(999000 + issueNumber),
		Title:         "Test Issue",
		Body:          &body,
		Labels:        "[]",
		State:         "open",
	}
	require.NoError(t, db.Create(issue).Error)

	issueID := issue.ID
	pr := &models.PullRequest{
		Repo:       repo,
		Number:     issueNumber, // PRのNumberはIssueのNumberと同じ（PRはIssueの一種なので）
		IssueID:    &issueID,
		Branch:     branch,
		BaseBranch: "main",
		Status:     "open",
		Mergeable:  &mergeable,
	}
	require.NoError(t, db.Create(pr).Error)

	return issue, pr
}

// createCISuccess creates an aggregated CIStatus with success conclusion
func createCISuccess(t *testing.T, db *gorm.DB, prID int, checkSuiteID int64, headSHA string) *models.CIStatus {
	now := time.Now()
	conclusion := "success"
	ciStatus := &models.CIStatus{
		PRID:         prID,
		CheckSuiteID: strconv.FormatInt(checkSuiteID, 10),
		Name:         "aggregated",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}

	ciRepo := repositories.NewCIStatusRepositoryWithDB(db)
	require.NoError(t, ciRepo.CreateOrUpdate(ciStatus))
	return ciStatus
}

// createCodexApproval creates a ReviewFeedback record with approval_detected=true
func createCodexApproval(t *testing.T, db *gorm.DB, prID int, commentID int64) *models.ReviewFeedback {
	content := "Codex Review: Didn't find any major issues."
	feedback := &models.ReviewFeedback{
		PRID:             prID,
		Source:           "Codex",
		Content:          &content,
		Status:           "completed",
		ApprovalDetected: true,
		GitHubCommentID:  &commentID,
	}

	rfRepo := repositories.NewReviewFeedbackRepositoryWithDB(db)
	require.NoError(t, rfRepo.Create(feedback))
	return feedback
}

// dbCIProviderForTest implements services.CIStatusProvider backed by DB aggregation rows (test version)
type dbCIProviderForTest struct {
	prRepo *repositories.PullRequestRepository
	ciRepo *repositories.CIStatusRepository
	logger *zap.Logger
}

func (p *dbCIProviderForTest) GetAggregatedState(ctx context.Context, owner, repo string, prNumber int) (services.CIState, error) {
	repoFull := owner + "/" + repo
	pr, err := p.prRepo.FindByRepoAndNumber(repoFull, prNumber)
	if err != nil || pr == nil {
		return services.CIStateUnknown, err
	}
	statuses, err := p.ciRepo.FindByPRID(pr.ID)
	if err != nil {
		return services.CIStateUnknown, err
	}
	var chosen *models.CIStatus
	for i := range statuses {
		s := statuses[i]
		if s.Name == "aggregated" {
			chosen = &s
			break
		}
	}
	if chosen == nil {
		// fallback aggregation: failed > pending > success > unknown
		hasFailed := false
		hasPending := false
		hasAny := len(statuses) > 0
		for i := range statuses {
			s := statuses[i]
			if s.Conclusion != nil {
				if *s.Conclusion == "failure" || *s.Conclusion == "cancelled" {
					hasFailed = true
				}
			} else if s.Status == "in_progress" || s.Status == "queued" {
				hasPending = true
			}
		}
		switch {
		case hasFailed:
			return services.CIStateFailed, nil
		case hasPending:
			return services.CIStatePending, nil
		case hasAny:
			return services.CIStateSuccess, nil
		default:
			return services.CIStateUnknown, nil
		}
	}
	if chosen.Conclusion != nil {
		switch *chosen.Conclusion {
		case "success":
			return services.CIStateSuccess, nil
		case "failure", "cancelled":
			return services.CIStateFailed, nil
		}
	}
	if chosen.Status == "in_progress" || chosen.Status == "queued" {
		return services.CIStatePending, nil
	}
	return services.CIStateUnknown, nil
}

// dbCodexCheckerForTest implements services.CodexApprovalChecker backed by ReviewFeedback (test version)
type dbCodexCheckerForTest struct {
	prRepo *repositories.PullRequestRepository
	rfRepo *repositories.ReviewFeedbackRepository
	logger *zap.Logger
}

func (c *dbCodexCheckerForTest) IsApproved(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	repoFull := owner + "/" + repo
	pr, err := c.prRepo.FindByRepoAndNumber(repoFull, prNumber)
	if err != nil || pr == nil {
		return false, err
	}
	fbs, err := c.rfRepo.FindByApprovalDetected(pr.ID, true)
	if err != nil {
		return false, err
	}
	return len(fbs) > 0, nil
}

// sendCheckSuiteWebhook sends a check_suite webhook to the router
func sendCheckSuiteWebhook(t *testing.T, router *gin.Engine, secret, deliveryID string, action string, conclusion *string, checkSuiteID int64, headBranch, headSHA string, prNumber int, repoFullName string) *httptest.ResponseRecorder {
	payload := handlers.CheckSuitePayload{
		Action: action,
		CheckSuite: handlers.CheckSuite{
			ID:         checkSuiteID,
			Status:     "completed",
			Conclusion: conclusion,
			HeadBranch: headBranch,
			HeadSHA:    headSHA,
			PullRequests: []handlers.CheckSuitePullRequest{
				{Number: prNumber},
			},
		},
		Repository: handlers.CheckSuiteRepository{
			FullName: repoFullName,
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature(secret, body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// sendStatusWebhook sends a status webhook to the router
func sendStatusWebhook(t *testing.T, router *gin.Engine, secret, deliveryID string, state, sha, context, repoFullName, branch string) *httptest.ResponseRecorder {
	payload := map[string]any{
		"state":      state,
		"sha":        sha,
		"context":    context,
		"repository": map[string]any{"full_name": repoFullName},
		"branches":   []map[string]any{{"name": branch}},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/webhooks/status", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature(secret, body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// sendPRReviewCommentWebhook sends a pull_request_review_comment webhook to the router
func sendPRReviewCommentWebhook(t *testing.T, router *gin.Engine, secret, deliveryID string, action string, commentBody, commentUser string, commentID int, prNumber int, repoFullName string, userID int64) *httptest.ResponseRecorder {
	payload := handlers.PullRequestReviewCommentPayload{
		Action: action,
		Comment: handlers.PullRequestReviewCommentComment{
			ID:        commentID,
			Body:      commentBody,
			User:      handlers.User{Login: commentUser, ID: userID},
			CreatedAt: time.Now().Format(time.RFC3339),
		},
		PullRequest: handlers.PullRequestReviewCommentPullRequest{
			Number: prNumber,
		},
		Repository: handlers.PullRequestReviewCommentRepository{
			FullName: repoFullName,
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review_comment")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature(secret, body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// sendIssueCommentWebhook sends an issue_comment webhook to the router
// issueNumber should be the same as prNumber (PRs are issues in GitHub)
func sendIssueCommentWebhook(t *testing.T, router *gin.Engine, secret, deliveryID string, action string, commentBody, commentUser string, commentID int, issueNumber int, repoFullName string, userID int64, issueState string) *httptest.ResponseRecorder {
	repoParts := strings.Split(repoFullName, "/")
	require.Len(t, repoParts, 2, "repoFullName must be in format 'owner/repo'")

	payload := handlers.IssueCommentPayload{
		Action: action,
		Issue: handlers.IssueCommentIssue{
			ID:     issueNumber, // GitHub issue ID (using number as ID for simplicity in tests)
			Number: issueNumber, // Issue number (same as PR number)
			Title:  "Test Issue",
			Body:   nil,
			State:  issueState,
			Labels: []handlers.Label{},
			User:   handlers.User{Login: "test-user", ID: 1},
		},
		Comment: handlers.IssueCommentComment{
			ID:        commentID,
			Body:      commentBody,
			User:      handlers.User{Login: commentUser, ID: userID},
			CreatedAt: time.Now().Format(time.RFC3339),
		},
		Repository: handlers.IssueCommentRepository{
			FullName: repoFullName,
			Owner:    handlers.User{Login: repoParts[0], ID: 1},
			Name:     repoParts[1],
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewBuffer(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", tu.ComputeGitHubSignature(secret, body))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// assertAutoMergeCalled verifies that the auto-merge service was called with the expected parameters
func assertAutoMergeCalled(t *testing.T, service *verifyingAutoMergeService, owner, repo string, prNumber int) {
	assert.True(t, service.Called, "AutoMergeService.AttemptAutoMerge should have been called")
	assert.GreaterOrEqual(t, service.CallCount, 1, "AutoMergeService.AttemptAutoMerge should have been called at least once")
	assert.Equal(t, owner, service.LastOwner, "AutoMergeService should have been called with correct owner")
	assert.Equal(t, repo, service.LastRepo, "AutoMergeService should have been called with correct repo")
	assert.Equal(t, prNumber, service.LastPRNumber, "AutoMergeService should have been called with correct PR number")
}

// assertMergeConditionResult verifies that the merge condition result matches the expected values
func assertMergeConditionResult(t *testing.T, result services.MergeConditionResult, expectedMergeable bool, expectedCIState services.CIState, expectedCodexApproved bool, expectedConflict services.MergeConflictStatus) {
	assert.Equal(t, expectedMergeable, result.Mergeable, "Mergeable should match expected value")
	assert.Equal(t, expectedCIState, result.CIState, "CIState should match expected value")
	assert.Equal(t, expectedCodexApproved, result.CodexApproved, "CodexApproved should match expected value")
	assert.Equal(t, expectedConflict, result.Conflict, "Conflict status should match expected value")
}

// setupRouterForUS4 constructs a router with webhook handlers for US4 auto-merge tests
func setupRouterForUS4(
	logger *zap.Logger,
	prRepo *repositories.PullRequestRepository,
	ciRepo *repositories.CIStatusRepository,
	rfRepo *repositories.ReviewFeedbackRepository,
	mockGH *mockGitHubClient,
	autoMergeService services.AutoMergeService,
	mergeChecker services.MergeConditionChecker,
	githubClient *clients.Client,
) (*gin.Engine, *clients.GitHubClient) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())

	issueAutoMergeService := autoMergeService
	statusAutoMergeService := autoMergeService
	if statusAutoMergeService == nil {
		statusAutoMergeService = &verifyingAutoMergeService{}
	}

	// Status webhook handler
	stDeps := handlers.StatusDeps{
		Logger:           logger,
		PullRequestRepo:  prRepo,
		CIStatusRepo:     ciRepo,
		MergeChecker:     mergeChecker,
		AutoMergeService: statusAutoMergeService,
	}
	r.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) { handlers.HandleStatusWithDeps(c, stDeps) },
	)

	// Check suite webhook handler
	csDeps := handlers.CheckSuiteDeps{
		Logger:                logger,
		PullRequestRepository: prRepo,
		CIStatusRepository:    ciRepo,
	}
	// Setup appGitHubClient for issue_comment handler
	// Create a GitHubAppClient for testing (using test mode)
	os.Setenv("GITHUB_APP_TEST_MODE", "1")
	os.Setenv("GITHUB_APP_ID", "1")
	os.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")
	appGitHubClient, err := clients.NewGitHubAppClient(logger)
	if err != nil {
		// If GitHubAppClient creation fails, log warning but continue
		// (some tests may not need it)
		logger.Warn("Failed to create GitHubAppClient for testing", zap.Error(err))
	} else {
		handlers.SetAppGitHubClient(appGitHubClient)
	}

	r.POST("/webhooks/github",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		func(c *gin.Context) {
			event := c.GetHeader("X-GitHub-Event")
			switch event {
			case "check_suite":
				handlers.HandleCheckSuiteWithDeps(c, csDeps)
			case "pull_request_review_comment":
				prrcDeps := handlers.PullRequestReviewCommentDeps{
					Logger:                   logger,
					GitHubClient:             githubClient,
					PullRequestRepository:    prRepo,
					ReviewFeedbackRepository: rfRepo,
					MergeConditionChecker:    mergeChecker,
					AutoMergeService:         issueAutoMergeService,
				}
				handlers.HandlePullRequestReviewCommentWithDeps(c, prrcDeps)
			case "issue_comment":
				// KubernetesClient is required for issue_comment handler
				k8sClient, k8sErr := clients.NewKubernetesClient(logger)
				if k8sErr != nil {
					// Use a stub client if initialization fails (for testing)
					k8sClient = clients.NewKubernetesClientWithClientset(logger)
				}
				icDeps := handlers.IssueCommentDeps{
					Logger:                    logger,
					GitHubClient:              githubClient,
					KubernetesClient:          k8sClient,
					AuthorizationService:      &tu.StubAuthorization{Allow: true},
					IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{}},
					AgentTypeDetectorService:  services.NewAgentTypeDetectorService(logger),
					StateMachine:              services.NewAgentRunStateMachine(repositories.NewAgentRunRepository(config.GetDB()), logger),
					GitHubNotificationService: &tu.StubGitHubNotification{},
					AutoMergeService:          issueAutoMergeService, // Inject mock AutoMergeService for testing
				}
				handlers.HandleIssueCommentWithDeps(c, icDeps)
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown event type"})
			}
		},
	)

	return r, appGitHubClient
}

// Test_ApproveAndCISuccess_TriggersAutoMerge tests the auto-merge flow when
// Codex approval and CI success conditions are met via status webhook.
func Test_ApproveAndCISuccess_TriggersAutoMerge(t *testing.T) {
	// Setup environment
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")
	defer os.Unsetenv("GITHUB_WEBHOOK_SECRET")

	logger := zaptest.NewLogger(t)
	config.SetLoggerForTesting(logger)

	db := setupDBForUS4(t)
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		config.ResetDBForTesting()
	}()
	config.SetDBForTesting(db)

	// Create test data
	repo := "test-org/test-repo"
	branch := "feature/issue-123"
	headSHA := "abc123def"
	_, pr := createPRWithIssue(t, db, repo, branch, true, 123)

	// Create CI success status
	checkSuiteID := int64(1001)
	createCISuccess(t, db, pr.ID, checkSuiteID, headSHA)

	// Create Codex approval (pre-existing approval for status webhook test)
	commentID := int64(888)
	createCodexApproval(t, db, pr.ID, commentID)

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepositoryWithDB(db)
	rfRepo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	// Setup mock GitHub client for MergeConflictDetector
	mergeableTrue := true
	cleanState := "clean"
	mockGH := &mockGitHubClient{
		headSHA:        headSHA,
		mergeable:      &mergeableTrue,
		mergeableState: &cleanState,
	}

	// Create a mock *github.Client wrapped in clients.Client
	// Use a custom HTTP transport that returns mock PR data
	mockTransport := &mockPRTransport{
		headSHA:        headSHA,
		mergeable:      true,
		mergeableState: "clean",
	}
	mockHTTPClient := &http.Client{Transport: mockTransport}
	mockGitHubRawClient := github.NewClient(mockHTTPClient)
	githubClient := clients.NewFromGitHub(mockGitHubRawClient, logger)

	// Setup services
	ciProvider := &dbCIProviderForTest{prRepo: prRepo, ciRepo: ciRepo, logger: logger}
	codexChecker := &dbCodexCheckerForTest{prRepo: prRepo, rfRepo: rfRepo, logger: logger}
	conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
	mergeChecker := services.NewMergeConditionChecker(ciProvider, codexChecker, conflictDetector, logger)

	// Setup verifying auto-merge service
	autoMergeService := &verifyingAutoMergeService{}

	// Setup router
	router, appClient := setupRouterForUS4(logger, prRepo, ciRepo, rfRepo, mockGH, autoMergeService, mergeChecker, githubClient)
	t.Cleanup(func() {
		handlers.SetAppGitHubClient(appClient)
	})

	// Send check_suite webhook (for CI aggregation)
	checkSuiteDeliveryID := "delivery-check-suite-1"
	successStr := "success"
	conclusion := &successStr
	w1 := sendCheckSuiteWebhook(t, router, "test-secret", checkSuiteDeliveryID, "completed", conclusion, checkSuiteID, branch, headSHA, pr.Number, repo)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Send status webhook (should trigger auto-merge)
	statusDeliveryID := "delivery-status-1"
	w2 := sendStatusWebhook(t, router, "test-secret", statusDeliveryID, "success", headSHA, "ci/test", repo, branch)
	assert.Equal(t, http.StatusOK, w2.Code)

	// Verify auto-merge was called
	owner := "test-org"
	repoName := "test-repo"
	assertAutoMergeCalled(t, autoMergeService, owner, repoName, pr.Number)
}

// mockPRTransport is an HTTP transport that returns mock PR data for GitHub API calls
type mockPRTransport struct {
	headSHA        string
	mergeable      bool
	mergeableState string
	checkRuns      []*github.CheckRun
}

func (m *mockPRTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Check if this is a PR GET request
	if strings.Contains(req.URL.Path, "/pulls/") && req.Method == "GET" {
		// Return mock PR data
		mergeableStr := "false"
		if m.mergeable {
			mergeableStr = "true"
		}
		prJSON := `{
			"number": 456,
			"head": {
				"sha": "` + m.headSHA + `"
			},
			"mergeable": ` + mergeableStr + `,
			"mergeable_state": "` + m.mergeableState + `"
		}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(prJSON)),
			Header:     make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	}

	// Check if this is a ListCheckRunsForRef request
	if strings.Contains(req.URL.Path, "/commits/") && strings.Contains(req.URL.Path, "/check-runs") && req.Method == "GET" {
		// Return mock check runs data
		// Default: one success check run
		status := "completed"
		conclusion := "success"
		checkRunsJSON := `{
			"total_count": 1,
			"check_runs": [
				{
					"id": 1,
					"name": "test-check",
					"status": "` + status + `",
					"conclusion": "` + conclusion + `"
				}
			]
		}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(checkRunsJSON)),
			Header:     make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	}

	// Default: return empty response
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("{}")),
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

// Test_CodexApprovalComment_TriggersAutoMerge tests the auto-merge flow when
// Codex approval comment is detected via issue_comment webhook (PR-related issue comment).
// Phase 0: Codex approve検出はissue_commentイベントで行うように変更されました。
func Test_CodexApprovalComment_TriggersAutoMerge(t *testing.T) {
	// Setup environment
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")
	defer os.Unsetenv("GITHUB_WEBHOOK_SECRET")

	logger := zaptest.NewLogger(t)
	config.SetLoggerForTesting(logger)

	db := setupDBForUS4(t)
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		config.ResetDBForTesting()
	}()
	config.SetDBForTesting(db)

	// Create test data
	repo := "test-org/test-repo"
	branch := "feature/issue-123"
	headSHA := "abc123def"
	_, pr := createPRWithIssue(t, db, repo, branch, true, 123)

	// Create CI success status
	checkSuiteID := int64(1001)
	createCISuccess(t, db, pr.ID, checkSuiteID, headSHA)

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepositoryWithDB(db)
	rfRepo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	// Setup mock GitHub client for MergeConflictDetector
	mergeableTrue := true
	cleanState := "clean"
	mockGH := &mockGitHubClient{
		headSHA:        headSHA,
		mergeable:      &mergeableTrue,
		mergeableState: &cleanState,
	}

	// Create a mock *github.Client wrapped in clients.Client
	mockTransport := &mockPRTransport{
		headSHA:        headSHA,
		mergeable:      true,
		mergeableState: "clean",
	}
	mockHTTPClient := &http.Client{Transport: mockTransport}
	mockGitHubRawClient := github.NewClient(mockHTTPClient)
	githubClient := clients.NewFromGitHub(mockGitHubRawClient, logger)

	// Setup services
	ciProvider := &dbCIProviderForTest{prRepo: prRepo, ciRepo: ciRepo, logger: logger}
	codexChecker := &dbCodexCheckerForTest{prRepo: prRepo, rfRepo: rfRepo, logger: logger}
	conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
	mergeChecker := services.NewMergeConditionChecker(ciProvider, codexChecker, conflictDetector, logger)

	// Setup verifying auto-merge service
	autoMergeService := &verifyingAutoMergeService{}

	// Setup router
	router, appClient := setupRouterForUS4(logger, prRepo, ciRepo, rfRepo, mockGH, autoMergeService, mergeChecker, githubClient)
	t.Cleanup(func() {
		handlers.SetAppGitHubClient(appClient)
	})

	// Send issue_comment webhook with Codex approval comment
	// Note: PRs are issues in GitHub, so issue number should be the same as PR number
	commentDeliveryID := "delivery-issue-comment-1"
	commentBody := "Codex Review: Didn't find any major issues."
	commentUser := "chatgpt-codex-connector[bot]"
	commentID := 999
	commentUserID := int64(199175422) // Default Codex bot user ID
	// PRのNumberと同じIssue Numberを使用（PRはIssueの一種なので）
	w := sendIssueCommentWebhook(t, router, "test-secret", commentDeliveryID, "created", commentBody, commentUser, commentID, pr.Number, repo, commentUserID, "open")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify auto-merge was called
	owner := "test-org"
	repoName := "test-repo"
	assertAutoMergeCalled(t, autoMergeService, owner, repoName, pr.Number)
}

// Test_CodexApprovalComment_SkipsAutoMergeWithoutAppClient ensures that when the GitHub App client
// is unavailable (and no AutoMergeService is injected), the handler skips auto-merge gracefully.
func Test_CodexApprovalComment_SkipsAutoMergeWithoutAppClient(t *testing.T) {
	// Setup environment
	os.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")
	defer os.Unsetenv("GITHUB_WEBHOOK_SECRET")

	logger := zaptest.NewLogger(t)
	config.SetLoggerForTesting(logger)

	db := setupDBForUS4(t)
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		config.ResetDBForTesting()
	}()
	config.SetDBForTesting(db)

	// Create test data
	repo := "test-org/test-repo"
	branch := "feature/issue-123"
	headSHA := "abc123def"
	_, pr := createPRWithIssue(t, db, repo, branch, true, 123)

	// Create CI success status
	checkSuiteID := int64(1001)
	createCISuccess(t, db, pr.ID, checkSuiteID, headSHA)

	// Setup repositories
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepositoryWithDB(db)
	rfRepo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	// Setup mock GitHub client for MergeConflictDetector
	mergeableTrue := true
	cleanState := "clean"
	mockGH := &mockGitHubClient{
		headSHA:        headSHA,
		mergeable:      &mergeableTrue,
		mergeableState: &cleanState,
	}

	// Create a mock *github.Client wrapped in clients.Client
	mockTransport := &mockPRTransport{
		headSHA:        headSHA,
		mergeable:      true,
		mergeableState: "clean",
	}
	mockHTTPClient := &http.Client{Transport: mockTransport}
	mockGitHubRawClient := github.NewClient(mockHTTPClient)
	githubClient := clients.NewFromGitHub(mockGitHubRawClient, logger)

	// Setup services
	ciProvider := &dbCIProviderForTest{prRepo: prRepo, ciRepo: ciRepo, logger: logger}
	codexChecker := &dbCodexCheckerForTest{prRepo: prRepo, rfRepo: rfRepo, logger: logger}
	conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
	mergeChecker := services.NewMergeConditionChecker(ciProvider, codexChecker, conflictDetector, logger)

	// Setup router without injecting an AutoMergeService and force appGitHubClient to nil
	router, appClient := setupRouterForUS4(logger, prRepo, ciRepo, rfRepo, mockGH, nil, mergeChecker, githubClient)
	defer handlers.SetAppGitHubClient(appClient)
	handlers.SetAppGitHubClient(nil)

	// Send issue_comment webhook with Codex approval comment
	commentDeliveryID := "delivery-issue-comment-no-app"
	commentBody := "Codex Review: Didn't find any major issues."
	commentUser := "chatgpt-codex-connector[bot]"
	commentID := 1001
	commentUserID := int64(199175422)
	w := sendIssueCommentWebhook(t, router, "test-secret", commentDeliveryID, "created", commentBody, commentUser, commentID, pr.Number, repo, commentUserID, "open")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mergeable_auto_merge_skipped", resp["status"])
	assert.Equal(t, "app_github_client_unavailable", resp["reason"])
}
