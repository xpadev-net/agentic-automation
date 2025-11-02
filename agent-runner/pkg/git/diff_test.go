package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a temporary git repository for testing.
// Returns the repository path and a cleanup function.
func setupTestRepo(t *testing.T) (string, func()) {
	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "git-diff-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		cleanup()
		t.Fatalf("Failed to initialize git repository: %v", err)
	}

	// Configure git user (required for commits)
	configNameCmd := exec.Command("git", "config", "user.name", "Test User")
	configNameCmd.Dir = tmpDir
	if err := configNameCmd.Run(); err != nil {
		cleanup()
		t.Fatalf("Failed to configure git user.name: %v", err)
	}

	configEmailCmd := exec.Command("git", "config", "user.email", "test@example.com")
	configEmailCmd.Dir = tmpDir
	if err := configEmailCmd.Run(); err != nil {
		cleanup()
		t.Fatalf("Failed to configure git user.email: %v", err)
	}

	// Create initial commit
	initialFile := filepath.Join(tmpDir, "initial.txt")
	if err := os.WriteFile(initialFile, []byte("initial content\n"), 0644); err != nil {
		cleanup()
		t.Fatalf("Failed to create initial file: %v", err)
	}

	addCmd := exec.Command("git", "add", "initial.txt")
	addCmd.Dir = tmpDir
	if err := addCmd.Run(); err != nil {
		cleanup()
		t.Fatalf("Failed to add initial file: %v", err)
	}

	commitCmd := exec.Command("git", "commit", "-m", "Initial commit")
	commitCmd.Dir = tmpDir
	if err := commitCmd.Run(); err != nil {
		cleanup()
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	return tmpDir, cleanup
}

// createTestFile creates a file in the test repository.
func createTestFile(t *testing.T, repoDir, filename, content string) {
	filePath := filepath.Join(repoDir, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file %s: %v", filename, err)
	}
}

// modifyTestFile modifies an existing file in the test repository.
func modifyTestFile(t *testing.T, repoDir, filename, newContent string) {
	filePath := filepath.Join(repoDir, filename)
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file %s: %v", filename, err)
	}
}

// stageFile stages a file in git.
func stageFile(t *testing.T, repoDir, filename string) {
	cmd := exec.Command("git", "add", filename)
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to stage file %s: %v", filename, err)
	}
}

func TestGetChangedFiles_NoChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	files, err := GetChangedFiles(repoDir)
	if err != nil {
		t.Fatalf("GetChangedFiles returned error: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected empty slice, got %v", files)
	}
}

func TestGetChangedFiles_OneFileChanged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create and modify a file
	createTestFile(t, repoDir, "test1.txt", "content\n")

	files, err := GetChangedFiles(repoDir)
	if err != nil {
		t.Fatalf("GetChangedFiles returned error: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 file, got %d: %v", len(files), files)
	}

	if files[0] != "test1.txt" {
		t.Errorf("Expected 'test1.txt', got %q", files[0])
	}
}

func TestGetChangedFiles_MultipleFilesChanged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create multiple files
	createTestFile(t, repoDir, "file1.txt", "content1\n")
	createTestFile(t, repoDir, "file2.txt", "content2\n")
	createTestFile(t, repoDir, "file3.txt", "content3\n")

	files, err := GetChangedFiles(repoDir)
	if err != nil {
		t.Fatalf("GetChangedFiles returned error: %v", err)
	}

	if len(files) != 3 {
		t.Fatalf("Expected 3 files, got %d: %v", len(files), files)
	}

	// Check that all files are present
	fileMap := make(map[string]bool)
	for _, f := range files {
		fileMap[f] = true
	}

	expected := []string{"file1.txt", "file2.txt", "file3.txt"}
	for _, expectedFile := range expected {
		if !fileMap[expectedFile] {
			t.Errorf("Expected file %q not found in result", expectedFile)
		}
	}
}

func TestGetChangedFiles_StagedAndUnstaged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create unstaged file
	createTestFile(t, repoDir, "unstaged.txt", "unstaged content\n")

	// Create and stage file
	createTestFile(t, repoDir, "staged.txt", "staged content\n")
	stageFile(t, repoDir, "staged.txt")

	files, err := GetChangedFiles(repoDir)
	if err != nil {
		t.Fatalf("GetChangedFiles returned error: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("Expected 2 files, got %d: %v", len(files), files)
	}

	// Both should be included
	fileMap := make(map[string]bool)
	for _, f := range files {
		fileMap[f] = true
	}

	if !fileMap["unstaged.txt"] {
		t.Error("Expected 'unstaged.txt' in result")
	}
	if !fileMap["staged.txt"] {
		t.Error("Expected 'staged.txt' in result")
	}
}

func TestGetChangedFiles_DirectoryNotExists(t *testing.T) {
	_, err := GetChangedFiles("/nonexistent/directory")
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}

	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Expected error about directory not existing, got: %v", err)
	}
}

