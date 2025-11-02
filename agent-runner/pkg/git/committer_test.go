package git

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// setupTestRepoWithRemote creates a temporary git repository with a remote configured.
func setupTestRepoWithRemote(t *testing.T, remoteURL string) (string, func()) {
	repoDir, cleanup := setupTestRepo(t)

	// Add remote
	cmd := exec.Command("git", "remote", "add", "origin", remoteURL)
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		cleanup()
		t.Fatalf("Failed to add remote: %v", err)
	}

	return repoDir, cleanup
}

// createBranch creates a new branch.
func createBranch(t *testing.T, repoDir, branchName string) {
	cmd := exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create branch %s: %v", branchName, err)
	}
}

// checkoutBranch checks out an existing branch.
func checkoutBranch(t *testing.T, repoDir, branchName string) {
	cmd := exec.Command("git", "checkout", branchName)
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to checkout branch %s: %v", branchName, err)
	}
}

// getRemoteURL gets the remote URL for verification.
func getRemoteURL(t *testing.T, repoDir string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get remote URL: %v", err)
	}
	return strings.TrimSpace(string(output))
}

// TestExtractRepoPath tests the extractRepoPath function with various URL formats.
func TestExtractRepoPath(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "HTTPS without .git",
			url:      "https://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS with .git",
			url:      "https://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS with token",
			url:      "https://token@github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "SSH without .git",
			url:      "git@github.com:owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "SSH with .git",
			url:      "git@github.com:owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "Invalid URL - GitLab",
			url:      "https://gitlab.com/owner/repo",
			expected: "",
		},
		{
			name:     "Invalid format - no protocol",
			url:      "owner/repo",
			expected: "",
		},
		{
			name:     "Empty string",
			url:      "",
			expected: "",
		},
		{
			name:     "HTTPS with path",
			url:      "https://github.com/owner/repo/path",
			expected: "",
		},
		{
			name:     "HTTPS with dots in repo name",
			url:      "https://github.com/owner/docs.v2.git",
			expected: "owner/docs.v2",
		},
		{
			name:     "HTTPS with dots in repo name without .git",
			url:      "https://github.com/owner/docs.v2",
			expected: "owner/docs.v2",
		},
		{
			name:     "SSH with dots in repo name",
			url:      "git@github.com:owner/docs.v2.git",
			expected: "owner/docs.v2",
		},
		{
			name:     "SSH with dots in repo name without .git",
			url:      "git@github.com:owner/docs.v2",
			expected: "owner/docs.v2",
		},
		{
			name:     "HTTPS with dots in owner and repo name",
			url:      "https://github.com/org.name/repo.v2.git",
			expected: "org.name/repo.v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRepoPath(tt.url)
			if got != tt.expected {
				t.Errorf("extractRepoPath(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

// TestCommitChanges_OneFile tests committing a single file.
func TestCommitChanges_OneFile(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a new file
	createTestFile(t, repoDir, "test.txt", "test content\n")

	// Commit changes
	sha, err := CommitChanges(repoDir, "Add test file")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Verify SHA format (40 characters hex)
	shaRegex := regexp.MustCompile(`^[a-f0-9]{40}$`)
	if !shaRegex.MatchString(sha) {
		t.Errorf("Commit SHA format invalid: %q (expected 40 hex characters)", sha)
	}

	// Verify file was committed
	cmd := exec.Command("git", "log", "-1", "--name-only", "--pretty=format:")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to check commit: %v", err)
	}

	if !strings.Contains(string(output), "test.txt") {
		t.Errorf("Expected test.txt in commit, got: %q", string(output))
	}
}

// TestCommitChanges_MultipleFiles tests committing multiple files.
func TestCommitChanges_MultipleFiles(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create multiple files
	createTestFile(t, repoDir, "file1.txt", "content1\n")
	createTestFile(t, repoDir, "file2.txt", "content2\n")
	createTestFile(t, repoDir, "file3.txt", "content3\n")

	// Commit changes
	sha, err := CommitChanges(repoDir, "Add multiple files")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Verify SHA format
	shaRegex := regexp.MustCompile(`^[a-f0-9]{40}$`)
	if !shaRegex.MatchString(sha) {
		t.Errorf("Commit SHA format invalid: %q", sha)
	}

	// Verify all files were committed
	cmd := exec.Command("git", "log", "-1", "--name-only", "--pretty=format:")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to check commit: %v", err)
	}

	outputStr := string(output)
	expectedFiles := []string{"file1.txt", "file2.txt", "file3.txt"}
	for _, expectedFile := range expectedFiles {
		if !strings.Contains(outputStr, expectedFile) {
			t.Errorf("Expected %q in commit, not found", expectedFile)
		}
	}
}

// TestCommitChanges_ModifiedFile tests committing a modified file.
func TestCommitChanges_ModifiedFile(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Modify existing file
	modifyTestFile(t, repoDir, "initial.txt", "modified content\n")

	// Commit changes
	sha, err := CommitChanges(repoDir, "Modify initial file")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Verify SHA format
	shaRegex := regexp.MustCompile(`^[a-f0-9]{40}$`)
	if !shaRegex.MatchString(sha) {
		t.Errorf("Commit SHA format invalid: %q", sha)
	}

	// Verify file was committed
	cmd := exec.Command("git", "show", "--name-only", "--pretty=format:", sha)
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to check commit: %v", err)
	}

	if !strings.Contains(string(output), "initial.txt") {
		t.Errorf("Expected initial.txt in commit, got: %q", string(output))
	}
}

