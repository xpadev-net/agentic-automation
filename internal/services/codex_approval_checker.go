package services

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"
	"context"
)

type codexApprovalChecker struct {
	reviewRepo *repositories.ReviewFeedbackRepository
	prRepo     *repositories.PullRequestRepository
	logger     *config.AppLogger
}

func NewCodexApprovalChecker(reviewRepo *repositories.ReviewFeedbackRepository, prRepo *repositories.PullRequestRepository, logger *config.AppLogger) CodexApprovalChecker {
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &codexApprovalChecker{reviewRepo: reviewRepo, prRepo: prRepo, logger: logger}
}

func (c *codexApprovalChecker) IsApproved(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	fullName := owner + "/" + repo
	pr, err := c.prRepo.FindByRepoAndNumber(fullName, prNumber)
	if err != nil {
		return false, err
	}

	latestFeedback, err := c.reviewRepo.FindLatestResponseByPRID(pr.ID)
	if err != nil {
		return false, err
	}
	if latestFeedback == nil {
		return false, nil
	}
	return latestFeedback.ApprovalDetected, nil
}