func TestGetChangedFiles_NotGitRepository(t *testing.T) {
	// Create temporary directory without git
	tmpDir, err := os.MkdirTemp("", "git-diff-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = GetChangedFiles(tmpDir)
	if err == nil {
		t.Error("Expected error for non-git directory")
	}
}

func TestGetDiff_NoChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	diff, err := GetDiff(repoDir)
	if err != nil {
		t.Fatalf("GetDiff returned error: %v", err)
	}

	if diff != "" {
		t.Errorf("Expected empty diff, got: %q", diff)
	}
}

func TestGetDiff_OneFileChanged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create and modify a file
	createTestFile(t, repoDir, "test.txt", "new content\n")

	diff, err := GetDiff(repoDir)
	if err != nil {
		t.Fatalf("GetDiff returned error: %v", err)
	}

	if diff == "" {
		t.Error("Expected non-empty diff")
	}

	if !strings.Contains(diff, "test.txt") {
		t.Errorf("Expected diff to contain 'test.txt', got: %q", diff)
	}

	if !strings.Contains(diff, "new content") {
		t.Errorf("Expected diff to contain 'new content', got: %q", diff)
	}
}

func TestGetDiff_MultipleFilesChanged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create multiple files
	createTestFile(t, repoDir, "file1.txt", "content1\n")
	createTestFile(t, repoDir, "file2.txt", "content2\n")

	diff, err := GetDiff(repoDir)
	if err != nil {
		t.Fatalf("GetDiff returned error: %v", err)
	}

	if diff == "" {
		t.Error("Expected non-empty diff")
	}

	if !strings.Contains(diff, "file1.txt") {
		t.Error("Expected diff to contain 'file1.txt'")
	}

	if !strings.Contains(diff, "file2.txt") {
		t.Error("Expected diff to contain 'file2.txt'")
	}
}

func TestGetDiff_NewlineHandling(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a file with multiple lines
	content := "line1\nline2\nline3\n"
	createTestFile(t, repoDir, "multiline.txt", content)

	diff, err := GetDiff(repoDir)
	if err != nil {
		t.Fatalf("GetDiff returned error: %v", err)
	}

	if !strings.Contains(diff, "\n") {
		t.Error("Expected diff to contain newlines")
	}
}

func TestGetDiff_TrailingNewline(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create file with trailing newline (common case)
	content := "line1\nline2\n"
	createTestFile(t, repoDir, "with_newline.txt", content)

	diff, err := GetDiff(repoDir)
	if err != nil {
		t.Fatalf("GetDiff returned error: %v", err)
	}

	// Count the number of content lines with "+" prefix (excluding hunk header and file paths)
	// Should be exactly 2 (line1 and line2), not 3 (no phantom blank line)
	lines := strings.Split(diff, "\n")
	var plusLineCount int
	for _, line := range lines {
		// Count lines that start with "+" and are actual content lines
		// Exclude: "@@ -0,0 +1,2 @@" (hunk header), "+++ b/file.txt" (file path)
		if strings.HasPrefix(line, "+") && len(line) > 1 {
			// Check if it's a content line (starts with "+" followed by non-special char)
			secondChar := line[1]
			if secondChar != '+' && secondChar != '@' {
				plusLineCount++
			}
		}
	}
	if plusLineCount != 2 {
		t.Errorf("Expected 2 content lines with + prefix, got %d. Diff:\n%s", plusLineCount, diff)
	}

	// Check hunk header - should report 2 lines (not 3, avoiding phantom blank line)
	if !strings.Contains(diff, "@@ -0,0 +1,2 @@") {
		t.Errorf("Expected hunk header to show 2 lines, got:\n%s", diff)
	}
}

func TestGetDiff_WithoutTrailingNewline(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create file without trailing newline
	content := "line1\nline2"
	createTestFile(t, repoDir, "no_newline.txt", content)

	diff, err := GetDiff(repoDir)
	if err != nil {
		t.Fatalf("GetDiff returned error: %v", err)
	}

	// Should have exactly 2 lines
	lines := strings.Split(diff, "\n")
	var plusLineCount int
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && len(line) > 1 {
			secondChar := line[1]
			if secondChar != '+' && secondChar != '@' {
				plusLineCount++
			}
		}
	}
	if plusLineCount != 2 {
		t.Errorf("Expected 2 content lines with + prefix, got %d. Diff:\n%s", plusLineCount, diff)
	}

	// Check hunk header
	if !strings.Contains(diff, "@@ -0,0 +1,2 @@") {
		t.Errorf("Expected hunk header to show 2 lines, got:\n%s", diff)
	}
}

func TestGetStagedDiff_NoStagedChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create unstaged file only
	createTestFile(t, repoDir, "unstaged.txt", "content\n")

	diff, err := GetStagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetStagedDiff returned error: %v", err)
	}

	if diff != "" {
		t.Errorf("Expected empty diff for unstaged changes, got: %q", diff)
	}
}

func TestGetStagedDiff_StagedChangesOnly(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create and stage file
	createTestFile(t, repoDir, "staged.txt", "staged content\n")
	stageFile(t, repoDir, "staged.txt")

	diff, err := GetStagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetStagedDiff returned error: %v", err)
	}

	if diff == "" {
		t.Error("Expected non-empty diff for staged changes")
	}

	if !strings.Contains(diff, "staged.txt") {
		t.Errorf("Expected diff to contain 'staged.txt', got: %q", diff)
	}
}

