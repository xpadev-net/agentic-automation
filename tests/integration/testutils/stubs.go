package testutils

import (
	"context"

	"agentic-automation/internal/services"
)

// StubAuthorization implements handlers.Authorization
type StubAuthorization struct {
	Allow bool
	Err   error
}

func (s *StubAuthorization) CheckPermission(ctx context.Context, owner, repo, username string) (bool, error) {
	return s.Allow, s.Err
}

// StubIssueContext implements handlers.IssueContext
type StubIssueContext struct {
	Context *services.IssueContext
}

func (s *StubIssueContext) CollectIssueContext(ctx context.Context, owner, repo string, issueNumber int) (*services.IssueContext, error) {
	return s.Context, nil
}

func (s *StubIssueContext) FormatPrompt(issueCtx *services.IssueContext) string {
	if issueCtx == nil {
		return ""
	}
	// Minimal prompt sufficient for assertions
	return "Issue #" + string(rune(issueCtx.Number))
}

// StubGitHubNotification implements handlers.GitHubNotification
type StubGitHubNotification struct {
	Called bool
	Err    error
}

func (s *StubGitHubNotification) PostExecutionStartComment(ctx context.Context, owner, repo string, issueNumber int, agentType string, agentRunID int) error {
	s.Called = true
	return s.Err
}
