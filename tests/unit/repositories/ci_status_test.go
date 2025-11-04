package repositories

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// setupTestDBForCIStatus creates an in-memory SQLite database for CIStatus testing
func setupTestDBForCIStatus(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Drop tables if they exist to ensure clean state
	db.Exec(`DROP TABLE IF EXISTS ci_status`)
	db.Exec(`DROP TABLE IF EXISTS pull_requests`)

	// Create pull_requests table
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

	// Create ci_status table with unique constraint on (check_suite_id, pr_id)
	db.Exec(`
		CREATE TABLE ci_status (
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
			UNIQUE(check_suite_id, pr_id)
		)
	`)

	return db
}

// createTestPullRequestForCIStatus creates a test PullRequest
func createTestPullRequestForCIStatus(t *testing.T, db *gorm.DB, repo string, number int) *models.PullRequest {
	pr := &models.PullRequest{
		Repo:       repo,
		Number:     number,
		Branch:     "feature/test",
		BaseBranch: "main",
		Status:     "open",
	}
	if err := db.Create(pr).Error; err != nil {
		t.Fatalf("Failed to create test pull request: %v", err)
	}
	return pr
}

func TestCIStatusRepository_CreateOrUpdate_Create(t *testing.T) {
	db := setupTestDBForCIStatus(t)
	repo := repositories.NewCIStatusRepositoryWithDB(db)

	// Create test PR
	pr := createTestPullRequestForCIStatus(t, db, "test/repo", 1)

	// Create CIStatus
	now := time.Now()
	conclusion := "success"
	ciStatus := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "12345",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}

	err := repo.CreateOrUpdate(ciStatus)
	if err != nil {
		t.Fatalf("Failed to create CIStatus: %v", err)
	}

	if ciStatus.ID == 0 {
		t.Error("CIStatus ID should be set after creation")
	}

	// Verify it was created
	var found models.CIStatus
	if err := db.Where("check_suite_id = ? AND pr_id = ?", "12345", pr.ID).First(&found).Error; err != nil {
		t.Fatalf("Failed to find created CIStatus: %v", err)
	}

	if found.CheckSuiteID != "12345" {
		t.Errorf("Expected CheckSuiteID '12345', got '%s'", found.CheckSuiteID)
	}
	if found.PRID != pr.ID {
		t.Errorf("Expected PRID %d, got %d", pr.ID, found.PRID)
	}
	if found.Status != "completed" {
		t.Errorf("Expected Status 'completed', got '%s'", found.Status)
	}
	if found.Conclusion == nil || *found.Conclusion != "success" {
		t.Errorf("Expected Conclusion 'success', got %v", found.Conclusion)
	}
}

func TestCIStatusRepository_CreateOrUpdate_Update(t *testing.T) {
	db := setupTestDBForCIStatus(t)
	repo := repositories.NewCIStatusRepositoryWithDB(db)

	// Create test PR
	pr := createTestPullRequestForCIStatus(t, db, "test/repo", 1)

	// Create initial CIStatus
	now := time.Now()
	conclusion1 := "pending"
	ciStatus1 := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "12345",
		Status:       "in_progress",
		Conclusion:   &conclusion1,
		StartedAt:    &now,
	}

	err := repo.CreateOrUpdate(ciStatus1)
	if err != nil {
		t.Fatalf("Failed to create initial CIStatus: %v", err)
	}

	initialID := ciStatus1.ID

	// Update CIStatus with same check_suite_id and pr_id
	conclusion2 := "success"
	completedAt := time.Now()
	ciStatus2 := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "12345",
		Status:       "completed",
		Conclusion:   &conclusion2,
		CompletedAt:  &completedAt,
	}

	err = repo.CreateOrUpdate(ciStatus2)
	if err != nil {
		t.Fatalf("Failed to update CIStatus: %v", err)
	}

	// Verify it was updated (same ID, different values)
	var found models.CIStatus
	if err := db.Where("check_suite_id = ? AND pr_id = ?", "12345", pr.ID).First(&found).Error; err != nil {
		t.Fatalf("Failed to find updated CIStatus: %v", err)
	}

	if found.ID != initialID {
		t.Errorf("Expected ID to remain %d, got %d", initialID, found.ID)
	}
	if found.Status != "completed" {
		t.Errorf("Expected Status 'completed', got '%s'", found.Status)
	}
	if found.Conclusion == nil || *found.Conclusion != "success" {
		t.Errorf("Expected Conclusion 'success', got %v", found.Conclusion)
	}
	if found.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set")
	}
}

