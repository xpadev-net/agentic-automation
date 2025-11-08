package clients

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"agentic-automation/internal/errors"
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
	Code    errors.ErrorCode
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

// GetErrorCode returns the error code for this error
func (e *GitHubError) GetErrorCode() errors.ErrorCode {
	if e.Code != "" {
		return e.Code
	}
	return errors.ERR_INTERNAL_UNEXPECTED
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

// NewFromGitHub wraps an existing *github.Client with our Client wrapper.
// Use this when an authenticated client is prepared elsewhere (e.g., via GitHub App installation token).
func NewFromGitHub(g *github.Client, logger *zap.Logger) *Client {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{
		Client:      g,
		logger:      logger,
		retryConfig: nil,
	}
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
				Code:    errors.ERR_GITHUB_RATE_LIMIT,
				Message: fmt.Sprintf("GitHub API rate limit exceeded. Reset at %v", resp.Rate.Reset.Time),
			}
		}
	}

	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		return &GitHubError{
			Code:    errors.ERR_GITHUB_RATE_LIMIT,
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

		// Determine error code based on status code
		var code errors.ErrorCode
		switch ghErr.Response.StatusCode {
		case http.StatusNotFound:
			code = errors.ERR_GITHUB_NOT_FOUND
		case http.StatusUnauthorized:
			code = errors.ERR_GITHUB_UNAUTHORIZED
		case http.StatusForbidden:
			code = errors.ERR_GITHUB_FORBIDDEN
		case http.StatusTooManyRequests:
			code = errors.ERR_GITHUB_RATE_LIMIT
		default:
			if ghErr.Response.StatusCode >= 500 {
				code = errors.ERR_GITHUB_SERVER_ERROR
			} else {
				code = errors.ERR_INTERNAL_UNEXPECTED
			}
		}

		// Handle 404 Not Found
		if ghErr.Response.StatusCode == http.StatusNotFound {
			return &GitHubError{
				ErrorResponse: ghErr,
				Code:          code,
				Message:       fmt.Sprintf("Resource not found: %s", ghErr.Message),
			}
		}

		return &GitHubError{
			ErrorResponse: ghErr,
			Code:          code,
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

// listIssueDependencies is an internal helper to retrieve issue dependencies for the given kind.
// kind must be either "blocked_by" or "blocking" per GitHub REST API.
func (c *Client) listIssueDependencies(ctx context.Context, owner, repo string, issueNumber int, kind string) ([]*github.Issue, error) {
	c.logger.Info("Listing GitHub issue dependencies",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.String("kind", kind),
	)

	// Pagination options
	opts := &github.ListOptions{
		Page:    1,
		PerPage: 100,
	}

	var allIssues []*github.Issue
	var resp *github.Response

	for {
		// Build path: /repos/{owner}/{repo}/issues/{issue_number}/dependencies/{kind}
		path := fmt.Sprintf("repos/%s/%s/issues/%d/dependencies/%s", owner, repo, issueNumber, kind)

		// Append pagination query params
		u, err := url.Parse(path)
		if err != nil {
			return nil, fmt.Errorf("failed to parse dependencies path: %w", err)
		}
		q := u.Query()
		q.Set("per_page", fmt.Sprintf("%d", opts.PerPage))
		q.Set("page", fmt.Sprintf("%d", opts.Page))
		u.RawQuery = q.Encode()

		req, err := c.NewRequest("GET", u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		// Explicit Accept header as per docs
		req.Header.Set("Accept", "application/vnd.github+json")

		var issues []*github.Issue
		pageResp, err := c.Do(ctx, req, &issues)
		if err != nil {
			return nil, c.handleError(err, pageResp, "listIssueDependencies")
		}

		allIssues = append(allIssues, issues...)
		resp = pageResp

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	c.handleRateLimit(resp)
	return allIssues, nil
}

// ListIssueDependenciesBlockedBy lists issues that block the given issue (blocked_by).
func (c *Client) ListIssueDependenciesBlockedBy(ctx context.Context, owner, repo string, issueNumber int) ([]*github.Issue, error) {
	return c.listIssueDependencies(ctx, owner, repo, issueNumber, "blocked_by")
}

// ListIssueDependenciesBlocking lists issues that are blocked by the given issue (blocking).
func (c *Client) ListIssueDependenciesBlocking(ctx context.Context, owner, repo string, issueNumber int) ([]*github.Issue, error) {
	return c.listIssueDependencies(ctx, owner, repo, issueNumber, "blocking")
}

// ListIssueComments retrieves all comments for a GitHub issue
// Handles pagination to return all comments, not just the first page
func (c *Client) ListIssueComments(ctx context.Context, owner, repo string, issueNumber int) ([]*github.IssueComment, error) {
	c.logger.Info("Listing GitHub issue comments",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{
			Page:    1,
			PerPage: 100, // Maximum per page to minimize API calls
		},
	}

	var allComments []*github.IssueComment
	var resp *github.Response

	for {
		comments, pageResp, err := c.Issues.ListComments(ctx, owner, repo, issueNumber, opts)
		if err != nil {
			return nil, c.handleError(err, pageResp, "ListIssueComments")
		}

		allComments = append(allComments, comments...)
		resp = pageResp

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	c.handleRateLimit(resp)
	return allComments, nil
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

// UpdateIssueComment updates an existing comment on a GitHub issue
func (c *Client) UpdateIssueComment(ctx context.Context, owner, repo string, commentID int64, body string) (*github.IssueComment, error) {
	c.logger.Info("Updating GitHub issue comment",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("comment_id", commentID),
	)

	comment := &github.IssueComment{
		Body: &body,
	}

	issueComment, resp, err := c.Issues.EditComment(ctx, owner, repo, commentID, comment)
	if err != nil {
		return nil, c.handleError(err, resp, "UpdateIssueComment")
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
func (c *Client) CreatePullRequest(ctx context.Context, owner, repo string, base, head, title, body string, issueNumber *int) (*github.PullRequest, error) {
	c.logger.Info("Creating GitHub pull request",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("base", base),
		zap.String("head", head),
		zap.String("title", title),
	)

	// If issueNumber is provided, append "close #123" to body
	finalBody := body
	if issueNumber != nil {
		if finalBody != "" {
			finalBody = fmt.Sprintf("%s\n\nclose #%d", finalBody, *issueNumber)
		} else {
			finalBody = fmt.Sprintf("close #%d", *issueNumber)
		}
	}

	newPR := &github.NewPullRequest{
		Title: &title,
		Head:  &head,
		Base:  &base,
		Body:  &finalBody,
	}

	pr, resp, err := c.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return nil, c.handleError(err, resp, "CreatePullRequest")
	}

	c.handleRateLimit(resp)
	return pr, nil
}

// ListPullRequestReviews retrieves all reviews for a GitHub pull request
// Handles pagination to return all reviews, not just the first page
func (c *Client) ListPullRequestReviews(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestReview, error) {
	c.logger.Info("Listing GitHub pull request reviews",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	opts := &github.ListOptions{
		Page:    1,
		PerPage: 100, // Maximum per page to minimize API calls
	}

	var allReviews []*github.PullRequestReview
	var resp *github.Response

	for {
		reviews, pageResp, err := c.PullRequests.ListReviews(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, c.handleError(err, pageResp, "ListPullRequestReviews")
		}

		allReviews = append(allReviews, reviews...)
		resp = pageResp

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	c.handleRateLimit(resp)
	return allReviews, nil
}

// ListPullRequestComments retrieves all comments for a GitHub pull request review
// Handles pagination to return all comments, not just the first page
func (c *Client) ListPullRequestComments(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestComment, error) {
	c.logger.Info("Listing GitHub pull request comments",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	opts := &github.PullRequestListCommentsOptions{
		ListOptions: github.ListOptions{
			Page:    1,
			PerPage: 100, // Maximum per page to minimize API calls
		},
	}

	var allComments []*github.PullRequestComment
	var resp *github.Response

	for {
		comments, pageResp, err := c.PullRequests.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, c.handleError(err, pageResp, "ListPullRequestComments")
		}

		allComments = append(allComments, comments...)
		resp = pageResp

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	c.handleRateLimit(resp)
	return allComments, nil
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
//
// Note: This method only checks for explicit collaborators and does not include
// repository owners or organization team members with write/admin access.
// For a more comprehensive check that includes owners and team members,
// use CheckWritePermission instead.
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

// CheckWritePermission checks if a user has write, maintain, or admin permission on a repository.
// This includes repository owners, explicit collaborators, and organization team members
// with write/maintain/admin access. This is the recommended method for checking FR-018 requirements
// (Collaborator+ permission) as it accurately includes all users with write-equivalent rights.
//
// The maintain role grants all write capabilities plus additional management rights and should
// be considered equivalent to write/admin for authorization purposes.
//
// Returns true if the user has admin, maintain, or write permission, false if the user has read
// permission, no permission, or the user/repository does not exist.
// Returns an error if the GitHub API call fails (network error, rate limit, etc.).
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - username: GitHub username to check (e.g., "octocat")
//
// Returns:
//   - bool: true if user has write/maintain/admin permission, false otherwise
//   - error: GitHub API error (network error, rate limit, authentication error, etc.)
func (c *Client) CheckWritePermission(ctx context.Context, owner, repo, username string) (bool, error) {
	c.logger.Info("Checking GitHub user write permission",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("username", username),
	)

	permissionLevel, resp, err := c.Repositories.GetPermissionLevel(ctx, owner, repo, username)
	if err != nil {
		// Check if it's a 404 (user has no permission, user doesn't exist, or repo doesn't exist)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			c.handleRateLimit(resp)
			c.logger.Info("GitHub user has no permission",
				zap.String("owner", owner),
				zap.String("repo", repo),
				zap.String("username", username),
			)
			return false, nil
		}
		return false, c.handleError(err, resp, "CheckWritePermission")
	}

	c.handleRateLimit(resp)

	// Check if permission level is admin, maintain, or write
	hasPermission := false
	if permissionLevel != nil && permissionLevel.Permission != nil {
		permission := *permissionLevel.Permission
		hasPermission = permission == "admin" || permission == "maintain" || permission == "write"
		c.logger.Info("GitHub user permission check completed",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.String("username", username),
			zap.String("permission_level", permission),
			zap.Bool("has_write_permission", hasPermission),
		)
	} else {
		c.logger.Warn("GitHub API returned nil permission level",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.String("username", username),
		)
	}

	return hasPermission, nil
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
// Handles pagination to return all check runs, not just the first page
func (c *Client) ListCheckRunsForCheckSuite(ctx context.Context, owner, repo string, checkSuiteID int64) ([]*github.CheckRun, error) {
	c.logger.Info("Listing GitHub check runs for check suite",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("check_suite_id", checkSuiteID),
	)

	opts := &github.ListCheckRunsOptions{
		ListOptions: github.ListOptions{
			Page:    1,
			PerPage: 100, // Maximum per page to minimize API calls
		},
	}

	var allCheckRuns []*github.CheckRun
	var resp *github.Response

	for {
		checkRunsResult, pageResp, err := c.Checks.ListCheckRunsCheckSuite(ctx, owner, repo, checkSuiteID, opts)
		if err != nil {
			return nil, c.handleError(err, pageResp, "ListCheckRunsForCheckSuite")
		}

		allCheckRuns = append(allCheckRuns, checkRunsResult.CheckRuns...)
		resp = pageResp

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	c.handleRateLimit(resp)
	return allCheckRuns, nil
}

// ListCheckRunsForRef retrieves all check runs for a commit SHA (ref)
// Handles pagination to return all check runs, not just the first page
func (c *Client) ListCheckRunsForRef(ctx context.Context, owner, repo, ref string) ([]*github.CheckRun, error) {
	c.logger.Info("Listing GitHub check runs for ref",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("ref", ref),
	)

	opts := &github.ListCheckRunsOptions{
		ListOptions: github.ListOptions{
			Page:    1,
			PerPage: 100, // Maximum per page to minimize API calls
		},
	}

	var allCheckRuns []*github.CheckRun
	var resp *github.Response

	for {
		checkRunsResult, pageResp, err := c.Checks.ListCheckRunsForRef(ctx, owner, repo, ref, opts)
		if err != nil {
			return nil, c.handleError(err, pageResp, "ListCheckRunsForRef")
		}

		allCheckRuns = append(allCheckRuns, checkRunsResult.CheckRuns...)
		resp = pageResp

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	c.handleRateLimit(resp)
	return allCheckRuns, nil
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

// InstallationTokenCache caches installation tokens per installation ID
type InstallationTokenCache struct {
	mutex      sync.RWMutex
	tokens     map[int64]*TokenEntry
	appID      int64
	privateKey []byte
}

// TokenEntry represents a cached token with its expiry
type TokenEntry struct {
	Token     string
	ExpiresAt time.Time
}

// GitHubClient provides GitHub App based client creation per repository
type GitHubClient struct {
	logger     *zap.Logger
	tokenCache *InstallationTokenCache
	// test hooks
	baseURL    *url.URL
	httpClient *http.Client
	testMode   bool
}

// NewGitHubAppClient initializes a GitHubClient using env vars GITHUB_APP_ID and GITHUB_PRIVATE_KEY
func NewGitHubAppClient(logger *zap.Logger) (*GitHubClient, error) {
	appIDStr := os.Getenv("GITHUB_APP_ID")
	privKey := os.Getenv("GITHUB_PRIVATE_KEY")
	testMode := os.Getenv("GITHUB_APP_TEST_MODE") == "1"
	// In test mode, allow missing credentials to avoid hard dependency on secrets
	if appIDStr == "" || privKey == "" {
		if !testMode {
			return nil, fmt.Errorf("missing GitHub App credentials")
		}
	}
	var appID int64
	if appIDStr != "" {
		var err error
		appID, err = strconv.ParseInt(appIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid GITHUB_APP_ID: %w", err)
		}
	}
	cache := &InstallationTokenCache{
		mutex:      sync.RWMutex{},
		tokens:     make(map[int64]*TokenEntry),
		appID:      appID,
		privateKey: []byte(privKey),
	}
	client := &GitHubClient{logger: logger, tokenCache: cache}
	if testMode {
		client.testMode = true
	}
	return client, nil
}

// ForRepo returns an authenticated *github.Client for the given repository via Installation Token
func (c *GitHubClient) ForRepo(ctx context.Context, owner, repo string) (*github.Client, error) {
	installationID, err := c.resolveInstallationID(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	token, err := c.getInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	base := http.DefaultTransport
	if c.httpClient != nil && c.httpClient.Transport != nil {
		base = c.httpClient.Transport
	}
	oauthTr := &oauth2.Transport{Source: ts, Base: base}
	hc := &http.Client{Transport: oauthTr}
	if c.httpClient != nil {
		hc.Timeout = c.httpClient.Timeout
	}
	client := github.NewClient(hc)
	if c.baseURL != nil {
		client.BaseURL = c.baseURL
	}
	return client, nil
}

// resolveInstallationID finds installation ID for owner/repo using App-scoped transport
func (c *GitHubClient) resolveInstallationID(ctx context.Context, owner, repo string) (int64, error) {
	var transport http.RoundTripper = http.DefaultTransport
	if c.httpClient != nil && c.httpClient.Transport != nil {
		transport = c.httpClient.Transport
	}
	var appHTTP *http.Client
	if c.testMode {
		appHTTP = &http.Client{Transport: transport}
	} else {
		appsTr, err := ghinstallation.NewAppsTransport(transport, c.tokenCache.appID, c.tokenCache.privateKey)
		if err != nil {
			return 0, fmt.Errorf("failed to create apps transport: %w", err)
		}
		appHTTP = &http.Client{Transport: appsTr}
	}
	if c.httpClient != nil {
		// copy timeouts from injected client if provided
		appHTTP.Timeout = c.httpClient.Timeout
	}
	appClient := github.NewClient(appHTTP)
	if c.baseURL != nil {
		appClient.BaseURL = c.baseURL
	}
	inst, _, err := appClient.Apps.FindRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return 0, fmt.Errorf("failed to find installation for %s/%s: %w", owner, repo, err)
	}
	if inst == nil || inst.ID == nil {
		return 0, fmt.Errorf("installation not found for %s/%s", owner, repo)
	}
	return *inst.ID, nil
}

// getInstallationToken returns a cached (or newly issued) installation token string
func (c *GitHubClient) getInstallationToken(ctx context.Context, installationID int64) (string, error) {
	// Fast path: read lock
	c.tokenCache.mutex.RLock()
	entry, ok := c.tokenCache.tokens[installationID]
	if ok && entry != nil && time.Until(entry.ExpiresAt) > 2*time.Minute {
		token := entry.Token
		c.tokenCache.mutex.RUnlock()
		return token, nil
	}
	c.tokenCache.mutex.RUnlock()

	// Slow path: write lock and refresh
	c.tokenCache.mutex.Lock()
	defer c.tokenCache.mutex.Unlock()
	// Re-check after acquiring lock
	entry = c.tokenCache.tokens[installationID]
	if entry != nil && time.Until(entry.ExpiresAt) > 2*time.Minute {
		return entry.Token, nil
	}

	var transport http.RoundTripper = http.DefaultTransport
	if c.httpClient != nil && c.httpClient.Transport != nil {
		transport = c.httpClient.Transport
	}
	var appHTTP *http.Client
	if c.testMode {
		appHTTP = &http.Client{Transport: transport}
	} else {
		appsTr, err := ghinstallation.NewAppsTransport(transport, c.tokenCache.appID, c.tokenCache.privateKey)
		if err != nil {
			return "", fmt.Errorf("failed to create apps transport: %w", err)
		}
		appHTTP = &http.Client{Transport: appsTr}
	}
	if c.httpClient != nil {
		appHTTP.Timeout = c.httpClient.Timeout
	}
	appClient := github.NewClient(appHTTP)
	if c.baseURL != nil {
		appClient.BaseURL = c.baseURL
	}
	// Request a new access token for the installation
	instToken, _, err := appClient.Apps.CreateInstallationToken(ctx, installationID, &github.InstallationTokenOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create installation token: %w", err)
	}
	if instToken == nil || instToken.Token == nil || instToken.ExpiresAt == nil {
		return "", fmt.Errorf("invalid installation token response")
	}
	// Cache the token with expiry
	c.tokenCache.tokens[installationID] = &TokenEntry{Token: *instToken.Token, ExpiresAt: instToken.ExpiresAt.Time}
	return *instToken.Token, nil
}

// invalidateInstallationToken removes a cached token forcing refresh on next use
func (c *GitHubClient) invalidateInstallationToken(installationID int64) {
	c.tokenCache.mutex.Lock()
	delete(c.tokenCache.tokens, installationID)
	c.tokenCache.mutex.Unlock()
}

// DoWithClientRetry provides a retry wrapper for a single GitHub API call.
// If the call returns 401/403, it invalidates the token and retries once.
func (c *GitHubClient) DoWithClientRetry(
	ctx context.Context,
	owner, repo string,
	call func(*github.Client) (*github.Response, error),
) error {
	installationID, err := c.resolveInstallationID(ctx, owner, repo)
	if err != nil {
		return err
	}
	client, err := c.ForRepo(ctx, owner, repo)
	if err != nil {
		return err
	}
	resp, err := call(client)
	if err == nil {
		return nil
	}
	if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		// Token might be expired or revoked; invalidate and retry once
		c.invalidateInstallationToken(installationID)
		client, err = c.ForRepo(ctx, owner, repo)
		if err != nil {
			return err
		}
		_, err = call(client)
		return err
	}
	return err
}

// SetTestServer sets the baseURL and httpClient for testing purposes
// This allows tests in other packages to configure the client to use a test server
func (c *GitHubClient) SetTestServer(baseURL *url.URL, httpClient *http.Client) {
	c.baseURL = baseURL
	c.httpClient = httpClient
}
