package git

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"agent-runner/pkg/agent"
	githubutil "agent-runner/pkg/github"
	"agent-runner/pkg/prompts"
	"agent-runner/pkg/utils"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

// CloneRepo clones the repository to the destination directory.
// token: GitHub PAT for authentication
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// dest: Destination directory path
func CloneRepo(token, repo, dest string) error {
	// Validate inputs
	if repo == "" {
		return fmt.Errorf("repository name is required")
	}
	if dest == "" {
		return fmt.Errorf("destination directory is required")
	}

	// Validate repo format (owner/repo)
	if !strings.Contains(repo, "/") || len(strings.Split(repo, "/")) != 2 {
		return fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}

	// Acquire token via GitHub App if not provided
	if token == "" {
		parts := strings.Split(repo, "/")
		tokenFetched, err := githubutil.GetGitHubToken(context.Background(), parts[0], parts[1])
		if err != nil {
			return fmt.Errorf("failed to obtain GitHub App installation token: %w", err)
		}
		token = tokenFetched
	}

	// Convert owner/repo to HTTPS URL with token as password
	// Format: https://x-access-token:<token>@github.com/owner/repo.git
	u := &url.URL{Scheme: "https", Host: "github.com", Path: fmt.Sprintf("/%s.git", repo)}
	u.User = url.UserPassword("x-access-token", token)
	cloneURL := u.String()

	// Check if destination directory exists
	if _, err := os.Stat(dest); err == nil {
		// Directory exists, check if it's empty or contains a git repo
		gitDir := fmt.Sprintf("%s/.git", dest)
		if _, err := os.Stat(gitDir); err == nil {
			return fmt.Errorf("destination directory %s already contains a git repository", dest)
		}
		// Directory exists but is not a git repo, create subdirectory or use it
		// For now, we'll clone into the directory directly
	}

	// Execute git clone command
	// Mask token in error messages to avoid leaking credentials
	cmd := exec.Command("git", "clone", cloneURL, dest)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Mask token in error output (both raw token and x-access-token:<token>)
		outputStr := strings.ReplaceAll(string(output), token, "***")
		outputStr = strings.ReplaceAll(outputStr, "x-access-token:"+token, "x-access-token:***")
		// Also mask the full URL
		maskedURL := fmt.Sprintf("https://x-access-token:***@github.com/%s.git", repo)
		outputStr = strings.ReplaceAll(outputStr, cloneURL, maskedURL)
		return fmt.Errorf("git clone failed: %w, output: %s", err, outputStr)
	}

	return nil
}

// CreateBranch creates a new git branch or checks out existing branch.
// Behavior:
//   - If existingBranchName is specified: Checks out the existing branch (for continuing work on existing PR)
//     If the branch doesn't exist, creates it from baseBranch
//   - If retryCount == 0: Creates new branch from baseBranch with "git checkout -b {branchName}"
//   - If retryCount > 0: Checks out existing branch with "git checkout {branchName}"
//     (or "git checkout -b {branchName} origin/{branchName}" if branch exists remotely but not locally)
//     If the branch doesn't exist, creates it from baseBranch
//
// workDir: Working directory (must be a git repository)
// branchName: Name of the branch to create or checkout
// retryCount: Number of retries (0 = first attempt, >0 = retry)
// existingBranchName: Optional existing branch name to checkout (if specified, this takes precedence)
// baseBranch: Base branch name to create from if branch doesn't exist (e.g., "master" or "main")
func CreateBranch(workDir, branchName string, retryCount int, existingBranchName, baseBranch string) error {
	// Validate inputs
	if workDir == "" {
		return fmt.Errorf("work directory is required")
	}
	if branchName == "" {
		return fmt.Errorf("branch name is required")
	}
	if baseBranch == "" {
		baseBranch = "master" // Default fallback
	}

	// Check if workDir is a git repository
	gitDir := fmt.Sprintf("%s/.git", workDir)
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return fmt.Errorf("work directory %s is not a git repository", workDir)
	}

	// If existingBranchName is specified, checkout that branch (for continuing work on existing PR)
	// If it doesn't exist, create it from baseBranch
	if existingBranchName != "" {
		return checkoutExistingBranch(workDir, existingBranchName, baseBranch)
	}

	if retryCount == 0 {
		// First attempt: Create new branch from baseBranch
		return createBranchFromBase(workDir, branchName, baseBranch)
	}

	// Retry: Checkout existing branch, or create from baseBranch if it doesn't exist
	// First check if branch exists locally
	checkLocalCmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branchName))
	checkLocalCmd.Dir = workDir
	localExists := checkLocalCmd.Run() == nil

	if localExists {
		// Branch exists locally, checkout it
		checkoutCmd := exec.Command("git", "checkout", branchName)
		checkoutCmd.Dir = workDir
		if err := checkoutCmd.Run(); err != nil {
			return fmt.Errorf("failed to checkout local branch %s: %w", branchName, err)
		}
		return nil
	}

	// Check if remote exists before fetching
	checkRemoteCmd := exec.Command("git", "remote", "get-url", "origin")
	checkRemoteCmd.Dir = workDir
	hasRemote := checkRemoteCmd.Run() == nil

	if hasRemote {
		// Check if branch exists remotely
		fetchCmd := exec.Command("git", "fetch", "origin")
		fetchCmd.Dir = workDir
		if err := fetchCmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch from origin: %w", err)
		}

		checkRemoteBranchCmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/remotes/origin/%s", branchName))
		checkRemoteBranchCmd.Dir = workDir
		remoteExists := checkRemoteBranchCmd.Run() == nil

		if remoteExists {
			// Branch exists remotely, create local tracking branch
			checkoutRemoteCmd := exec.Command("git", "checkout", "-b", branchName, fmt.Sprintf("origin/%s", branchName))
			checkoutRemoteCmd.Dir = workDir
			if err := checkoutRemoteCmd.Run(); err != nil {
				return fmt.Errorf("failed to checkout remote branch %s: %w", branchName, err)
			}
			return nil
		}
	}

	// Branch doesn't exist locally or remotely, create it from baseBranch
	return createBranchFromBase(workDir, branchName, baseBranch)
}