func TestCIStatusRepository_FindByCheckSuiteID(t *testing.T) {
	db := setupTestDBForCIStatus(t)
	repo := repositories.NewCIStatusRepositoryWithDB(db)

	// Create test PRs
	pr1 := createTestPullRequest(t, db, "test/repo", 1)
	pr2 := createTestPullRequest(t, db, "test/repo", 2)

	// Create CIStatus records
	now := time.Now()
	conclusion := "success"

	ciStatus1 := &models.CIStatus{
		PRID:         pr1.ID,
		CheckSuiteID: "12345",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}
	repo.CreateOrUpdate(ciStatus1)

	ciStatus2 := &models.CIStatus{
		PRID:         pr2.ID,
		CheckSuiteID: "12345",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}
	repo.CreateOrUpdate(ciStatus2)

	// Find by check_suite_id
	statuses, err := repo.FindByCheckSuiteID("12345")
	if err != nil {
		t.Fatalf("Failed to find CIStatus by check_suite_id: %v", err)
	}

	if len(statuses) != 2 {
		t.Errorf("Expected 2 CIStatus records, got %d", len(statuses))
	}
}

func TestCIStatusRepository_FindByPRID(t *testing.T) {
	db := setupTestDBForCIStatus(t)
	repo := repositories.NewCIStatusRepositoryWithDB(db)

	// Create test PR
	pr := createTestPullRequestForCIStatus(t, db, "test/repo", 1)

	// Create CIStatus records
	now := time.Now()
	conclusion := "success"

	ciStatus1 := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "12345",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}
	repo.CreateOrUpdate(ciStatus1)

	ciStatus2 := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "67890",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}
	repo.CreateOrUpdate(ciStatus2)

	// Find by pr_id
	statuses, err := repo.FindByPRID(pr.ID)
	if err != nil {
		t.Fatalf("Failed to find CIStatus by pr_id: %v", err)
	}

	if len(statuses) != 2 {
		t.Errorf("Expected 2 CIStatus records, got %d", len(statuses))
	}
}

func TestCIStatusRepository_FindByPRIDAndCheckSuiteID(t *testing.T) {
	db := setupTestDBForCIStatus(t)
	repo := repositories.NewCIStatusRepositoryWithDB(db)

	// Create test PR
	pr := createTestPullRequestForCIStatus(t, db, "test/repo", 1)

	// Create CIStatus
	now := time.Now()
	conclusion := "success"
	ciStatus := &models.CIStatus{
		PRID:         pr.ID,
		CheckSuiteID: "12345",
		Status:       "completed",
		Conclusion:   &conclusion,
		CompletedAt:  &now,
	}
	repo.CreateOrUpdate(ciStatus)

	// Find by pr_id and check_suite_id
	found, err := repo.FindByPRIDAndCheckSuiteID(pr.ID, "12345")
	if err != nil {
		t.Fatalf("Failed to find CIStatus: %v", err)
	}

	if found == nil {
		t.Fatal("Expected to find CIStatus, got nil")
	}

	if found.CheckSuiteID != "12345" {
		t.Errorf("Expected CheckSuiteID '12345', got '%s'", found.CheckSuiteID)
	}
	if found.PRID != pr.ID {
		t.Errorf("Expected PRID %d, got %d", pr.ID, found.PRID)
	}
}