// TestCommitChanges_NoChanges tests committing when there are no changes.
func TestCommitChanges_NoChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Try to commit with no changes
	_, err := CommitChanges(repoDir, "No changes commit")
	if err == nil {
		t.Error("Expected error for commit with no changes, got nil")
	}

	if !strings.Contains(err.Error(), "git commit failed") {
		t.Errorf("Expected 'git commit failed' in error message, got: %v", err)
	}
}

// TestCommitChanges_EmptyMessage tests committing with an empty message.
func TestCommitChanges_EmptyMessage(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a file
	createTestFile(t, repoDir, "test.txt", "content\n")

	// Try to commit with empty message
	_, err := CommitChanges(repoDir, "")
	if err == nil {
		t.Error("Expected error for empty commit message, got nil")
	}

	if !strings.Contains(err.Error(), "git commit failed") {
		t.Errorf("Expected 'git commit failed' in error message, got: %v", err)
	}
}

// TestCommitChanges_InvalidWorkDir tests committing with an invalid directory.
func TestCommitChanges_InvalidWorkDir(t *testing.T) {
	_, err := CommitChanges("/nonexistent/directory", "Test commit")
	if err == nil {
		t.Error("Expected error for nonexistent directory, got nil")
	}

	if !strings.Contains(err.Error(), "git add failed") {
		t.Errorf("Expected 'git add failed' in error message, got: %v", err)
	}
}

// TestCommitChanges_CommitSHAFormat tests that the commit SHA is in the correct format.
func TestCommitChanges_CommitSHAFormat(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a file
	createTestFile(t, repoDir, "test.txt", "content\n")

	// Commit changes
	sha, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Verify SHA is exactly 40 hex characters
	shaRegex := regexp.MustCompile(`^[a-f0-9]{40}$`)
	if !shaRegex.MatchString(sha) {
		t.Errorf("Commit SHA format invalid: %q (expected 40 hex characters)", sha)
	}

	if len(sha) != 40 {
		t.Errorf("Commit SHA length = %d, want 40", len(sha))
	}
}

// TestGetCurrentBranch_DefaultBranch tests getting the default branch name.
func TestGetCurrentBranch_DefaultBranch(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	branch, err := GetCurrentBranch(repoDir)
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}

	// Git 2.28+ uses "main", older versions use "master"
	if branch != "master" && branch != "main" {
		t.Errorf("GetCurrentBranch() = %q, want 'master' or 'main'", branch)
	}
}

