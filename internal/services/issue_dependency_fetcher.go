package services

import (
	"context"
	"net/url"
	"strings"
	"time"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"

	"github.com/google/go-github/v76/github"
)

// DependencyIssue represents a minimal cross-repository issue reference
// used by dependency graph building.
type DependencyIssue struct {
	Owner   string
	Repo    string
	Number  int
	Title   string
	State   string
	HTMLURL string
}

// IssueDependencyFetcher provides methods to list issue dependencies.
type IssueDependencyFetcher interface {
	ListBlockedBy(ctx context.Context, owner, repo string, issueNumber int) ([]DependencyIssue, error)
	ListBlocking(ctx context.Context, owner, repo string, issueNumber int) ([]DependencyIssue, error)
}

// IssueDependencyFetcherService implements IssueDependencyFetcher using GitHub REST API.
type IssueDependencyFetcherService struct {
	githubClient *clients.Client
	logger       *config.AppLogger
}

// NewIssueDependencyFetcher creates a new fetcher service.
func NewIssueDependencyFetcher(githubClient *clients.Client, logger *config.AppLogger) *IssueDependencyFetcherService {
	if githubClient == nil {
		panic("githubClient is required for IssueDependencyFetcherService")
	}
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &IssueDependencyFetcherService{githubClient: githubClient, logger: logger}
}

func (s *IssueDependencyFetcherService) ListBlockedBy(ctx context.Context, owner, repo string, issueNumber int) ([]DependencyIssue, error) {
	start := time.Now()
	issues, err := s.githubClient.ListIssueDependenciesBlockedBy(ctx, owner, repo, issueNumber)
	if err != nil {
		s.logger.Error("Failed to list blocked_by issues",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
			config.Error(err),
		)
		return nil, err
	}
	out := make([]DependencyIssue, 0, len(issues))
	for _, is := range issues {
		out = append(out, mapIssueToDependency(is))
	}
	s.logger.Info("Listed blocked_by issues",
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
		config.Int("count", len(out)),
		config.Duration("elapsed", time.Since(start)),
	)
	return out, nil
}

func (s *IssueDependencyFetcherService) ListBlocking(ctx context.Context, owner, repo string, issueNumber int) ([]DependencyIssue, error) {
	start := time.Now()
	issues, err := s.githubClient.ListIssueDependenciesBlocking(ctx, owner, repo, issueNumber)
	if err != nil {
		s.logger.Error("Failed to list blocking issues",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
			config.Error(err),
		)
		return nil, err
	}
	out := make([]DependencyIssue, 0, len(issues))
	for _, is := range issues {
		out = append(out, mapIssueToDependency(is))
	}
	s.logger.Info("Listed blocking issues",
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
		config.Int("count", len(out)),
		config.Duration("elapsed", time.Since(start)),
	)
	return out, nil
}

func mapIssueToDependency(is *github.Issue) DependencyIssue {
	var title, state, htmlURL string
	if is.GetTitle() != "" {
		title = is.GetTitle()
	}
	if is.GetState() != "" {
		state = is.GetState()
	}
	if is.GetHTMLURL() != "" {
		htmlURL = is.GetHTMLURL()
	}
	owner, repo := parseOwnerRepoFromAPIURL(is.GetRepositoryURL())
	return DependencyIssue{
		Owner:   owner,
		Repo:    repo,
		Number:  is.GetNumber(),
		Title:   title,
		State:   state,
		HTMLURL: htmlURL,
	}
}

// parseOwnerRepoFromAPIURL extracts owner and repo from a repository API URL,
// e.g., https://api.github.com/repos/{owner}/{repo}
func parseOwnerRepoFromAPIURL(raw string) (string, string) {
	if raw == "" {
		return "", ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	// Expect path like /repos/{owner}/{repo}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "repos" {
		return parts[1], parts[2]
	}
	return "", ""
}
