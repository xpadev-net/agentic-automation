package repositories

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// setupTestDBForReviewFeedback creates an in-memory SQLite database for ReviewFeedback testing
// Uses file::memory:?cache=shared to allow multiple connections to share the same database
func setupTestDBForReviewFeedback(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Enable foreign key constraints in SQLite
	db.Exec("PRAGMA foreign_keys = ON")

	// SQLite doesn't support ENUM, so we create tables manually with TEXT types
	// This matches the behavior in production MySQL but uses TEXT for SQLite
	db.Exec(`
		CREATE TABLE IF NOT EXISTS pull_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo TEXT,
			number INTEGER,
			issue_id INTEGER,
			branch TEXT,
			base_branch TEXT,
			status TEXT,
			mergeable BOOLEAN,
			created_at DATETIME,
			updated_at DATETIME
		)
	`)

	db.Exec(`
		CREATE TABLE IF NOT EXISTS review_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_id INTEGER NOT NULL,
			source TEXT DEFAULT 'Codex',
			content TEXT,
			status TEXT DEFAULT 'requested',
			approval_detected BOOLEAN DEFAULT FALSE,
			github_comment_id INTEGER,
			created_at DATETIME,
			updated_at DATETIME,
			FOREIGN KEY(pr_id) REFERENCES pull_requests(id) ON DELETE CASCADE
		)
	`)

	return db
}

// createTestPullRequestForReviewFeedback creates a test PullRequest for ReviewFeedback testing
func createTestPullRequestForReviewFeedback(t *testing.T, db *gorm.DB) *models.PullRequest {
	pr := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		Branch:     "feature/test",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := db.Create(pr).Error; err != nil {
		t.Fatalf("Failed to create test pull request: %v", err)
	}
	return pr
}

// TestCreateRequestedReview_ValidPRID tests creating a ReviewFeedback record with valid PR ID
func TestCreateRequestedReview_ValidPRID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	// Create test PR
	pr := createTestPullRequestForReviewFeedback(t, db)

	// Create ReviewFeedback with nil githubCommentID
	feedback, err := repo.CreateRequestedReview(pr.ID, nil)
	if err != nil {
		t.Fatalf("CreateRequestedReview() error = %v, want nil", err)
	}

	// Verify feedback was created
	if feedback == nil {
		t.Fatal("CreateRequestedReview() returned nil feedback")
	}
	if feedback.ID == 0 {
		t.Error("CreateRequestedReview() ID = 0, want non-zero")
	}
	if feedback.PRID != pr.ID {
		t.Errorf("CreateRequestedReview() PRID = %v, want %v", feedback.PRID, pr.ID)
	}
	if feedback.Source != "Codex" {
		t.Errorf("CreateRequestedReview() Source = %v, want 'Codex'", feedback.Source)
	}
	if feedback.Status != "requested" {
		t.Errorf("CreateRequestedReview() Status = %v, want 'requested'", feedback.Status)
	}
	if feedback.ApprovalDetected {
		t.Error("CreateRequestedReview() ApprovalDetected = true, want false")
	}
	if feedback.Content != nil {
		t.Error("CreateRequestedReview() Content != nil, want nil")
	}
	if feedback.GitHubCommentID != nil {
		t.Error("CreateRequestedReview() GitHubCommentID != nil, want nil")
	}
}

// TestCreateRequestedReview_WithGitHubCommentID tests creating a ReviewFeedback record with GitHub comment ID
func TestCreateRequestedReview_WithGitHubCommentID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)
	commentID := int64(12345)

	feedback, err := repo.CreateRequestedReview(pr.ID, &commentID)
	if err != nil {
		t.Fatalf("CreateRequestedReview() error = %v, want nil", err)
	}

	if feedback.GitHubCommentID == nil {
		t.Error("CreateRequestedReview() GitHubCommentID = nil, want non-nil")
	} else if *feedback.GitHubCommentID != commentID {
		t.Errorf("CreateRequestedReview() GitHubCommentID = %v, want %v", *feedback.GitHubCommentID, commentID)
	}
}

// TestCreateRequestedReview_InvalidPRID tests creating a ReviewFeedback record with invalid PR ID
func TestCreateRequestedReview_InvalidPRID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	tests := []struct {
		name string
		prID int
	}{
		{"zero PR ID", 0},
		{"negative PR ID", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feedback, err := repo.CreateRequestedReview(tt.prID, nil)
			if err == nil {
				t.Error("CreateRequestedReview() error = nil, want error")
			}
			if feedback != nil {
				t.Errorf("CreateRequestedReview() feedback = %v, want nil", feedback)
			}
			if err.Error() != "prID must be greater than 0" {
				t.Errorf("CreateRequestedReview() error = %v, want 'prID must be greater than 0'", err)
			}
		})
	}
}