// checkoutExistingBranch checks out an existing branch (local or remote).
// This is used when continuing work on an existing PR.
// If the branch doesn't exist, creates it from baseBranch.
func checkoutExistingBranch(workDir, branchName, baseBranch string) error {
	// First check if branch exists locally
	checkLocalCmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branchName))
	checkLocalCmd.Dir = workDir
	localExists := checkLocalCmd.Run() == nil

	if localExists {
		// Branch exists locally, checkout it
		checkoutCmd := exec.Command("git", "checkout", branchName)
		checkoutCmd.Dir = workDir
		if err := checkoutCmd.Run(); err != nil {
			return fmt.Errorf("failed to checkout local branch %s: %w", branchName, err)
		}
		return nil
	}

	// Check if remote exists before fetching
	checkRemoteCmd := exec.Command("git", "remote", "get-url", "origin")
	checkRemoteCmd.Dir = workDir
	hasRemote := checkRemoteCmd.Run() == nil

	if hasRemote {
		// Fetch latest changes from remote
		fetchCmd := exec.Command("git", "fetch", "origin")
		fetchCmd.Dir = workDir
		if err := fetchCmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch from origin: %w", err)
		}

		// Check if branch exists remotely
		checkRemoteBranchCmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/remotes/origin/%s", branchName))
		checkRemoteBranchCmd.Dir = workDir
		remoteExists := checkRemoteBranchCmd.Run() == nil

		if remoteExists {
			// Branch exists remotely, create local tracking branch
			checkoutRemoteCmd := exec.Command("git", "checkout", "-b", branchName, fmt.Sprintf("origin/%s", branchName))
			checkoutRemoteCmd.Dir = workDir
			if err := checkoutRemoteCmd.Run(); err != nil {
				return fmt.Errorf("failed to checkout remote branch %s: %w", branchName, err)
			}
			return nil
		}
	}

	// Branch doesn't exist locally or remotely, create it from baseBranch
	return createBranchFromBase(workDir, branchName, baseBranch)
}

// createBranchFromBase creates a new branch from the specified base branch.
func createBranchFromBase(workDir, branchName, baseBranch string) error {
	// First, ensure we're on baseBranch
	checkoutBaseCmd := exec.Command("git", "checkout", baseBranch)
	checkoutBaseCmd.Dir = workDir
	if err := checkoutBaseCmd.Run(); err != nil {
		// If baseBranch doesn't exist, try alternative (master/main)
		altBranch := "main"
		if baseBranch == "main" {
			altBranch = "master"
		}
		checkoutBaseCmd = exec.Command("git", "checkout", altBranch)
		checkoutBaseCmd.Dir = workDir
		if err := checkoutBaseCmd.Run(); err != nil {
			return fmt.Errorf("failed to checkout base branch %s or %s: %w", baseBranch, altBranch, err)
		}
		baseBranch = altBranch
	}

	// Check if remote exists before fetching
	checkRemoteCmd := exec.Command("git", "remote", "get-url", "origin")
	checkRemoteCmd.Dir = workDir
	hasRemote := checkRemoteCmd.Run() == nil

	if hasRemote {
		// Pull latest changes from baseBranch
		fetchCmd := exec.Command("git", "fetch", "origin")
		fetchCmd.Dir = workDir
		if err := fetchCmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch from origin: %w", err)
		}

		// Reset to origin/baseBranch
		resetCmd := exec.Command("git", "reset", "--hard", fmt.Sprintf("origin/%s", baseBranch))
		resetCmd.Dir = workDir
		if err := resetCmd.Run(); err != nil {
			// If remote branch doesn't exist, continue with local branch
			// This is acceptable for local repositories
		}
	}

	// Create new branch
	createBranchCmd := exec.Command("git", "checkout", "-b", branchName)
	createBranchCmd.Dir = workDir
	if err := createBranchCmd.Run(); err != nil {
		return fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}

	return nil
}

// HasChanges checks if there are any file changes in the workspace.
func HasChanges(workDir string) (bool, error) {
	changedFiles, err := GetChangedFiles(workDir)
	if err != nil {
		return false, err
	}
	return len(changedFiles) > 0, nil
}

// CommitChanges and PushBranch are implemented in committer.go (T036).

// GetDefaultBranch retrieves the default branch of a repository using GitHub API.
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// Returns the default branch name (e.g., "master" or "main"), or an error if retrieval fails.
func GetDefaultBranch(repo string) (string, error) {
	// Validate repo format (owner/repo)
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return "", fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}
	owner := repoParts[0]
	repoName := repoParts[1]

	// Acquire token via GitHub App
	ctx := context.Background()
	token, err := githubutil.GetGitHubToken(ctx, owner, repoName)
	if err != nil {
		return "", fmt.Errorf("failed to obtain GitHub App installation token: %w", err)
	}

	// Create GitHub API client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get repository information to retrieve default branch
	base := "master" // default fallback
	if repoInfo, repoResp, derr := client.Repositories.Get(ctx, owner, repoName); derr == nil && repoInfo != nil && repoInfo.DefaultBranch != nil && *repoInfo.DefaultBranch != "" {
		base = *repoInfo.DefaultBranch
		_ = repoResp // rate limit handled by caller if needed
	}

	return base, nil
}

