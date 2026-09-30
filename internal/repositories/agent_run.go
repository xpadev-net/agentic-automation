package repositories

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-automation/internal/models"
)

// ErrInvalidStateTransition indicates an invalid state transition
type ErrInvalidStateTransition struct {
	CurrentState string
	NewState     string
}

func (e *ErrInvalidStateTransition) Error() string {
	return fmt.Sprintf("invalid state transition: cannot transition from %s to %s", e.CurrentState, e.NewState)
}

// ErrMissingPRIDForSucceeded indicates that pr_id is required when transitioning to succeeded state
var ErrMissingPRIDForSucceeded = errors.New("pr_id is required when transitioning to succeeded state")

// AgentRunRepository defines the interface for AgentRun data access operations
type AgentRunRepository interface {
	// CreateOrGet creates a new AgentRun or returns existing one by idempotency key
	// Returns (agentRun, isNew, error) where isNew=true means newly created, isNew=false means existing record found
	CreateOrGet(idempotencyKey string, run *models.AgentRun) (*models.AgentRun, bool, error)

	// GetByID retrieves an AgentRun by its primary key
	GetByID(id int) (*models.AgentRun, error)

	// GetByIDempotencyKey retrieves an AgentRun by its idempotency key
	GetByIDempotencyKey(key string) (*models.AgentRun, error)

	// Update updates an existing AgentRun record
	Update(run *models.AgentRun) error

	// UpdateState updates only the state field of an AgentRun
	UpdateState(id int, state string) error

	// GetByIssueID retrieves all AgentRuns for a specific Issue
	GetByIssueID(issueID int) ([]*models.AgentRun, error)

	// GetByPRID retrieves all AgentRuns for a specific PullRequest
	GetByPRID(prID int) ([]*models.AgentRun, error)

	// IncrementRetryCount atomically increments the retry_count field by 1
	// This method uses an atomic UPDATE query to prevent lost increments under concurrent retries.
	// Returns the updated retry_count value and an error if the increment fails or max retries already reached.
	// Parameters:
	//   - id: AgentRun ID to update
	//   - maxRetryCount: Maximum allowed retry count (increment will fail if current count >= maxRetryCount)
	// Returns:
	//   - int: New retry_count value after increment
	//   - error: Error if update fails, record not found, or max retries already reached
	IncrementRetryCount(id int, maxRetryCount int) (int, error)
}

// agentRunRepository implements AgentRunRepository using GORM
type agentRunRepository struct {
	db *gorm.DB
}

// NewAgentRunRepository creates a new instance of AgentRunRepository
func NewAgentRunRepository(db *gorm.DB) AgentRunRepository {
	return &agentRunRepository{db: db}
}

// CreateOrGet creates a new AgentRun or returns existing one by idempotency key
// Implements idempotency check: if a record with the same idempotency_key exists,
// it returns that record instead of creating a duplicate
func (r *agentRunRepository) CreateOrGet(idempotencyKey string, run *models.AgentRun) (*models.AgentRun, bool, error) {
	// First, try to find existing record by idempotency_key
	existing := &models.AgentRun{}
	err := r.db.Where("idempotency_key = ?", idempotencyKey).First(existing).Error

	if err == nil {
		// Record exists, return it
		return existing, false, nil
	}

	// If not found, check if it's a "not found" error or something else
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	// Record doesn't exist, set the idempotency key and create new record
	run.IdempotencyKey = idempotencyKey
	err = r.db.Create(run).Error
	if err != nil {
		// Handle unique constraint violation (race condition)
		// If another goroutine created the record between our check and create,
		// we'll get a unique constraint violation. Retry the lookup.
		if isUniqueConstraintViolation(err) {
			// Retry lookup
			existing := &models.AgentRun{}
			lookupErr := r.db.Where("idempotency_key = ?", idempotencyKey).First(existing).Error
			if lookupErr == nil {
				return existing, false, nil
			}
			// If lookup still fails, return the original error
		}
		return nil, false, err
	}

	return run, true, nil
}

