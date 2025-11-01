package repositories

import (
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// AutoMigrate all models
	err = db.AutoMigrate(
		&models.Issue{},
		&models.PullRequest{},
		&models.AgentRun{},
	)
	if err != nil {
		t.Fatalf("Failed to migrate test database: %v", err)
	}

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

	// Create test AgentRun
	run := &models.AgentRun{
		IdempotencyKey: "test-key-" + state,
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

// Helper functions
func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}
