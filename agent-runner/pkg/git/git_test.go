package git

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCloneRepo_InvalidInputs tests CloneRepo with invalid inputs.
func TestCloneRepo_InvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		repo        string
		dest        string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty repo",
			token:       "test-token",
			repo:        "",
			dest:        "/tmp/test",
			expectError: true,
			errorMsg:    "repository name is required",
		},
		{
			name:        "empty dest",
			token:       "test-token",
			repo:        "owner/repo",
			dest:        "",
			expectError: true,
			errorMsg:    "destination directory is required",
		},
		{
			name:        "invalid repo format - no slash",
			token:       "test-token",
			repo:        "invalidrepo",
			dest:        "/tmp/test",
			expectError: true,
			errorMsg:    "repository must be in format owner/repo",
		},
		{
			name:        "invalid repo format - multiple slashes",
			token:       "test-token",
			repo:        "owner/repo/path",
			dest:        "/tmp/test",
			expectError: true,
			errorMsg:    "repository must be in format owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CloneRepo(tt.token, tt.repo, tt.dest)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestCloneRepo_TokenMasking tests that tokens are masked in error messages.
func TestCloneRepo_TokenMasking(t *testing.T) {
	// This test verifies that sensitive tokens are not exposed in error messages
	// We'll use an invalid repository URL to trigger an error
	token := "secret-token-12345"
	repo := "owner/repo"
	dest := "/nonexistent/path/to/test"

	err := CloneRepo(token, repo, dest)
	if err == nil {
		t.Skip("CloneRepo did not return error, cannot test token masking")
		return
	}

	errorMsg := err.Error()
	if strings.Contains(errorMsg, token) {
		t.Errorf("error message contains unmasked token: %s", errorMsg)
	}
	if strings.Contains(errorMsg, "secret-token-12345") {
		t.Errorf("error message contains unmasked token: %s", errorMsg)
	}
	// Token should be masked as *** and x-access-token:***
	if !strings.Contains(errorMsg, "***") {
		t.Logf("warning: token may not be masked in error message: %s", errorMsg)
	}
	if strings.Contains(errorMsg, "x-access-token:"+token) {
		t.Errorf("error message contains unmasked x-access-token:<token> segment: %s", errorMsg)
	}
}

// TestCreateBranch_InvalidInputs tests CreateBranch with invalid inputs.
func TestCreateBranch_InvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		workDir     string
		branchName  string
		retryCount  int
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty workDir",
			workDir:     "",
			branchName:  "test-branch",
			retryCount:  0,
			expectError: true,
			errorMsg:    "work directory is required",
		},
		{
			name:        "empty branchName",
			workDir:     "/tmp/test",
			branchName:  "",
			retryCount:  0,
			expectError: true,
			errorMsg:    "branch name is required",
		},
		{
			name:        "non-existent directory",
			workDir:     "/nonexistent/path",
			branchName:  "test-branch",
			retryCount:  0,
			expectError: true,
			errorMsg:    "is not a git repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CreateBranch(tt.workDir, tt.branchName, tt.retryCount, "")
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestCreateBranch_NewBranch tests creating a new branch (retryCount == 0).
func TestCreateBranch_NewBranch(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create master branch (if not exists)
	checkoutMasterCmd := exec.Command("git", "checkout", "-b", "master")
	checkoutMasterCmd.Dir = repoDir
	_ = checkoutMasterCmd.Run() // Ignore error if branch already exists

	// Add a commit to master
	createTestFile(t, repoDir, "test.txt", "test content")
	stageFile(t, repoDir, "test.txt")
	commitCmd := exec.Command("git", "commit", "-m", "test commit")
	commitCmd.Dir = repoDir
	if err := commitCmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Test creating new branch
	branchName := "feature/test-branch"
	err := CreateBranch(repoDir, branchName, 0, "")
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Verify branch was created and checked out
	currentBranch, err := GetCurrentBranch(repoDir)
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}
	if currentBranch != branchName {
		t.Errorf("expected current branch %q, got %q", branchName, currentBranch)
	}
}

