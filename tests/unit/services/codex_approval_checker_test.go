package services

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"testing"

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
        created_at DATETIME,
        updated_at DATETIME
    )`)
	return db
}

func TestCodexApprovalChecker_IsApproved(t *testing.T) {
	db := setupTestDBForCodexApproval(t)
	prRepo := repositories.NewPullRequestRepository(db)
	reviewRepo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := &models.PullRequest{Repo: "o/r", Number: 10, Status: "open"}
	require.NoError(t, db.Create(pr).Error)

	// not approved initially
	checker := services.NewCodexApprovalChecker(reviewRepo, prRepo, nil)
	ok, err := checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.False(t, ok)

	// create approval record
	fb := &models.ReviewFeedback{PRID: pr.ID, Source: "Codex", Status: "received", ApprovalDetected: true}
	require.NoError(t, db.Create(fb).Error)

	ok2, err := checker.IsApproved(nil, "o", "r", 10)
	require.NoError(t, err)
	require.True(t, ok2)
}
