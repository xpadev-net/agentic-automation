package repositories

import (
	"errors"

	"gorm.io/gorm"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
)

// ReviewFeedbackRepository provides data access methods for ReviewFeedback
type ReviewFeedbackRepository struct {
	db *gorm.DB
}

// NewReviewFeedbackRepository creates a new ReviewFeedbackRepository instance
func NewReviewFeedbackRepository() *ReviewFeedbackRepository {
	return &ReviewFeedbackRepository{
		db: config.GetDB(),
	}
}

// Create creates a new review feedback record
func (r *ReviewFeedbackRepository) Create(feedback *models.ReviewFeedback) error {
	if feedback == nil {
		return errors.New("feedback cannot be nil")
	}
	return r.db.Create(feedback).Error
}

// FindByID finds a review feedback by ID
func (r *ReviewFeedbackRepository) FindByID(id int) (*models.ReviewFeedback, error) {
	if id <= 0 {
		return nil, errors.New("id must be greater than 0")
	}

	var feedback models.ReviewFeedback
	err := r.db.First(&feedback, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &feedback, nil
}

// FindByPRID finds all review feedbacks for a specific PR
func (r *ReviewFeedbackRepository) FindByPRID(prID int) ([]*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}

	var feedbacks []*models.ReviewFeedback
	err := r.db.Where("pr_id = ?", prID).Order("created_at DESC").Find(&feedbacks).Error
	if err != nil {
		return nil, err
	}
	return feedbacks, nil
}

// FindByPRIDAndStatus finds review feedbacks by PR ID and status
func (r *ReviewFeedbackRepository) FindByPRIDAndStatus(prID int, status string) ([]*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}
	if status == "" {
		return nil, errors.New("status cannot be empty")
	}

	var feedbacks []*models.ReviewFeedback
	err := r.db.Where("pr_id = ? AND status = ?", prID, status).Order("created_at DESC").Find(&feedbacks).Error
	if err != nil {
		return nil, err
	}
	return feedbacks, nil
}

// FindByApprovalDetected finds review feedbacks by PR ID and approval status
func (r *ReviewFeedbackRepository) FindByApprovalDetected(prID int, approvalDetected bool) ([]*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}

	var feedbacks []*models.ReviewFeedback
	err := r.db.Where("pr_id = ? AND approval_detected = ?", prID, approvalDetected).Order("created_at DESC").Find(&feedbacks).Error
	if err != nil {
		return nil, err
	}
	return feedbacks, nil
}

// FindLatestByPRID finds the most recent review feedback for a PR
func (r *ReviewFeedbackRepository) FindLatestByPRID(prID int) (*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}

	var feedback models.ReviewFeedback
	err := r.db.Where("pr_id = ?", prID).Order("created_at DESC").First(&feedback).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &feedback, nil
}

// Update updates an existing review feedback record
func (r *ReviewFeedbackRepository) Update(feedback *models.ReviewFeedback) error {
	if feedback == nil {
		return errors.New("feedback cannot be nil")
	}
	if feedback.ID <= 0 {
		return errors.New("feedback ID must be greater than 0")
	}

	return r.db.Save(feedback).Error
}

// UpdateStatus updates only the status field of a review feedback record
func (r *ReviewFeedbackRepository) UpdateStatus(id int, status string) error {
	if id <= 0 {
		return errors.New("id must be greater than 0")
	}
	if status == "" {
		return errors.New("status cannot be empty")
	}

	return r.db.Model(&models.ReviewFeedback{}).Where("id = ?", id).Update("status", status).Error
}
