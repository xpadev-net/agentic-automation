package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupPRReviewDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS pull_requests (
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
		)`,
		`CREATE TABLE IF NOT EXISTS review_feedback (
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
		)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error)
	}
	return db
}

func createPRFixture(t *testing.T, db *gorm.DB) *models.PullRequest {
	pr := &models.PullRequest{Repo: "owner/repo", Number: 10, Branch: "feature/x"}
	require.NoError(t, db.Create(pr).Error)
	return pr
}

func invokePRReview(t *testing.T, payload *PullRequestReviewPayload, deps PullRequestReviewDeps) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(b))
	req.Header.Set("X-GitHub-Delivery", "delivery-test")
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	ctx.Set("webhook_payload", b)
	HandlePullRequestReviewWithDeps(ctx, deps)
	return w
}

// test GitHub server responding for PR and PR comments
func newTestGitHubClient(t *testing.T, prBody *string, reviewID int64, comments []string) *clients.Client {
	mux := http.NewServeMux()

	// PR endpoint
	mux.HandleFunc("/repos/owner/repo/pulls/10", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		pr := &github.PullRequest{Body: prBody}
		_ = json.NewEncoder(w).Encode(pr)
	})

	// PR comments endpoint
	mux.HandleFunc("/repos/owner/repo/pulls/10/comments", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		var list []*github.PullRequestComment
		for _, c := range comments {
			cpy := c
			list = append(list, &github.PullRequestComment{Body: &cpy, PullRequestReviewID: &reviewID})
		}
		_ = json.NewEncoder(w).Encode(list)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// go-github requires BaseURL to end with slash
	base, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	ghc := github.NewClient(srv.Client())
	ghc.BaseURL = base
	return clients.NewFromGitHub(ghc, zap.NewNop())
}

func TestHandlePullRequestReview_PrependsPRBodyWhenPresent(t *testing.T) {
	db := setupPRReviewDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	pr := createPRFixture(t, db)

	body := "This is PR body about implementation details."
	reviewID := int64(1234)
	gh := newTestGitHubClient(t, &body, reviewID, []string{"Inline comment A"})

	deps := PullRequestReviewDeps{
		Logger:                   zap.NewNop(),
		GitHubClient:             gh,
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
	}

	payload := &PullRequestReviewPayload{
		Action:      models.PullRequestReviewActionSubmitted,
		Review:      PullRequestReviewReview{ID: reviewID, Body: "Looks good", User: User{Login: "reviewer"}},
		PullRequest: PullRequestReviewPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewRepository{FullName: pr.Repo},
	}

	w := invokePRReview(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	// verify ReviewFeedback content
	var feedback models.ReviewFeedback
	require.NoError(t, db.Last(&feedback).Error)
	require.NotNil(t, feedback.Content)
	content := *feedback.Content
	require.True(t, strings.HasPrefix(content, "--- Pull Request Body ---"))
	require.Contains(t, content, "This is PR body")
	// separator should remain
	require.Contains(t, content, "\n\n--- Review Comment ---\n\n")
	// review body still included
	require.Contains(t, content, "Looks good")
}

func TestHandlePullRequestReview_SkipsPRBodyWhenEmptyOrNil(t *testing.T) {
	cases := []struct {
		name    string
		prBody  *string
		comment string
	}{
		{
			name:    "nil body",
			prBody:  nil,
			comment: "Inline comment B",
		},
		{
			name:    "empty string body",
			prBody:  func() *string { s := "   "; return &s }(),
			comment: "Inline comment C",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			db := setupPRReviewDB(t)
			config.SetDBForTesting(db)
			config.SetLoggerForTesting(zap.NewNop())
			t.Cleanup(func() {
				config.ResetDBForTesting()
				config.ResetLoggerForTesting()
			})

			pr := createPRFixture(t, db)

			reviewID := int64(777)
			gh := newTestGitHubClient(t, tc.prBody, reviewID, []string{tc.comment})

			deps := PullRequestReviewDeps{
				Logger:                   zap.NewNop(),
				GitHubClient:             gh,
				PullRequestRepository:    repositories.NewPullRequestRepository(db),
				ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
			}

			payload := &PullRequestReviewPayload{
				Action:      models.PullRequestReviewActionSubmitted,
				Review:      PullRequestReviewReview{ID: reviewID, Body: "Please check"},
				PullRequest: PullRequestReviewPullRequest{Number: pr.Number},
				Repository:  PullRequestReviewRepository{FullName: pr.Repo},
			}

			w := invokePRReview(t, payload, deps)
			require.Equal(t, http.StatusOK, w.Code)

			var feedback models.ReviewFeedback
			require.NoError(t, db.Last(&feedback).Error)
			require.NotNil(t, feedback.Content)
			content := *feedback.Content
			require.NotContains(t, content, "--- Pull Request Body ---")
			require.Contains(t, content, "Please check")
		})
	}
}

func TestHandlePullRequestReview_DetectsCodexApprovalWithPRBodyContext(t *testing.T) {
	db := setupPRReviewDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(zap.NewNop())
	t.Cleanup(func() {
		config.ResetDBForTesting()
		config.ResetLoggerForTesting()
	})

	pr := createPRFixture(t, db)

	body := "Design docs and context for reviewers."
	reviewID := int64(4242)
	gh := newTestGitHubClient(t, &body, reviewID, []string{})

	deps := PullRequestReviewDeps{
		Logger:                   zap.NewNop(),
		GitHubClient:             gh,
		PullRequestRepository:    repositories.NewPullRequestRepository(db),
		ReviewFeedbackRepository: repositories.NewReviewFeedbackRepositoryWithDB(db),
		CodexApprovalDetector:    services.NewCodexApprovalDetector(zap.NewNop()),
	}

	approvalBody := "Codex Review: Didn't find any major issues."
	payload := &PullRequestReviewPayload{
		Action: models.PullRequestReviewActionSubmitted,
		Review: PullRequestReviewReview{
			ID:    reviewID,
			Body:  approvalBody,
			State: "approved",
			User:  User{Login: "chatgpt-codex-connector[bot]", ID: 199175422},
		},
		PullRequest: PullRequestReviewPullRequest{Number: pr.Number},
		Repository:  PullRequestReviewRepository{FullName: pr.Repo},
	}

	w := invokePRReview(t, payload, deps)
	require.Equal(t, http.StatusOK, w.Code)

	var feedback models.ReviewFeedback
	require.NoError(t, db.Last(&feedback).Error)
	require.NotNil(t, feedback.Content)
	require.True(t, feedback.ApprovalDetected, "expected Codex approval detection to remain true")
	content := *feedback.Content
	require.True(t, strings.HasPrefix(content, "--- Pull Request Body ---"))
	require.Contains(t, content, approvalBody)
}