// TestCreateRequestedReview_NonExistentPRID tests creating a ReviewFeedback record with non-existent PR ID
func TestCreateRequestedReview_NonExistentPRID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	// Try to create with non-existent PR ID
	nonExistentPRID := 999
	feedback, err := repo.CreateRequestedReview(nonExistentPRID, nil)

	// Should get foreign key constraint error
	if err == nil {
		t.Error("CreateRequestedReview() error = nil, want foreign key constraint error")
	}
	if feedback != nil {
		t.Errorf("CreateRequestedReview() feedback = %v, want nil", feedback)
	}
}

// TestCreateReceivedReview_ValidPRID tests creating a ReviewFeedback record with valid PR ID
func TestCreateReceivedReview_ValidPRID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)
	content := "This is a review comment"
	approvalDetected := false

	feedback, err := repo.CreateReceivedReview(pr.ID, content, approvalDetected, nil)
	if err != nil {
		t.Fatalf("CreateReceivedReview() error = %v, want nil", err)
	}

	if feedback == nil {
		t.Fatal("CreateReceivedReview() returned nil feedback")
	}
	if feedback.Status != "received" {
		t.Errorf("CreateReceivedReview() Status = %v, want 'received'", feedback.Status)
	}
	if feedback.Content == nil {
		t.Error("CreateReceivedReview() Content = nil, want non-nil")
	} else if *feedback.Content != content {
		t.Errorf("CreateReceivedReview() Content = %v, want %v", *feedback.Content, content)
	}
	if feedback.ApprovalDetected != approvalDetected {
		t.Errorf("CreateReceivedReview() ApprovalDetected = %v, want %v", feedback.ApprovalDetected, approvalDetected)
	}
}

// TestCreateReceivedReview_EmptyContent tests creating a ReviewFeedback record with empty content
func TestCreateReceivedReview_EmptyContent(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)

	feedback, err := repo.CreateReceivedReview(pr.ID, "", false, nil)
	if err != nil {
		t.Fatalf("CreateReceivedReview() error = %v, want nil", err)
	}

	if feedback.Content != nil {
		t.Error("CreateReceivedReview() Content != nil for empty string, want nil")
	}
}

// TestCreateReceivedReview_ApprovalDetected tests creating a ReviewFeedback record with approval detected
func TestCreateReceivedReview_ApprovalDetected(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)
	content := "Codex Review: Didn't find any major issues."

	feedback, err := repo.CreateReceivedReview(pr.ID, content, true, nil)
	if err != nil {
		t.Fatalf("CreateReceivedReview() error = %v, want nil", err)
	}

	if !feedback.ApprovalDetected {
		t.Error("CreateReceivedReview() ApprovalDetected = false, want true")
	}
}

// TestCreateReceivedReview_InvalidPRID tests creating a ReviewFeedback record with invalid PR ID
func TestCreateReceivedReview_InvalidPRID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	tests := []struct {
		name string
		prID int
	}{
		{"zero PR ID", 0},
		{"negative PR ID", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feedback, err := repo.CreateReceivedReview(tt.prID, "content", false, nil)
			if err == nil {
				t.Error("CreateReceivedReview() error = nil, want error")
			}
			if feedback != nil {
				t.Errorf("CreateReceivedReview() feedback = %v, want nil", feedback)
			}
			if err.Error() != "prID must be greater than 0" {
				t.Errorf("CreateReceivedReview() error = %v, want 'prID must be greater than 0'", err)
			}
		})
	}
}

