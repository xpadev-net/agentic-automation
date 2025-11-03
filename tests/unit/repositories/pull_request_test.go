package repositories

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// setupTestDBForPR creates an in-memory SQLite database for PullRequest testing
// Uses file::memory:?cache=shared to allow multiple connections to share the same database
func setupTestDBForPR(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Drop tables if they exist to ensure clean state
	db.Exec(`DROP TABLE IF EXISTS pull_requests`)
	db.Exec(`DROP TABLE IF EXISTS agent_runs`)
	db.Exec(`DROP TABLE IF EXISTS issues`)

	// SQLite doesn't support ENUM, so we create tables manually with TEXT types
	// This matches the behavior in production MySQL but uses TEXT for SQLite
	db.Exec(`
		CREATE TABLE issues (
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

	// Create table with unique constraint directly
	// Note: SQLite requires UNIQUE constraint to be defined in CREATE TABLE, not added later
	db.Exec(`
		CREATE TABLE pull_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo TEXT,
			number INTEGER,
			issue_id INTEGER,
			branch TEXT,
			base_branch TEXT DEFAULT 'main',
			status TEXT DEFAULT 'open',
			mergeable BOOLEAN,
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE(repo, number)
		)
	`)

	db.Exec(`
		CREATE TABLE agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT UNIQUE,
			issue_id INTEGER,
			pr_id INTEGER,
			state TEXT,
			agent_type TEXT,
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
		)
	`)

	return db
}

// createTestIssue creates a test Issue
func createTestIssue(t *testing.T, db *gorm.DB, repo string, number int) *models.Issue {
	issue := &models.Issue{
		Repo:          repo,
		Number:        number,
		GitHubIssueID: uint64(100 + number),
		Title:         fmt.Sprintf("Test Issue #%d", number),
		State:         "open",
	}
	if err := db.Create(issue).Error; err != nil {
		t.Fatalf("Failed to create test issue: %v", err)
	}
	return issue
}

// createTestPullRequest creates a test PullRequest
func createTestPullRequest(t *testing.T, db *gorm.DB, repo string, number int, issueID *int) *models.PullRequest {
	pr := &models.PullRequest{
		Repo:       repo,
		Number:     number,
		IssueID:    issueID,
		Branch:     fmt.Sprintf("feature/test-%d", number),
		BaseBranch: "main",
		Status:     "open",
	}
	if err := db.Create(pr).Error; err != nil {
		t.Fatalf("Failed to create test pull request: %v", err)
	}
	return pr
}

// TestUpsert_CreateNewPR tests creating a new PullRequest
func TestUpsert_CreateNewPR(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create test Issue
	issue := createTestIssue(t, db, "test/repo", 1)

	// Create new PR object
	newPR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    &issue.ID,
		Branch:     "feature/test",
		BaseBranch: "main",
		Status:     "open",
		Mergeable:  boolPtr(true),
	}

	// Execute Upsert
	err := repo.Upsert(newPR)
	if err != nil {
		t.Fatalf("Upsert() error = %v, want nil", err)
	}

	// Verify PR was created
	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}

	// Verify all fields are correctly saved
	if retrieved.Repo != "test/repo" {
		t.Errorf("Repo = %v, want 'test/repo'", retrieved.Repo)
	}
	if retrieved.Number != 1 {
		t.Errorf("Number = %v, want 1", retrieved.Number)
	}
	if retrieved.IssueID == nil || *retrieved.IssueID != issue.ID {
		t.Errorf("IssueID = %v, want %d", retrieved.IssueID, issue.ID)
	}
	if retrieved.Branch != "feature/test" {
		t.Errorf("Branch = %v, want 'feature/test'", retrieved.Branch)
	}
	if retrieved.BaseBranch != "main" {
		t.Errorf("BaseBranch = %v, want 'main'", retrieved.BaseBranch)
	}
	if retrieved.Status != "open" {
		t.Errorf("Status = %v, want 'open'", retrieved.Status)
	}
	if retrieved.Mergeable == nil || !*retrieved.Mergeable {
		t.Errorf("Mergeable = %v, want true", retrieved.Mergeable)
	}

	// Verify timestamps are set
	if retrieved.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want non-zero")
	}
	if retrieved.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero, want non-zero")
	}

	// Verify ID is set
	if retrieved.ID == 0 {
		t.Error("ID is zero, want non-zero")
	}
}

// TestUpsert_UpdateExistingPR tests updating an existing PullRequest
func TestUpsert_UpdateExistingPR(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create existing PR using Upsert to avoid unique constraint issues
	existingPR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    nil,
		Branch:     "original-branch",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := repo.Upsert(existingPR); err != nil {
		t.Fatalf("Failed to create existing PR: %v", err)
	}
	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("Failed to find existing PR: %v", err)
	}
	originalID := retrieved.ID
	originalCreatedAt := retrieved.CreatedAt

	// Wait a bit to ensure updated_at will change
	time.Sleep(10 * time.Millisecond)

	// Create new PR object with same repo+number but different fields
	updatePR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		Branch:     "updated-branch",
		BaseBranch: "develop",
		Status:     "closed",
		Mergeable:  boolPtr(false),
	}

	// Execute Upsert
	err = repo.Upsert(updatePR)
	if err != nil {
		t.Fatalf("Upsert() error = %v, want nil", err)
	}

	// Verify PR was updated (ID is the same)
	retrieved, err = repo.FindByID(originalID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}

	// Verify ID is unchanged
	if retrieved.ID != originalID {
		t.Errorf("ID = %v, want %v", retrieved.ID, originalID)
	}

	// Verify fields were updated
	if retrieved.Branch != "updated-branch" {
		t.Errorf("Branch = %v, want 'updated-branch'", retrieved.Branch)
	}
	if retrieved.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %v, want 'develop'", retrieved.BaseBranch)
	}
	if retrieved.Status != "closed" {
		t.Errorf("Status = %v, want 'closed'", retrieved.Status)
	}
	if retrieved.Mergeable == nil || *retrieved.Mergeable {
		t.Errorf("Mergeable = %v, want false", retrieved.Mergeable)
	}

	// Verify created_at is preserved
	if !retrieved.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt = %v, want %v (should be preserved)", retrieved.CreatedAt, originalCreatedAt)
	}

	// Verify updated_at was updated
	if !retrieved.UpdatedAt.After(originalCreatedAt) {
		t.Error("UpdatedAt should be after CreatedAt")
	}
}

// TestUpsert_UpdateAllFields tests updating all updatable fields
func TestUpsert_UpdateAllFields(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create two issues for testing issue_id updates
	issue1 := createTestIssue(t, db, "test/repo", 1)
	issue2 := createTestIssue(t, db, "test/repo", 2)

	// Create existing PR using Upsert
	existingPR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    nil,
		Branch:     "original-branch",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := repo.Upsert(existingPR); err != nil {
		t.Fatalf("Failed to create existing PR: %v", err)
	}

	// Test 1: Update issue_id from nil to value
	update1 := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    &issue1.ID,
		Branch:     "branch-1",
		BaseBranch: "main",
		Status:     "open",
		Mergeable:  boolPtr(true),
	}
	err := repo.Upsert(update1)
	if err != nil {
		t.Fatalf("Upsert(update1) error = %v, want nil", err)
	}

	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}
	if retrieved.IssueID == nil || *retrieved.IssueID != issue1.ID {
		t.Errorf("IssueID = %v, want %d", retrieved.IssueID, issue1.ID)
	}

	// Test 2: Update issue_id to different value
	update2 := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    &issue2.ID,
		Branch:     "branch-2",
		BaseBranch: "develop",
		Status:     "closed",
		Mergeable:  boolPtr(false),
	}
	err = repo.Upsert(update2)
	if err != nil {
		t.Fatalf("Upsert(update2) error = %v, want nil", err)
	}

	retrieved, err = repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}
	if retrieved.IssueID == nil || *retrieved.IssueID != issue2.ID {
		t.Errorf("IssueID = %v, want %d", retrieved.IssueID, issue2.ID)
	}
	if retrieved.Branch != "branch-2" {
		t.Errorf("Branch = %v, want 'branch-2'", retrieved.Branch)
	}
	if retrieved.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %v, want 'develop'", retrieved.BaseBranch)
	}
	if retrieved.Status != "closed" {
		t.Errorf("Status = %v, want 'closed'", retrieved.Status)
	}
	if retrieved.Mergeable == nil || *retrieved.Mergeable {
		t.Errorf("Mergeable = %v, want false", retrieved.Mergeable)
	}

	// Test 3: Update issue_id to nil
	update3 := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    nil,
		Branch:     "branch-3",
		BaseBranch: "main",
		Status:     "merged",
		Mergeable:  boolPtr(true),
	}
	err = repo.Upsert(update3)
	if err != nil {
		t.Fatalf("Upsert(update3) error = %v, want nil", err)
	}

	retrieved, err = repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}
	if retrieved.IssueID != nil {
		t.Errorf("IssueID = %v, want nil", retrieved.IssueID)
	}
	if retrieved.Status != "merged" {
		t.Errorf("Status = %v, want 'merged'", retrieved.Status)
	}
}

// TestUpsert_WithIssueID tests Upsert with issue_id set
func TestUpsert_WithIssueID(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create test Issue
	issue := createTestIssue(t, db, "test/repo", 1)

	// Create PR with issue_id
	pr := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    &issue.ID,
		Branch:     "feature/test",
		BaseBranch: "main",
		Status:     "open",
	}

	err := repo.Upsert(pr)
	if err != nil {
		t.Fatalf("Upsert() error = %v, want nil", err)
	}

	// Verify issue_id was saved
	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}
	if retrieved.IssueID == nil || *retrieved.IssueID != issue.ID {
		t.Errorf("IssueID = %v, want %d", retrieved.IssueID, issue.ID)
	}
}

// TestUpsert_WithoutIssueID tests Upsert without issue_id (nil)
func TestUpsert_WithoutIssueID(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create PR without issue_id
	pr := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    nil,
		Branch:     "feature/test",
		BaseBranch: "main",
		Status:     "open",
	}

	err := repo.Upsert(pr)
	if err != nil {
		t.Fatalf("Upsert() error = %v, want nil", err)
	}

	// Verify issue_id is nil
	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}
	if retrieved.IssueID != nil {
		t.Errorf("IssueID = %v, want nil", retrieved.IssueID)
	}
}

// TestUpsert_PreserveCreatedAt tests that created_at is preserved on update
func TestUpsert_PreserveCreatedAt(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create existing PR using Upsert
	existingPR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    nil,
		Branch:     "original-branch",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := repo.Upsert(existingPR); err != nil {
		t.Fatalf("Failed to create existing PR: %v", err)
	}
	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("Failed to find existing PR: %v", err)
	}
	originalCreatedAt := retrieved.CreatedAt

	// Wait to ensure timestamps are different
	time.Sleep(10 * time.Millisecond)

	// Update PR
	updatePR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		Branch:     "updated-branch",
		BaseBranch: "main",
		Status:     "open",
	}

	err = repo.Upsert(updatePR)
	if err != nil {
		t.Fatalf("Upsert() error = %v, want nil", err)
	}

	// Verify created_at is preserved
	retrieved, err = repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}

	// CreatedAt should be exactly the same (preserved)
	if !retrieved.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt = %v, want %v (should be preserved)", retrieved.CreatedAt, originalCreatedAt)
	}

	// UpdatedAt should be different (updated)
	if !retrieved.UpdatedAt.After(originalCreatedAt) {
		t.Error("UpdatedAt should be after CreatedAt")
	}
}

// TestUpsert_StatusEnumValues tests all valid status values
func TestUpsert_StatusEnumValues(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	statuses := []string{"open", "closed", "merged"}

	for i, status := range statuses {
		pr := &models.PullRequest{
			Repo:       "test/repo",
			Number:     i + 1,
			Branch:     fmt.Sprintf("branch-%d", i+1),
			BaseBranch: "main",
			Status:     status,
		}

		err := repo.Upsert(pr)
		if err != nil {
			t.Fatalf("Upsert() error for status %s = %v, want nil", status, err)
		}

		// Verify status was saved
		retrieved, err := repo.FindByRepoAndNumber("test/repo", i+1)
		if err != nil {
			t.Fatalf("FindByRepoAndNumber() error = %v", err)
		}
		if retrieved.Status != status {
			t.Errorf("Status = %v, want %v", retrieved.Status, status)
		}
	}
}

// TestUpsert_UniqueConstraint tests that unique constraint on (repo, number) is enforced
func TestUpsert_UniqueConstraint(t *testing.T) {
	db := setupTestDBForPR(t)

	// Create PR directly using Create
	pr1 := &models.PullRequest{
		Repo:       "test/repo-unique",
		Number:     10,
		Branch:     "branch-1",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := db.Create(pr1).Error; err != nil {
		t.Fatalf("Failed to create first PR: %v", err)
	}

	// Try to create another PR with same repo+number directly (should fail)
	pr2 := &models.PullRequest{
		Repo:       "test/repo-unique",
		Number:     10, // Same as pr1 to trigger unique constraint
		Branch:     "branch-2",
		BaseBranch: "main",
		Status:     "open",
	}
	err := db.Create(pr2).Error
	if err == nil {
		t.Error("Expected unique constraint violation error, got nil")
	}
}

// TestUpsert_ConcurrentUpdates tests concurrent updates to the same PR
func TestUpsert_ConcurrentUpdates(t *testing.T) {
	db := setupTestDBForPR(t)
	repo := repositories.NewPullRequestRepository(db)

	// Create existing PR using Upsert
	existingPR := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    nil,
		Branch:     "original-branch",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := repo.Upsert(existingPR); err != nil {
		t.Fatalf("Failed to create existing PR: %v", err)
	}
	retrieved, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("Failed to find existing PR: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)
	branches := make([]string, 5)

	// 5 goroutines updating concurrently
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			defer wg.Done()
			branch := fmt.Sprintf("branch-%d", idx)
			branches[idx] = branch

			updatePR := &models.PullRequest{
				Repo:       "test/repo",
				Number:     1,
				Branch:     branch,
				BaseBranch: "main",
				Status:     "open",
			}
			errs[idx] = repo.Upsert(updatePR)
		}(i)
	}

	wg.Wait()

	// Most goroutines should succeed (SQLite may return lock errors in concurrent scenarios)
	// In production MySQL this won't be an issue, but SQLite has table-level locking
	successCount := 0
	for i, err := range errs {
		if err != nil {
			// Check if it's a SQLite lock error (acceptable for concurrent updates in SQLite)
			errStr := err.Error()
			if !strings.Contains(errStr, "database table is locked") && !strings.Contains(errStr, "table is locked") {
				t.Errorf("Goroutine %d: Upsert() error = %v, want nil or lock error", i, err)
			}
		} else {
			successCount++
		}
	}

	// At least some updates should succeed
	if successCount == 0 {
		t.Error("Expected at least some successful updates")
	}

	// Verify final state (last update should be applied)
	final, err := repo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}

	// Verify ID is unchanged
	if final.ID != retrieved.ID {
		t.Errorf("ID = %v, want %v", final.ID, retrieved.ID)
	}

	// Branch should be one of the updated branches (last one wins)
	found := false
	for _, branch := range branches {
		if final.Branch == branch {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Branch = %v, expected one of %v", final.Branch, branches)
	}
}

// TestUpsert_WithAgentRunIntegration tests integration with AgentRun (preparation for T082)
func TestUpsert_WithAgentRunIntegration(t *testing.T) {
	db := setupTestDBForPR(t)
	prRepo := repositories.NewPullRequestRepository(db)
	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Create Issue
	issue := createTestIssue(t, db, "test/repo", 1)

	// Create AgentRun
	idempotencyKey := fmt.Sprintf("test-key-%d", time.Now().UnixNano())
	agentRun := &models.AgentRun{
		IdempotencyKey: idempotencyKey,
		IssueID:        issue.ID,
		State:          "started",
		AgentType:      "claude-code",
		RetryCount:     0,
	}
	if err := db.Create(agentRun).Error; err != nil {
		t.Fatalf("Failed to create AgentRun: %v", err)
	}

	// Create PR using Upsert (simulating agent-runner report)
	pr := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		IssueID:    &issue.ID,
		Branch:     "feature/test",
		BaseBranch: "main",
		Status:     "open",
	}
	err := prRepo.Upsert(pr)
	if err != nil {
		t.Fatalf("Upsert() error = %v, want nil", err)
	}

	// Verify PR was created
	retrieved, err := prRepo.FindByRepoAndNumber("test/repo", 1)
	if err != nil {
		t.Fatalf("FindByRepoAndNumber() error = %v", err)
	}

	// Update AgentRun with PR ID (simulating T082 behavior)
	agentRun.PRID = &retrieved.ID
	err = agentRunRepo.Update(agentRun)
	if err != nil {
		t.Fatalf("Failed to update AgentRun: %v", err)
	}

	// Verify relationship
	updatedRun, err := agentRunRepo.GetByID(agentRun.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if updatedRun.PRID == nil || *updatedRun.PRID != retrieved.ID {
		t.Errorf("AgentRun.PRID = %v, want %d", updatedRun.PRID, retrieved.ID)
	}

	// Verify PR has correct issue_id
	if retrieved.IssueID == nil || *retrieved.IssueID != issue.ID {
		t.Errorf("PR.IssueID = %v, want %d", retrieved.IssueID, issue.ID)
	}
}