// extractIssueInfoFromPrompt extracts Issue title and description from XML-formatted prompt.
// It looks for <issue_context> or <issue> tags and extracts <title> and <description> content.
// Returns the combined issue info (title + description) and a boolean indicating if extraction was successful.
func extractIssueInfoFromPrompt(prompt string) (string, bool) {
	// Check if prompt contains issue context tags
	if !strings.Contains(prompt, "<issue_context>") && !strings.Contains(prompt, "<issue>") {
		return "", false
	}

	// Try to extract from <issue_context><issue>...</issue></issue_context> structure
	var title, description string

	// Pattern 1: <issue_context><issue><title>...</title><description>...</description></issue></issue_context>
	issueContextPattern := regexp.MustCompile(`(?s)<issue_context>.*?<issue[^>]*>.*?<title>(.*?)</title>.*?<description>(.*?)</description>.*?</issue>.*?</issue_context>`)
	matches := issueContextPattern.FindStringSubmatch(prompt)
	if len(matches) == 3 {
		title = strings.TrimSpace(matches[1])
		description = strings.TrimSpace(matches[2])
		if title != "" || description != "" {
			return combineIssueInfo(title, description), true
		}
	}

	// Pattern 2: <issue><title>...</title><description>...</description></issue>
	issuePattern := regexp.MustCompile(`(?s)<issue[^>]*>.*?<title>(.*?)</title>.*?<description>(.*?)</description>.*?</issue>`)
	matches = issuePattern.FindStringSubmatch(prompt)
	if len(matches) == 3 {
		title = strings.TrimSpace(matches[1])
		description = strings.TrimSpace(matches[2])
		if title != "" || description != "" {
			return combineIssueInfo(title, description), true
		}
	}

	// Pattern 3: <issue_context><title>...</title><description>...</description></issue_context>
	issueContextDirectPattern := regexp.MustCompile(`(?s)<issue_context>.*?<title>(.*?)</title>.*?<description>(.*?)</description>.*?</issue_context>`)
	matches = issueContextDirectPattern.FindStringSubmatch(prompt)
	if len(matches) == 3 {
		title = strings.TrimSpace(matches[1])
		description = strings.TrimSpace(matches[2])
		if title != "" || description != "" {
			return combineIssueInfo(title, description), true
		}
	}

	return "", false
}

// combineIssueInfo combines title and description into a single string.
func combineIssueInfo(title, description string) string {
	if title == "" && description == "" {
		return ""
	}
	if title == "" {
		return description
	}
	if description == "" {
		return title
	}
	return title + "\n\n" + description
}

// fetchIssueInfoFromGitHub fetches Issue information from GitHub API.
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// issueNumber: Issue number to fetch
// Returns the combined issue info (title + description) or an error.
func fetchIssueInfoFromGitHub(repo string, issueNumber int) (string, error) {
	// Validate repo format
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return "", fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}
	owner := repoParts[0]
	repoName := repoParts[1]

	// Acquire token via GitHub App
	ctx := context.Background()
	token, err := githubutil.GetGitHubToken(ctx, owner, repoName)
	if err != nil {
		return "", fmt.Errorf("failed to obtain GitHub App installation token: %w", err)
	}

	// Create GitHub API client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get Issue information
	issue, resp, err := client.Issues.Get(ctx, owner, repoName, issueNumber)
	if err != nil {
		// 401/403 retry once with refreshed token
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			if t, terr := githubutil.GetGitHubToken(ctx, owner, repoName); terr == nil && t != "" && t != token {
				token = t
				ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
				tc = oauth2.NewClient(ctx, ts)
				client = github.NewClient(tc)
				issue, resp, err = client.Issues.Get(ctx, owner, repoName, issueNumber)
				if err == nil {
					// retry success → continue normal flow
					goto GET_SUCCESS
				}
			}
		}
		// Check if it's a rate limit error
		if resp != nil && resp.StatusCode == 403 {
			return "", fmt.Errorf("GitHub API rate limit exceeded")
		}
		return "", fmt.Errorf("failed to get issue #%d: %w", issueNumber, err)
	}
GET_SUCCESS:

	if issue == nil {
		return "", fmt.Errorf("issue #%d not found", issueNumber)
	}

	title := ""
	if issue.Title != nil {
		title = *issue.Title
	}

	description := ""
	if issue.Body != nil {
		description = *issue.Body
	}

	return combineIssueInfo(title, description), nil
}

// GeneratePRTitleAndBody generates PR title and body using an AI agent in read-only mode.
// It uses Issue information, changed files, commit message, and git diff to generate the PR content.
// Returns title and body, or an error if generation fails.
func GeneratePRTitleAndBody(workDir string, repo string, issueNumber int, issuePrompt, commitMsg, agentType, model, baseBranch string) (string, string, error) {
	// Only option-capable agents (cursor-agent, codex) are supported for PR generation
	if agentType != "cursor-agent" && agentType != "codex" {
		return "", "", fmt.Errorf("PR generation is only supported for cursor-agent or codex, got: %s", agentType)
	}

	// Extract Issue information from prompt or fetch from GitHub API
	issueInfo := ""
	if extracted, found := extractIssueInfoFromPrompt(issuePrompt); found {
		issueInfo = extracted
	} else {
		// Try to fetch from GitHub API
		if fetched, err := fetchIssueInfoFromGitHub(repo, issueNumber); err == nil {
			issueInfo = fetched
		} else {
			// Fallback: use issuePrompt as is (existing behavior)
			issueInfo = issuePrompt
		}
	}

	// Get changed files
	changedFiles, err := GetChangedFiles(workDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to get changed files: %w", err)
	}

	// If no changes in working tree (already committed), get changed files from base branch
	if len(changedFiles) == 0 && baseBranch != "" {
		// Get changed files from base branch diff
		diffOutput, err := runGitDiff(workDir, "--name-only", fmt.Sprintf("%s..HEAD", baseBranch))
		if err != nil {
			return "", "", fmt.Errorf("failed to get changed files from base branch: %w", err)
		}
		if diffOutput != "" {
			// Parse diff output to get file list
			lines := strings.Split(strings.TrimSpace(diffOutput), "\n")
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					changedFiles = append(changedFiles, strings.TrimSpace(line))
				}
			}
		}
	}

	// Build changed files list
	changedFilesList := strings.Join(changedFiles, "\n- ")
	if changedFilesList != "" {
		changedFilesList = "- " + changedFilesList
	} else {
		changedFilesList = "(no files changed)"
	}

	// Build prompt with structured information
	// Use base branch diff command if base branch is provided, otherwise use regular git diff
	diffCommand := "git diff"
	if baseBranch != "" {
		diffCommand = fmt.Sprintf("git diff %s..HEAD", baseBranch)
	}
	prompt := prompts.BuildPRTitleGenerationPrompt(issueNumber, issueInfo, changedFilesList, commitMsg, diffCommand)

	// Execute agent in read-only mode
	executor := agent.NewExecutor(agentType)
	output, err := executor.ExecuteWithOptions(workDir, prompt, model, false)
	if err != nil {
		return "", "", fmt.Errorf("%s execution failed: %w", agentType, err)
	}

	// Parse output to extract title and body
	title, body, err := utils.ParsePRTitleAndBody(output)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse PR title and body: %w", err)
	}

	return title, body, nil
}

