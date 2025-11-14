package repositories

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CIStatusRepository provides CRUD operations for CIStatus entities
type CIStatusRepository struct {
	db *gorm.DB
}

// NewCIStatusRepository creates a new CIStatusRepository instance
func NewCIStatusRepository() *CIStatusRepository {
	return &CIStatusRepository{
		db: config.GetDB(),
	}
}

// NewCIStatusRepositoryWithDB creates a new CIStatusRepository instance with a custom DB connection.
// This is primarily for testing purposes.
func NewCIStatusRepositoryWithDB(db *gorm.DB) *CIStatusRepository {
	return &CIStatusRepository{
		db: db,
	}
}

// CreateOrUpdate creates or updates a CIStatus record based on check_suite_id and pr_id.
// If a record with the same check_suite_id and pr_id exists, it updates the record;
// otherwise, a new record is created.
//
// Parameters:
//   - ciStatus: CIStatus record to create or update (must not be nil)
//
// Returns:
//   - error: Error if database operation fails
func (r *CIStatusRepository) CreateOrUpdate(ciStatus *models.CIStatus) error {
	if ciStatus == nil {
		return gorm.ErrRecordNotFound
	}

	// Use GORM's clause.OnConflict to handle upsert based on unique constraint (check_suite_id, pr_id)
	// Note: This assumes a unique index on (check_suite_id, pr_id) exists in the database
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "check_suite_id"},
			{Name: "pr_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"check_run_id",
			"name",
			"status",
			"conclusion",
			"logs",
			"logs_url",
			"started_at",
			"completed_at",
			"updated_at",
		}),
	}).Create(ciStatus).Error
}

// FindByCheckSuiteID finds CIStatus records by check_suite_id
func (r *CIStatusRepository) FindByCheckSuiteID(checkSuiteID string) ([]models.CIStatus, error) {
	var statuses []models.CIStatus
	err := r.db.Where("check_suite_id = ?", checkSuiteID).Find(&statuses).Error
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// FindByPRID finds CIStatus records by pr_id
func (r *CIStatusRepository) FindByPRID(prID int) ([]models.CIStatus, error) {
	var statuses []models.CIStatus
	err := r.db.Where("pr_id = ?", prID).Find(&statuses).Error
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// FindByPRIDAndCheckSuiteID finds a CIStatus record by pr_id and check_suite_id
func (r *CIStatusRepository) FindByPRIDAndCheckSuiteID(prID int, checkSuiteID string) (*models.CIStatus, error) {
	var status models.CIStatus
	err := r.db.Where("pr_id = ? AND check_suite_id = ?", prID, checkSuiteID).First(&status).Error
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// FindByPRIDAndName finds a CIStatus record by pr_id and name (status context)
func (r *CIStatusRepository) FindByPRIDAndName(prID int, name string) (*models.CIStatus, error) {
	var status models.CIStatus
	err := r.db.Where("pr_id = ? AND name = ?", prID, name).First(&status).Error
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// Create inserts a new CIStatus row
func (r *CIStatusRepository) Create(ciStatus *models.CIStatus) error {
	return r.db.Create(ciStatus).Error
}

// Update updates an existing CIStatus row
func (r *CIStatusRepository) Update(ciStatus *models.CIStatus) error {
	return r.db.Save(ciStatus).Error
}
