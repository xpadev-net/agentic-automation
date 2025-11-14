package repositories

import (
	"agentic-automation/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PullRequestRepository provides CRUD operations for PullRequest entities
type PullRequestRepository struct {
	db *gorm.DB
}

// NewPullRequestRepository creates a new PullRequestRepository instance
func NewPullRequestRepository(db *gorm.DB) *PullRequestRepository {
	return &PullRequestRepository{db: db}
}

// FindByID finds a PullRequest by its primary key ID
func (r *PullRequestRepository) FindByID(id int) (*models.PullRequest, error) {
	var pr models.PullRequest
	err := r.db.First(&pr, id).Error
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// FindByRepoAndNumber finds a PullRequest by repo and number (unique key)
func (r *PullRequestRepository) FindByRepoAndNumber(repo string, number int) (*models.PullRequest, error) {
	var pr models.PullRequest
	err := r.db.Where("repo = ? AND number = ?", repo, number).First(&pr).Error
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// FindByIssueID finds all PullRequests associated with an Issue
func (r *PullRequestRepository) FindByIssueID(issueID int) ([]models.PullRequest, error) {
	var prs []models.PullRequest
	err := r.db.Where("issue_id = ?", issueID).Find(&prs).Error
	if err != nil {
		return nil, err
	}
	return prs, nil
}

// Create creates a new PullRequest record
func (r *PullRequestRepository) Create(pr *models.PullRequest) error {
	return r.db.Create(pr).Error
}

// Update updates an existing PullRequest record
func (r *PullRequestRepository) Update(pr *models.PullRequest) error {
	return r.db.Save(pr).Error
}

// UpdateStatus updates only the status field of a PullRequest
func (r *PullRequestRepository) UpdateStatus(id int, status string) error {
	return r.db.Model(&models.PullRequest{}).Where("id = ?", id).Update("status", status).Error
}

// UpdateMergeable updates only the mergeable field of a PullRequest
func (r *PullRequestRepository) UpdateMergeable(id int, mergeable bool) error {
	return r.db.Model(&models.PullRequest{}).Where("id = ?", id).Update("mergeable", mergeable).Error
}

// Upsert inserts or updates a PullRequest based on repo+number uniqueness.
// If a PR with the same repo and number exists, it updates the specified fields;
// otherwise, a new record is created.
//
// Fields updated on conflict: issue_id, branch, base_branch, status, mergeable, updated_at
// Fields preserved on update: id, repo, number, created_at
//
// Returns error if database operation fails (e.g., unique constraint violation, connection error)
func (r *PullRequestRepository) Upsert(pr *models.PullRequest) error {
	// Use GORM's clause.OnConflict to handle upsert based on unique constraint (repo, number)
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "repo"},
			{Name: "number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"issue_id",
			"branch",
			"base_branch",
			"status",
			"mergeable",
			"updated_at",
		}),
	}).Create(pr).Error
}

// FindOpenByRepoAndBranch finds an open PR by repo and head branch name.
func (r *PullRequestRepository) FindOpenByRepoAndBranch(repo string, branch string) (*models.PullRequest, error) {
	var pr models.PullRequest
	err := r.db.Where("repo = ? AND branch = ? AND status = ?", repo, branch, "open").First(&pr).Error
	if err != nil {
		return nil, err
	}
	return &pr, nil
}
