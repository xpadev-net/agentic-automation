package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

// CloneRepo clones the repository to the destination directory.
// token: GitHub PAT for authentication
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// dest: Destination directory path
func CloneRepo(token, repo, dest string) error {
	// Validate inputs
	if token == "" {
		return fmt.Errorf("GitHub token is required")
	}
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

	// Convert owner/repo to HTTPS URL with token
	// Format: https://token@github.com/owner/repo.git
	cloneURL := fmt.Sprintf("https://%s@github.com/%s.git", token, repo)

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
		// Mask token in error output
		outputStr := strings.ReplaceAll(string(output), token, "***")
		outputStr = strings.ReplaceAll(outputStr, cloneURL, fmt.Sprintf("https://***@github.com/%s.git", repo))
		return fmt.Errorf("git clone failed: %w, output: %s", err, outputStr)
	}

	return nil
}

// CreateBranch creates a new git branch or checks out existing branch.
// Behavior:
//   - If retryCount == 0: Creates new branch from master with "git checkout -b {branchName}"
//   - If retryCount > 0: Checks out existing branch with "git checkout {branchName}"
//     (or "git checkout -b {branchName} origin/{branchName}" if branch exists remotely but not locally)
//
// workDir: Working directory (must be a git repository)
// branchName: Name of the branch to create or checkout
// retryCount: Number of retries (0 = first attempt, >0 = retry)
func CreateBranch(workDir, branchName string, retryCount int) error {
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

// HasChanges checks if there are any file changes in the workspace.
// Implementation will be added in T036.
func HasChanges(workDir string) (bool, error) {
	return false, fmt.Errorf("not implemented: T036")
}

// CommitChanges and PushBranch are implemented in committer.go (T036).

// CreatePR creates a Pull Request via GitHub API.
// If PR already exists for this branch, returns existing PR number (idempotent).
// token: GitHub PAT for authentication
// repo: Repository in format owner/repo (e.g., "octocat/Hello-World")
// branchName: Name of the branch to create PR from
// issueNumber: Issue number to reference in PR title
// Returns: PR number (existing or newly created)
func CreatePR(token, repo, branchName string, issueNumber int) (int, error) {
	// Validate inputs
	if token == "" {
		return 0, fmt.Errorf("GitHub token is required")
	}
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
		// Check if it's a rate limit error
		if resp != nil && resp.StatusCode == 403 {
			return 0, fmt.Errorf("GitHub API rate limit exceeded")
		}
		return 0, fmt.Errorf("failed to list pull requests: %w", err)
	}

	// If PR exists, return its number
	if len(prs) > 0 {
		// Return the first matching PR (there should typically be only one)
		if prs[0].Number != nil {
			return *prs[0].Number, nil
		}
	}

	// Create new PR
	base := "master" // Default base branch
	title := fmt.Sprintf("Fix: issue #%d", issueNumber)
	body := "自動生成: エージェントによる修正"

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
		// Check if base branch doesn't exist (might be "main" instead of "master")
		if resp != nil && resp.StatusCode == 422 {
			// Try with "main" as base branch
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

	if pr.Number == nil {
		return 0, fmt.Errorf("created PR but PR number is nil")
	}

	return *pr.Number, nil
}
