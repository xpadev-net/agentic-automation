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

	// If token is provided, check if URL needs to be updated
	// Update if:
	// 1. Token is not already in the URL (check if token string exists in credential part of URL)
	// 2. Can extract and rebuild URL in canonical format (github.com without port/enterprise host)
	if token != "" {
		// First check if token is already present in URL credential position
		// Supports any format: https://token@github.com:443/... or https://token@github.example.com/...
		// Check for pattern: https://token@ or token@ followed by host
		hasToken := strings.Contains(remoteUrl, token+"@") || strings.HasPrefix(remoteUrl, "https://"+token+"@") || strings.HasPrefix(remoteUrl, "http://"+token+"@")
		if hasToken {
			// Token is already in URL credential position, no need to update (supports non-canonical formats)
			// This handles cases like https://token@github.com:443/... or https://token@github.example.com/...
		} else {
			// Token not found, try to extract repo path and rebuild in canonical format
			repoPath := extractRepoPath(remoteUrl)
			if repoPath == "" {
				// Cannot extract repo path (non-canonical format like enterprise host or port)
				// Skip URL update and proceed with push using existing remote URL
				// This maintains backward compatibility with non-standard remotes
			} else {
				// Successfully extracted repo path, rebuild URL with new token
				newUrl := fmt.Sprintf("https://%s@github.com/%s.git", token, repoPath)
				setUrlCmd := exec.Command("git", "remote", "set-url", "origin", newUrl)
				setUrlCmd.Dir = workDir
				if err := setUrlCmd.Run(); err != nil {
					return fmt.Errorf("failed to update remote URL: %w", err)
				}
			}
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
// Only supports canonical github.com format (without port or enterprise hosts).
// Returns empty string for non-canonical formats to allow graceful fallback.
// Supports formats:
// - https://github.com/owner/repo.git
// - https://token@github.com/owner/repo.git
// - git@github.com:owner/repo.git
// Does NOT support:
// - https://github.com:443/owner/repo.git (port numbers)
// - https://github.example.com/owner/repo.git (enterprise hosts)
func extractRepoPath(url string) string {
	// Pattern for https:// URLs (with optional token, no port)
	// Capture group matches owner/repo (may include .git extension)
	// Only matches canonical github.com without port or enterprise hosts
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
