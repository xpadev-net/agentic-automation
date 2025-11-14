package repositories

import (
	"testing"

	"agentic-automation/internal/models"

	"gorm.io/gorm"
)

// ensureIssuesTable ensures the issues table exists for this test (SQLite in-memory)
func ensureIssuesTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Exec(`
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
    `)
}

func TestIssue_GitHubIssueID_BigValue_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	ensureIssuesTable(t, db)

	bigID := uint64(3583340286)

	issue := &models.Issue{
		Repo:          "test/repo",
		Number:        100,
		GitHubIssueID: bigID,
		Title:         "Big ID",
		State:         "open",
	}

	if err := db.Create(issue).Error; err != nil {
		t.Fatalf("failed to insert issue with big github_issue_id: %v", err)
	}

	var fetched models.Issue
	if err := db.Where("repo = ? AND number = ?", issue.Repo, issue.Number).First(&fetched).Error; err != nil {
		t.Fatalf("failed to fetch issue: %v", err)
	}

	if fetched.GitHubIssueID != bigID {
		t.Fatalf("github_issue_id mismatch: got %d, want %d", fetched.GitHubIssueID, bigID)
	}
}
