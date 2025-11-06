package handlers

import (
	"context"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"errors"
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
		return err
	}

	// Build dependencies for triggering
	logger := config.GetLogger()
	db := config.GetDB()

	agentRunRepo := repositories.NewAgentRunRepository(db)

	// Initialize GitHub per-repo client
	if appGitHubClient == nil {
		return errors.New("github app client not initialized")
	}
	rawClient, err := appGitHubClient.ForRepo(ctx, owner, repo)
	if err != nil {
		return err
	}
	ghClient := clients.NewFromGitHub(rawClient, logger)
	issueCtxSvc := services.NewIssueContextService(ghClient, logger)

	// Initialize Kubernetes job service
	k8sClient, err := clients.NewKubernetesClient(logger)
	if err != nil {
		return err
	}
	jobSvc := services.NewKubernetesJobService(k8sClient, logger)

	// State machine
	sm := services.NewAgentRunStateMachine(agentRunRepo, logger)

	// Trigger jobs for unblocked tasks
	return services.TriggerJobsForUnblockedTasks(
		ctx,
		a.resolver,
		a.issues,
		agentRunRepo,
		jobSvc,
		sm,
		issueCtxSvc,
		ghClient,
		owner,
		repo,
		int64(issue.ID),
	)
}
