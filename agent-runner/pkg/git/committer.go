package git

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// CommitChanges commits all changes with the given message and returns the commit SHA.
func CommitChanges(workDir, message string) (string, error) {
	// Step 1: Stage all changes
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = workDir
	if err := addCmd.Run(); err != nil {
		return "", fmt.Errorf("git add failed: %w", err)
	}

	// Step 2: Commit
	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = workDir
	if err := commitCmd.Run(); err != nil {
		return "", fmt.Errorf("git commit failed: %w", err)
	}

	// Step 3: Get commit SHA
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = workDir
	output, err := shaCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get commit SHA: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// PushBranch pushes the branch to the remote repository.
// The token is used to authenticate with GitHub if the remote URL doesn't already contain credentials.
func PushBranch(workDir, branchName, token string) error {
	// Get current remote URL
	getUrlCmd := exec.Command("git", "remote", "get-url", "origin")
	getUrlCmd.Dir = workDir
	remoteUrlOutput, err := getUrlCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get remote URL: %w", err)
	}

	remoteUrl := strings.TrimSpace(string(remoteUrlOutput))

	// If URL doesn't contain a token, update it to include token for authentication
	// CloneRepo sets up URL with token, but we ensure it's set here as well
	// Check if URL already has a token: HTTPS URLs with token contain "://token@github.com"
	// SSH URLs (git@github.com) don't have tokens, so we should update them
	hasToken := strings.Contains(remoteUrl, "://") && strings.Contains(remoteUrl, "@github.com") && !strings.HasPrefix(remoteUrl, "git@")
	if token != "" && !hasToken {
		// Extract repo path from URL (e.g., owner/repo from https://github.com/owner/repo.git)
		// Support both https://github.com/owner/repo.git and https://token@github.com/owner/repo.git formats
		repoPath := extractRepoPath(remoteUrl)
		if repoPath == "" {
			return fmt.Errorf("failed to extract repo path from remote URL: %s", remoteUrl)
		}

		// Update remote URL to include token
		newUrl := fmt.Sprintf("https://%s@github.com/%s.git", token, repoPath)
		setUrlCmd := exec.Command("git", "remote", "set-url", "origin", newUrl)
		setUrlCmd.Dir = workDir
		if err := setUrlCmd.Run(); err != nil {
			return fmt.Errorf("failed to update remote URL: %w", err)
		}
	}

	// Push branch
	pushCmd := exec.Command("git", "push", "-u", "origin", branchName)
	pushCmd.Dir = workDir
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}

	return nil
}

// extractRepoPath extracts the repository path (owner/repo) from a GitHub URL.
// Supports formats:
// - https://github.com/owner/repo.git
// - https://token@github.com/owner/repo.git
// - git@github.com:owner/repo.git
func extractRepoPath(url string) string {
	// Pattern for https:// URLs (with optional token)
	// Capture group matches owner/repo (may include .git extension)
	httpsPattern := regexp.MustCompile(`^https://(?:[^@]+@)?github\.com/([^/]+/[^/]+)(?:\.git)?$`)
	if matches := httpsPattern.FindStringSubmatch(url); len(matches) == 2 {
		repoPath := matches[1]
		// Remove .git extension if present
		return strings.TrimSuffix(repoPath, ".git")
	}

	// Pattern for SSH URLs (git@github.com:owner/repo.git)
	// Capture group matches owner/repo (may include .git extension)
	sshPattern := regexp.MustCompile(`^git@github\.com:([^/]+/[^/]+)(?:\.git)?$`)
	if matches := sshPattern.FindStringSubmatch(url); len(matches) == 2 {
		repoPath := matches[1]
		// Remove .git extension if present
		return strings.TrimSuffix(repoPath, ".git")
	}

	return ""
}

// GetCurrentBranch returns the current branch name.
// This is a helper function for future extensibility.
func GetCurrentBranch(workDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}