// GenerateCommitMessage generates commit message using an AI agent in read-only mode.
// It uses Issue information, changed files, and staged diff to generate the commit message.
// Returns commit message, or an error if generation fails.
func GenerateCommitMessage(workDir string, repo string, issueNumber int, issuePrompt, agentType, model string) (string, error) {
	// Only option-capable agents (cursor-agent, codex) are supported for commit message generation
	if agentType != "cursor-agent" && agentType != "codex" {
		return "", fmt.Errorf("commit message generation is only supported for cursor-agent or codex, got: %s", agentType)
	}

	// Extract Issue information from prompt or fetch from GitHub API
	issueInfo := ""
	if extracted, found := extractIssueInfoFromPrompt(issuePrompt); found {
		issueInfo = extracted
	} else {
		// Try to fetch from GitHub API
		if fetched, err := fetchIssueInfoFromGitHub(repo, issueNumber); err == nil {
			issueInfo = fetched
		} else {
			// Fallback: use issuePrompt as is (existing behavior)
			issueInfo = issuePrompt
		}
	}

	// Get changed files
	changedFiles, err := GetChangedFiles(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to get changed files: %w", err)
	}

	// Build changed files list
	changedFilesList := strings.Join(changedFiles, "\n- ")
	if changedFilesList != "" {
		changedFilesList = "- " + changedFilesList
	} else {
		changedFilesList = "(no files changed)"
	}

	// Get staged diff
	stagedDiff, err := GetStagedDiff(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to get staged diff: %w", err)
	}

	// Build prompt with structured information
	prompt := prompts.BuildCommitMessageGenerationPrompt(issueNumber, issueInfo, changedFilesList, stagedDiff)

	// Execute agent in read-only mode (allowWrite=false) to prevent file modifications
	// This ensures that the agent cannot modify files after validation has passed
	executor := agent.NewExecutor(agentType)
	output, err := executor.ExecuteWithOptions(workDir, prompt, model, false)
	if err != nil {
		return "", fmt.Errorf("%s execution failed: %w", agentType, err)
	}

	// Parse output to extract commit message
	commitMsg, err := utils.ParseCommitMessage(output)
	if err != nil {
		return "", fmt.Errorf("failed to parse commit message: %w", err)
	}

	return commitMsg, nil
}

// CreatePR creates a Pull Request via GitHub API.
// If PR already exists for this branch, returns existing PR number (idempotent).
// token: GitHub PAT for authentication
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// branchName: Name of the branch to create PR from
// issueNumber: Issue number to reference in PR title
// title: Optional PR title (if empty, uses default format)
// body: Optional PR body (if empty, uses default format)
// baseBranch: Base branch name (e.g., "master" or "main")
// Returns: PR number (existing or newly created)
func CreatePR(token, repo, branchName string, issueNumber int, title, body, baseBranch string) (int, error) {
	// Validate inputs
	if repo == "" {
		return 0, fmt.Errorf("repository name is required")
	}
	if branchName == "" {
		return 0, fmt.Errorf("branch name is required")
	}
	if issueNumber <= 0 {
		return 0, fmt.Errorf("issue number must be positive, got: %d", issueNumber)
	}

	// Validate repo format (owner/repo)
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return 0, fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}
	owner := repoParts[0]
	repoName := repoParts[1]

	// Acquire token via GitHub App if not provided
	if token == "" {
		t, err := githubutil.GetGitHubToken(context.Background(), owner, repoName)
		if err != nil {
			return 0, fmt.Errorf("failed to obtain GitHub App installation token: %w", err)
		}
		token = t
	}

	// Create GitHub API client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Check if PR already exists for this branch
	// Use Head filter to search for PRs with the same branch
	head := fmt.Sprintf("%s:%s", owner, branchName)
	opts := &github.PullRequestListOptions{
		Head:  head,
		State: "open",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	prs, resp, err := client.PullRequests.List(ctx, owner, repoName, opts)
	if err != nil {
		// 401/403 retry once with refreshed token
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			if t, terr := githubutil.GetGitHubToken(ctx, owner, repoName); terr == nil && t != "" && t != token {
				token = t
				ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
				tc = oauth2.NewClient(ctx, ts)
				client = github.NewClient(tc)
				prs, resp, err = client.PullRequests.List(ctx, owner, repoName, opts)
				if err == nil {
					// retry success → continue normal flow
					goto LIST_SUCCESS
				}
			}
		}
		// Check if it's a rate limit error
		if resp != nil && resp.StatusCode == 403 {
			return 0, fmt.Errorf("GitHub API rate limit exceeded")
		}
		return 0, fmt.Errorf("failed to list pull requests: %w", err)
	}
