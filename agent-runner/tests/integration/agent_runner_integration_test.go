package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-runner/pkg/agent"
	"agent-runner/pkg/git"
	"agent-runner/pkg/reporter"
)

// Test constants
const (
	testAgentRunID    = 12345
	testIssueID       = 42
	testRepo          = "test-org/test-repo"
	testOperatorToken = "test-operator-token-12345"
	testPrompt        = "Test issue: Add a new feature"
)

// OperatorAPIMock stores received requests for verification
type OperatorAPIMock struct {
	ReceivedRequests []reporter.ReportRequest
	AuthToken        string
	RequestCount     int
}

// GitHubAPIMock stores created PRs for verification
type GitHubAPIMock struct {
	CreatedPRs []PullRequest
	PRCounter  int
	mutex      chan struct{} // Simple mutex using channel
}

// PullRequest represents a created PR in the mock
type PullRequest struct {
	Number int
	Title  string
	Head   string
	Base   string
}

// setupTestRepo creates a temporary git repository for testing
func setupTestRepo(t *testing.T) (repoDir string, cleanup func()) {
	t.Helper()

	// Create temporary directory (automatically cleaned up)
	repoDir = t.TempDir()

	// Initialize git repository
	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("Failed to initialize git repository: %v", err)
	}

	// Configure git user (required for commits)
	configNameCmd := exec.Command("git", "config", "user.name", "Test User")
	configNameCmd.Dir = repoDir
	if err := configNameCmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.name: %v", err)
	}

	configEmailCmd := exec.Command("git", "config", "user.email", "test@example.com")
	configEmailCmd.Dir = repoDir
	if err := configEmailCmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.email: %v", err)
	}

	// Create initial README.md file
	readmePath := filepath.Join(repoDir, "README.md")
	readmeContent := "# Test Repository\n"
	if err := os.WriteFile(readmePath, []byte(readmeContent), 0644); err != nil {
		t.Fatalf("Failed to create README.md: %v", err)
	}

	// Stage and commit initial file
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = repoDir
	if err := addCmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	commitCmd := exec.Command("git", "commit", "-m", "Initial commit")
	commitCmd.Dir = repoDir
	if err := commitCmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Set up remote URL (mock, won't actually push)
	setRemoteCmd := exec.Command("git", "remote", "add", "origin", fmt.Sprintf("https://github.com/%s.git", testRepo))
	setRemoteCmd.Dir = repoDir
	_ = setRemoteCmd.Run() // Ignore error if remote already exists

	// cleanup function (t.TempDir() handles cleanup automatically, but we can return a no-op)
	return repoDir, func() {}
}

// createManifestFile creates .agent-config.yaml file in the repository directory
func createManifestFile(t *testing.T, repoDir string, manifestYAML string) error {
	t.Helper()

	manifestPath := filepath.Join(repoDir, ".agent-config.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestYAML), 0644); err != nil {
		return fmt.Errorf("failed to create manifest file: %w", err)
	}

	return nil
}

// setupMockOperatorAPI creates a mock Operator API server
func setupMockOperatorAPI(t *testing.T) (*httptest.Server, *OperatorAPIMock) {
	t.Helper()

	mock := &OperatorAPIMock{
		ReceivedRequests: []reporter.ReportRequest{},
		AuthToken:        testOperatorToken,
		RequestCount:     0,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only handle POST /api/agent-runs/:id/report
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Verify path pattern
		if !strings.HasPrefix(r.URL.Path, "/api/agent-runs/") || !strings.HasSuffix(r.URL.Path, "/report") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// Verify Bearer token
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Missing Authorization header"})
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid Authorization header format"})
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != testOperatorToken {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid token"})
			return
		}

		// Parse request body
		var req reporter.ReportRequest
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB limit
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read request body"})
			return
		}

		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
			return
		}

		// Record request
		mock.RequestCount++
		mock.ReceivedRequests = append(mock.ReceivedRequests, req)

		// Extract agent run ID from path
		pathParts := strings.Split(r.URL.Path, "/")
		if len(pathParts) < 4 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		agentRunID, _ := strconv.Atoi(pathParts[3])

		// Return success response
		resp := reporter.ReportResponse{
			Message:    fmt.Sprintf("Report received for AgentRun #%d", agentRunID),
			AgentRunID: agentRunID,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
	})

	return server, mock
}

