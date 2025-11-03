package handlers

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"errors"
	"fmt"
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
	PRURL      string `json:"pr_url,omitempty"`
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

	// Validate required fields for succeeded status
	if req.Status == "succeeded" {
		if req.PRNumber == nil || *req.PRNumber <= 0 {
			logger.Warn("Missing or invalid PR number for succeeded status",
				zap.Int("agent_run_id", agentRunID),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "INVALID_REQUEST",
				"message": "pr_number is required and must be > 0 when status is succeeded",
			})
			return
		}
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

	// Prepare values common to both success and failure
	now := time.Now()
	agentRun.State = req.Status
	agentRun.AgentType = req.AgentType
	agentRun.CompletedAt = &now

	var prURL string

	if req.Status == "succeeded" {
		// Load Issue for repo to build PR URL
		issueRepo := repositories.NewIssueRepository()
		issue, err := issueRepo.FindByID(agentRun.IssueID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warn("Issue not found for AgentRun",
					zap.Int("agent_run_id", agentRunID),
					zap.Int("issue_id", agentRun.IssueID),
				)
				c.JSON(http.StatusNotFound, gin.H{
					"error":   "ISSUE_NOT_FOUND",
					"message": "Issue not found for the agent run",
				})
				return
			}
			logger.Error("Failed to load Issue for AgentRun",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.Int("issue_id", agentRun.IssueID),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to load related issue",
			})
			return
		}

		// Upsert PullRequest and link to AgentRun
		prRepo := repositories.NewPullRequestRepository(db)
		pr := &models.PullRequest{
			Repo:    issue.Repo,
			Number:  *req.PRNumber,
			IssueID: &agentRun.IssueID,
			Branch:  req.Branch,
			Status:  "open",
		}
		if err := prRepo.Upsert(pr); err != nil {
			logger.Error("Failed to upsert PullRequest",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.String("repo", issue.Repo),
				zap.Int("pr_number", *req.PRNumber),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to upsert pull request",
			})
			return
		}

		// Reload PR to obtain ID (Upsert doesn't mutate ID reliably)
		savedPR, err := prRepo.FindByRepoAndNumber(issue.Repo, *req.PRNumber)
		if err != nil {
			logger.Error("Failed to load PullRequest after upsert",
				zap.Error(err),
				zap.Int("agent_run_id", agentRunID),
				zap.String("repo", issue.Repo),
				zap.Int("pr_number", *req.PRNumber),
			)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Failed to load pull request",
			})
			return
		}

		// Link PR to AgentRun and build PR URL
		agentRun.PRID = &savedPR.ID
		prURL = fmt.Sprintf("https://github.com/%s/pull/%d", issue.Repo, savedPR.Number)
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
	if prURL != "" {
		logger.Info("Agent execution report received",
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("agent_type", req.AgentType),
			zap.String("pr_url", prURL),
			zap.String("path", c.Request.URL.Path),
		)
	} else {
		logger.Info("Agent execution report received",
			zap.Int("agent_run_id", agentRunID),
			zap.String("status", req.Status),
			zap.String("agent_type", req.AgentType),
			zap.String("path", c.Request.URL.Path),
		)
	}

	// Return success response
	c.JSON(http.StatusOK, ReportResponse{
		Message:    "Report received, AgentRun #" + idStr + " updated to " + req.Status,
		AgentRunID: agentRunID,
		PRURL:      prURL,
	})
}
