package services

import (
	"context"
	"errors"
	"fmt"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/utils"
	"encoding/json"

	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	logger := config.GetLogger().With(config.String("component", "blocked_task"))
	const snapshotLimit = 100 * 1024 // 100KB
	graphJSON, truncated, hash := utils.BuildGraphSnapshot(graph, snapshotLimit)
	logger.Info("blocked_task.resolution_started",
		config.Int64("eventIssueID", eventIssueID),
		config.Int("nodeCount", len(graph.Nodes())),
		config.Int("edgeCount", len(graph.Edges())),
		config.String("graphHash", hash),
		config.Bool("snapshotTruncated", truncated),
	)
	logger.Debug("blocked_task.graph_snapshot", config.String("graphSnapshot", graphJSON))

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
		config.Int("count", len(candidateIDs)),
		config.Ints("ids", shownCandidateIDs),
		config.Bool("idsTruncated", candTrunc),
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
			config.Int("taskId", iss.ID),
			config.Ints("incomingEdges", graph.DependenciesOf(iss.ID)),
			config.Ints("outgoingEdges", graph.DependentsOf(iss.ID)),
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
		config.Int("count", len(finalIDs)),
		config.Ints("ids", shownFinalIDs),
		config.Bool("idsTruncated", finalTrunc),
		config.String("reason", "all_dependencies_completed"),
	)

	return result, nil
}

