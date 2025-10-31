package repositories

import (
	"errors"

	"gorm.io/gorm"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
)

// IssueRepository provides data access methods for Issue model
type IssueRepository struct {
	db *gorm.DB
}

// NewIssueRepository creates a new IssueRepository instance
func NewIssueRepository() *IssueRepository {
	return &IssueRepository{
		db: config.GetDB(),
	}
}

// FindByRepoAndNumber finds an Issue by repository and issue number (unique key)
// Returns gorm.ErrRecordNotFound if not found
func (r *IssueRepository) FindByRepoAndNumber(repo string, number int) (*models.Issue, error) {
	var issue models.Issue
	err := r.db.Where("repo = ? AND number = ?", repo, number).First(&issue).Error
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// FindByID finds an Issue by primary key
// Returns gorm.ErrRecordNotFound if not found
func (r *IssueRepository) FindByID(id int) (*models.Issue, error) {
	var issue models.Issue
	err := r.db.First(&issue, id).Error
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// FindByIDWithRelations finds an Issue by ID and preloads related AgentRuns and PullRequests
// Returns gorm.ErrRecordNotFound if not found
func (r *IssueRepository) FindByIDWithRelations(id int) (*models.Issue, error) {
	var issue models.Issue
	err := r.db.Preload("AgentRuns").Preload("PullRequests").First(&issue, id).Error
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// Create inserts a new Issue record
// Returns error if duplicate (repo, number) violation occurs
func (r *IssueRepository) Create(issue *models.Issue) error {
	return r.db.Create(issue).Error
}

// Update updates an existing Issue record by ID
// Uses GORM's Save method which updates all fields
func (r *IssueRepository) Update(issue *models.Issue) error {
	return r.db.Save(issue).Error
}

// Upsert creates or updates an Issue based on (repo, number) unique constraint
// If an issue with the same repo and number exists, it updates it; otherwise creates a new one
func (r *IssueRepository) Upsert(issue *models.Issue) error {
	// Try to find existing issue by repo and number
	existing, err := r.FindByRepoAndNumber(issue.Repo, issue.Number)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Not found, create new
			return r.Create(issue)
		}
		// Some other error occurred
		return err
	}

	// Update existing issue with new data (preserve ID)
	issue.ID = existing.ID
	return r.Update(issue)
}

// FindByState finds all Issues with the given state ('open' or 'closed')
// Returns empty slice if none found
func (r *IssueRepository) FindByState(state string) ([]models.Issue, error) {
	var issues []models.Issue
	err := r.db.Where("state = ?", state).Find(&issues).Error
	if err != nil {
		return nil, err
	}
	return issues, nil
}