// TestUpdateToReceived_ValidUpdate tests updating a requested ReviewFeedback to received status
func TestUpdateToReceived_ValidUpdate(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)

	// Create requested review
	requested, err := repo.CreateRequestedReview(pr.ID, nil)
	if err != nil {
		t.Fatalf("CreateRequestedReview() error = %v", err)
	}

	// Update to received
	content := "Review feedback content"
	commentID := int64(67890)
	err = repo.UpdateToReceived(requested.ID, content, true, &commentID)
	if err != nil {
		t.Fatalf("UpdateToReceived() error = %v, want nil", err)
	}

	// Verify update
	updated, err := repo.FindByID(requested.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if updated.Status != "received" {
		t.Errorf("UpdateToReceived() Status = %v, want 'received'", updated.Status)
	}
	if updated.Content == nil {
		t.Error("UpdateToReceived() Content = nil, want non-nil")
	} else if *updated.Content != content {
		t.Errorf("UpdateToReceived() Content = %v, want %v", *updated.Content, content)
	}
	if !updated.ApprovalDetected {
		t.Error("UpdateToReceived() ApprovalDetected = false, want true")
	}
	if updated.GitHubCommentID == nil {
		t.Error("UpdateToReceived() GitHubCommentID = nil, want non-nil")
	} else if *updated.GitHubCommentID != commentID {
		t.Errorf("UpdateToReceived() GitHubCommentID = %v, want %v", *updated.GitHubCommentID, commentID)
	}
}

// TestUpdateToReceived_EmptyContent tests updating with empty content
func TestUpdateToReceived_EmptyContent(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)

	requested, err := repo.CreateRequestedReview(pr.ID, nil)
	if err != nil {
		t.Fatalf("CreateRequestedReview() error = %v", err)
	}

	err = repo.UpdateToReceived(requested.ID, "", false, nil)
	if err != nil {
		t.Fatalf("UpdateToReceived() error = %v, want nil", err)
	}

	updated, err := repo.FindByID(requested.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if updated.Content != nil {
		t.Error("UpdateToReceived() Content != nil for empty string, want nil")
	}
}

// TestUpdateToReceived_PreserveGitHubCommentID tests that nil githubCommentID preserves existing value
func TestUpdateToReceived_PreserveGitHubCommentID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)
	originalCommentID := int64(11111)

	requested, err := repo.CreateRequestedReview(pr.ID, &originalCommentID)
	if err != nil {
		t.Fatalf("CreateRequestedReview() error = %v", err)
	}

	// Update with nil githubCommentID
	err = repo.UpdateToReceived(requested.ID, "content", false, nil)
	if err != nil {
		t.Fatalf("UpdateToReceived() error = %v, want nil", err)
	}

	updated, err := repo.FindByID(requested.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if updated.GitHubCommentID == nil {
		t.Error("UpdateToReceived() GitHubCommentID = nil, want preserved value")
	} else if *updated.GitHubCommentID != originalCommentID {
		t.Errorf("UpdateToReceived() GitHubCommentID = %v, want %v", *updated.GitHubCommentID, originalCommentID)
	}
}

// TestUpdateToReceived_InvalidID tests updating with invalid ID
func TestUpdateToReceived_InvalidID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	tests := []struct {
		name string
		id   int
	}{
		{"zero ID", 0},
		{"negative ID", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.UpdateToReceived(tt.id, "content", false, nil)
			if err == nil {
				t.Error("UpdateToReceived() error = nil, want error")
			}
			if err.Error() != "id must be greater than 0" {
				t.Errorf("UpdateToReceived() error = %v, want 'id must be greater than 0'", err)
			}
		})
	}
}

// TestUpdateToReceived_NonExistentID tests updating non-existent record
func TestUpdateToReceived_NonExistentID(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	nonExistentID := 999
	err := repo.UpdateToReceived(nonExistentID, "content", false, nil)

	if err == nil {
		t.Error("UpdateToReceived() error = nil, want error")
	}
	if err.Error() != "ReviewFeedback record not found" {
		t.Errorf("UpdateToReceived() error = %v, want 'ReviewFeedback record not found'", err)
	}
}

// TestUpdateToReceived_InvalidStatus tests updating a record that is not in 'requested' status
func TestUpdateToReceived_InvalidStatus(t *testing.T) {
	db := setupTestDBForReviewFeedback(t)
	repo := repositories.NewReviewFeedbackRepositoryWithDB(db)

	pr := createTestPullRequestForReviewFeedback(t, db)

	// Create a received review (not requested)
	received, err := repo.CreateReceivedReview(pr.ID, "content", false, nil)
	if err != nil {
		t.Fatalf("CreateReceivedReview() error = %v", err)
	}

	// Try to update to received (should fail)
	err = repo.UpdateToReceived(received.ID, "new content", false, nil)
	if err == nil {
		t.Error("UpdateToReceived() error = nil, want error")
	}
	if err.Error() != "can only update ReviewFeedback records with status 'requested'" {
		t.Errorf("UpdateToReceived() error = %v, want 'can only update ReviewFeedback records with status 'requested''", err)
	}
}
