package handlers

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ReportRequest represents the request body for agent execution report
type ReportRequest struct {
	Status       string `json:"status" binding:"required,oneof=succeeded failed"`
	AgentType    string `json:"agent_type" binding:"required,oneof=claude-code cursor-agents"`
	PRNumber     *int   `json:"pr_number,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Logs         string `json:"logs,omitempty"`
}

// ReportResponse represents the response for successful report
type ReportResponse struct {
	Message    string `json:"message"`
	AgentRunID int    `json:"agent_run_id"`
}

// HandleAgentReport handles POST /api/agent-runs/:id/report requests
// It receives execution results from agent-runner Pods and updates AgentRun state
func HandleAgentReport(c *gin.Context) {
	logger := config.GetLogger()

	// Get AgentRun ID from path parameter
	idStr := c.Param("id")
	agentRunID, err := strconv.Atoi(idStr)
	if err != nil {
		logger.Warn("Invalid agent run ID in path",
			zap.String("id", idStr),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid agent run ID",
		})
		return
	}

	// Parse request body
	var req ReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Get database connection and repository
	db := config.GetDB()
	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Get AgentRun by ID
	agentRun, err := agentRunRepo.GetByID(agentRunID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("AgentRun not found",
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "AGENT_RUN_NOT_FOUND",
				"message": "AgentRun with ID " + idStr + " not found",
			})
			return
		}

		logger.Error("Failed to retrieve AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to retrieve agent run",
		})
		return
	}

	// Update AgentRun state and related fields
	now := time.Now()
	agentRun.State = req.Status
	agentRun.AgentType = req.AgentType
	agentRun.CompletedAt = &now

	// Update PR ID if provided (for succeeded status)
	if req.Status == "succeeded" && req.PRNumber != nil {
		prID := *req.PRNumber
		agentRun.PRID = &prID
	}

	// Update commit SHA if provided
	if req.CommitSHA != "" {
		agentRun.CommitSHA = &req.CommitSHA
	}

	// Update error message if provided (for failed status)
	if req.Status == "failed" && req.ErrorMessage != "" {
		agentRun.ErrorMessage = &req.ErrorMessage
	}

	// Save updated AgentRun
	if err := agentRunRepo.Update(agentRun); err != nil {
		logger.Error("Failed to update AgentRun",
			zap.Error(err),
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Failed to update agent run",
		})
		return
	}

	// Log successful report
	logger.Info("Agent execution report received",
		zap.Int("agent_run_id", agentRunID),
		zap.String("status", req.Status),
		zap.String("agent_type", req.AgentType),
		zap.String("path", c.Request.URL.Path),
	)

	// Return success response
	c.JSON(http.StatusOK, ReportResponse{
		Message:    "Report received, AgentRun #" + idStr + " updated to " + req.Status,
		AgentRunID: agentRunID,
	})
}
