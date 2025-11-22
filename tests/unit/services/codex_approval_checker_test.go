package services

import (
	"testing"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDBForCodexApproval(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db.Exec(`CREATE TABLE pull_requests (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        repo TEXT,
        number INTEGER,
        issue_id INTEGER,
        branch TEXT,
        base_branch TEXT,
        status TEXT,
        mergeable BOOLEAN,
        created_at DATETIME,
        updated_at DATETIME,
        UNIQUE(repo, number)
    )`)
	db.Exec(`CREATE TABLE review_feedback (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        pr_id INTEGER,
        source TEXT,
        content TEXT,
        status TEXT,
        approval_detected BOOLEAN,
        github_comment_id INTEGER,
        plan_creation_status TEXT DEFAULT 'pending',
        plan_content TEXT,
        plan_agent_run_id INTEGER,
        execution_agent_run_id INTEGER,
        created_at DATETIME,
        updated_at DATETIME
    )`)
	return db
}

func TestCodexApprovalChecker_IsApprovedUsesLatestNonRequestedResponse(t *testing.T) {
	db := setupTestDBForCodexApproval(t)
	prRepo := repositories.NewPullRequestRepository(db)
	reviewRepo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := &models.PullRequest{Repo: "o/r", Number: 10, Status: "open"}
	require.NoError(t, db.Create(pr).Error)

	checker := services.NewCodexApprovalChecker(reviewRepo, prRepo, nil)

	// no feedbacks -> false
	ok, err := checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.False(t, ok)

	// requested entry alone should be ignored
	requested := &models.ReviewFeedback{PRID: pr.ID, Source: "Codex", Status: "requested", ApprovalDetected: false}
	require.NoError(t, db.Create(requested).Error)
	ok, err = checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.False(t, ok)

	// received approval -> true
	approved := &models.ReviewFeedback{PRID: pr.ID, Source: "Codex", Status: "received", ApprovalDetected: true}
	require.NoError(t, db.Create(approved).Error)
	ok, err = checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.True(t, ok)

	// later non-approval response should override previous approval
	rejection := &models.ReviewFeedback{PRID: pr.ID, Source: "Codex", Status: "commented", ApprovalDetected: false}
	require.NoError(t, db.Create(rejection).Error)
	ok, err = checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.False(t, ok)

	// newest entry re-approves -> true
	latestApprove := &models.ReviewFeedback{PRID: pr.ID, Source: "Codex", Status: "commented", ApprovalDetected: true}
	require.NoError(t, db.Create(latestApprove).Error)
	ok, err = checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.True(t, ok)

	// adding a requested entry after latest response should not change result
	newRequest := &models.ReviewFeedback{PRID: pr.ID, Source: "Codex", Status: "requested", ApprovalDetected: false}
	require.NoError(t, db.Create(newRequest).Error)
	ok, err = checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.True(t, ok)
}