// TestGetCurrentBranch_FeatureBranch tests getting the current branch name after creating a branch.
func TestGetCurrentBranch_FeatureBranch(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create and checkout a new branch
	createBranch(t, repoDir, "feature-branch")

	branch, err := GetCurrentBranch(repoDir)
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}

	if branch != "feature-branch" {
		t.Errorf("GetCurrentBranch() = %q, want 'feature-branch'", branch)
	}
}

// TestGetCurrentBranch_InvalidWorkDir tests getting branch name with invalid directory.
func TestGetCurrentBranch_InvalidWorkDir(t *testing.T) {
	_, err := GetCurrentBranch("/nonexistent/directory")
	if err == nil {
		t.Error("Expected error for nonexistent directory, got nil")
	}

	if !strings.Contains(err.Error(), "failed to get current branch") {
		t.Errorf("Expected 'failed to get current branch' in error message, got: %v", err)
	}
}

// TestGetCurrentBranch_NotGitRepo tests getting branch name in a non-git directory.
func TestGetCurrentBranch_NotGitRepo(t *testing.T) {
	// Create temporary directory without git
	tmpDir, err := os.MkdirTemp("", "git-committer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = GetCurrentBranch(tmpDir)
	if err == nil {
		t.Error("Expected error for non-git directory, got nil")
	}

	if !strings.Contains(err.Error(), "failed to get current branch") {
		t.Errorf("Expected 'failed to get current branch' in error message, got: %v", err)
	}
}

// TestPushBranch_UpdateRemoteURLWithoutToken tests updating remote URL when token is not in URL.
func TestPushBranch_UpdateRemoteURLWithoutToken(t *testing.T) {
	repoDir, cleanup := setupTestRepoWithRemote(t, "https://github.com/owner/repo.git")
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	// Commit the file
	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Call PushBranch with a token
	token := "test-token-123"
	err = PushBranch(repoDir, "test-branch", token)
	if err == nil {
		// Push will fail because we don't have a real remote, but URL should be updated
		// Check if URL was updated
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://test-token-123@github.com/owner/repo.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q", remoteURL, expectedURL)
		}
	} else {
		// Even if push fails, URL should be updated
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://test-token-123@github.com/owner/repo.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q (push failed but URL should be updated)", remoteURL, expectedURL)
		}
	}
}

// TestPushBranch_RemoteURLAlreadyHasToken tests that URL is not updated when it already has a token.
func TestPushBranch_RemoteURLAlreadyHasToken(t *testing.T) {
	repoDir, cleanup := setupTestRepoWithRemote(t, "https://existing-token@github.com/owner/repo.git")
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Call PushBranch with a different token
	token := "new-token-456"
	err = PushBranch(repoDir, "test-branch", token)
	if err == nil {
		// Check if URL was NOT updated (should still have existing-token)
		remoteURL := getRemoteURL(t, repoDir)
		if !strings.Contains(remoteURL, "existing-token") {
			t.Errorf("Remote URL should not be updated, but got: %q", remoteURL)
		}
		if strings.Contains(remoteURL, "new-token-456") {
			t.Errorf("Remote URL should not contain new token, got: %q", remoteURL)
		}
	} else {
		// Even if push fails, URL should NOT be updated
		remoteURL := getRemoteURL(t, repoDir)
		if !strings.Contains(remoteURL, "existing-token") {
			t.Errorf("Remote URL should not be updated, but got: %q", remoteURL)
		}
	}
}

// TestPushBranch_NoTokenProvided tests PushBranch when no token is provided.
func TestPushBranch_NoTokenProvided(t *testing.T) {
	repoDir, cleanup := setupTestRepoWithRemote(t, "https://github.com/owner/repo.git")
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Call PushBranch with empty token
	err = PushBranch(repoDir, "test-branch", "")
	if err == nil {
		// Push will fail, but URL should remain unchanged
		remoteURL := getRemoteURL(t, repoDir)
		if remoteURL != "https://github.com/owner/repo.git" {
			t.Errorf("Remote URL should not be updated, got: %q", remoteURL)
		}
	} else {
		// Check that URL was not updated
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://github.com/owner/repo.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q (should not be updated when no token)", remoteURL, expectedURL)
		}
	}
}

