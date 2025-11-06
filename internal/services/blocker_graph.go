package services

import (
	"context"
	"fmt"
	"time"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"go.uber.org/zap"
)

// BlockerGraphBuilder builds and persists blocker graph edges for issues.
// It consumes dependency data from GitHub (blocked_by) and normalizes it
// into DB records via repositories.
type BlockerGraphBuilder interface {
	// BuildForIssue fetches blocked_by dependencies for (owner/repo #issueNumber),
	// ensures referenced issues exist/upserted, and persists edges (diff-based replace).
	BuildForIssue(ctx context.Context, owner, repo string, issueNumber int) error
}

type blockerGraphBuilder struct {
	fetcher IssueDependencyFetcher
	issues  *repositories.IssueRepository
	edges   *repositories.BlockerGraphRepository
	logger  *zap.Logger
}

// NewBlockerGraphBuilder constructs a BlockerGraphBuilder.
// All dependencies are required; logger may be nil and will default to zap.NewNop().
func NewBlockerGraphBuilder(
	fetcher IssueDependencyFetcher,
	issues *repositories.IssueRepository,
	edges *repositories.BlockerGraphRepository,
	logger *zap.Logger,
) BlockerGraphBuilder {
	if fetcher == nil {
		panic("fetcher is required for BlockerGraphBuilder")
	}
	if issues == nil {
		panic("issue repository is required for BlockerGraphBuilder")
	}
	if edges == nil {
		panic("edge repository is required for BlockerGraphBuilder")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &blockerGraphBuilder{fetcher: fetcher, issues: issues, edges: edges, logger: logger}
}

// BuildForIssue builds blocker graph edges for the specified issue by replacing
// current edges with those derived from GitHub "blocked_by" dependencies.
func (b *blockerGraphBuilder) BuildForIssue(ctx context.Context, owner, repo string, issueNumber int) error {
	start := time.Now()
	repoKey := makeRepoKey(owner, repo)

	if err := ctx.Err(); err != nil {
		return err
	}

	// Upsert root issue (owner/repo#number)
	root, err := b.issues.FindByRepoAndNumber(repoKey, issueNumber)
	if err != nil {
		// Create on not found; IssueRepository.FindByRepoAndNumber returns gorm.ErrRecordNotFound
		// which we cannot import here; just attempt an upsert path.
		root = &models.Issue{Repo: repoKey, Number: issueNumber}
		if upErr := b.issues.Upsert(root); upErr != nil {
			b.logger.Error("failed to upsert root issue", zap.String("repo", repoKey), zap.Int("number", issueNumber), zap.Error(upErr))
			return upErr
		}
		// reload to ensure ID populated (Upsert may have created it)
		root, err = b.issues.FindByRepoAndNumber(repoKey, issueNumber)
		if err != nil {
			b.logger.Error("failed to load root issue after upsert", zap.String("repo", repoKey), zap.Int("number", issueNumber), zap.Error(err))
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Fetch dependencies (blocked_by)
	deps, err := b.fetcher.ListBlockedBy(ctx, owner, repo, issueNumber)
	if err != nil {
		b.logger.Error("failed to fetch blocked_by list", zap.String("owner", owner), zap.String("repo", repo), zap.Int("issue_number", issueNumber), zap.Error(err))
		return err
	}

	// Upsert dependency issues and prepare desired edges
	desired := make(map[int]struct{}) // DependsOnTaskID set for root.TaskID
	createdIssues := 0
	for _, d := range deps {
		if err := ctx.Err(); err != nil {
			return err
		}
		depRepoKey := makeRepoKey(d.Owner, d.Repo)
		depIssue := &models.Issue{Repo: depRepoKey, Number: d.Number}
		// Optional fields
		if d.Title != "" {
			title := d.Title
			depIssue.Title = title
		}
		if d.State != "" {
			depIssue.State = d.State
		}
		if upErr := b.issues.Upsert(depIssue); upErr != nil {
			b.logger.Error("failed to upsert dependency issue", zap.String("repo", depRepoKey), zap.Int("number", d.Number), zap.Error(upErr))
			return upErr
		}
		// Ensure we have ID
		loaded, findErr := b.issues.FindByRepoAndNumber(depRepoKey, d.Number)
		if findErr != nil {
			b.logger.Error("failed to load dependency issue after upsert", zap.String("repo", depRepoKey), zap.Int("number", d.Number), zap.Error(findErr))
			return findErr
		}
		desired[loaded.ID] = struct{}{}
		createdIssues++
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Load existing edges for the task (root issue)
	existingEdges, err := b.edges.GetDependenciesForTask(root.ID)
	if err != nil {
		b.logger.Error("failed to load existing edges", zap.Int("task_id", root.ID), zap.Error(err))
		return err
	}

	// Compute diff (delete obsolete, create missing)
	toDelete := make([][2]int, 0)
	existingSet := make(map[int]struct{})
	for _, e := range existingEdges {
		existingSet[e.DependsOnTaskID] = struct{}{}
		if _, ok := desired[e.DependsOnTaskID]; !ok {
			toDelete = append(toDelete, [2]int{e.TaskID, e.DependsOnTaskID})
		}
	}

	toCreate := make([]models.BlockerGraphEdge, 0)
	for depID := range desired {
		if _, ok := existingSet[depID]; !ok {
			toCreate = append(toCreate, models.BlockerGraphEdge{TaskID: root.ID, DependsOnTaskID: depID})
		}
	}

	// Apply deletes
	for _, pair := range toDelete {
		if err := ctx.Err(); err != nil {
			return err
		}
		if delErr := b.edges.DeleteEdge(pair[0], pair[1]); delErr != nil {
			b.logger.Error("failed to delete obsolete edge", zap.Int("task_id", pair[0]), zap.Int("depends_on_task_id", pair[1]), zap.Error(delErr))
			return delErr
		}
	}

	// Apply creates (idempotent in repository)
	if len(toCreate) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if crtErr := b.edges.CreateEdges(toCreate); crtErr != nil {
			b.logger.Error("failed to create edges", zap.Int("task_id", root.ID), zap.Int("count", len(toCreate)), zap.Error(crtErr))
			return crtErr
		}
	}

	b.logger.Info("blocker graph updated",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.Int("task_id", root.ID),
		zap.Int("deps_fetched", len(deps)),
		zap.Int("edges_deleted", len(toDelete)),
		zap.Int("edges_created", len(toCreate)),
		zap.Duration("elapsed", time.Since(start)),
	)

	return nil
}

func makeRepoKey(owner, repo string) string {
	if owner == "" || repo == "" {
		return fmt.Sprintf("%s/%s", owner, repo)
	}
	return owner + "/" + repo
}