// TriggerJobsForUnblockedTasks resolves unblocked issues and triggers Kubernetes Jobs for each.
// It uses the provided dependencies to ensure idempotent behavior and proper state transitions.
// Note: This function intentionally does not write OperationLog entries due to current enum constraints.
func TriggerJobsForUnblockedTasks(
	ctx context.Context,
	resolver BlockedTaskResolver,
	issues *repositories.IssueRepository,
	agents repositories.AgentRunRepository,
	jobService KubernetesJobService,
	stateMachine AgentRunStateMachine,
	issueCtxSvc *IssueContextService,
	ghClient *clients.Client,
	owner, repo string,
	eventIssueDBID int64,
) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	if resolver == nil || issues == nil || agents == nil || jobService == nil || stateMachine == nil || issueCtxSvc == nil || ghClient == nil {
		return errors.New("missing dependencies for TriggerJobsForUnblockedTasks")
	}

	// Resolve unblocked issues based on the current graph and DB state
	logger := config.LoggerWithTraceIDs(ctx).With(config.String("component", "blocked_task"))
	logger.Info("blocked_task.resume_evaluation_started",
		config.Int64("eventIssueID", eventIssueDBID),
		config.String("owner", owner),
		config.String("repo", repo),
	)
	candidates, err := resolver.FindUnblockedTasks(ctx, eventIssueDBID)
	if err != nil {
		return err
	}

	// Process each candidate
	evaluatedCount := 0
	jobsTriggered := 0
	for _, is := range candidates {
		evaluatedCount++
		// Respect context cancellation
		if err := ctx.Err(); err != nil {
			return err
		}

		// Filter by repository (owner/repo)
		if is.Repo != owner+"/"+repo {
			logger.Info("blocked_task.resume_repo_mismatch",
				config.Int("taskId", is.ID),
				config.String("issueRepo", is.Repo),
				config.String("owner", owner),
				config.String("repo", repo),
			)
			continue
		}

		// Double-check active/completed runs to avoid duplicate starts
		runs, runsErr := agents.GetByIssueID(is.ID)
		if runsErr != nil {
			return fmt.Errorf("failed to load agent runs for issue %d: %w", is.ID, runsErr)
		}
		skip := false
		skippedStates := make([]string, 0, len(runs))
		for _, run := range runs {
			if run == nil {
				continue
			}
			s := run.State
			if s == "queued" || s == "started" || s == "succeeded" {
				skip = true
				if len(skippedStates) < 3 {
					skippedStates = append(skippedStates, s)
				}
				break
			}
		}
		if skip {
			logger.Info("blocked_task.resume_skipped_existing_run",
				config.Int("taskId", is.ID),
				config.Strings("agentRunStates", skippedStates),
			)
			continue
		}

		// Collect issue context and build prompt
		issueCtx, ctxErr := issueCtxSvc.CollectIssueContext(ctx, owner, repo, is.Number)
		if ctxErr != nil {
			logger.Error("blocked_task.issue_context_failed",
				config.Int("taskId", is.ID),
				config.Int("issueNumber", is.Number),
				config.Error(ctxErr),
			)
			return fmt.Errorf("failed to collect issue context for #%d: %w", is.Number, ctxErr)
		}
		logger.Info("blocked_task.issue_context_collected",
			config.Int("taskId", is.ID),
			config.Int("issueNumber", is.Number),
			config.Int("labelsCount", len(issueCtx.Labels)),
			config.Int("commentsCount", len(issueCtx.Comments)),
		)
		prompt := issueCtxSvc.FormatPrompt(issueCtx, "") // No user instruction for blocked task resume

		// Respect labels from freshly fetched issue context for agent detection
		labelsJSON, _ := json.Marshal(issueCtx.Labels)
		updatedIssue := is
		updatedIssue.Labels = string(labelsJSON)
		_ = issues.Update(&updatedIssue)

		// Detect agent type using updated labels
		agentType := NewAgentTypeDetectorService(nil).DetectAgentType(&updatedIssue)

		// Prepare input payload (schema v1)
		inputPayload := map[string]any{
			"schema_version": "1",
			"prompt":         prompt,
			"agent_type":     agentType,
			"issue": map[string]any{
				"repo":           is.Repo,
				"number":         is.Number,
				"has_body":       issueCtx.Body != "",
				"labels":         issueCtx.Labels,
				"comments_count": len(issueCtx.Comments),
			},
		}
		inputBytes, _ := json.Marshal(inputPayload)

		// Create or get AgentRun using deterministic idempotency key per issue
		idemp := fmt.Sprintf("unblock:%s#%d", is.Repo, is.Number)
		newRun := &models.AgentRun{
			IssueID:   is.ID,
			State:     "queued",
			AgentType: agentType,
			Input:     inputBytes,
			Output:    []byte("{}"),
		}
		agentRun, isNew, createErr := agents.CreateOrGet(idemp, newRun)
		if createErr != nil {
			return fmt.Errorf("failed to create agent run: %w", createErr)
		}
		logger.Info("blocked_task.agent_run_upserted",
			config.Int("taskId", is.ID),
			config.Int("agentRunId", agentRun.ID),
			config.Bool("isNew", isNew),
			config.String("agentType", agentType),
		)

		// If an existing run already exists, branch by state to avoid duplicate jobs
		if !isNew {
			switch agentRun.State {
			case "queued":
				// Update prompt and agent type to latest context, then proceed
				agentRun.AgentType = agentType
				agentRun.Input = inputBytes
				if err := agents.Update(agentRun); err != nil {
					return fmt.Errorf("failed to update existing queued run %d: %w", agentRun.ID, err)
				}
			case "started", "succeeded", "failed":
				// Another worker already started or finished this issue; skip
				continue
			}
		}

		// Transition to started before Job creation (align with comment-trigger flow)
		logger.Info("blocked_task.run_transition_started",
			config.Int("agentRunId", agentRun.ID),
			config.String("from", "queued"),
			config.String("to", "started"),
		)
		if err := stateMachine.TransitionToStarted(agentRun.ID); err != nil {
			logger.Error("blocked_task.run_transition_failed",
				config.Int("agentRunId", agentRun.ID),
				config.Error(err),
			)
			return fmt.Errorf("failed to transition run %d to started: %w", agentRun.ID, err)
		}

		// Create Job
		if job, err := jobService.CreateJobForAgentRun(ctx, agentRun, &updatedIssue, prompt, ""); err != nil {
			// If a job with same name already exists, treat as success (another handler created it)
			if apierrors.IsAlreadyExists(err) {
				logger.Info("blocked_task.job_already_exists",
					config.Int("agentRunId", agentRun.ID),
					config.Int("taskId", is.ID),
				)
				continue
			}
			// Rollback to queued only when Job was not created
			_ = stateMachine.TransitionToQueued(agentRun.ID)
			logger.Error("blocked_task.job_creation_failed",
				config.Int("agentRunId", agentRun.ID),
				config.Int("taskId", is.ID),
				config.Error(err),
			)
			return fmt.Errorf("failed to create job for run %d: %w", agentRun.ID, err)
		} else {
			jobsTriggered++
			logger.Info("blocked_task.job_created",
				config.Int("agentRunId", agentRun.ID),
				config.Int("taskId", is.ID),
				config.String("jobName", job.Name),
			)
		}
	}

	logger.Info("blocked_task.resume_evaluation_completed",
		config.Int("evaluatedCount", evaluatedCount),
		config.Int("jobsTriggered", jobsTriggered),
	)
	return nil
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
func logResumeAttempt(logger *config.AppLogger, issueID int64, taskID int, agentType string, retryCount int) {
	if logger == nil {
		return
	}
	logger.Info("blocked_task.resume_attempt",
		config.Int64("issueId", issueID),
		config.Int("taskId", taskID),
		config.String("agentType", agentType),
		config.Int("retryCount", retryCount),
	)
}

func logResumeScheduled(logger *config.AppLogger, issueID int64, taskID int, jobName string) {
	if logger == nil {
		return
	}
	logger.Info("blocked_task.resume_scheduled",
		config.Int64("issueId", issueID),
		config.Int("taskId", taskID),
		config.String("jobName", jobName),
	)
}
