package handlers

import (
	"context"

	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
)

// blockedTaskResolverAdapter adapts services.BlockedTaskResolver to
// handlers.BlockedTaskResolver expected by issues handler.
type blockedTaskResolverAdapter struct {
	resolver services.BlockedTaskResolver
	issues   *repositories.IssueRepository
}

// NewBlockedTaskResolverAdapter returns an adapter that implements the
// handlers.BlockedTaskResolver interface by delegating to services.BlockedTaskResolver.
func NewBlockedTaskResolverAdapter(
	resolver services.BlockedTaskResolver,
	issues *repositories.IssueRepository,
) BlockedTaskResolver {
	return &blockedTaskResolverAdapter{resolver: resolver, issues: issues}
}

// ResolveAndMaybeTrigger fulfills handlers.BlockedTaskResolver by resolving
// the DB issue ID then invoking services.BlockedTaskResolver.FindUnblockedTasks.
//
// Note: T126 only resolves; actual triggering is handled in T128.
func (a *blockedTaskResolverAdapter) ResolveAndMaybeTrigger(ctx context.Context, owner, repo string, issueNumber int) error {
	if ctx == nil {
		return nil
	}
	if a == nil || a.resolver == nil || a.issues == nil {
		return nil
	}

	// Map owner/repo + issueNumber to DB issue ID
	repoKey := owner + "/" + repo
	issue, err := a.issues.FindByRepoAndNumber(repoKey, issueNumber)
	if err != nil {
		// Let handler decide how to surface errors; return to be logged as accepted_with_errors
		return err
	}

	// Delegate to service resolver (ignore returned issues here; T128 will trigger jobs)
	_, err = a.resolver.FindUnblockedTasks(ctx, int64(issue.ID))
	return err
}