func TestGetStagedDiff_StagedAndUnstaged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create unstaged file
	createTestFile(t, repoDir, "unstaged.txt", "unstaged\n")

	// Create and stage file
	createTestFile(t, repoDir, "staged.txt", "staged\n")
	stageFile(t, repoDir, "staged.txt")

	diff, err := GetStagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetStagedDiff returned error: %v", err)
	}

	// Should only contain staged file
	if !strings.Contains(diff, "staged.txt") {
		t.Error("Expected diff to contain 'staged.txt'")
	}

	if strings.Contains(diff, "unstaged.txt") {
		t.Error("Expected diff to NOT contain 'unstaged.txt'")
	}
}

func TestGetStagedDiff_NoChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	diff, err := GetStagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetStagedDiff returned error: %v", err)
	}

	if diff != "" {
		t.Errorf("Expected empty diff, got: %q", diff)
	}
}

func TestGetUnstagedDiff_NoUnstagedChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create and stage file
	createTestFile(t, repoDir, "staged.txt", "content\n")
	stageFile(t, repoDir, "staged.txt")

	diff, err := GetUnstagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetUnstagedDiff returned error: %v", err)
	}

	if diff != "" {
		t.Errorf("Expected empty diff for staged-only changes, got: %q", diff)
	}
}

func TestGetUnstagedDiff_UnstagedChangesOnly(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create unstaged file
	createTestFile(t, repoDir, "unstaged.txt", "unstaged content\n")

	diff, err := GetUnstagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetUnstagedDiff returned error: %v", err)
	}

	if diff == "" {
		t.Error("Expected non-empty diff for unstaged changes")
	}

	if !strings.Contains(diff, "unstaged.txt") {
		t.Errorf("Expected diff to contain 'unstaged.txt', got: %q", diff)
	}
}

func TestGetUnstagedDiff_StagedAndUnstaged(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create unstaged file
	createTestFile(t, repoDir, "unstaged.txt", "unstaged\n")

	// Create and stage file
	createTestFile(t, repoDir, "staged.txt", "staged\n")
	stageFile(t, repoDir, "staged.txt")

	diff, err := GetUnstagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetUnstagedDiff returned error: %v", err)
	}

	// Should only contain unstaged file (untracked file)
	// Note: staged.txt is staged, so it should NOT appear in unstaged diff
	if !strings.Contains(diff, "unstaged.txt") {
		t.Errorf("Expected diff to contain 'unstaged.txt', got diff:\n%s", diff)
	}

	// staged.txt should NOT be in unstaged diff because it's staged
	// Check for the exact filename, not just a substring (unstaged.txt contains "staged" as substring)
	if strings.Contains(diff, "staged.txt") && !strings.Contains(diff, "unstaged.txt") {
		t.Errorf("Expected diff to NOT contain 'staged.txt', got diff:\n%s", diff)
	}
	// More precise check: look for the pattern that indicates staged.txt file
	if strings.Contains(diff, "diff --git a/staged.txt") || strings.Contains(diff, "+++ b/staged.txt") {
		t.Errorf("Expected diff to NOT contain staged.txt file, got diff:\n%s", diff)
	}
}

func TestGetUnstagedDiff_NoChanges(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	diff, err := GetUnstagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetUnstagedDiff returned error: %v", err)
	}

	if diff != "" {
		t.Errorf("Expected empty diff, got: %q", diff)
	}
}

func TestGetUnstagedDiff_TrailingNewline(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create file with trailing newline
	content := "unstaged line1\nunstaged line2\n"
	createTestFile(t, repoDir, "unstaged_newline.txt", content)

	diff, err := GetUnstagedDiff(repoDir)
	if err != nil {
		t.Fatalf("GetUnstagedDiff returned error: %v", err)
	}

	// Should have exactly 2 lines, not 3
	lines := strings.Split(diff, "\n")
	var plusLineCount int
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && len(line) > 1 {
			secondChar := line[1]
			if secondChar != '+' && secondChar != '@' {
				plusLineCount++
			}
		}
	}
	if plusLineCount != 2 {
		t.Errorf("Expected 2 content lines with + prefix, got %d. Diff:\n%s", plusLineCount, diff)
	}

	// Check hunk header
	if !strings.Contains(diff, "@@ -0,0 +1,2 @@") {
		t.Errorf("Expected hunk header to show 2 lines, got:\n%s", diff)
	}
}

func TestGetDiff_DirectoryNotExists(t *testing.T) {
	_, err := GetDiff("/nonexistent/directory")
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}
}

func TestGetStagedDiff_DirectoryNotExists(t *testing.T) {
	_, err := GetStagedDiff("/nonexistent/directory")
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}
}

func TestGetUnstagedDiff_DirectoryNotExists(t *testing.T) {
	_, err := GetUnstagedDiff("/nonexistent/directory")
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}
}
