package services

import (
	"encoding/json"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
)

type agentRunFailurePayload struct {
	RunID        int    `json:"run_id"`
	AgentType    string `json:"agent_type"`
	ErrorSummary string `json:"error_summary"`
	LogExcerpt   string `json:"log_excerpt"`
}

func RecordAgentRunFailure(run *models.AgentRun, summary, excerpt string) error {
	db := config.GetDB()
	payload := agentRunFailurePayload{
		RunID:        run.ID,
		AgentType:    run.AgentType,
		ErrorSummary: summary,
		LogExcerpt:   excerpt,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	entry := &models.AuditLog{
		EventType:    "agent-run.failed",
		Actor:        "operator",
		ResourceType: "AgentRun",
		ResourceID:   run.ID,
		Payload:      string(b),
	}
	return db.Create(entry).Error
}
