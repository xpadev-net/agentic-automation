package services

import (
	"context"
	"errors"
	"fmt"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/utils"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// BlockedTaskResolver locates tasks (issues) that have become unblocked
// given the current blocker graph and issue states in the database.
//
// It does NOT perform any side effects (no job trigger). Triggering is handled
// separately in T128.
type BlockedTaskResolver interface {
	// FindUnblockedTasks returns issues whose prerequisites are all closed.
	// The evaluation uses the persisted blocker graph and current issue states.
	//
	// eventIssueID is the issue that triggered the evaluation (closed/reopened),
	// but the resolver may return any issues that are now unblocked.
	FindUnblockedTasks(ctx context.Context, eventIssueID int64) ([]models.Issue, error)
}

// blockedTaskResolver is the concrete implementation.
type blockedTaskResolver struct {
	issues *repositories.IssueRepository
	edges  *repositories.BlockerGraphRepository
	agents repositories.AgentRunRepository
}

// NewBlockedTaskResolver constructs a BlockedTaskResolver.
// All dependencies are required.
func NewBlockedTaskResolver(
	issues *repositories.IssueRepository,
	edges *repositories.BlockerGraphRepository,
	agents repositories.AgentRunRepository,
) BlockedTaskResolver {
	if issues == nil {
		panic("issues repository is required for BlockedTaskResolver")
	}
	if edges == nil {
		panic("blocker graph repository is required for BlockedTaskResolver")
	}
	if agents == nil {
		panic("agent run repository is required for BlockedTaskResolver")
	}
	return &blockedTaskResolver{issues: issues, edges: edges, agents: agents}
}

// isIssueClosed returns true if the issue state is 'closed'.
func isIssueClosed(issue *models.Issue) bool {
	if issue == nil {
		return false
	}
	return issue.State == "closed"
}

// FindUnblockedTasks implements BlockedTaskResolver.
func (r *blockedTaskResolver) FindUnblockedTasks(ctx context.Context, eventIssueID int64) ([]models.Issue, error) {
	// Validate inputs early
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Load all edges to build an in-memory dependency graph.
	allEdges, err := r.edges.GetAllEdges()
	if err != nil {
		return nil, fmt.Errorf("failed to load blocker graph edges: %w", err)
	}
	graph := utils.FromEdges(allEdges)

	logger := config.GetLogger().With(zap.String("component", "blocked_task"))
	const snapshotLimit = 100 * 1024 // 100KB
	graphJSON, truncated, hash := utils.BuildGraphSnapshot(graph, snapshotLimit)
	logger.Info("blocked_task.resolution_started",
		zap.Int64("eventIssueID", eventIssueID),
		zap.Int("nodeCount", len(graph.Nodes())),
		zap.Int("edgeCount", len(graph.Edges())),
		zap.String("graphHash", hash),
		zap.Bool("snapshotTruncated", truncated),
	)
	logger.Debug("blocked_task.graph_snapshot", zap.String("graphSnapshot", graphJSON))

	// Build the set of closed issues (by DB state)
	closedIssues, err := r.issues.FindByState("closed")
	if err != nil {
		return nil, fmt.Errorf("failed to load closed issues: %w", err)
	}
	closedSet := make(map[int]struct{}, len(closedIssues))
	for _, is := range closedIssues {
		closedSet[is.ID] = struct{}{}
	}

	// Compute candidate nodes that are unblocked given closedSet
	candidateIDs := graph.UnblockedGiven(closedSet)

	shownCandidateIDs, candTrunc := truncateIDsForInfo(candidateIDs, 50)
	logger.Info("blocked_task.candidate_ids",
		zap.Int("count", len(candidateIDs)),
		zap.Ints("ids", shownCandidateIDs),
		zap.Bool("idsTruncated", candTrunc),
	)

	// Exclude issues that are themselves still open (must be open to proceed)?
	// For the purpose of T126, we only require that prerequisites are closed.
	// We'll filter by AgentRun presence next.

	// Load issues for candidates and filter those that have active/finished runs
	result := make([]models.Issue, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		iss, findErr := r.issues.FindByID(id)
		if findErr != nil {
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				// record truly doesn't exist; skip
				continue
			}
			// unexpected data access error: propagate so caller can retry/surface
			return nil, fmt.Errorf("failed to load issue %d: %w", id, findErr)
		}

		// Only consider issues that are still open; skip closed ones
		if iss.State != "open" {
			continue
		}

		// Skip if this issue has any AgentRun in queued/started/succeeded
		runs, runsErr := r.agents.GetByIssueID(iss.ID)
		if runsErr != nil {
			return nil, fmt.Errorf("failed to load agent runs for issue %d: %w", iss.ID, runsErr)
		}
		hasActiveOrCompleted := false
		for _, run := range runs {
			if run == nil {
				continue
			}
			s := run.State
			if s == "queued" || s == "started" || s == "succeeded" {
				hasActiveOrCompleted = true
				break
			}
		}
		if hasActiveOrCompleted {
			continue
		}

		// Debug incoming/outgoing edges for this candidate
		logger.Debug("blocked_task.candidate_edges",
			zap.Int("taskId", iss.ID),
			zap.Ints("incomingEdges", graph.DependenciesOf(iss.ID)),
			zap.Ints("outgoingEdges", graph.DependentsOf(iss.ID)),
		)

		// Keep the candidate
		result = append(result, *iss)
	}

	finalIDs := make([]int, 0, len(result))
	for _, iss := range result {
		finalIDs = append(finalIDs, iss.ID)
	}
	shownFinalIDs, finalTrunc := truncateIDsForInfo(finalIDs, 50)
	logger.Info("blocked_task.unblocked_found",
		zap.Int("count", len(finalIDs)),
		zap.Ints("ids", shownFinalIDs),
		zap.Bool("idsTruncated", finalTrunc),
		zap.String("reason", "all_dependencies_completed"),
	)

	return result, nil
}

// truncateIDsForInfo trims an int slice to at most limit elements and reports whether truncation occurred.
func truncateIDsForInfo(ids []int, limit int) ([]int, bool) {
	if limit <= 0 || len(ids) <= limit {
		return ids, false
	}
	return ids[:limit], true
}

// The following helper stubs are placeholders for T128 integration points.
// They are not used yet but kept to centralize log formats for resume events.
func logResumeAttempt(logger *zap.Logger, issueID int64, taskID int, agentType string, retryCount int) {
	if logger == nil {
		return
	}
	logger.Info("blocked_task.resume_attempt",
		zap.Int64("issueId", issueID),
		zap.Int("taskId", taskID),
		zap.String("agentType", agentType),
		zap.Int("retryCount", retryCount),
	)
}

func logResumeScheduled(logger *zap.Logger, issueID int64, taskID int, jobName string) {
	if logger == nil {
		return
	}
	logger.Info("blocked_task.resume_scheduled",
		zap.Int64("issueId", issueID),
		zap.Int("taskId", taskID),
		zap.String("jobName", jobName),
	)
}
