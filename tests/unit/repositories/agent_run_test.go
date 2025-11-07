package repositories

import (
	"errors"
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

// setupTestDB creates an in-memory SQLite database for testing
// Uses file::memory:?cache=shared to allow multiple connections to share the same database
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// SQLite doesn't support ENUM, so we create tables manually with TEXT types
	// This matches the behavior in production MySQL but uses TEXT for SQLite
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
		CREATE TABLE IF NOT EXISTS agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT UNIQUE,
			issue_id INTEGER,
			pr_id INTEGER,
			state TEXT,
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
		)
	`)

	return db
}

// createTestAgentRun creates a test AgentRun with the specified state
func createTestAgentRun(t *testing.T, db *gorm.DB, state string, prID *int) *models.AgentRun {
	// Create test Issue first
	issue := &models.Issue{
		Repo:          "test/repo",
		Number:        1,
		GitHubIssueID: 123,
		Title:         "Test Issue",
		State:         "open",
	}
	if err := db.Create(issue).Error; err != nil {
		t.Fatalf("Failed to create test issue: %v", err)
	}

	// Generate unique idempotency key using timestamp and state
	idempotencyKey := fmt.Sprintf("test-key-%s-%d", state, time.Now().UnixNano())

	// Create test AgentRun
	run := &models.AgentRun{
		IdempotencyKey: idempotencyKey,
		IssueID:        issue.ID,
		PRID:           prID,
		State:          state,
		AgentType:      "claude-code",
		RetryCount:     0,
	}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("Failed to create test agent run: %v", err)
	}

	return run
}

func TestUpdateState_ValidTransitions(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	tests := []struct {
		name      string
		fromState string
		toState   string
		prID      *int
	}{
		{
			name:      "queued to started",
			fromState: "queued",
			toState:   "started",
			prID:      nil,
		},
		{
			name:      "started to succeeded with pr_id",
			fromState: "started",
			toState:   "succeeded",
			prID:      intPtr(1),
		},
		{
			name:      "started to failed",
			fromState: "started",
			toState:   "failed",
			prID:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create PullRequest if prID is provided
			if tt.prID != nil {
				pr := &models.PullRequest{
					Repo:       "test/repo",
					Number:     *tt.prID,
					Branch:     "test-branch",
					BaseBranch: "main",
					Status:     "open",
					Mergeable:  boolPtr(true),
				}
				if err := db.Create(pr).Error; err != nil {
					t.Fatalf("Failed to create test PR: %v", err)
				}
				tt.prID = &pr.ID
			}

			run := createTestAgentRun(t, db, tt.fromState, tt.prID)

			// Update state
			err := repo.UpdateState(run.ID, tt.toState)
			if err != nil {
				t.Errorf("UpdateState() error = %v, want nil", err)
				return
			}

			// Verify state was updated
			updated, err := repo.GetByID(run.ID)
			if err != nil {
				t.Fatalf("GetByID() error = %v", err)
			}
			if updated.State != tt.toState {
				t.Errorf("State = %v, want %v", updated.State, tt.toState)
			}
		})
	}
}

func TestUpdateState_InvalidTransitions(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	tests := []struct {
		name      string
		fromState string
		toState   string
	}{
		{
			name:      "succeeded to started",
			fromState: "succeeded",
			toState:   "started",
		},
		{
			name:      "failed to started",
			fromState: "failed",
			toState:   "started",
		},
		{
			name:      "queued to succeeded",
			fromState: "queued",
			toState:   "succeeded",
		},
		{
			name:      "queued to failed",
			fromState: "queued",
			toState:   "failed",
		},
		{
			name:      "started to queued",
			fromState: "started",
			toState:   "queued",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := createTestAgentRun(t, db, tt.fromState, nil)

			// Attempt invalid transition
			err := repo.UpdateState(run.ID, tt.toState)

			// Verify error
			var transitionErr *repositories.ErrInvalidStateTransition
			if !errors.As(err, &transitionErr) {
				t.Errorf("UpdateState() error = %v, want ErrInvalidStateTransition", err)
				return
			}
			if transitionErr.CurrentState != tt.fromState {
				t.Errorf("ErrInvalidStateTransition.CurrentState = %v, want %v", transitionErr.CurrentState, tt.fromState)
			}
			if transitionErr.NewState != tt.toState {
				t.Errorf("ErrInvalidStateTransition.NewState = %v, want %v", transitionErr.NewState, tt.toState)
			}

			// Verify state was not changed
			unchanged, err := repo.GetByID(run.ID)
			if err != nil {
				t.Fatalf("GetByID() error = %v", err)
			}
			if unchanged.State != tt.fromState {
				t.Errorf("State = %v, want %v (should not change)", unchanged.State, tt.fromState)
			}
		})
	}
}

func TestUpdateState_SucceededRequiresPRID(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	// Create AgentRun in started state without pr_id
	run := createTestAgentRun(t, db, "started", nil)

	// Attempt to transition to succeeded without pr_id
	err := repo.UpdateState(run.ID, "succeeded")

	// Verify error
	if !errors.Is(err, repositories.ErrMissingPRIDForSucceeded) {
		t.Errorf("UpdateState() error = %v, want ErrMissingPRIDForSucceeded", err)
	}

	// Verify state was not changed
	unchanged, err := repo.GetByID(run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if unchanged.State != "started" {
		t.Errorf("State = %v, want started (should not change)", unchanged.State)
	}
}

func TestUpdateState_IdempotentUpdate(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	tests := []struct {
		name  string
		state string
	}{
		{
			name:  "queued to queued",
			state: "queued",
		},
		{
			name:  "started to started",
			state: "started",
		},
		{
			name:  "succeeded to succeeded",
			state: "succeeded",
		},
		{
			name:  "failed to failed",
			state: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var prID *int
			if tt.state == "succeeded" {
				// Create PR for succeeded state
				pr := &models.PullRequest{
					Repo:       "test/repo",
					Number:     1,
					Branch:     "test-branch",
					BaseBranch: "main",
					Status:     "open",
					Mergeable:  boolPtr(true),
				}
				if err := db.Create(pr).Error; err != nil {
					t.Fatalf("Failed to create test PR: %v", err)
				}
				prID = &pr.ID
			}

			run := createTestAgentRun(t, db, tt.state, prID)

			// Update to same state (idempotent)
			err := repo.UpdateState(run.ID, tt.state)
			if err != nil {
				t.Errorf("UpdateState() error = %v, want nil", err)
				return
			}

			// Verify state remains the same
			unchanged, err := repo.GetByID(run.ID)
			if err != nil {
				t.Fatalf("GetByID() error = %v", err)
			}
			if unchanged.State != tt.state {
				t.Errorf("State = %v, want %v", unchanged.State, tt.state)
			}
		})
	}
}

func TestUpdateState_RecordNotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	// Attempt to update non-existent record
	err := repo.UpdateState(999, "started")

	// Verify error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("UpdateState() error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestUpdateState_ZeroID(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	// Attempt to update with zero ID
	err := repo.UpdateState(0, "started")

	// Verify error
	if err == nil {
		t.Error("UpdateState() error = nil, want error")
		return
	}
	if err.Error() != "cannot update AgentRun with zero ID" {
		t.Errorf("UpdateState() error = %v, want 'cannot update AgentRun with zero ID'", err)
	}
}

func TestUpdateState_ConcurrentStateTransition(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	// Create test Issue (createTestAgentRun will create another, but we need one for PR)
	issue := &models.Issue{
		Repo:          "test/repo",
		Number:        1,
		GitHubIssueID: 123,
		Title:         "Test Issue",
		State:         "open",
	}
	if err := db.Create(issue).Error; err != nil {
		t.Fatalf("Failed to create test issue: %v", err)
	}

	// Create PR for succeeded transition
	pr := &models.PullRequest{
		Repo:       "test/repo",
		Number:     1,
		Branch:     "test-branch",
		BaseBranch: "main",
		Status:     "open",
		Mergeable:  boolPtr(true),
	}
	if err := db.Create(pr).Error; err != nil {
		t.Fatalf("Failed to create test PR: %v", err)
	}

	// Create AgentRun in started state with pr_id
	// Note: createTestAgentRun creates its own Issue, but we'll use the PR we created
	run := createTestAgentRun(t, db, "started", &pr.ID)

	// Verify the run exists and table is ready before starting goroutines
	_, err := repo.GetByID(run.ID)
	if err != nil {
		t.Fatalf("Failed to verify AgentRun exists before concurrent test: %v", err)
	}

	// Test concurrent transitions: one to succeeded, one to failed
	// Only one should succeed, the other should detect conflict
	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)

	// Goroutine 1: transition to succeeded
	go func() {
		defer wg.Done()
		errs[0] = repo.UpdateState(run.ID, "succeeded")
	}()

	// Goroutine 2: transition to failed
	go func() {
		defer wg.Done()
		errs[1] = repo.UpdateState(run.ID, "failed")
	}()

	wg.Wait()

	// One should succeed, one should fail with ErrInvalidStateTransition or database lock
	successCount := 0
	transitionErrorCount := 0
	lockErrorCount := 0

	for i, err := range errs {
		if err == nil {
			successCount++
		} else {
			var transitionErr *repositories.ErrInvalidStateTransition
			if errors.As(err, &transitionErr) {
				transitionErrorCount++
			} else {
				errStr := err.Error()
				if strings.Contains(errStr, "database table is locked") || strings.Contains(errStr, "table is locked") {
					// SQLite lock error is acceptable, but the important thing is that
					// the race was detected and only one transition succeeded
					lockErrorCount++
				} else {
					t.Errorf("Unexpected error in goroutine %d: %v", i, err)
				}
			}
		}
	}

	// Exactly one should succeed (due to WHERE id = ? AND state = ? optimistic locking)
	// The other should either get a transition error or a lock error
	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful update, got %d (transition errors: %d, lock errors: %d)",
			successCount, transitionErrorCount, lockErrorCount)
	}

	// At least one should have a transition error or lock error (the one that lost the race)
	// If both got lock errors, that's also acceptable - the WHERE condition prevented invalid transitions
	totalConflicts := transitionErrorCount + lockErrorCount
	if totalConflicts == 0 && successCount == 2 {
		t.Error("Expected at least one conflict (transition error or lock error), but both updates succeeded")
	}

	// Verify final state is either succeeded or failed
	final, err := repo.GetByID(run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if final.State != "succeeded" && final.State != "failed" {
		t.Errorf("Final state = %v, expected succeeded or failed", final.State)
	}

	// Verify that we can't transition from the final terminal state
	err = repo.UpdateState(run.ID, "started")
	var transitionErr *repositories.ErrInvalidStateTransition
	if !errors.As(err, &transitionErr) {
		t.Errorf("UpdateState() error = %v, want ErrInvalidStateTransition", err)
	}
}

func TestUpdateState_ConcurrentIdempotentUpdate(t *testing.T) {
	db := setupTestDB(t)
	repo := repositories.NewAgentRunRepository(db)

	// Create AgentRun in started state (createTestAgentRun creates its own Issue)
	run := createTestAgentRun(t, db, "started", nil)

	// Verify the run exists and table is ready before starting goroutines
	_, err := repo.GetByID(run.ID)
	if err != nil {
		t.Fatalf("Failed to verify AgentRun exists before concurrent test: %v", err)
	}

	// Multiple goroutines trying to update to the same state (idempotent)
	var wg sync.WaitGroup
	errs := make([]error, 5)

	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = repo.UpdateState(run.ID, "started")
		}(i)
	}

	wg.Wait()

	// All should succeed (idempotent updates)
	// Note: SQLite may return "database table is locked" errors due to concurrent writes,
	// but in production MySQL this won't be an issue. The important thing is that
	// the state remains correct and no invalid transitions occur.
	successCount := 0
	for i, err := range errs {
		if err != nil {
			// Check if it's a SQLite lock error (acceptable for concurrent idempotent updates)
			errStr := err.Error()
			if !strings.Contains(errStr, "database table is locked") && !strings.Contains(errStr, "table is locked") {
				t.Errorf("Goroutine %d: UpdateState() error = %v, want nil or lock error", i, err)
			}
		} else {
			successCount++
		}
	}

	// At least some updates should succeed
	if successCount == 0 {
		t.Error("Expected at least some successful idempotent updates")
	}

	// Verify state is still started
	final, err := repo.GetByID(run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if final.State != "started" {
		t.Errorf("Final state = %v, expected started", final.State)
	}
}

// Helper functions
func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}