LIST_SUCCESS:

	// If PR exists, return its number
	if len(prs) > 0 {
		// Return the first matching PR (there should typically be only one)
		if prs[0].Number != nil {
			return *prs[0].Number, nil
		}
	}

	// Use provided base branch, fallback to master if empty
	base := baseBranch
	if base == "" {
		base = "master"
	}
	// Fallback to main if default branch retrieval failed and master fails later
	// Use provided title/body if available, otherwise use default format
	if title == "" {
		title = fmt.Sprintf("Fix: issue #%d", issueNumber)
	}
	if body == "" {
		body = fmt.Sprintf("自動生成: エージェントによる修正\n\nclose #%d", issueNumber)
	}

	newPR := &github.NewPullRequest{
		Title: &title,
		Head:  &branchName,
		Base:  &base,
		Body:  &body,
	}

	pr, resp, err := client.PullRequests.Create(ctx, owner, repoName, newPR)
	if err != nil {
		// Check if it's a rate limit error
		if resp != nil && resp.StatusCode == 403 {
			return 0, fmt.Errorf("GitHub API rate limit exceeded")
		}
		// Retry once on 401/403 unauthorized with refreshed token
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			if t, terr := githubutil.GetGitHubToken(ctx, owner, repoName); terr == nil && t != "" && t != token {
				token = t
				ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
				tc = oauth2.NewClient(ctx, ts)
				client = github.NewClient(tc)
				pr, resp, err = client.PullRequests.Create(ctx, owner, repoName, newPR)
				if err == nil {
					// retry success → continue normal flow
					goto CREATE_SUCCESS
				}
			}
		}
		// If validation error, try fallback to "main" only if different from current base
		if resp != nil && resp.StatusCode == 422 && base != "main" {
			base = "main"
			newPR.Base = &base
			pr, resp, err = client.PullRequests.Create(ctx, owner, repoName, newPR)
			if err != nil {
				if resp != nil && resp.StatusCode == 403 {
					return 0, fmt.Errorf("GitHub API rate limit exceeded")
				}
				return 0, fmt.Errorf("failed to create pull request: %w", err)
			}
		} else {
			return 0, fmt.Errorf("failed to create pull request: %w", err)
		}
	}
CREATE_SUCCESS:

	if pr.Number == nil {
		return 0, fmt.Errorf("created PR but PR number is nil")
	}

	return *pr.Number, nil
}

// FindPRByBranch finds an existing Pull Request for the given branch.
// token: GitHub PAT for authentication (empty string will auto-fetch via GitHub App)
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// branchName: Name of the branch to search for
// Returns: PR number if found, 0 if not found, or an error if search fails
func FindPRByBranch(token, repo, branchName string) (int, error) {
	// Validate inputs
	if repo == "" {
		return 0, fmt.Errorf("repository name is required")
	}
	if branchName == "" {
		return 0, fmt.Errorf("branch name is required")
	}

	// Validate repo format (owner/repo)
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return 0, fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}
	owner := repoParts[0]
	repoName := repoParts[1]

	// Acquire token via GitHub App if not provided
	if token == "" {
		t, err := githubutil.GetGitHubToken(context.Background(), owner, repoName)
		if err != nil {
			return 0, fmt.Errorf("failed to obtain GitHub App installation token: %w", err)
		}
		token = t
	}

	// Create GitHub API client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Check if PR already exists for this branch
	// Use Head filter to search for PRs with the same branch
	head := fmt.Sprintf("%s:%s", owner, branchName)
	opts := &github.PullRequestListOptions{
		Head:  head,
		State: "open",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	prs, resp, err := client.PullRequests.List(ctx, owner, repoName, opts)
	if err != nil {
		// 401/403 retry once with refreshed token
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			if t, terr := githubutil.GetGitHubToken(ctx, owner, repoName); terr == nil && t != "" && t != token {
				token = t
				ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
				tc = oauth2.NewClient(ctx, ts)
				client = github.NewClient(tc)
				prs, resp, err = client.PullRequests.List(ctx, owner, repoName, opts)
				if err == nil {
					// retry success → continue normal flow
					goto LIST_SUCCESS
				}
			}
		}
		// Check if it's a rate limit error
		if resp != nil && resp.StatusCode == 403 {
			return 0, fmt.Errorf("GitHub API rate limit exceeded")
		}
		return 0, fmt.Errorf("failed to list pull requests: %w", err)
	}
LIST_SUCCESS:

	// If PR exists, return its number
	if len(prs) > 0 {
		// Return the first matching PR (there should typically be only one)
		if prs[0].Number != nil {
			return *prs[0].Number, nil
		}
	}

	// PR not found
	return 0, nil
}