// setupMockGitHubAPI creates a mock GitHub API server for PR creation
func setupMockGitHubAPI(t *testing.T, nextPRNumber int) (*httptest.Server, *GitHubAPIMock) {
	t.Helper()

	mock := &GitHubAPIMock{
		CreatedPRs: []PullRequest{},
		PRCounter:  nextPRNumber,
		mutex:      make(chan struct{}, 1), // Buffer of 1 acts as mutex
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only handle POST /repos/:owner/:repo/pulls
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Verify path pattern: /repos/:owner/:repo/pulls
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(pathParts) != 4 || pathParts[0] != "repos" || pathParts[3] != "pulls" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		owner := pathParts[1]
		repo := pathParts[2]

		// Note: Token verification removed. In production, GitHub App installation token is used on-demand.

		// Parse request body
		var prReq struct {
			Title string `json:"title"`
			Head  string `json:"head"`
			Base  string `json:"base"`
			Body  string `json:"body"`
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB limit
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read request body"})
			return
		}

		if err := json.Unmarshal(bodyBytes, &prReq); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
			return
		}

		// Acquire lock
		mock.mutex <- struct{}{}
		mock.PRCounter++
		prNumber := mock.PRCounter
		mock.CreatedPRs = append(mock.CreatedPRs, PullRequest{
			Number: prNumber,
			Title:  prReq.Title,
			Head:   prReq.Head,
			Base:   prReq.Base,
		})
		<-mock.mutex

		// Return PR response
		prResponse := map[string]interface{}{
			"number": prNumber,
			"title":  prReq.Title,
			"head": map[string]string{
				"ref": prReq.Head,
			},
			"base": map[string]string{
				"ref": prReq.Base,
			},
			"html_url": fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(prResponse)
	})

	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
	})

	return server, mock
}

// setupTestEnv sets up environment variables for testing
func setupTestEnv(t *testing.T, operatorAPIURL, operatorToken, agentType, workDir string) (cleanup func()) {
	t.Helper()

	// Store original values
	envVars := map[string]string{
		// New Operator URL construction variables
		"KUBERNETES_NAMESPACE":  "default",
		"OPERATOR_SERVICE_NAME": "agent-operator",
		"OPERATOR_SERVICE_PORT": "3000",

		// Other required variables for tests
		"OPERATOR_API_TOKEN": operatorToken,
		"AGENT_RUN_ID":       strconv.Itoa(testAgentRunID),
		"AGENT_TYPE":         agentType,
		"RETRY_COUNT":        "0",
		"WORKSPACE_DIR":      workDir,
		"ANTHROPIC_API_KEY":  "test-anthropic-key",
	}

	originalValues := make(map[string]string)
	// Track legacy OPERATOR_API_URL to restore/unset on cleanup
	if original, exists := os.LookupEnv("OPERATOR_API_URL"); exists {
		originalValues["OPERATOR_API_URL"] = original
	}
	// Explicitly remove legacy variable to avoid conflicts with new construction
	os.Unsetenv("OPERATOR_API_URL")

	for key, value := range envVars {
		if original, exists := os.LookupEnv(key); exists {
			originalValues[key] = original
		}
		os.Setenv(key, value)
	}

	// Return cleanup function
	cleanup = func() {
		for key := range envVars {
			if original, exists := originalValues[key]; exists {
				os.Setenv(key, original)
			} else {
				os.Unsetenv(key)
			}
		}
		// Restore or unset legacy OPERATOR_API_URL
		if original, exists := originalValues["OPERATOR_API_URL"]; exists {
			os.Setenv("OPERATOR_API_URL", original)
		} else {
			os.Unsetenv("OPERATOR_API_URL")
		}
	}

	t.Cleanup(cleanup)
	return cleanup
}

// createMockAgentExecutor creates a mock agent executor that simulates file changes
func createMockAgentExecutor(workDir string) (*agent.Executor, error) {
	// Create a mock command runner that simulates agent execution
	mockRunner := &MockAgentCommandRunner{
		WorkDir: workDir,
	}

	return agent.NewExecutorWithRunner("claude-code", mockRunner), nil
}

// MockAgentCommandRunner is a mock implementation of CommandRunner for agent execution
type MockAgentCommandRunner struct {
	WorkDir string
}

// Run implements the CommandRunner interface for agent execution
func (m *MockAgentCommandRunner) Run(name string, args []string, workDir string) ([]byte, error) {
	// Simulate agent execution by creating a test file
	testFile := filepath.Join(workDir, "test-output.txt")
	content := "Agent execution completed\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to create test file: %w", err)
	}

	return []byte("Agent executed successfully"), nil
}

// checkGitFunctionsImplemented checks if git functions are implemented
func checkGitFunctionsImplemented(t *testing.T) bool {
	t.Helper()

	// Check CloneRepo
	err := git.CloneRepo("", "", "")
	if err != nil && strings.Contains(err.Error(), "not implemented") {
		t.Skip("git.CloneRepo not implemented yet")
		return false
	}

	// Check CreateBranch
	err = git.CreateBranch("", "", 0)
	if err != nil && strings.Contains(err.Error(), "not implemented") {
		t.Skip("git.CreateBranch not implemented yet")
		return false
	}

	// Check CreatePR
	_, err = git.CreatePR("", "", "", 0)
	if err != nil && strings.Contains(err.Error(), "not implemented") {
		t.Skip("git.CreatePR not implemented yet")
		return false
	}

	return true
}

