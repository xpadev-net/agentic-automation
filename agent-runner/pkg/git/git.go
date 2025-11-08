package git

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"agent-runner/pkg/agent"
	githubutil "agent-runner/pkg/github"
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
//   - If retryCount == 0: Creates new branch from master with "git checkout -b {branchName}"
//   - If retryCount > 0: Checks out existing branch with "git checkout {branchName}"
//     (or "git checkout -b {branchName} origin/{branchName}" if branch exists remotely but not locally)
//
// workDir: Working directory (must be a git repository)
// branchName: Name of the branch to create or checkout
// retryCount: Number of retries (0 = first attempt, >0 = retry)
// existingBranchName: Optional existing branch name to checkout (if specified, this takes precedence)
func CreateBranch(workDir, branchName string, retryCount int, existingBranchName string) error {
	// Validate inputs
	if workDir == "" {
		return fmt.Errorf("work directory is required")
	}
	if branchName == "" {
		return fmt.Errorf("branch name is required")
	}

	// Check if workDir is a git repository
	gitDir := fmt.Sprintf("%s/.git", workDir)
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return fmt.Errorf("work directory %s is not a git repository", workDir)
	}

	// If existingBranchName is specified, checkout that branch (for continuing work on existing PR)
	if existingBranchName != "" {
		return checkoutExistingBranch(workDir, existingBranchName)
	}

	if retryCount == 0 {
		// First attempt: Create new branch from master
		// First, ensure we're on master branch
		checkoutMasterCmd := exec.Command("git", "checkout", "master")
		checkoutMasterCmd.Dir = workDir
		if err := checkoutMasterCmd.Run(); err != nil {
			// If master doesn't exist, try main
			checkoutMasterCmd = exec.Command("git", "checkout", "main")
			checkoutMasterCmd.Dir = workDir
			if err := checkoutMasterCmd.Run(); err != nil {
				return fmt.Errorf("failed to checkout master/main branch: %w", err)
			}
		}

		// Check if remote exists before fetching
		checkRemoteCmd := exec.Command("git", "remote", "get-url", "origin")
		checkRemoteCmd.Dir = workDir
		hasRemote := checkRemoteCmd.Run() == nil

		if hasRemote {
			// Pull latest changes from master
			fetchCmd := exec.Command("git", "fetch", "origin")
			fetchCmd.Dir = workDir
			if err := fetchCmd.Run(); err != nil {
				return fmt.Errorf("failed to fetch from origin: %w", err)
			}

			// Reset to origin/master or origin/main
			resetCmd := exec.Command("git", "reset", "--hard", "origin/master")
			resetCmd.Dir = workDir
			if err := resetCmd.Run(); err != nil {
				resetCmd = exec.Command("git", "reset", "--hard", "origin/main")
				resetCmd.Dir = workDir
				if err := resetCmd.Run(); err != nil {
					// If remote branch doesn't exist, continue with local branch
					// This is acceptable for local repositories
				}
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

	// Retry: Checkout existing branch
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

	// Branch doesn't exist locally or remotely
	return fmt.Errorf("branch %s does not exist locally or remotely", branchName)
}

// checkoutExistingBranch checks out an existing branch (local or remote).
// This is used when continuing work on an existing PR.
func checkoutExistingBranch(workDir, branchName string) error {
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

	// Branch doesn't exist locally or remotely
	return fmt.Errorf("branch %s does not exist locally or remotely", branchName)
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

// GeneratePRTitleAndBody generates PR title and body using cursor-agent in read-only mode.
// It uses Issue information, changed files, commit message, and git diff to generate the PR content.
// Returns title and body, or an error if generation fails.
func GeneratePRTitleAndBody(workDir string, issueNumber int, issuePrompt, commitMsg, agentType, cursorModel string) (string, string, error) {
	// Only cursor-agent is supported for PR generation
	if agentType != "cursor-agent" {
		return "", "", fmt.Errorf("PR generation is only supported for cursor-agent, got: %s", agentType)
	}

	// Get changed files
	changedFiles, err := GetChangedFiles(workDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to get changed files: %w", err)
	}

	// Build changed files list
	changedFilesList := strings.Join(changedFiles, "\n- ")
	if changedFilesList != "" {
		changedFilesList = "- " + changedFilesList
	} else {
		changedFilesList = "(no files changed)"
	}

	// Build prompt with structured information
	prompt := fmt.Sprintf("以下の情報を基に、Pull Requestのタイトルと概要を生成してください。\n\n<issue>\n<number>%d</number>\n<description>%s</description>\n</issue>\n\n<changed_files>\n%s\n</changed_files>\n\n<commit_message>\n%s\n</commit_message>\n\n作業ディレクトリで `git diff` を実行して変更内容を確認し、それを基にPRタイトルと概要を生成してください。\n\n出力形式:\n以下のXML形式で出力してください。\n<title>PRタイトル</title>\n<body>PR概要（Markdown形式可）</body>", issueNumber, issuePrompt, changedFilesList, commitMsg)

	// Execute cursor-agent in read-only mode
	executor := agent.NewExecutor(agentType)
	output, err := executor.ExecuteWithOptions(workDir, prompt, cursorModel, false)
	if err != nil {
		return "", "", fmt.Errorf("cursor-agent execution failed: %w", err)
	}

	// Parse output to extract title and body
	title, body, err := utils.ParsePRTitleAndBody(output)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse PR title and body: %w", err)
	}

	return title, body, nil
}

// CreatePR creates a Pull Request via GitHub API.
// If PR already exists for this branch, returns existing PR number (idempotent).
// token: GitHub PAT for authentication
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// branchName: Name of the branch to create PR from
// issueNumber: Issue number to reference in PR title
// title: Optional PR title (if empty, uses default format)
// body: Optional PR body (if empty, uses default format)
// Returns: PR number (existing or newly created)
func CreatePR(token, repo, branchName string, issueNumber int, title, body string) (int, error) {
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

	// Determine base branch using repository default branch
	base := "master"
	if repoInfo, repoResp, derr := client.Repositories.Get(ctx, owner, repoName); derr == nil && repoInfo != nil && repoInfo.DefaultBranch != nil && *repoInfo.DefaultBranch != "" {
		base = *repoInfo.DefaultBranch
		_ = repoResp // rate limit handled by caller if needed
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
