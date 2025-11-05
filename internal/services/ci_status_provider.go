package services

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"context"
	"time"

	"go.uber.org/zap"
)

// ciStatusReader is a narrow interface of CIStatusRepository for easier testing.
type ciStatusReader interface {
	FindByPRID(prID int) ([]repositories.CIStatusModel, error)
}

// prFinder is a narrow interface of PullRequestRepository for easier testing.
type prFinder interface {
	FindByRepoAndNumber(repo string, number int) (*repositories.PullRequestModel, error)
}

// NOTE: We use concrete repositories but access them through helper wrappers
// because repository packages expose models via internal/models. To avoid
// importing models here, we define small adapters below.

type ciStatusProvider struct {
	ciRepo *repositories.CIStatusRepository
	prRepo *repositories.PullRequestRepository
	logger *zap.Logger
}

func NewCIStatusProvider(ciRepo *repositories.CIStatusRepository, prRepo *repositories.PullRequestRepository, logger *zap.Logger) CIStatusProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ciStatusProvider{ciRepo: ciRepo, prRepo: prRepo, logger: logger}
}

func (p *ciStatusProvider) GetAggregatedState(ctx context.Context, owner, repo string, prNumber int) (CIState, error) {
	// PullRequestRepository identifies PRs by "owner/repo" in repo column
	fullName := owner + "/" + repo
	pr, err := p.prRepo.FindByRepoAndNumber(fullName, prNumber)
	if err != nil {
		return CIStateUnknown, err
	}

	statuses, err := p.ciRepo.FindByPRID(pr.ID)
	if err != nil {
		return CIStateUnknown, err
	}

	// Pick latest aggregated status: prefer CompletedAt desc, then UpdatedAt desc
	var best *models.CIStatus
	var bestCompletedAt time.Time
	var bestUpdatedAt time.Time
	for i := range statuses {
		s := &statuses[i]
		if s.Name != "aggregated" { // only synthetic aggregated rows
			continue
		}
		var completedAt time.Time
		if s.CompletedAt != nil {
			completedAt = *s.CompletedAt
		}
		updatedAt := s.UpdatedAt
		if best == nil || completedAt.After(bestCompletedAt) || (completedAt.Equal(bestCompletedAt) && updatedAt.After(bestUpdatedAt)) {
			best = s
			bestCompletedAt = completedAt
			bestUpdatedAt = updatedAt
		}
	}

	if best == nil {
		// No aggregated row yet -> treat as pending so re-evaluation can occur later
		return CIStatePending, nil
	}

	// Map to CIState using status and conclusion
	// success => CIStateSuccess; failure => CIStateFailed; in_progress/queued/neutral => CIStatePending
	if best.Conclusion != nil {
		switch *best.Conclusion {
		case "success":
			return CIStateSuccess, nil
		case "failure":
			return CIStateFailed, nil
		case "neutral":
			return CIStatePending, nil
		}
	}
	switch best.Status {
	case "in_progress", "queued":
		return CIStatePending, nil
	case "completed":
		// completed without clear conclusion -> unknown
		return CIStateUnknown, nil
	default:
		return CIStatePending, nil
	}
}
