package repositories

import (
	"agentic-automation/internal/models"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentRunLogRepository persists and queries runner log lines.
type AgentRunLogRepository struct {
	db *gorm.DB
}

// NewAgentRunLogRepository creates a new AgentRunLogRepository.
func NewAgentRunLogRepository(db *gorm.DB) *AgentRunLogRepository {
	return &AgentRunLogRepository{db: db}
}

// CreateBatch inserts log entries, skipping duplicates on (agent_run_id, seq)
// so retries are idempotent. It returns the number of inserted rows.
func (r *AgentRunLogRepository) CreateBatch(logs []*models.AgentRunLog) (int, error) {
	if len(logs) == 0 {
		return 0, nil
	}
	res := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "agent_run_id"}, {Name: "seq"}},
		DoNothing: true,
	}).Create(&logs)
	return int(res.RowsAffected), res.Error
}

// GetAfterSeq returns up to limit log lines for a run ordered by seq,
// restricted to entries with seq greater than afterSeq (0 = from start).
func (r *AgentRunLogRepository) GetAfterSeq(agentRunID int, afterSeq int64, limit int) ([]*models.AgentRunLog, error) {
	var logs []*models.AgentRunLog
	q := r.db.Where("agent_run_id = ?", agentRunID)
	if afterSeq > 0 {
		q = q.Where("seq > ?", afterSeq)
	}
	if err := q.Order("seq ASC").Limit(limit).Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

// CountByAgentRun returns how many log lines are stored for a run.
func (r *AgentRunLogRepository) CountByAgentRun(agentRunID int) (int64, error) {
	var count int64
	err := r.db.Model(&models.AgentRunLog{}).Where("agent_run_id = ?", agentRunID).Count(&count).Error
	return count, err
}

// DeleteOlderThan removes log rows created before cutoff (retention policy).
func (r *AgentRunLogRepository) DeleteOlderThan(cutoff time.Time) (int64, error) {
	res := r.db.Where("created_at < ?", cutoff).Delete(&models.AgentRunLog{})
	return res.RowsAffected, res.Error
}
