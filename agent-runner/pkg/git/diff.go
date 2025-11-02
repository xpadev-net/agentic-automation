package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GetChangedFiles returns a list of file paths that have been changed
// compared to HEAD. It includes both staged and unstaged changes, as well as untracked files.
// Returns an empty slice if there are no changes (not an error).
func GetChangedFiles(workDir string) ([]string, error) {
	// Check if workDir exists
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("work directory does not exist: %s", workDir)
	}

	// Use git status --porcelain to get all changed files (including untracked)
	// This includes staged, unstaged, and untracked files
	cmd := exec.Command("git", "-C", workDir, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		// Check if git command not found
		if err == exec.ErrNotFound {
			return nil, fmt.Errorf("git command not found: %w", err)
		}
		return nil, fmt.Errorf("git status --porcelain failed: %w", err)
	}

	// Empty output means no changes
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []string{}, nil
	}

	// Parse porcelain output: status codes + filename
	// Examples: " M file.txt" (modified), "A  file.txt" (staged), "?? untracked.txt" (untracked)
	lines := strings.Split(trimmed, "\n")
	var result []string
	for _, line := range lines {
		if len(line) >= 3 {
			// Extract filename (everything after the status codes)
			// Status codes are 2 characters, then space, then filename
			filename := strings.TrimSpace(line[3:])
			if filename != "" {
				result = append(result, filename)
			}
		}
	}
	return result, nil
}

// GetDiff returns the full diff output for all changes (staged + unstaged + untracked)
// compared to HEAD. For untracked files, it shows the full file content.
// Returns an empty string if there are no changes (not an error).
func GetDiff(workDir string) (string, error) {
	// Check if workDir exists
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return "", fmt.Errorf("work directory does not exist: %s", workDir)
	}

	var result strings.Builder

	// Get diff for tracked files (staged + unstaged)
	diff, err := runGitDiff(workDir, "HEAD")
	if err != nil {
		return "", err
	}
	if diff != "" {
		result.WriteString(diff)
	}

	// Get untracked files and add them to diff
	untrackedFiles, err := getUntrackedFiles(workDir)
	if err != nil {
		return "", err
	}

	for _, file := range untrackedFiles {
		filePath := filepath.Join(workDir, file)
		content, err := os.ReadFile(filePath)
		if err != nil {
			// Skip files that can't be read
			continue
		}

		// Format as new file in diff
		result.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", file, file))
		result.WriteString(fmt.Sprintf("new file mode 100644\n"))
		result.WriteString(fmt.Sprintf("index 0000000..%s\n", "0000000"))
		result.WriteString(fmt.Sprintf("--- /dev/null\n"))
		result.WriteString(fmt.Sprintf("+++ b/%s\n", file))
		result.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", strings.Count(string(content), "\n")+1))

		// Add content with + prefix
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			result.WriteString(fmt.Sprintf("+%s\n", line))
		}
	}

	return result.String(), nil
}

// GetStagedDiff returns the diff output for staged changes only.
// This is useful for Claude Code which auto-commits changes.
// Returns an empty string if there are no staged changes (not an error).
func GetStagedDiff(workDir string) (string, error) {
	// Check if workDir exists
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return "", fmt.Errorf("work directory does not exist: %s", workDir)
	}

	return runGitDiff(workDir, "--cached")
}

// GetUnstagedDiff returns the diff output for unstaged changes only.
// This includes unstaged modifications to tracked files and all untracked files.
// This is useful for Cursor which does not auto-commit changes.
// Returns an empty string if there are no unstaged changes (not an error).
func GetUnstagedDiff(workDir string) (string, error) {
	// Check if workDir exists
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return "", fmt.Errorf("work directory does not exist: %s", workDir)
	}

	var result strings.Builder

	// Get diff for unstaged tracked files
	diff, err := runGitDiff(workDir)
	if err != nil {
		return "", err
	}
	if diff != "" {
		result.WriteString(diff)
	}

	// Get untracked files and add them to diff
	untrackedFiles, err := getUntrackedFiles(workDir)
	if err != nil {
		return "", err
	}

	for _, file := range untrackedFiles {
		filePath := filepath.Join(workDir, file)
		content, err := os.ReadFile(filePath)
		if err != nil {
			// Skip files that can't be read
			continue
		}

		// Format as new file in diff
		result.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", file, file))
		result.WriteString(fmt.Sprintf("new file mode 100644\n"))
		result.WriteString(fmt.Sprintf("index 0000000..0000000\n"))
		result.WriteString(fmt.Sprintf("--- /dev/null\n"))
		result.WriteString(fmt.Sprintf("+++ b/%s\n", file))
		result.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", strings.Count(string(content), "\n")+1))

		// Add content with + prefix
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			result.WriteString(fmt.Sprintf("+%s\n", line))
		}
	}

	return result.String(), nil
}

// getUntrackedFiles returns a list of untracked files in the repository.
func getUntrackedFiles(workDir string) ([]string, error) {
	cmd := exec.Command("git", "-C", workDir, "ls-files", "--others", "--exclude-standard")
	output, err := cmd.Output()
	if err != nil {
		if err == exec.ErrNotFound {
			return nil, fmt.Errorf("git command not found: %w", err)
		}
		return nil, fmt.Errorf("git ls-files failed: %w", err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []string{}, nil
	}

	files := strings.Split(trimmed, "\n")
	var result []string
	for _, file := range files {
		if file != "" {
			result = append(result, file)
		}
	}
	return result, nil
}

// runGitDiff executes git diff command with the given arguments.
// It handles the case where exit code 1 means "no differences" (normal case).
func runGitDiff(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", workDir, "diff"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// exit code 1 means no differences (normal case)
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return "", nil
		}
		// Check if git command not found
		if err == exec.ErrNotFound {
			return "", fmt.Errorf("git command not found: %w", err)
		}
		return "", fmt.Errorf("git diff failed: %w", err)
	}
	return string(output), nil
}
