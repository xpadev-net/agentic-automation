package clients

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

// RetryConfig represents retry configuration for GitHub API calls
// This is a placeholder for T022 retry utility integration
type RetryConfig struct {
	// T022で実装予定
}

// Client wraps github.Client with additional functionality
type Client struct {
	*github.Client
	logger      *zap.Logger
	retryConfig *RetryConfig // T022統合用（現時点ではnil）
}

// GitHubError wraps github.ErrorResponse with additional context
type GitHubError struct {
	*github.ErrorResponse
	Message string
}

func (e *GitHubError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.ErrorResponse != nil {
		return fmt.Sprintf("GitHub API error: %s", e.ErrorResponse.Message)
	}
	return "GitHub API error"
}

// NewClient creates a new GitHub API client with OAuth2 authentication
// token should be a GitHub App installation token (retrieved via config.GetEnvRequired("GITHUB_TOKEN"))
func NewClient(token string, logger *zap.Logger) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("GitHub token is required")
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	githubClient := github.NewClient(tc)

	return &Client{
		Client:      githubClient,
		logger:      logger,
		retryConfig: nil, // T022で設定される
	}, nil
}

// handleRateLimit extracts and logs rate limit information from response
func (c *Client) handleRateLimit(resp *github.Response) {
	if resp != nil && resp.Rate.Remaining > 0 {
		c.logger.Debug("GitHub API rate limit",
			zap.Int("remaining", resp.Rate.Remaining),
			zap.Int("limit", resp.Rate.Limit),
			zap.Time("reset", resp.Rate.Reset.Time),
		)
	} else if resp != nil && resp.Rate.Remaining == 0 {
		c.logger.Warn("GitHub API rate limit exhausted",
			zap.Int("limit", resp.Rate.Limit),
			zap.Time("reset", resp.Rate.Reset.Time),
		)
	}
}

// handleError processes GitHub API errors and returns appropriate error types
func (c *Client) handleError(err error, resp *github.Response, method string) error {
	if err == nil {
		return nil
	}

	// Log error details
	c.logger.Error("GitHub API error",
		zap.String("method", method),
		zap.Error(err),
	)

	if resp != nil {
		c.handleRateLimit(resp)
	}

	// Handle rate limit errors
	if resp != nil && resp.StatusCode == http.StatusForbidden {
		if resp.Rate.Remaining == 0 {
			return &GitHubError{
				Message: fmt.Sprintf("GitHub API rate limit exceeded. Reset at %v", resp.Rate.Reset.Time),
			}
		}
	}

	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		return &GitHubError{
			Message: fmt.Sprintf("GitHub API rate limit exceeded. Reset at %v", resp.Rate.Reset.Time),
		}
	}

	// Handle GitHub API error response
	if ghErr, ok := err.(*github.ErrorResponse); ok {
		// Convert []github.Error to []string for logging
		errorStrings := make([]string, len(ghErr.Errors))
		for i, e := range ghErr.Errors {
			errorStrings[i] = e.Message
		}

		c.logger.Error("GitHub API error response",
			zap.Int("status_code", ghErr.Response.StatusCode),
			zap.String("message", ghErr.Message),
			zap.Strings("errors", errorStrings),
		)

		// Handle 404 Not Found
		if ghErr.Response.StatusCode == http.StatusNotFound {
			return &GitHubError{
				ErrorResponse: ghErr,
				Message:       fmt.Sprintf("Resource not found: %s", ghErr.Message),
			}
		}

		return &GitHubError{
			ErrorResponse: ghErr,
			Message:       ghErr.Message,
		}
	}

	// Handle network or other errors
	return fmt.Errorf("GitHub API call failed: %w", err)
}