// GetByID retrieves an AgentRun by its primary key
func (r *agentRunRepository) GetByID(id int) (*models.AgentRun, error) {
	run := &models.AgentRun{}
	err := r.db.First(run, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	return run, nil
}

// GetByIDempotencyKey retrieves an AgentRun by its idempotency key
func (r *agentRunRepository) GetByIDempotencyKey(key string) (*models.AgentRun, error) {
	run := &models.AgentRun{}
	err := r.db.Where("idempotency_key = ?", key).First(run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	return run, nil
}

// Update updates an existing AgentRun record
// ErrConcurrentLifecycle means a callback, retry or reconciler won the state change.
var ErrConcurrentLifecycle = errors.New("agent run lifecycle changed concurrently")

// LifecycleQuery matches the observed attempt, including NULL JobName. Callers
// must not use full Save without this guard after reading lifecycle state.
func LifecycleQuery(db *gorm.DB, id int, snapshot models.AgentRunLifecycle) *gorm.DB {
	query := db.Model(&models.AgentRun{}).Where("id = ? AND state = ? AND retry_count = ?", id, snapshot.State, snapshot.RetryCount)
	if snapshot.JobName == nil {
		return query.Where("job_name IS NULL")
	}
	return query.Where("job_name = ?", *snapshot.JobName)
}

func (r *agentRunRepository) Update(run *models.AgentRun) error {
	if run.ID == 0 {
		return errors.New("cannot update AgentRun with zero ID")
	}
	snapshot, ok := run.ObservedLifecycle()
	if !ok {
		current, err := r.GetByID(run.ID)
		if err != nil {
			return err
		}
		snapshot, _ = current.ObservedLifecycle()
	}
	result := LifecycleQuery(r.db, run.ID, snapshot).Select("*").Omit("id", "created_at", clause.Associations).Updates(run)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		current, err := r.GetByID(run.ID)
		if err != nil {
			return err
		}
		observed, _ := current.ObservedLifecycle()
		sameName := (snapshot.JobName == nil && observed.JobName == nil) || (snapshot.JobName != nil && observed.JobName != nil && *snapshot.JobName == *observed.JobName)
		if observed.State != snapshot.State || observed.RetryCount != snapshot.RetryCount || !sameName {
			return ErrConcurrentLifecycle
		}
	}
	run.CaptureLifecycle()
	return nil
}

// UpdateState updates only the state field of an AgentRun
// Uses optimistic locking with WHERE id = ? AND state = ? to prevent race conditions
func (r *agentRunRepository) UpdateState(id int, state string) error {
	if id == 0 {
		return errors.New("cannot update AgentRun with zero ID")
	}

	// Get current AgentRun to check current state and validate transition
	currentRun, err := r.GetByID(id)
	if err != nil {
		return err
	}

	currentState := currentRun.State

	// Allow idempotent update: same state can be set again
	if currentState == state {
		// Perform the update with WHERE condition to ensure state hasn't changed
		result := r.db.Model(&models.AgentRun{}).Where("id = ? AND state = ?", id, currentState).Update("state", state)
		if result.Error != nil {
			return result.Error
		}
		// If RowsAffected is 0, state was changed by another goroutine, but since we're setting same state, it's fine
		return nil
	}

	// Validate state transition
	// Terminal states cannot transition to other states
	if currentState == "succeeded" || currentState == "failed" {
		return &ErrInvalidStateTransition{
			CurrentState: currentState,
			NewState:     state,
		}
	}

	// Validate allowed transitions
	var updateCondition *gorm.DB
	switch currentState {
	case "queued":
		// queued can only transition to started
		if state != "started" {
			return &ErrInvalidStateTransition{
				CurrentState: currentState,
				NewState:     state,
			}
		}
		updateCondition = r.db.Model(&models.AgentRun{}).Where("id = ? AND state = ?", id, currentState)
	case "started":
		// started can transition to succeeded or failed
		if state != "succeeded" && state != "failed" {
			return &ErrInvalidStateTransition{
				CurrentState: currentState,
				NewState:     state,
			}
		}
		// If transitioning to succeeded, verify pr_id is set (except for plan_creation mode)
		if state == "succeeded" {
			// For plan_creation mode, pr_id may be nil, so skip the check
			if currentRun.ExecutionMode != "plan_creation" {
				if currentRun.PRID == nil {
					return ErrMissingPRIDForSucceeded
				}
				// Include pr_id IS NOT NULL in WHERE condition to ensure pr_id is still set at update time
				updateCondition = r.db.Model(&models.AgentRun{}).Where("id = ? AND state = ? AND pr_id IS NOT NULL", id, currentState)
			} else {
				// For plan_creation mode, just check state (pr_id can be nil)
				updateCondition = r.db.Model(&models.AgentRun{}).Where("id = ? AND state = ?", id, currentState)
			}
		} else {
			// For failed transition, just check state
			updateCondition = r.db.Model(&models.AgentRun{}).Where("id = ? AND state = ?", id, currentState)
		}
	default:
		// Unknown current state - reject transition
		return &ErrInvalidStateTransition{
			CurrentState: currentState,
			NewState:     state,
		}
	}

	// Perform the state update atomically using WHERE conditions
	// This ensures the state (and pr_id for succeeded) hasn't changed between validation and update
	result := updateCondition.Update("state", state)
	if result.Error != nil {
		return result.Error
	}

	// Check if update actually affected any rows
	// If RowsAffected == 0, the state was changed by another goroutine between validation and update
	if result.RowsAffected == 0 {
		// State changed by another goroutine, get current state and return appropriate error
		updated, err := r.GetByID(id)
		if err != nil {
			return err
		}

		// If state is now the target state, another goroutine already made the transition (idempotent)
		if updated.State == state {
			return nil
		}

		// State changed to something else, return transition error with actual current state
		return &ErrInvalidStateTransition{
			CurrentState: updated.State,
			NewState:     state,
		}
	}

	return nil
}

// GetByIssueID retrieves all AgentRuns for a specific Issue
func (r *agentRunRepository) GetByIssueID(issueID int) ([]*models.AgentRun, error) {
	var runs []*models.AgentRun
	err := r.db.Where("issue_id = ?", issueID).Find(&runs).Error
	if err != nil {
		return nil, err
	}
	return runs, nil
}

// GetByPRID retrieves all AgentRuns for a specific PullRequest
func (r *agentRunRepository) GetByPRID(prID int) ([]*models.AgentRun, error) {
	var runs []*models.AgentRun
	err := r.db.Where("pr_id = ?", prID).Find(&runs).Error
	if err != nil {
		return nil, err
	}
	return runs, nil
}

// IncrementRetryCount atomically increments the retry_count field by 1
// This method uses an atomic UPDATE query to prevent lost increments under concurrent retries.
// The WHERE clause ensures that retry_count is only incremented if it's below the maximum.
//
// Parameters:
//   - id: AgentRun ID to update
//   - maxRetryCount: Maximum allowed retry count (increment will fail if current count >= maxRetryCount)
//
// Returns:
//   - int: New retry_count value after increment
//   - error: Error if update fails, record not found, or max retries already reached
func (r *agentRunRepository) IncrementRetryCount(id int, maxRetryCount int) (int, error) {
	if id == 0 {
		return 0, errors.New("cannot increment retry count for AgentRun with zero ID")
	}

	// Perform atomic increment using SQL: UPDATE ... SET retry_count = retry_count + 1 WHERE id = ? AND retry_count < ?
	// This ensures that:
	// 1. The increment is atomic at the database level
	// 2. Concurrent increments don't overwrite each other
	// 3. The maximum retry count is enforced atomically
	result := r.db.Model(&models.AgentRun{}).
		Where("id = ? AND retry_count < ?", id, maxRetryCount).
		Update("retry_count", gorm.Expr("retry_count + 1"))

	if result.Error != nil {
		return 0, fmt.Errorf("failed to increment retry count: %w", result.Error)
	}

	// Check if the update actually affected any rows
	if result.RowsAffected == 0 {
		// Either the record doesn't exist, or retry_count is already at or above maxRetryCount
		// Check if the record exists
		run, err := r.GetByID(id)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, fmt.Errorf("AgentRun with ID %d not found", id)
			}
			return 0, fmt.Errorf("failed to check AgentRun: %w", err)
		}

		// Record exists, so retry_count must be at or above maxRetryCount
		return run.RetryCount, fmt.Errorf("cannot increment retry count: already at or above maximum (%d >= %d)", run.RetryCount, maxRetryCount)
	}

	// Reload the record to get the updated retry_count value
	updatedRun, err := r.GetByID(id)
	if err != nil {
		return 0, fmt.Errorf("failed to reload AgentRun after increment: %w", err)
	}

	return updatedRun.RetryCount, nil
}

// isUniqueConstraintViolation checks if an error is a unique constraint violation
func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}

	// Check for MySQL unique constraint violation error code (1062)
	// GORM wraps database errors, so we need to check the underlying error
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}

	// Also check for common unique constraint error messages
	errStr := err.Error()
	return strings.Contains(strings.ToLower(errStr), "duplicate entry") ||
		strings.Contains(strings.ToLower(errStr), "unique constraint")
}
