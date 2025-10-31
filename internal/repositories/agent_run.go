package repositories

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"agentic-automation/internal/models"
)

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
func (r *agentRunRepository) Update(run *models.AgentRun) error {
	if run.ID == 0 {
		return errors.New("cannot update AgentRun with zero ID")
	}
	return r.db.Save(run).Error
}

// UpdateState updates only the state field of an AgentRun
func (r *agentRunRepository) UpdateState(id int, state string) error {
	if id == 0 {
		return errors.New("cannot update AgentRun with zero ID")
	}
	return r.db.Model(&models.AgentRun{}).Where("id = ?", id).Update("state", state).Error
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