// PostPRComment posts a comment on a Pull Request via GitHub API.
// token: GitHub PAT for authentication (empty string will auto-fetch via GitHub App)
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// prNumber: PR number to post comment on
// comment: Comment body to post
// Returns: error if posting fails
func PostPRComment(token, repo string, prNumber int, comment string) error {
	// Validate inputs
	if repo == "" {
		return fmt.Errorf("repository name is required")
	}
	if prNumber <= 0 {
		return fmt.Errorf("PR number must be positive, got: %d", prNumber)
	}
	if comment == "" {
		return fmt.Errorf("comment body is required")
	}

	// Validate repo format (owner/repo)
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}
	owner := repoParts[0]
	repoName := repoParts[1]

	// Acquire token via GitHub App if not provided
	if token == "" {
		t, err := githubutil.GetGitHubToken(context.Background(), owner, repoName)
		if err != nil {
			return fmt.Errorf("failed to obtain GitHub App installation token: %w", err)
		}
		token = t
	}

	// Create GitHub API client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Create comment
	issueComment := &github.IssueComment{
		Body: &comment,
	}

	_, resp, err := client.Issues.CreateComment(ctx, owner, repoName, prNumber, issueComment)
	if err != nil {
		// 401/403 retry once with refreshed token
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			if t, terr := githubutil.GetGitHubToken(ctx, owner, repoName); terr == nil && t != "" && t != token {
				token = t
				ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
				tc = oauth2.NewClient(ctx, ts)
				client = github.NewClient(tc)
				_, resp, err = client.Issues.CreateComment(ctx, owner, repoName, prNumber, issueComment)
				if err == nil {
					// retry success
					return nil
				}
			}
		}
		// Check if it's a rate limit error
		if resp != nil && resp.StatusCode == 403 {
			return fmt.Errorf("GitHub API rate limit exceeded")
		}
		return fmt.Errorf("failed to post PR comment: %w", err)
	}

	return nil
}

// IsBehind checks if headRef is behind baseRef by comparing their merge base.
// It fetches from origin first to ensure we have the latest remote state.
// Returns true if headRef is behind baseRef, false if up-to-date or ahead.
func IsBehind(workDir, baseRef, headRef string) (bool, error) {
	// Validate inputs
	if workDir == "" {
		return false, fmt.Errorf("work directory is required")
	}
	if baseRef == "" {
		return false, fmt.Errorf("base reference is required")
	}
	if headRef == "" {
		return false, fmt.Errorf("head reference is required")
	}

	// Fetch latest changes from origin
	fetchCmd := exec.Command("git", "fetch", "origin")
	fetchCmd.Dir = workDir
	if err := fetchCmd.Run(); err != nil {
		return false, fmt.Errorf("failed to fetch from origin: %w", err)
	}

	// Get merge base (common ancestor)
	mergeBaseCmd := exec.Command("git", "merge-base", baseRef, headRef)
	mergeBaseCmd.Dir = workDir
	mergeBaseOutput, err := mergeBaseCmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to get merge base: %w", err)
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOutput))

	// Check if headRef is at merge base (meaning it's behind or equal)
	// If headRef is ahead, merge base will be different from headRef
	revParseCmd := exec.Command("git", "rev-parse", headRef)
	revParseCmd.Dir = workDir
	headOutput, err := revParseCmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to get head commit: %w", err)
	}
	headSHA := strings.TrimSpace(string(headOutput))

	// If merge base equals head, then head is behind or equal to base
	if mergeBase == headSHA {
		// Check if base is ahead of head
		revListCmd := exec.Command("git", "rev-list", "--count", fmt.Sprintf("%s..%s", headRef, baseRef))
		revListCmd.Dir = workDir
		countOutput, err := revListCmd.Output()
		if err != nil {
			// If rev-list fails, assume not behind
			return false, nil
		}
		count := strings.TrimSpace(string(countOutput))
		if count == "0" {
			return false, nil // Equal, not behind
		}
		return true, nil // Behind
	}

	// headRef is ahead or diverged, check if base has commits not in head
	revListCmd := exec.Command("git", "rev-list", "--count", fmt.Sprintf("%s..%s", headRef, baseRef))
	revListCmd.Dir = workDir
	countOutput, err := revListCmd.Output()
	if err != nil {
		// If rev-list fails, assume not behind
		return false, nil
	}
	count := strings.TrimSpace(string(countOutput))
	return count != "0", nil
}

// MergeBase returns the merge base (common ancestor) commit SHA between baseRef and headRef.
func MergeBase(workDir, baseRef, headRef string) (string, error) {
	// Validate inputs
	if workDir == "" {
		return "", fmt.Errorf("work directory is required")
	}
	if baseRef == "" {
		return "", fmt.Errorf("base reference is required")
	}
	if headRef == "" {
		return "", fmt.Errorf("head reference is required")
	}

	mergeBaseCmd := exec.Command("git", "merge-base", baseRef, headRef)
	mergeBaseCmd.Dir = workDir
	output, err := mergeBaseCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get merge base: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// MergeBranch merges the specified branch (fromRef) into the current branch.
// It performs a merge operation and returns an error only if the merge command fails.
// Conflicts are not treated as errors here; they should be detected using ListConflicts.
func MergeBranch(workDir, fromRef string) error {
	// Validate inputs
	if workDir == "" {
		return fmt.Errorf("work directory is required")
	}
	if fromRef == "" {
		return fmt.Errorf("from reference is required")
	}

	// Ensure fromRef is in the format origin/branchName if it's a remote branch
	mergeRef := fromRef
	if !strings.HasPrefix(fromRef, "origin/") && !strings.HasPrefix(fromRef, "refs/") {
		// Assume it's a branch name, try origin/branchName first
		mergeRef = fmt.Sprintf("origin/%s", fromRef)
	}

	// Execute git merge (fast-forward will happen automatically if possible)
	mergeCmd := exec.Command("git", "merge", mergeRef, "--no-edit")
	mergeCmd.Dir = workDir
	output, err := mergeCmd.CombinedOutput()
	if err != nil {
		// Check if it's a merge conflict (exit code 1 can mean conflicts OR other errors)
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			// Verify that a merge actually started by checking for MERGE_HEAD
			// MERGE_HEAD is only created when a merge conflict occurs
			// If MERGE_HEAD doesn't exist, the merge failed for other reasons
			// (e.g., working tree has unstaged changes, merge already in progress)
			mergeHeadPath := fmt.Sprintf("%s/.git/MERGE_HEAD", workDir)
			if _, statErr := os.Stat(mergeHeadPath); statErr == nil {
				// MERGE_HEAD exists, this is a real merge conflict
				// Return nil here, conflicts will be detected by ListConflicts
				return nil
			}
			// MERGE_HEAD doesn't exist, merge failed for other reasons
			// Propagate the error to the caller
			return fmt.Errorf("git merge failed (not a conflict): %w, output: %s", err, string(output))
		}
		// Other exit codes are errors
		return fmt.Errorf("git merge failed: %w, output: %s", err, string(output))
	}

	return nil
}

