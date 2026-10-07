package handlers

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/loghub"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Limits for the log ingestion endpoint.
const (
	maxLogEntriesPerRequest = 500
	maxLogLineBytes         = 8192
	maxLogRequestBodyBytes  = 4 << 20 // 4 MiB
)

// LogEntryRequest is one log line in the ingestion batch.
type LogEntryRequest struct {
	Seq  int64      `json:"seq" binding:"required,min=1"`
	TS   *time.Time `json:"ts,omitempty"`
	Line string     `json:"line"`
}

// AgentRunLogsRequest is the request body of POST /api/agent-runs/:id/logs.
type AgentRunLogsRequest struct {
	Entries []LogEntryRequest `json:"entries" binding:"required,min=1,dive"`
}

// HandleAgentRunLogs ingests batched log lines from an agent-runner pod.
// Bearer-authenticated like the report endpoint; entries are stored
// idempotently on (agent_run_id, seq) and published to the live log hub.
func HandleAgentRunLogs(c *gin.Context) {
	logger := config.GetLogger()

	runID, err := strconv.Atoi(c.Param("id"))
	if err != nil || runID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_AGENT_RUN_ID",
			"message": "agent run id must be a positive integer",
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLogRequestBodyBytes)
	var req AgentRunLogsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "request body must be JSON with a non-empty entries array",
		})
		return
	}
	if len(req.Entries) > maxLogEntriesPerRequest {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "TOO_MANY_ENTRIES",
			"message": "entries exceeds the per-request maximum",
		})
		return
	}

	db := config.GetDB()
	runRepo := repositories.NewAgentRunRepository(db)
	run, err := runRepo.GetByID(runID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "AGENT_RUN_NOT_FOUND",
				"message": "agent run not found",
			})
			return
		}
		logger.Error("Failed to load agent run for log ingestion",
			config.Int("agent_run_id", runID), config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "failed to load agent run",
		})
		return
	}

	logs := make([]*models.AgentRunLog, 0, len(req.Entries))
	for _, entry := range req.Entries {
		line := entry.Line
		if len(line) > maxLogLineBytes {
			line = line[:maxLogLineBytes] + "…[truncated]"
		}
		logs = append(logs, &models.AgentRunLog{
			AgentRunID: run.ID,
			Seq:        entry.Seq,
			TS:         entry.TS,
			Line:       line,
		})
	}

	logRepo := repositories.NewAgentRunLogRepository(db)
	stored, err := logRepo.CreateBatch(logs)
	if err != nil {
		logger.Error("Failed to store agent run logs",
			config.Int("agent_run_id", runID), config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "failed to store logs",
		})
		return
	}

	for _, l := range logs {
		loghub.Publish(loghub.Event{
			AgentRunID: l.AgentRunID,
			Seq:        l.Seq,
			TS:         l.TS,
			Line:       l.Line,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "logs ingested",
		"agent_run_id": runID,
		"received":     len(req.Entries),
		"stored":       stored,
	})
}
