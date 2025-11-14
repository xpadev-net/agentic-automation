package services

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"context"
	"strconv"
	"time"

	"github.com/google/go-github/v76/github"
)

// CIStatusAggregator provides CI aggregation per PR at latest head SHA.
type CIStatusAggregator interface {
	// AggregateAndStore fetches check runs for the given checkSuiteID/headSHA and stores an aggregated record.
	// It validates that the provided headSHA matches the PR's current head before persisting.
	AggregateAndStore(ctx context.Context, owner, repo string, prID int, prNumber int, checkSuiteID int64, headSHA string) (*models.CIStatus, error)
	// AddStatusSignal persists a supplementary status signal (from status webhook) as CIStatus row.
	// This is treated as auxiliary info; check_suite remains the source of truth for aggregation.
	AddStatusSignal(ctx context.Context, prID int, name string, state string, targetURL string) error
}

// GitHubChecks defines the subset of GitHub client methods needed by the aggregator.
type GitHubChecks interface {
	GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*github.PullRequest, error)
	ListCheckRunsForCheckSuite(ctx context.Context, owner, repo string, checkSuiteID int64) ([]*github.CheckRun, error)
}

// CIStatusSaver abstracts persistence for easier testing.
type CIStatusSaver interface {
	CreateOrUpdate(ciStatus *models.CIStatus) error
}

type ciStatusAggregator struct {
	gh     GitHubChecks
	repo   CIStatusSaver
	logger *config.AppLogger
}

func NewCIStatusAggregator(gh *clients.Client, repo *repositories.CIStatusRepository, logger *config.AppLogger) CIStatusAggregator {
	if logger == nil {
		logger = config.NewNopLogger()
	}
	// clients.Client and repositories.CIStatusRepository satisfy the interfaces
	return &ciStatusAggregator{gh: GitHubChecks(gh), repo: CIStatusSaver(repo), logger: logger}
}

// NewCIStatusAggregatorWithDeps allows injecting interface-based dependencies (for tests).
func NewCIStatusAggregatorWithDeps(gh GitHubChecks, saver CIStatusSaver, logger *config.AppLogger) CIStatusAggregator {
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &ciStatusAggregator{gh: gh, repo: saver, logger: logger}
}

func (s *ciStatusAggregator) AggregateAndStore(ctx context.Context, owner, repo string, prID int, prNumber int, checkSuiteID int64, headSHA string) (*models.CIStatus, error) {
	// Validate head SHA matches current PR head
	pr, err := s.gh.GetPullRequest(ctx, owner, repo, prNumber)
	if err != nil {
		s.logger.Warn("Failed to get PR for head SHA validation; skip aggregation",
			config.Error(err),
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("pr_number", prNumber),
		)
		return nil, nil
	}
	if pr == nil || pr.Head == nil || pr.Head.SHA == nil {
		s.logger.Warn("PR head SHA not available; skip aggregation",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("pr_number", prNumber),
		)
		return nil, nil
	}
	current := *pr.Head.SHA
	if current != headSHA {
		s.logger.Info("Stale check_suite ignored due to head SHA mismatch",
			config.Int("pr_id", prID),
			config.Int("pr_number", prNumber),
			config.Int64("check_suite_id", checkSuiteID),
			config.String("suite_head_sha", headSHA),
			config.String("current_pr_head_sha", current),
		)
		return nil, nil
	}

	// Retrieve all check runs for the (validated) check suite
	runs, err := s.gh.ListCheckRunsForCheckSuite(ctx, owner, repo, checkSuiteID)
	if err != nil {
		return nil, err
	}

	agg := AggregateFromRuns(runs)

	// Upsert aggregated status as a synthetic CIStatus row identified by (pr_id, check_suite_id)
	var completedAt *time.Time
	var conclusion *string
	status := "completed"
	switch agg.Aggregated {
	case "success":
		now := time.Now()
		completedAt = &now
		conclusion = aggToConclusionPtr("success")
		status = "completed"
	case "failed":
		now := time.Now()
		completedAt = &now
		conclusion = aggToConclusionPtr("failed")
		status = "completed"
	default: // pending
		// keep in-progress; do not set conclusion/completedAt
		status = "in_progress"
		completedAt = nil
		conclusion = nil
	}

	ci := &models.CIStatus{
		PRID:         prID,
		CheckSuiteID: strconv.FormatInt(checkSuiteID, 10),
		Name:         "aggregated",
		Status:       status,
		Conclusion:   conclusion,
		CompletedAt:  completedAt,
	}

	if err := s.repo.CreateOrUpdate(ci); err != nil {
		return nil, err
	}
	s.logger.Info("CI aggregated status stored",
		config.Int("pr_id", prID),
		config.Int64("check_suite_id", checkSuiteID),
		config.String("aggregated", agg.Aggregated),
		config.Int("total", agg.Total),
		config.Int("success", agg.SuccessCount),
		config.Int("failed", agg.FailedCount),
		config.Int("pending", agg.PendingCount),
	)

	return ci, nil
}