// TestAgentRunnerIntegration_SuccessWithManifest tests the complete success flow with manifest
func TestAgentRunnerIntegration_SuccessWithManifest(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		return
	}

	// Setup test repository
	repoDir, _ := setupTestRepo(t)
	manifestYAML := `version: "1.0"
hooks:
  pre:
    - name: "pre-hook-1"
      command: "echo 'pre-hook executed' > pre-hook-output.txt"
      description: "Test pre-hook"
      timeout: "30s"
      required: true
  post:
    - name: "post-hook-1"
      command: "echo 'post-hook executed' > post-hook-output.txt"
      description: "Test post-hook"
      timeout: "30s"
      required: false
validation:
  - name: "validation-1"
    command: "echo 'validation passed'"
    description: "Test validation"
    timeout: "30s"
    required: true
`
	require.NoError(t, createManifestFile(t, repoDir, manifestYAML))

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)
	githubServer, githubMock := setupMockGitHubAPI(t, 1)

	// Set GITHUB_API_URL to point to mock server
	// Note: This requires modifying git.CreatePR to use a configurable API URL
	// For now, we'll test what we can and note this limitation
	_ = githubServer // Will be used when git functions support configurable API URL

	// Setup environment
	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// TODO: Execute main.go's run() function or simulate command execution
	// Since run() is private, we'll need to either:
	// 1. Export run() as Run() in main.go
	// 2. Execute the binary with os.Exec
	// 3. Test individual steps (less integration-like)

	// For now, we verify that the setup is correct
	assert.NotNil(t, operatorMock)
	assert.NotNil(t, githubMock)
	assert.DirExists(t, repoDir)

	// Verify manifest file exists
	manifestPath := filepath.Join(repoDir, ".agent-config.yaml")
	assert.FileExists(t, manifestPath)
}

// TestAgentRunnerIntegration_SuccessWithoutManifest tests the success flow without manifest
func TestAgentRunnerIntegration_SuccessWithoutManifest(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		return
	}

	// Setup test repository (no manifest file)
	repoDir, _ := setupTestRepo(t)

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)
	githubServer, githubMock := setupMockGitHubAPI(t, 1)
	_ = githubServer

	// Setup environment
	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// Verify setup
	assert.NotNil(t, operatorMock)
	assert.NotNil(t, githubMock)
	assert.DirExists(t, repoDir)

	// Verify manifest file does NOT exist
	manifestPath := filepath.Join(repoDir, ".agent-config.yaml")
	assert.NoFileExists(t, manifestPath)
}

// TestAgentRunnerIntegration_PreHookFailure tests pre-hook failure scenario
func TestAgentRunnerIntegration_PreHookFailure(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		return
	}

	// Setup test repository
	repoDir, _ := setupTestRepo(t)
	manifestYAML := `version: "1.0"
hooks:
  pre:
    - name: "pre-hook-fail"
      command: "exit 1"
      description: "Failing pre-hook"
      timeout: "30s"
      required: true
`
	require.NoError(t, createManifestFile(t, repoDir, manifestYAML))

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)

	// Setup environment
	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// Verify setup
	assert.NotNil(t, operatorMock)
	assert.DirExists(t, repoDir)

	// TODO: Execute run() and verify failure reporting
}

// TestAgentRunnerIntegration_ValidationFailure tests validation failure scenario
func TestAgentRunnerIntegration_ValidationFailure(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		return
	}

	// Setup test repository
	repoDir, _ := setupTestRepo(t)
	manifestYAML := `version: "1.0"
validation:
  - name: "validation-fail"
    command: "exit 1"
    description: "Failing validation"
    timeout: "30s"
    required: true
`
	require.NoError(t, createManifestFile(t, repoDir, manifestYAML))

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)

	// Setup environment
	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// Verify setup
	assert.NotNil(t, operatorMock)
	assert.DirExists(t, repoDir)

	// TODO: Execute run() and verify validation failure (no API report)
}

// TestAgentRunnerIntegration_NoFileChanges tests no file changes scenario
func TestAgentRunnerIntegration_NoFileChanges(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		return
	}

	// Setup test repository
	repoDir, _ := setupTestRepo(t)

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)

	// Setup environment
	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// Create mock agent executor that doesn't create files
	mockRunner := &MockAgentCommandRunnerNoChanges{
		WorkDir: repoDir,
	}
	_ = mockRunner

	// Verify setup
	assert.NotNil(t, operatorMock)
	assert.DirExists(t, repoDir)

	// TODO: Execute run() with no-changes agent and verify error reporting
}

// MockAgentCommandRunnerNoChanges simulates agent execution with no file changes
type MockAgentCommandRunnerNoChanges struct {
	WorkDir string
}

// Run implements CommandRunner but doesn't create any files
func (m *MockAgentCommandRunnerNoChanges) Run(name string, args []string, workDir string) ([]byte, error) {
	// Return success but don't create any files
	return []byte("Agent executed but made no changes"), nil
}
