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

// FindByGitHubCommentID finds a review feedback by GitHub comment ID.
// This is used to prevent duplicate processing of the same review comment.
//
// Usage:
//   - T094 (startPlanCreationIfNeeded): Check if a review comment has already been processed
//
// Parameters:
//   - githubCommentID: GitHub comment ID (must be > 0)
//
// Returns:
//   - *models.ReviewFeedback: Found ReviewFeedback record, or nil if not found
//   - error: Error if query fails
func (r *ReviewFeedbackRepository) FindByGitHubCommentID(githubCommentID int64) (*models.ReviewFeedback, error) {
	if githubCommentID <= 0 {
		return nil, errors.New("githubCommentID must be greater than 0")
	}

	var feedback models.ReviewFeedback
	err := r.db.Where("github_comment_id = ?", githubCommentID).First(&feedback).Error
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

// UpdatePlanCreationStatus updates only the plan_creation_status field of a review feedback record
func (r *ReviewFeedbackRepository) UpdatePlanCreationStatus(id int, planCreationStatus string) error {
	if id <= 0 {
		return errors.New("id must be greater than 0")
	}
	if planCreationStatus == "" {
		return errors.New("planCreationStatus cannot be empty")
	}

	return r.db.Model(&models.ReviewFeedback{}).Where("id = ?", id).Update("plan_creation_status", planCreationStatus).Error
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

// TryStartPlanCreation atomically updates PlanCreationStatus from 'pending' to 'creating'.
// This prevents concurrent plan creation attempts for the same ReviewFeedback.
//
// Usage:
//   - startPlanCreationIfNeeded: Use to atomically claim plan creation for a ReviewFeedback
//
// Parameters:
//   - reviewFeedbackID: ReviewFeedback ID (must be > 0)
//   - planAgentRunID: AgentRun ID for the plan creation (must be > 0)
//
// Returns:
//   - bool: true if the update succeeded (status was 'pending' and is now 'creating'), false if it was already 'creating' or in another state
//   - error: Error if the database operation fails
func (r *ReviewFeedbackRepository) TryStartPlanCreation(reviewFeedbackID int, planAgentRunID int) (bool, error) {
	if reviewFeedbackID <= 0 {
		return false, errors.New("reviewFeedbackID must be greater than 0")
	}
	if planAgentRunID <= 0 {
		return false, errors.New("planAgentRunID must be greater than 0")
	}

	// Atomically update PlanCreationStatus from 'pending' to 'creating'
	// This uses optimistic locking - only one concurrent request will succeed
	result := r.db.Model(&models.ReviewFeedback{}).
		Where("id = ? AND plan_creation_status = ?", reviewFeedbackID, "pending").
		Updates(map[string]interface{}{
			"plan_creation_status": "creating",
			"plan_agent_run_id":    planAgentRunID,
		})

	if result.Error != nil {
		return false, result.Error
	}

	// If rows affected is 1, the update succeeded (status was 'pending')
	// If rows affected is 0, the status was already 'creating' or in another state
	return result.RowsAffected == 1, nil
}

// TryStartPlanCreationForPR atomically updates PlanCreationStatus from 'pending' to 'creating'
// for a specific ReviewFeedback, but only if no other ReviewFeedback for the same PR
// is already in 'creating' status. This prevents concurrent plan creation attempts
// for the same PR across multiple ReviewFeedback records.
//
// Usage:
//   - startPlanCreationIfNeeded: Use to atomically claim plan creation for a PR
//
// Parameters:
//   - prID: PullRequest ID (must be > 0)
//   - reviewFeedbackID: ReviewFeedback ID (must be > 0)
//   - planAgentRunID: AgentRun ID for the plan creation (must be > 0)
//
// Returns:
//   - bool: true if the update succeeded (no other ReviewFeedback for the PR is 'creating' and this one was 'pending'), false otherwise
//   - error: Error if the database operation fails
func (r *ReviewFeedbackRepository) TryStartPlanCreationForPR(prID int, reviewFeedbackID int, planAgentRunID int) (bool, error) {
	if prID <= 0 {
		return false, errors.New("prID must be greater than 0")
	}
	if reviewFeedbackID <= 0 {
		return false, errors.New("reviewFeedbackID must be greater than 0")
	}
	if planAgentRunID <= 0 {
		return false, errors.New("planAgentRunID must be greater than 0")
	}

	// Use SELECT FOR UPDATE in a transaction to lock rows before checking and updating.
	// This prevents race conditions where two concurrent transactions could both evaluate
	// the NOT EXISTS check before either commits, allowing both to succeed.
	// Row-level locking ensures only one transaction can proceed at a time.
	// Note: SQLite doesn't support FOR UPDATE, so we omit it for SQLite (tests only).
	var success bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Lock all rows for this PR that are in 'pending' or 'creating' status.
		// ORDER BY id ensures consistent lock ordering to prevent deadlocks.
		type lockedRow struct {
			ID                 int
			PlanCreationStatus string
		}
		var lockedRows []lockedRow

		// Detect database type to handle SQLite (which doesn't support FOR UPDATE)
		dbName := tx.Dialector.Name()
		lockQuery := `SELECT id, plan_creation_status FROM review_feedback 
WHERE pr_id = ? AND plan_creation_status IN ('pending', 'creating') 
ORDER BY id`

		// Add FOR UPDATE for databases that support it (MySQL, PostgreSQL)
		// SQLite doesn't support FOR UPDATE, but transactions still provide some isolation
		if dbName != "sqlite" {
			lockQuery += " FOR UPDATE"
		}

		if err := tx.Raw(lockQuery, prID).Scan(&lockedRows).Error; err != nil {
			return err
		}

		// Check if any locked rows are already in 'creating' status
		for _, row := range lockedRows {
			if row.PlanCreationStatus == "creating" {
				// Another ReviewFeedback for this PR is already in 'creating' status
				success = false
				return nil
			}
		}

		// No rows are in 'creating' status, so we can update the target row.
		// Verify the target row is in the locked set and is 'pending'
		targetFound := false
		for _, row := range lockedRows {
			if row.ID == reviewFeedbackID {
				if row.PlanCreationStatus != "pending" {
					// Target row is not in 'pending' status
					success = false
					return nil
				}
				targetFound = true
				break
			}
		}

		if !targetFound {
			// Target row is not in 'pending' or 'creating' status (might be in another state)
			success = false
			return nil
		}

		// Update the target row to 'creating'
		updateQuery := `UPDATE review_feedback 
SET plan_creation_status = 'creating', plan_agent_run_id = ?
WHERE id = ? AND pr_id = ? AND plan_creation_status = 'pending'`

		result := tx.Exec(updateQuery, planAgentRunID, reviewFeedbackID, prID)
		if result.Error != nil {
			return result.Error
		}

		// If rows affected is 1, the update succeeded
		success = result.RowsAffected == 1
		return nil
	})

	if err != nil {
		return false, err
	}

	return success, nil
}

// FindNewerReviewsByPRID finds review feedbacks for a PR that have a GitHubCommentID
// greater than the specified afterCommentID.
//
// Usage:
//   - handlePlanCreated: Check for new reviews after plan creation completes
//
// Parameters:
//   - prID: PullRequest ID (must be > 0)
//   - afterCommentID: GitHub comment ID to compare against (must be > 0)
//
// Returns:
//   - []*models.ReviewFeedback: List of newer review feedbacks, ordered by created_at DESC
//   - error: Error if query fails
func (r *ReviewFeedbackRepository) FindNewerReviewsByPRID(prID int, afterCommentID int64) ([]*models.ReviewFeedback, error) {
	if prID <= 0 {
		return nil, errors.New("prID must be greater than 0")
	}
	if afterCommentID <= 0 {
		return nil, errors.New("afterCommentID must be greater than 0")
	}

	var feedbacks []*models.ReviewFeedback
	err := r.db.Where("pr_id = ? AND github_comment_id > ? AND github_comment_id IS NOT NULL", prID, afterCommentID).
		Order("created_at DESC").
		Find(&feedbacks).Error
	if err != nil {
		return nil, err
	}
	return feedbacks, nil
}
