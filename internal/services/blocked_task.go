package services

import (
	"context"
	"errors"
	"fmt"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/utils"
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
			// If the record vanished or cannot be loaded, skip safely
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

		// Keep the candidate
		result = append(result, *iss)
	}

	return result, nil
}