// TestPushBranch_NoRemoteConfigured tests PushBranch when no remote is configured.
func TestPushBranch_NoRemoteConfigured(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Try to push without remote
	err = PushBranch(repoDir, "test-branch", "token")
	if err == nil {
		t.Error("Expected error when no remote is configured, got nil")
	}

	if !strings.Contains(err.Error(), "failed to get remote URL") {
		t.Errorf("Expected 'failed to get remote URL' in error message, got: %v", err)
	}
}

// TestPushBranch_InvalidRemoteURL tests PushBranch with an invalid remote URL.
func TestPushBranch_InvalidRemoteURL(t *testing.T) {
	repoDir, cleanup := setupTestRepoWithRemote(t, "https://invalid-url")
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Try to push with invalid URL
	err = PushBranch(repoDir, "test-branch", "token")
	if err == nil {
		t.Error("Expected error for invalid remote URL, got nil")
	}

	if !strings.Contains(err.Error(), "failed to extract repo path") {
		t.Errorf("Expected 'failed to extract repo path' in error message, got: %v", err)
	}
}

// TestPushBranch_InvalidWorkDir tests PushBranch with an invalid directory.
func TestPushBranch_InvalidWorkDir(t *testing.T) {
	err := PushBranch("/nonexistent/directory", "test-branch", "token")
	if err == nil {
		t.Error("Expected error for nonexistent directory, got nil")
	}

	if !strings.Contains(err.Error(), "failed to get remote URL") {
		t.Errorf("Expected 'failed to get remote URL' in error message, got: %v", err)
	}
}

// TestPushBranch_SSHRemoteURL tests PushBranch with SSH remote URL format.
func TestPushBranch_SSHRemoteURL(t *testing.T) {
	repoDir, cleanup := setupTestRepoWithRemote(t, "git@github.com:owner/repo.git")
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Call PushBranch with a token (should convert SSH to HTTPS with token)
	token := "test-token-789"
	err = PushBranch(repoDir, "test-branch", token)
	if err == nil {
		// Check if URL was updated to HTTPS format with token
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://test-token-789@github.com/owner/repo.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q", remoteURL, expectedURL)
		}
	} else {
		// Even if push fails, URL should be updated
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://test-token-789@github.com/owner/repo.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q (push failed but URL should be updated)", remoteURL, expectedURL)
		}
	}
}

// TestPushBranch_RemoteURLWithDots tests PushBranch with remote URL containing dots in repo name.
func TestPushBranch_RemoteURLWithDots(t *testing.T) {
	repoDir, cleanup := setupTestRepoWithRemote(t, "https://github.com/owner/docs.v2.git")
	defer cleanup()

	// Create a branch and make a commit
	createBranch(t, repoDir, "test-branch")
	createTestFile(t, repoDir, "test.txt", "content\n")

	_, err := CommitChanges(repoDir, "Test commit")
	if err != nil {
		t.Fatalf("CommitChanges() error = %v", err)
	}

	// Call PushBranch with a token
	token := "test-token-123"
	err = PushBranch(repoDir, "test-branch", token)
	if err == nil {
		// Check if URL was updated correctly (should preserve dots in repo name)
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://test-token-123@github.com/owner/docs.v2.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q", remoteURL, expectedURL)
		}
	} else {
		// Even if push fails, URL should be updated correctly
		remoteURL := getRemoteURL(t, repoDir)
		expectedURL := "https://test-token-123@github.com/owner/docs.v2.git"
		if remoteURL != expectedURL {
			t.Errorf("Remote URL = %q, want %q (push failed but URL should be updated)", remoteURL, expectedURL)
		}
	}
}
