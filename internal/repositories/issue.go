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
	if issue.Labels == "" {
		issue.Labels = "[]"
	}
	return r.db.Create(issue).Error
}

// Update updates an existing Issue record by ID
// Uses GORM's Save method which updates all fields
func (r *IssueRepository) Update(issue *models.Issue) error {
	if issue.Labels == "" {
		issue.Labels = "[]"
	}
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

// UpsertSelective creates or updates an Issue by (repo, number), updating only provided columns.
// This avoids wiping existing metadata when some fields are zero-values in the input.
//
// Behavior:
//   - If record exists: updates only keys present in 'updates' map
//   - If not exists: creates a new record initialized with repo, number and provided updates
func (r *IssueRepository) UpsertSelective(repo string, number int, updates map[string]interface{}) error {
	if updates == nil {
		updates = map[string]interface{}{}
	}

	var existing models.Issue
	err := r.db.Where("repo = ? AND number = ?", repo, number).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Create new with partial fields
			newIssue := models.Issue{Repo: repo, Number: number}
			if title, ok := updates["title"].(string); ok {
				newIssue.Title = title
			}
			if state, ok := updates["state"].(string); ok {
				newIssue.State = state
			}
			return r.db.Create(&newIssue).Error
		}
		return err
	}

	if len(updates) == 0 {
		return nil
	}

	// Update only specified columns
	return r.db.Model(&existing).Select(getMapKeys(updates)).Updates(updates).Error
}

// getMapKeys returns keys of a map[string]interface{} in a slice
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
