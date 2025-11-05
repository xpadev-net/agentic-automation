package services

import (
	"agentic-automation/internal/repositories"
	"context"

	"go.uber.org/zap"
)

type codexApprovalChecker struct {
	reviewRepo *repositories.ReviewFeedbackRepository
	prRepo     *repositories.PullRequestRepository
	logger     *zap.Logger
}

func NewCodexApprovalChecker(reviewRepo *repositories.ReviewFeedbackRepository, prRepo *repositories.PullRequestRepository, logger *zap.Logger) CodexApprovalChecker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &codexApprovalChecker{reviewRepo: reviewRepo, prRepo: prRepo, logger: logger}
}

func (c *codexApprovalChecker) IsApproved(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	fullName := owner + "/" + repo
	pr, err := c.prRepo.FindByRepoAndNumber(fullName, prNumber)
	if err != nil {
		return false, err
	}

	feedbacks, err := c.reviewRepo.FindByPRID(pr.ID)
	if err != nil {
		return false, err
	}
	for _, fb := range feedbacks {
		if fb != nil && fb.ApprovalDetected {
			return true, nil
		}
	}
	return false, nil
}
