package services

import (
	"context"
	"fmt"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/utils"
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
	logger  *config.AppLogger
}

// NewBlockerGraphBuilder constructs a BlockerGraphBuilder.
// All dependencies are required; logger may be nil and will default to config.NewNopLogger().
func NewBlockerGraphBuilder(
	fetcher IssueDependencyFetcher,
	issues *repositories.IssueRepository,
	edges *repositories.BlockerGraphRepository,
	logger *config.AppLogger,
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
		logger = config.NewNopLogger()
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
			b.logger.Error("failed to upsert root issue", config.String("repo", repoKey), config.Int("number", issueNumber), config.Error(upErr))
			return upErr
		}
		// reload to ensure ID populated (Upsert may have created it)
		root, err = b.issues.FindByRepoAndNumber(repoKey, issueNumber)
		if err != nil {
			b.logger.Error("failed to load root issue after upsert", config.String("repo", repoKey), config.Int("number", issueNumber), config.Error(err))
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Fetch dependencies (blocked_by)
	deps, err := b.fetcher.ListBlockedBy(ctx, owner, repo, issueNumber)
	if err != nil {
		b.logger.Error("failed to fetch blocked_by list", config.String("owner", owner), config.String("repo", repo), config.Int("issue_number", issueNumber), config.Error(err))
		return err
	}

	// Upsert dependency issues and prepare desired edges
	desired := make(map[int]struct{}) // DependsOnTaskID set for root.TaskID
	for _, d := range deps {
		if err := ctx.Err(); err != nil {
			return err
		}
		depRepoKey := makeRepoKey(d.Owner, d.Repo)
		updates := map[string]interface{}{}
		if d.Title != "" {
			updates["title"] = d.Title
		}
		if d.State != "" {
			updates["state"] = d.State
		}
		if upErr := b.issues.UpsertSelective(depRepoKey, d.Number, updates); upErr != nil {
			b.logger.Error("failed to upsert (selective) dependency issue", config.String("repo", depRepoKey), config.Int("number", d.Number), config.Error(upErr))
			return upErr
		}
		// Ensure we have ID
		loaded, findErr := b.issues.FindByRepoAndNumber(depRepoKey, d.Number)
		if findErr != nil {
			b.logger.Error("failed to load dependency issue after upsert", config.String("repo", depRepoKey), config.Int("number", d.Number), config.Error(findErr))
			return findErr
		}
		desired[loaded.ID] = struct{}{}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Load existing edges for the task (root issue)
	existingEdges, err := b.edges.GetDependenciesForTask(root.ID)
	if err != nil {
		b.logger.Error("failed to load existing edges", config.Int("task_id", root.ID), config.Error(err))
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
			b.logger.Error("failed to delete obsolete edge", config.Int("task_id", pair[0]), config.Int("depends_on_task_id", pair[1]), config.Error(delErr))
			return delErr
		}
	}

	// Apply creates (idempotent in repository)
	if len(toCreate) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if crtErr := b.edges.CreateEdges(toCreate); crtErr != nil {
			b.logger.Error("failed to create edges", config.Int("task_id", root.ID), config.Int("count", len(toCreate)), config.Error(crtErr))
			return crtErr
		}
	}

	// Reload current edges for this task to compute snapshot/hash.
	currentEdges, curErr := b.edges.GetDependenciesForTask(root.ID)
	if curErr != nil {
		b.logger.Error("failed to reload edges for snapshot",
			config.Int("task_id", root.ID),
			config.Error(curErr),
		)
		return curErr
	}

	g := utils.NewDependencyGraph()
	// Ensure root node is present even if it has no edges
	g.AddNode(root.ID)
	for _, e := range currentEdges {
		g.AddEdge(e.DependsOnTaskID, e.TaskID)
	}
	const snapshotLimit = 100 * 1024 // 100KB
	snapshotJSON, truncated, hash := utils.BuildGraphSnapshot(g, snapshotLimit)

	b.logger.Info("blocker_graph.updated",
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
		config.Int("task_id", root.ID),
		config.Int("deps_fetched", len(deps)),
		config.Int("edges_deleted", len(toDelete)),
		config.Int("edges_created", len(toCreate)),
		config.Int("totalEdges", len(currentEdges)),
		config.String("graphHash", hash),
		config.Bool("snapshotTruncated", truncated),
		config.Duration("elapsed", time.Since(start)),
	)

	// Full snapshot only on debug to avoid log bloat
	b.logger.Debug("blocker_graph.snapshot", config.String("graphSnapshot", snapshotJSON))

	return nil
}

func makeRepoKey(owner, repo string) string {
	if owner == "" || repo == "" {
		return fmt.Sprintf("%s/%s", owner, repo)
	}
	return owner + "/" + repo
}
