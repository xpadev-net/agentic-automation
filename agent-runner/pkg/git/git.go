package git

import "fmt"

// CloneRepo clones the repository to the destination directory.
// Implementation will be added in T035.
func CloneRepo(token, repo, dest string) error {
	return fmt.Errorf("not implemented: T035")
}

// CreateBranch creates a new git branch or checks out existing branch.
// Behavior:
//   - If retryCount == 0: Creates new branch with "git checkout -b {branchName}"
//   - If retryCount > 0: Checks out existing branch with "git checkout {branchName}"
//     (or "git checkout -b {branchName} origin/{branchName}" if branch exists remotely)
//
// Implementation will be added in T035.
func CreateBranch(workDir, branchName string, retryCount int) error {
	return fmt.Errorf("not implemented: T035")
}

// HasChanges checks if there are any file changes in the workspace.
// Implementation will be added in T036.
func HasChanges(workDir string) (bool, error) {
	return false, fmt.Errorf("not implemented: T036")
}

// CommitChanges and PushBranch are implemented in committer.go (T036).

// CreatePR creates a Pull Request via GitHub API.
// If PR already exists for this branch, returns existing PR number.
// Implementation will be added in T035.
func CreatePR(token, repo, branchName string, issueNumber int) (int, error) {
	return 0, fmt.Errorf("not implemented: T035")
}