// TestCreateBranch_ExistingLocalBranch tests checking out existing local branch (retryCount > 0).
func TestCreateBranch_ExistingLocalBranch(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create master branch
	checkoutMasterCmd := exec.Command("git", "checkout", "-b", "master")
	checkoutMasterCmd.Dir = repoDir
	_ = checkoutMasterCmd.Run()

	// Create a test branch
	branchName := "feature/existing-branch"
	createBranchCmd := exec.Command("git", "checkout", "-b", branchName)
	createBranchCmd.Dir = repoDir
	if err := createBranchCmd.Run(); err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}

	// Switch back to master
	checkoutMasterCmd = exec.Command("git", "checkout", "master")
	checkoutMasterCmd.Dir = repoDir
	if err := checkoutMasterCmd.Run(); err != nil {
		t.Fatalf("Failed to checkout master: %v", err)
	}

	// Test checking out existing branch (retryCount > 0)
	err := CreateBranch(repoDir, branchName, 1, "")
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Verify branch was checked out
	currentBranch, err := GetCurrentBranch(repoDir)
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}
	if currentBranch != branchName {
		t.Errorf("expected current branch %q, got %q", branchName, currentBranch)
	}
}

// TestCreateBranch_ExistingRemoteBranch tests checking out existing remote branch (retryCount > 0).
func TestCreateBranch_ExistingRemoteBranch(t *testing.T) {
	// This test requires a remote repository setup
	// For now, we'll skip it or create a mock remote
	t.Skip("Skipping remote branch test - requires remote repository setup")
}

// TestCreateBranch_NonExistentBranch tests error when branch doesn't exist (retryCount > 0).
func TestCreateBranch_NonExistentBranch(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create master branch
	checkoutMasterCmd := exec.Command("git", "checkout", "-b", "master")
	checkoutMasterCmd.Dir = repoDir
	_ = checkoutMasterCmd.Run()

	// Try to checkout non-existent branch (retryCount > 0)
	branchName := "feature/non-existent"
	err := CreateBranch(repoDir, branchName, 1, "")
	if err == nil {
		t.Errorf("expected error for non-existent branch, got nil")
		return
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error message %q does not contain 'does not exist'", err.Error())
	}
}

// TestCreatePR_InvalidInputs tests CreatePR with invalid inputs.
func TestCreatePR_InvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		repo        string
		branchName  string
		issueNumber int
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty repo",
			token:       "test-token",
			repo:        "",
			branchName:  "feature/test",
			issueNumber: 1,
			expectError: true,
			errorMsg:    "repository name is required",
		},
		{
			name:        "empty branchName",
			token:       "test-token",
			repo:        "owner/repo",
			branchName:  "",
			issueNumber: 1,
			expectError: true,
			errorMsg:    "branch name is required",
		},
		{
			name:        "invalid issue number",
			token:       "test-token",
			repo:        "owner/repo",
			branchName:  "feature/test",
			issueNumber: 0,
			expectError: true,
			errorMsg:    "issue number must be positive",
		},
		{
			name:        "invalid repo format",
			token:       "test-token",
			repo:        "invalidrepo",
			branchName:  "feature/test",
			issueNumber: 1,
			expectError: true,
			errorMsg:    "repository must be in format owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreatePR(tt.token, tt.repo, tt.branchName, tt.issueNumber, "", "", "master")
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// Minimal test to assert CreatePR determines base branch via API client path exists.
// Full integration is skipped by default.
func TestCreatePR_DefaultBaseDetection_Smoke(t *testing.T) {
	t.Skip("Default branch detection requires live GitHub; skipping")
}

// TestCreatePR_Integration tests CreatePR with actual GitHub API.
// This test is skipped by default and should be run manually with valid credentials.
func TestCreatePR_Integration(t *testing.T) {
	t.Skip("Skipping integration test - requires valid GitHub token and repository access")
	// To run this test:
	// 1. Configure GitHub App credentials and obtain an installation token for the target repo
	// 2. Use a test repository
	// 3. Remove t.Skip() call
	//
	// token := "<installation-token>"
	// repo := "owner/repo"
	// branchName := "test-branch"
	// issueNumber := 1
	//
	// prNumber, err := CreatePR(token, repo, branchName, issueNumber, "", "")
	// if err != nil {
	// 	t.Fatalf("CreatePR failed: %v", err)
	// }
	// if prNumber <= 0 {
	// 	t.Errorf("invalid PR number: %d", prNumber)
	// }
}