// AddStatusSignal stores a supplementary CI status row from status webhook.
// Mapping:
//
//	state: success -> completed/success, failure|error -> completed/failure, pending/other -> in_progress
func (s *ciStatusAggregator) AddStatusSignal(ctx context.Context, prID int, name string, state string, targetURL string) error {
	status := "in_progress"
	var conclusion *string
	switch state {
	case "success":
		status = "completed"
		v := "success"
		conclusion = &v
	case "failure", "error":
		status = "completed"
		v := "failure"
		conclusion = &v
	default:
		status = "in_progress"
		conclusion = nil
	}

	var logsURL *string
	if targetURL != "" {
		u := targetURL
		logsURL = &u
	}

	// Prefer updating per-context row to avoid clobbering other contexts
	if saver, ok := s.repo.(*repositories.CIStatusRepository); ok {
		if existing, err := saver.FindByPRIDAndName(prID, name); err == nil && existing != nil {
			existing.Status = status
			existing.Conclusion = conclusion
			existing.LogsURL = logsURL
			return saver.Update(existing)
		}
		ci := &models.CIStatus{PRID: prID, CheckSuiteID: "", Name: name, Status: status, Conclusion: conclusion, LogsURL: logsURL}
		return saver.Create(ci)
	}
	// Fallback to generic upsert
	ci := &models.CIStatus{PRID: prID, CheckSuiteID: "", Name: name, Status: status, Conclusion: conclusion, LogsURL: logsURL}
	return s.repo.CreateOrUpdate(ci)
}

type aggregationResult struct {
	Aggregated   string
	Total        int
	SuccessCount int
	FailedCount  int
	PendingCount int
}

func AggregateFromRuns(runs []*github.CheckRun) aggregationResult {
	res := aggregationResult{}
	for _, r := range runs {
		status := ""
		conclusion := ""
		if r.Status != nil {
			status = *r.Status
		}
		if r.Conclusion != nil {
			conclusion = *r.Conclusion
		}
		mapped := MapConclusion(status, conclusion)
		switch mapped {
		case "success":
			res.SuccessCount++
		case "failed":
			res.FailedCount++
		default: // pending
			res.PendingCount++
		}
		res.Total++
	}

	// Aggregation priority: failed > pending > success
	switch {
	case res.FailedCount > 0:
		res.Aggregated = "failed"
	case res.PendingCount > 0 || res.Total == 0:
		res.Aggregated = "pending"
	default:
		res.Aggregated = "success"
	}
	return res
}

func MapConclusion(status, conclusion string) string {
	switch conclusion {
	case "success":
		return "success"
	case "neutral", "skipped":
		return "success"
	case "cancelled", "timed_out", "action_required", "stale", "failure":
		return "failed"
	}
	if status == "queued" || status == "in_progress" {
		return "pending"
	}
	return "pending"
}

func aggToConclusionPtr(v string) *string {
	switch v {
	case "success":
		s := "success"
		return &s
	case "failed":
		s := "failure"
		return &s
	default:
		// pending -> store as neutral (no exact GitHub conclusion for pending after completed)
		s := "neutral"
		return &s
	}
}
