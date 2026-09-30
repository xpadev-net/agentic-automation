package repositories

import (
	"agentic-automation/internal/models"
	"errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ReserveIssueRun admits at most one active issue-triggered execution. The Issue
// lock serializes separate delivery IDs as well as duplicate webhook deliveries
// across operator replicas. This is deliberately separate from receipt handling.
func ReserveIssueRun(db *gorm.DB, deliveryID string, issueID int) (*models.AgentRun, bool, error) {
	if deliveryID == "" {
		return nil, false, errors.New("delivery ID is required")
	}
	var run models.AgentRun
	admitted := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var issue models.Issue
		// A no-op write takes the per-Issue lock on MySQL and a write reservation
		// on SQLite. Perform it before any snapshot read; no empty-range/gap lock
		// on agent_runs is needed, so unrelated first admissions do not deadlock.
		if err := tx.Model(&models.Issue{}).Where("id = ?", issueID).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return err
		}
		if err := tx.First(&issue, issueID).Error; err != nil {
			return err
		}
		err := tx.Where("idempotency_key = ?", deliveryID).First(&run).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// The snapshot was established after acquiring the parent row lock.
		err = tx.Where("issue_id = ? AND state IN ?", issueID, []string{"queued", "started"}).Order("id").First(&run).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		run = models.AgentRun{IssueID: issueID, IdempotencyKey: deliveryID, State: "queued", ExecutionMode: "plan_creation", Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		name := run.AttemptJobName()
		if err := tx.Model(&run).Update("job_name", name).Error; err != nil {
			return err
		}
		run.JobName = &name
		run.CaptureLifecycle()
		admitted = true
		return nil
	})
	return &run, admitted, err
}
