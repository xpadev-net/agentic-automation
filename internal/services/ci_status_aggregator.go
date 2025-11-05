package services

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"context"
	"strconv"
	"time"

	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
)

// CIStatusAggregator provides CI aggregation per PR at latest head SHA.
type CIStatusAggregator interface {
	// AggregateAndStore fetches check runs for the given checkSuiteID/headSHA and stores an aggregated record.
	AggregateAndStore(ctx context.Context, owner, repo string, prID int, checkSuiteID int64, headSHA string) (*models.CIStatus, error)
}

type ciStatusAggregator struct {
	gh     *clients.Client
	repo   *repositories.CIStatusRepository
	logger *zap.Logger
}

func NewCIStatusAggregator(gh *clients.Client, repo *repositories.CIStatusRepository, logger *zap.Logger) CIStatusAggregator {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ciStatusAggregator{gh: gh, repo: repo, logger: logger}
}

func (s *ciStatusAggregator) AggregateAndStore(ctx context.Context, owner, repo string, prID int, checkSuiteID int64, headSHA string) (*models.CIStatus, error) {
	// Retrieve all check runs for the check suite (headSHA is informational here)
	runs, err := s.gh.ListCheckRunsForCheckSuite(ctx, owner, repo, checkSuiteID)
	if err != nil {
		return nil, err
	}

	agg := AggregateFromRuns(runs)

	// Upsert aggregated status as a synthetic CIStatus row identified by (pr_id, check_suite_id)
	now := time.Now()
	conclusion := aggToConclusionPtr(agg.Aggregated)
	ci := &models.CIStatus{
		PRID:         prID,
		CheckSuiteID: strconv.FormatInt(checkSuiteID, 10),
		Name:         "aggregated",
		Status:       "completed",
		Conclusion:   conclusion,
		CompletedAt:  &now,
	}

	if err := s.repo.CreateOrUpdate(ci); err != nil {
		return nil, err
	}
	s.logger.Info("CI aggregated status stored",
		zap.Int("pr_id", prID),
		zap.Int64("check_suite_id", checkSuiteID),
		zap.String("aggregated", agg.Aggregated),
		zap.Int("total", agg.Total),
		zap.Int("success", agg.SuccessCount),
		zap.Int("failed", agg.FailedCount),
		zap.Int("pending", agg.PendingCount),
	)

	return ci, nil
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
