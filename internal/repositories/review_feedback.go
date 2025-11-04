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

// NewReviewFeedbackRepositoryWithDB creates a new ReviewFeedbackRepository instance with a custom DB connection.
// This is primarily for testing purposes.
func NewReviewFeedbackRepositoryWithDB(db *gorm.DB) *ReviewFeedbackRepository {
	return &ReviewFeedbackRepository{
		db: db,
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

// FindByPRID finds all review feedbacks for a specific PR.
//
// Usage:
//   - T097 (FeedbackAggregator): Use to retrieve past review history for aggregation
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

// CreateRequestedReview creates a new ReviewFeedback record for a review request.
// This is used when a review is requested (e.g., PR created or @codex review comment detected).
//
// Usage:
//   - T090 (pull_request_review_comment handler): Call when "@codex review" comment is detected
//   - T091 (CodexReviewService): Call after posting review request comment, save githubCommentID
//   - US3 T103: Called automatically when PR is created successfully
//
// Parameters:
//   - prID: PullRequest ID (must be > 0)
//   - githubCommentID: GitHub comment ID (optional, can be nil)
//
// Returns:
//   - *models.ReviewFeedback: Created ReviewFeedback record
//   - error: Error if creation fails
func (r *ReviewFeedbackRepository) CreateRequestedReview(prID int, githubCommentID *int64) (*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}

	feedback := &models.ReviewFeedback{
		PRID:             prID,
		Source:           "Codex",
		Status:           "requested",
		ApprovalDetected: false,
		Content:          nil,
		GitHubCommentID:  githubCommentID,
	}

	if err := r.db.Create(feedback).Error; err != nil {
		return nil, err
	}

	return feedback, nil
}

// CreateReceivedReview creates a new ReviewFeedback record for a received review.
// This is used when a review is received from Codex (when no existing requested record exists).
//
// Usage:
//   - T093 (CodexApprovalDetector): Call when Codex review is received and no existing requested record exists
//
// Parameters:
//   - prID: PullRequest ID (must be > 0)
//   - content: Review content (empty string will be stored as nil)
//   - approvalDetected: Whether approval was detected in the review
//   - githubCommentID: GitHub comment ID (optional, can be nil)
//
// Returns:
//   - *models.ReviewFeedback: Created ReviewFeedback record
//   - error: Error if creation fails
func (r *ReviewFeedbackRepository) CreateReceivedReview(prID int, content string, approvalDetected bool, githubCommentID *int64) (*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}

	feedback := &models.ReviewFeedback{
		PRID:             prID,
		Source:           "Codex",
		Status:           "received",
		ApprovalDetected: approvalDetected,
		Content:          contentPtr,
		GitHubCommentID:  githubCommentID,
	}

	if err := r.db.Create(feedback).Error; err != nil {
		return nil, err
	}

	return feedback, nil
}

// UpdateToReceived updates an existing 'requested' ReviewFeedback record to 'received' status.
// This is used when a review request is followed by an actual review response.
//
// Usage:
//   - T093 (CodexApprovalDetector): Call when Codex review is received and a requested record exists
//   - T097 (FeedbackAggregator): May use FindByPRID() to retrieve past review history for aggregation
//
// Parameters:
//   - id: ReviewFeedback ID (must be > 0)
//   - content: Review content (empty string will be stored as nil)
//   - approvalDetected: Whether approval was detected in the review
//   - githubCommentID: GitHub comment ID (optional, can be nil; if nil, existing value is preserved)
//
// Returns:
//   - error: Error if update fails
func (r *ReviewFeedbackRepository) UpdateToReceived(id int, content string, approvalDetected bool, githubCommentID *int64) error {
	if id <= 0 {
		return errors.New("id must be greater than 0")
	}

	// Get existing record
	existing, err := r.FindByID(id)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("ReviewFeedback record not found")
	}
	if existing.Status != "requested" {
		return errors.New("can only update ReviewFeedback records with status 'requested'")
	}

	// Prepare content
	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}

	// Update fields
	existing.Status = "received"
	existing.Content = contentPtr
	existing.ApprovalDetected = approvalDetected
	if githubCommentID != nil {
		existing.GitHubCommentID = githubCommentID
	}

	return r.db.Save(existing).Error
}