// ListConflicts returns a list of files with merge conflicts.
// It uses git diff --name-only --diff-filter=U to find unmerged files.
func ListConflicts(workDir string) ([]string, error) {
	// Validate inputs
	if workDir == "" {
		return nil, fmt.Errorf("work directory is required")
	}

	// Check for merge in progress
	mergeHeadPath := fmt.Sprintf("%s/.git/MERGE_HEAD", workDir)
	if _, err := os.Stat(mergeHeadPath); os.IsNotExist(err) {
		// Not in merge state, no conflicts
		return []string{}, nil
	}

	// Get list of unmerged files
	// git diff returns exit code 1 when there are differences (unmerged files),
	// and exit code 0 only when there are no differences
	// We need to parse the output even when exit code is 1
	diffCmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	diffCmd.Dir = workDir
	output, err := diffCmd.CombinedOutput()
	if err != nil {
		// Exit code 1 means there are differences (conflicts), which is expected
		// Exit code 0 means no differences (no conflicts)
		// We need to parse the output in both cases
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			// Exit code 1: there are conflicts, parse the output
			trimmed := strings.TrimSpace(string(output))
			if trimmed == "" {
				// No output means no conflicts (shouldn't happen with exit code 1, but handle it)
				return []string{}, nil
			}
			lines := strings.Split(trimmed, "\n")
			var conflicts []string
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					conflicts = append(conflicts, strings.TrimSpace(line))
				}
			}
			return conflicts, nil
		}
		// Other exit codes are errors
		return nil, fmt.Errorf("failed to list conflicts: %w", err)
	}

	// Exit code 0: no differences (no conflicts)
	// Parse output to be safe, but should be empty
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []string{}, nil
	}

	lines := strings.Split(trimmed, "\n")
	var conflicts []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			conflicts = append(conflicts, strings.TrimSpace(line))
		}
	}

	return conflicts, nil
}

// AbortMerge aborts an ongoing merge operation.
// It runs `git merge --abort` to clean up the merge state.
// If not in a merge state, it returns nil (idempotent).
func AbortMerge(workDir string) error {
	// Validate inputs
	if workDir == "" {
		return fmt.Errorf("work directory is required")
	}

	// Check if we're in a merge state
	mergeHeadPath := fmt.Sprintf("%s/.git/MERGE_HEAD", workDir)
	if _, err := os.Stat(mergeHeadPath); os.IsNotExist(err) {
		// Not in merge state, nothing to abort
		return nil
	}

	// Execute git merge --abort
	abortCmd := exec.Command("git", "merge", "--abort")
	abortCmd.Dir = workDir
	output, err := abortCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git merge --abort failed: %w, output: %s", err, string(output))
	}

	return nil
}

// HasConflictMarkers checks if the specified files contain conflict markers (<<<<<<<, =======, >>>>>>>).
// It reads the file contents and checks for the presence of conflict markers.
// Returns a list of files that still contain conflict markers.
func HasConflictMarkers(workDir string, files []string) ([]string, error) {
	// Validate inputs
	if workDir == "" {
		return nil, fmt.Errorf("work directory is required")
	}
	if len(files) == 0 {
		return []string{}, nil
	}

	var filesWithMarkers []string

	for _, file := range files {
		filePath := fmt.Sprintf("%s/%s", workDir, file)
		content, err := os.ReadFile(filePath)
		if err != nil {
			// If file doesn't exist or can't be read, skip it
			// This can happen if the file was deleted during conflict resolution
			continue
		}

		// Check for conflict markers
		contentStr := string(content)
		if strings.Contains(contentStr, "<<<<<<<") ||
			strings.Contains(contentStr, "=======") ||
			strings.Contains(contentStr, ">>>>>>>") {
			filesWithMarkers = append(filesWithMarkers, file)
		}
	}

	return filesWithMarkers, nil
}