// GetIssue retrieves a GitHub issue
func (c *Client) GetIssue(ctx context.Context, owner, repo string, issueNumber int) (*github.Issue, error) {
	c.logger.Info("Getting GitHub issue",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	issue, resp, err := c.Issues.Get(ctx, owner, repo, issueNumber)
	if err != nil {
		return nil, c.handleError(err, resp, "GetIssue")
	}

	c.handleRateLimit(resp)
	return issue, nil
}

// ListIssueComments retrieves all comments for a GitHub issue
func (c *Client) ListIssueComments(ctx context.Context, owner, repo string, issueNumber int) ([]*github.IssueComment, error) {
	c.logger.Info("Listing GitHub issue comments",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	comments, resp, err := c.Issues.ListComments(ctx, owner, repo, issueNumber, nil)
	if err != nil {
		return nil, c.handleError(err, resp, "ListIssueComments")
	}

	c.handleRateLimit(resp)
	return comments, nil
}

// CreateIssueComment creates a comment on a GitHub issue
func (c *Client) CreateIssueComment(ctx context.Context, owner, repo string, issueNumber int, body string) (*github.IssueComment, error) {
	c.logger.Info("Creating GitHub issue comment",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	comment := &github.IssueComment{
		Body: &body,
	}

	issueComment, resp, err := c.Issues.CreateComment(ctx, owner, repo, issueNumber, comment)
	if err != nil {
		return nil, c.handleError(err, resp, "CreateIssueComment")
	}

	c.handleRateLimit(resp)
	return issueComment, nil
}

// GetPullRequest retrieves a GitHub pull request
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*github.PullRequest, error) {
	c.logger.Info("Getting GitHub pull request",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	pr, resp, err := c.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, c.handleError(err, resp, "GetPullRequest")
	}

	c.handleRateLimit(resp)
	return pr, nil
}

// CreatePullRequest creates a new GitHub pull request
func (c *Client) CreatePullRequest(ctx context.Context, owner, repo string, base, head, title, body string) (*github.PullRequest, error) {
	c.logger.Info("Creating GitHub pull request",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("base", base),
		zap.String("head", head),
		zap.String("title", title),
	)

	newPR := &github.NewPullRequest{
		Title: &title,
		Head:  &head,
		Base:  &base,
		Body:  &body,
	}

	pr, resp, err := c.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return nil, c.handleError(err, resp, "CreatePullRequest")
	}

	c.handleRateLimit(resp)
	return pr, nil
}

// ListPullRequestReviews retrieves all reviews for a GitHub pull request
func (c *Client) ListPullRequestReviews(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestReview, error) {
	c.logger.Info("Listing GitHub pull request reviews",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	reviews, resp, err := c.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, c.handleError(err, resp, "ListPullRequestReviews")
	}

	c.handleRateLimit(resp)
	return reviews, nil
}

// ListPullRequestComments retrieves all comments for a GitHub pull request review
func (c *Client) ListPullRequestComments(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestComment, error) {
	c.logger.Info("Listing GitHub pull request comments",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	comments, resp, err := c.PullRequests.ListComments(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, c.handleError(err, resp, "ListPullRequestComments")
	}

	c.handleRateLimit(resp)
	return comments, nil
}

// MergePullRequest merges a GitHub pull request
func (c *Client) MergePullRequest(ctx context.Context, owner, repo string, prNumber int, commitMessage string, mergeMethod string) (*github.PullRequestMergeResult, error) {
	c.logger.Info("Merging GitHub pull request",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.String("merge_method", mergeMethod),
	)

	opts := &github.PullRequestOptions{
		CommitTitle: commitMessage,
		MergeMethod: mergeMethod,
	}

	result, resp, err := c.PullRequests.Merge(ctx, owner, repo, prNumber, commitMessage, opts)
	if err != nil {
		return nil, c.handleError(err, resp, "MergePullRequest")
	}

	c.handleRateLimit(resp)
	return result, nil
}

// GetPullRequestMergeable checks if a pull request is mergeable
// Returns true if mergeable, false if not mergeable, error if status is unknown or error occurred
func (c *Client) GetPullRequestMergeable(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	c.logger.Info("Checking GitHub pull request mergeable status",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	pr, resp, err := c.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return false, c.handleError(err, resp, "GetPullRequestMergeable")
	}

	c.handleRateLimit(resp)

	if pr.Mergeable == nil {
		// Status is unknown (GitHub is still computing)
		return false, fmt.Errorf("mergeable status is unknown")
	}

	return *pr.Mergeable, nil
}

// CheckCollaboratorPermission checks if a user is a collaborator on the repository
// Returns true if the user is a collaborator (204 response), false otherwise
func (c *Client) CheckCollaboratorPermission(ctx context.Context, owner, repo, username string) (bool, error) {
	c.logger.Info("Checking GitHub collaborator permission",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("username", username),
	)

	isCollaborator, resp, err := c.Repositories.IsCollaborator(ctx, owner, repo, username)
	if err != nil {
		// Check if it's a 404 (user is not a collaborator)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			c.handleRateLimit(resp)
			return false, nil
		}
		return false, c.handleError(err, resp, "CheckCollaboratorPermission")
	}

	c.handleRateLimit(resp)
	return isCollaborator, nil
}

// GetCheckSuite retrieves a GitHub check suite
func (c *Client) GetCheckSuite(ctx context.Context, owner, repo string, checkSuiteID int64) (*github.CheckSuite, error) {
	c.logger.Info("Getting GitHub check suite",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("check_suite_id", checkSuiteID),
	)

	checkSuite, resp, err := c.Checks.GetCheckSuite(ctx, owner, repo, checkSuiteID)
	if err != nil {
		return nil, c.handleError(err, resp, "GetCheckSuite")
	}

	c.handleRateLimit(resp)
	return checkSuite, nil
}

// ListCheckRunsForCheckSuite retrieves all check runs for a check suite
func (c *Client) ListCheckRunsForCheckSuite(ctx context.Context, owner, repo string, checkSuiteID int64) ([]*github.CheckRun, error) {
	c.logger.Info("Listing GitHub check runs for check suite",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("check_suite_id", checkSuiteID),
	)

	opts := &github.ListCheckRunsOptions{}

	checkRuns, resp, err := c.Checks.ListCheckRunsCheckSuite(ctx, owner, repo, checkSuiteID, opts)
	if err != nil {
		return nil, c.handleError(err, resp, "ListCheckRunsForCheckSuite")
	}

	c.handleRateLimit(resp)
	return checkRuns.CheckRuns, nil
}

// GetCheckRunLogs retrieves logs for a specific check run
// Note: GitHub API doesn't provide direct logs endpoint, so this uses the check run details
// The actual logs URL is available in CheckRun.HTMLURL
func (c *Client) GetCheckRunLogs(ctx context.Context, owner, repo string, checkRunID int64) (string, error) {
	c.logger.Info("Getting GitHub check run logs",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("check_run_id", checkRunID),
	)

	checkRun, resp, err := c.Checks.GetCheckRun(ctx, owner, repo, checkRunID)
	if err != nil {
		return "", c.handleError(err, resp, "GetCheckRunLogs")
	}

	c.handleRateLimit(resp)

	// GitHub API doesn't provide direct logs content via API
	// The logs are available via HTML URL or via downloading artifacts
	// For now, return the HTML URL where logs can be viewed
	if checkRun.HTMLURL != nil {
		return *checkRun.HTMLURL, nil
	}

	return "", fmt.Errorf("logs URL not available for check run %d", checkRunID)
}

// GetRepository retrieves a GitHub repository
func (c *Client) GetRepository(ctx context.Context, owner, repo string) (*github.Repository, error) {
	c.logger.Info("Getting GitHub repository",
		zap.String("owner", owner),
		zap.String("repo", repo),
	)

	repository, resp, err := c.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, c.handleError(err, resp, "GetRepository")
	}

	c.handleRateLimit(resp)
	return repository, nil
}

// GetDefaultBranch retrieves the default branch name for a repository
func (c *Client) GetDefaultBranch(ctx context.Context, owner, repo string) (string, error) {
	c.logger.Info("Getting GitHub repository default branch",
		zap.String("owner", owner),
		zap.String("repo", repo),
	)

	repository, resp, err := c.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return "", c.handleError(err, resp, "GetDefaultBranch")
	}

	c.handleRateLimit(resp)

	if repository.DefaultBranch == nil {
		return "", fmt.Errorf("default branch not found for repository %s/%s", owner, repo)
	}

	return *repository.DefaultBranch, nil
}