// ResolveConflictsWithAI resolves merge conflicts using AI agent (cursor-agent).
// It gets conflict files, builds a prompt with HEAD and MERGE_HEAD content,
// executes cursor-agent to resolve conflicts, and commits the resolution.
func ResolveConflictsWithAI(workDir, repo string, issueID int, agentType string) error {
	// Validate inputs
	if workDir == "" {
		return fmt.Errorf("work directory is required")
	}
	if repo == "" {
		return fmt.Errorf("repository is required")
	}
	if issueID <= 0 {
		return fmt.Errorf("issue ID must be positive, got: %d", issueID)
	}

	// Only option-capable agents (cursor-agent, codex) are supported for conflict resolution
	if agentType != "cursor-agent" && agentType != "codex" {
		return fmt.Errorf("conflict resolution is only supported for cursor-agent or codex, got: %s", agentType)
	}

	// Get list of conflict files
	conflictFiles, err := ListConflicts(workDir)
	if err != nil {
		return fmt.Errorf("failed to list conflicts: %w", err)
	}
	if len(conflictFiles) == 0 {
		// No conflicts, nothing to resolve
		return nil
	}

	// Get HEAD and MERGE_HEAD commit SHAs
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = workDir
	headOutput, err := headCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get HEAD commit: %w", err)
	}
	headSHA := strings.TrimSpace(string(headOutput))

	mergeHeadCmd := exec.Command("git", "rev-parse", "MERGE_HEAD")
	mergeHeadCmd.Dir = workDir
	mergeHeadOutput, err := mergeHeadCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get MERGE_HEAD commit: %w", err)
	}
	mergeHeadSHA := strings.TrimSpace(string(mergeHeadOutput))

	// Build conflict file list for prompt
	conflictFilesList := strings.Join(conflictFiles, "\n- ")
	conflictFilesList = "- " + conflictFilesList

	// Get diff for each conflict file to include in prompt
	var conflictDiffs strings.Builder
	for _, file := range conflictFiles {
		// Get the conflicted file content from stage 2 (ours/HEAD)
		showCmd := exec.Command("git", "show", fmt.Sprintf(":2:%s", file))
		showCmd.Dir = workDir
		ourContent, ourErr := showCmd.Output()

		// Get the conflicted file content from stage 3 (theirs/MERGE_HEAD)
		showCmd = exec.Command("git", "show", fmt.Sprintf(":3:%s", file))
		showCmd.Dir = workDir
		theirContent, theirErr := showCmd.Output()

		// Read current file content (with conflict markers)
		currentContent, readErr := os.ReadFile(fmt.Sprintf("%s/%s", workDir, file))

		conflictDiffs.WriteString(fmt.Sprintf("\n## File: %s\n", file))
		if ourErr == nil {
			conflictDiffs.WriteString(fmt.Sprintf("### HEAD (current branch) version:\n```\n%s\n```\n", string(ourContent)))
		}
		if theirErr == nil {
			conflictDiffs.WriteString(fmt.Sprintf("### MERGE_HEAD (incoming branch) version:\n```\n%s\n```\n", string(theirContent)))
		}
		if readErr == nil {
			conflictDiffs.WriteString(fmt.Sprintf("### Current file with conflict markers:\n```\n%s\n```\n", string(currentContent)))
		}
	}

	// Build prompt
	prompt := prompts.BuildConflictResolutionPrompt(issueID, conflictFilesList, headSHA, mergeHeadSHA, conflictDiffs.String())

	// Execute agent to resolve conflicts
	// Model "auto" keeps the agent's default model (cursor "auto"; codex ignores it).
	executor := agent.NewExecutor(agentType)
	output, err := executor.ExecuteWithOptions(workDir, prompt, "auto", true)
	if err != nil {
		return fmt.Errorf("%s execution failed: %w\nOutput: %s", agentType, err, output)
	}

	// Check for conflict markers in files directly BEFORE staging
	// git diff --diff-filter=U reports conflicts until files are staged,
	// so we check file contents directly to verify markers are removed
	filesWithMarkers, err := HasConflictMarkers(workDir, conflictFiles)
	if err != nil {
		return fmt.Errorf("failed to check for conflict markers: %w", err)
	}
	if len(filesWithMarkers) > 0 {
		return fmt.Errorf("conflict markers still present in files: %v\nAgent output: %s", filesWithMarkers, output)
	}

	// Stage only conflict files to avoid staging untracked files
	// (e.g., coverage reports, build outputs from validations)
	// Handle both file modifications and deletions (delete/modify or delete/delete conflicts)
	for _, file := range conflictFiles {
		filePath := fmt.Sprintf("%s/%s", workDir, file)
		_, err := os.Stat(filePath)
		if err == nil {
			// File exists, stage it with git add
			addCmd := exec.Command("git", "add", file)
			addCmd.Dir = workDir
			if err := addCmd.Run(); err != nil {
				return fmt.Errorf("git add failed for file %s: %w", file, err)
			}
		} else if os.IsNotExist(err) {
			// File was deleted, stage the deletion with git rm
			rmCmd := exec.Command("git", "rm", file)
			rmCmd.Dir = workDir
			if err := rmCmd.Run(); err != nil {
				return fmt.Errorf("git rm failed for file %s: %w", file, err)
			}
		} else {
			// Other error (e.g., permission denied)
			return fmt.Errorf("failed to check file status for %s: %w", file, err)
		}
	}

	// Verify conflicts are resolved AFTER staging
	// git diff --diff-filter=U works correctly after staging
	remainingConflicts, err := ListConflicts(workDir)
	if err != nil {
		return fmt.Errorf("failed to verify conflict resolution: %w", err)
	}
	if len(remainingConflicts) > 0 {
		return fmt.Errorf("conflicts not fully resolved, remaining files: %v\nAgent output: %s", remainingConflicts, output)
	}

	// Commit the resolution
	commitMsg := "chore: resolve merge conflicts with AI"
	commitCmd := exec.Command("git", "commit", "-m", commitMsg)
	commitCmd.Dir = workDir
	commitOutput, err := commitCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit failed: %w, output: %s", err, string(commitOutput))
	}

	return nil
}

// GetCurrentCommitSHA returns the current HEAD commit SHA.
// workDir: Working directory (must be a git repository)
// Returns the commit SHA as a string, or an error if retrieval fails.
func GetCurrentCommitSHA(workDir string) (string, error) {
	if workDir == "" {
		return "", fmt.Errorf("work directory is required")
	}

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = workDir
	output, err := shaCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current commit SHA: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// ResetToCommit resets the repository to a specific commit using soft reset.
// This removes commits but keeps changes in the staging area.
// workDir: Working directory (must be a git repository)
// commitSHA: The commit SHA to reset to
// Returns an error if the reset operation fails.
func ResetToCommit(workDir, commitSHA string) error {
	if workDir == "" {
		return fmt.Errorf("work directory is required")
	}
	if commitSHA == "" {
		return fmt.Errorf("commit SHA is required")
	}

	resetCmd := exec.Command("git", "reset", "--soft", commitSHA)
	resetCmd.Dir = workDir
	output, err := resetCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reset to commit %s: %w, output: %s", commitSHA, err, string(output))
	}

	return nil
}
