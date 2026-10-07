package agent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("progress sink failed")
}

// MockCommandRunner is a mock implementation of CommandRunner for testing.
type MockCommandRunner struct {
	Output      []byte
	Err         error
	CommandName string   // Verification: called command name
	Args        []string // Verification: called arguments
	WorkDir     string   // Verification: working directory
	CallCount   int      // Verification: number of calls
}

// Run implements the CommandRunner interface.
func (m *MockCommandRunner) Run(name string, args []string, workDir string) ([]byte, error) {
	m.CallCount++
	m.CommandName = name
	m.Args = args
	m.WorkDir = workDir
	return m.Output, m.Err
}

// setupEnvVar sets an environment variable and returns a cleanup function.
func setupEnvVar(t *testing.T, key, value string) func() {
	t.Helper()
	oldValue, existed := os.LookupEnv(key)
	os.Setenv(key, value)
	return func() {
		if existed {
			os.Setenv(key, oldValue)
		} else {
			os.Unsetenv(key)
		}
	}
}

// restoreEnvVar removes an environment variable and returns a cleanup function to restore it.
func restoreEnvVar(t *testing.T, key string) func() {
	t.Helper()
	oldValue, existed := os.LookupEnv(key)
	if existed {
		os.Unsetenv(key)
		return func() {
			os.Setenv(key, oldValue)
		}
	}
	return func() {}
}

// createMockRunner creates a MockCommandRunner with the specified output and error.
func createMockRunner(output []byte, err error) *MockCommandRunner {
	return &MockCommandRunner{
		Output: output,
		Err:    err,
	}
}

// TestNewExecutor_ValidTypes tests that NewExecutor creates executors with correct agent types.
func TestNewExecutor_ValidTypes(t *testing.T) {
	t.Run("claude-code", func(t *testing.T) {
		executor := NewExecutor("claude-code")
		if executor.agentType != "claude-code" {
			t.Errorf("Expected agentType 'claude-code', got %q", executor.agentType)
		}
		if executor.cmdRunner != nil {
			t.Error("Expected cmdRunner to be nil for default executor")
		}
	})

	t.Run("cursor-agent", func(t *testing.T) {
		executor := NewExecutor("cursor-agent")
		if executor.agentType != "cursor-agent" {
			t.Errorf("Expected agentType 'cursor-agent', got %q", executor.agentType)
		}
		if executor.cmdRunner != nil {
			t.Error("Expected cmdRunner to be nil for default executor")
		}
	})

	t.Run("codex", func(t *testing.T) {
		executor := NewExecutor("codex")
		if executor.agentType != "codex" {
			t.Errorf("Expected agentType 'codex', got %q", executor.agentType)
		}
		if executor.cmdRunner != nil {
			t.Error("Expected cmdRunner to be nil for default executor")
		}
	})
}

// TestNewExecutorWithRunner tests that NewExecutorWithRunner creates executors with injected command runner.
func TestNewExecutorWithRunner(t *testing.T) {
	mockRunner := createMockRunner([]byte("test output"), nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	if executor.agentType != "claude-code" {
		t.Errorf("Expected agentType 'claude-code', got %q", executor.agentType)
	}
	if executor.cmdRunner != mockRunner {
		t.Error("Expected cmdRunner to be set to mockRunner")
	}
}

// TestExecutor_Execute_ClaudeCode_Success tests successful claude-code execution.
func TestExecutor_Execute_ClaudeCode_Success(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Agent executed successfully")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	output, err := executor.Execute(workDir, prompt)

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}

	// Verify mock was called correctly
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
	if mockRunner.CommandName != "claude-code" {
		t.Errorf("Expected CommandName = 'claude-code', got %q", mockRunner.CommandName)
	}
	expectedArgs := []string{"-p", prompt}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
	if mockRunner.WorkDir != workDir {
		t.Errorf("Expected WorkDir = %q, got %q", workDir, mockRunner.WorkDir)
	}
}

// TestExecutor_Execute_Cursor_Success tests successful cursor-agent execution with defaults.
func TestExecutor_Execute_Cursor_Success(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Cursor agent executed")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	output, err := executor.Execute(workDir, prompt)

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}

	// Verify mock was called correctly with default values
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
	if mockRunner.CommandName != "cursor-agent" {
		t.Errorf("Expected CommandName = 'cursor-agent', got %q", mockRunner.CommandName)
	}
	// Default values: model="auto", allowWrite=true
	expectedArgs := []string{"--model", "auto", "--output-format", "stream-json", "-p", prompt, "--force"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
	if mockRunner.WorkDir != workDir {
		t.Errorf("Expected WorkDir = %q, got %q", workDir, mockRunner.WorkDir)
	}
}

// TestExecutor_Execute_Codex_Success tests successful codex execution with defaults.
func TestExecutor_Execute_Codex_Success(t *testing.T) {
	cleanup := setupEnvVar(t, "CODEX_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Codex agent executed")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("codex", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	output, err := executor.Execute(workDir, prompt)

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}

	// Verify mock was called correctly with default values
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
	if mockRunner.CommandName != "codex" {
		t.Errorf("Expected CommandName = 'codex', got %q", mockRunner.CommandName)
	}
	// Default values: allowWrite=true uses unrestricted execution, JSONL output,
	// and the repository's default Codex model.
	expectedArgs := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--json", "-m", defaultCodexModel, prompt}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
	if mockRunner.WorkDir != workDir {
		t.Errorf("Expected WorkDir = %q, got %q", workDir, mockRunner.WorkDir)
	}
}

// TestExecutor_Execute_Codex_Success_OpenAIKey tests that codex also accepts OPENAI_API_KEY.
func TestExecutor_Execute_Codex_Success_OpenAIKey(t *testing.T) {
	cleanupCodex := restoreEnvVar(t, "CODEX_API_KEY")
	defer cleanupCodex()
	cleanup := setupEnvVar(t, "OPENAI_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("Codex agent executed"), nil)
	executor := NewExecutorWithRunner("codex", mockRunner)

	_, err := executor.Execute("/tmp/work", "test prompt")

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_Execute_Codex_Success_AuthFile tests OAuth authentication via ~/.codex/auth.json.
func TestExecutor_Execute_Codex_Success_AuthFile(t *testing.T) {
	cleanupCodex := restoreEnvVar(t, "CODEX_API_KEY")
	defer cleanupCodex()
	cleanupOpenAI := restoreEnvVar(t, "OPENAI_API_KEY")
	defer cleanupOpenAI()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	authDir := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"synthetic"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	mockRunner := createMockRunner([]byte("Codex agent executed"), nil)
	executor := NewExecutorWithRunner("codex", mockRunner)
	_, err := executor.Execute("/tmp/work", "test prompt")
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_Execute_ClaudeCode_MissingAPIKey tests error when ANTHROPIC_API_KEY is not set.
func TestExecutor_Execute_ClaudeCode_MissingAPIKey(t *testing.T) {
	cleanup := restoreEnvVar(t, "ANTHROPIC_API_KEY")
	defer cleanup()

	mockRunner := createMockRunner([]byte("should not be called"), nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	_, err := executor.Execute("/tmp/work", "test prompt")

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	expectedError := "ANTHROPIC_API_KEY environment variable is not set"
	if err.Error() != expectedError {
		t.Errorf("Execute() error = %q, want %q", err.Error(), expectedError)
	}

	// Verify command was not executed
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_Execute_Cursor_MissingAPIKey tests error when CURSOR_API_KEY is not set.
func TestExecutor_Execute_Cursor_MissingAPIKey(t *testing.T) {
	cleanup := restoreEnvVar(t, "CURSOR_API_KEY")
	defer cleanup()

	mockRunner := createMockRunner([]byte("should not be called"), nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	_, err := executor.Execute("/tmp/work", "test prompt")

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	expectedError := "CURSOR_API_KEY environment variable is not set"
	if err.Error() != expectedError {
		t.Errorf("Execute() error = %q, want %q", err.Error(), expectedError)
	}

	// Verify command was not executed
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_Execute_Codex_MissingAPIKey tests error when neither CODEX_API_KEY nor OPENAI_API_KEY is set.
func TestExecutor_Execute_Codex_MissingAPIKey(t *testing.T) {
	cleanupCodex := restoreEnvVar(t, "CODEX_API_KEY")
	defer cleanupCodex()
	cleanupOpenAI := restoreEnvVar(t, "OPENAI_API_KEY")
	defer cleanupOpenAI()
	t.Setenv("HOME", t.TempDir())

	mockRunner := createMockRunner([]byte("should not be called"), nil)
	executor := NewExecutorWithRunner("codex", mockRunner)

	_, err := executor.Execute("/tmp/work", "test prompt")

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	expectedError := "codex credentials are not configured"
	if err.Error() != expectedError {
		t.Errorf("Execute() error = %q, want %q", err.Error(), expectedError)
	}

	// Verify command was not executed
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

func TestExecutor_Execute_Codex_EmptyAuthFile(t *testing.T) {
	cleanupCodex := restoreEnvVar(t, "CODEX_API_KEY")
	defer cleanupCodex()
	cleanupOpenAI := restoreEnvVar(t, "OPENAI_API_KEY")
	defer cleanupOpenAI()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	authDir := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	mockRunner := createMockRunner([]byte("should not be called"), nil)
	executor := NewExecutorWithRunner("codex", mockRunner)
	_, err := executor.Execute("/tmp/work", "test prompt")
	if err == nil || err.Error() != "codex credentials are not configured" {
		t.Fatalf("Execute() error = %v, want credential error", err)
	}
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_Execute_ClaudeCode_CommandFailure tests error handling when claude-code command fails.
func TestExecutor_Execute_ClaudeCode_CommandFailure(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Command failed with exit code 1")
	mockErr := errors.New("exit status 1")
	mockRunner := createMockRunner(mockOutput, mockErr)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	output, err := executor.Execute(workDir, prompt)

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	errorMsg := err.Error()
	if !strings.Contains(errorMsg, "claude-code execution failed") {
		t.Errorf("Expected error message to contain 'claude-code execution failed', got %q", errorMsg)
	}
	if !strings.Contains(errorMsg, "Command failed with exit code 1") {
		t.Errorf("Expected error message to contain output tail, got %q", errorMsg)
	}

	// Verify output is still returned
	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}
}

// TestExecutor_Execute_Cursor_CommandFailure tests error handling when cursor command fails.
func TestExecutor_Execute_Cursor_CommandFailure(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Cursor agent failed")
	mockErr := errors.New("exit status 1")
	mockRunner := createMockRunner(mockOutput, mockErr)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	output, err := executor.Execute("/tmp/work", "test prompt")

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	errorMsg := err.Error()
	if !strings.Contains(errorMsg, "cursor agent execution failed") {
		t.Errorf("Expected error message to contain 'cursor agent execution failed', got %q", errorMsg)
	}

	// Verify output is still returned
	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}

	// Verify command was called with correct arguments
	expectedArgs := []string{"--model", "auto", "--output-format", "stream-json", "-p", "test prompt", "--force"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_Execute_Codex_CommandFailure tests error handling when codex command fails.
func TestExecutor_Execute_Codex_CommandFailure(t *testing.T) {
	cleanup := setupEnvVar(t, "CODEX_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Codex agent failed")
	mockErr := errors.New("exit status 1")
	mockRunner := createMockRunner(mockOutput, mockErr)
	executor := NewExecutorWithRunner("codex", mockRunner)

	output, err := executor.Execute("/tmp/work", "test prompt")

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	errorMsg := err.Error()
	if !strings.Contains(errorMsg, "codex execution failed") {
		t.Errorf("Expected error message to contain 'codex execution failed', got %q", errorMsg)
	}

	// Verify output is still returned
	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}

	// Verify command was called with correct arguments
	expectedArgs := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--json", "-m", defaultCodexModel, "test prompt"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_Execute_UnknownAgentType tests error when agent type is unknown.
func TestExecutor_Execute_UnknownAgentType(t *testing.T) {
	executor := NewExecutor("unknown-agent")

	_, err := executor.Execute("/tmp/work", "test prompt")

	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	expectedError := "unknown agent type: unknown-agent"
	if err.Error() != expectedError {
		t.Errorf("Execute() error = %q, want %q", err.Error(), expectedError)
	}
}

// TestExecutor_Execute_ClaudeCode_OutputCapture tests that multi-line output is captured correctly.
func TestExecutor_Execute_ClaudeCode_OutputCapture(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Line 1\nLine 2\nLine 3")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	output, err := executor.Execute("/tmp/work", "test prompt")

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("Execute() output = %q, want %q", output, expectedOutput)
	}
}

// TestExecutor_Execute_Cursor_WorkDirSetting tests that working directory is set correctly.
func TestExecutor_Execute_Cursor_WorkDirSetting(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("output"), nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	customWorkDir := "/custom/path"
	executor.Execute(customWorkDir, "test prompt")

	if mockRunner.WorkDir != customWorkDir {
		t.Errorf("Expected WorkDir = %q, got %q", customWorkDir, mockRunner.WorkDir)
	}
}

// TestExecutor_Execute_ClaudeCode_EmptyOutput tests handling of empty output.
func TestExecutor_Execute_ClaudeCode_EmptyOutput(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte{}, nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	output, err := executor.Execute("/tmp/work", "test prompt")

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	if output != "" {
		t.Errorf("Execute() output = %q, want empty string", output)
	}
}

// TestExecutor_Execute_Cursor_LongPrompt tests handling of long prompts.
func TestExecutor_Execute_Cursor_LongPrompt(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("output"), nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	longPrompt := strings.Repeat("a", 1000)
	executor.Execute("/tmp/work", longPrompt)

	expectedArgs := []string{"--model", "auto", "--output-format", "stream-json", "-p", longPrompt, "--force"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_Execute_ClaudeCode_EmptyWorkDir tests handling of empty work directory.
func TestExecutor_Execute_ClaudeCode_EmptyWorkDir(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("output"), nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	executor.Execute("", "test prompt")

	if mockRunner.WorkDir != "" {
		t.Errorf("Expected WorkDir = empty string, got %q", mockRunner.WorkDir)
	}
}

// TestExecutor_Execute_Cursor_EmptyPrompt tests handling of empty prompt.
func TestExecutor_Execute_Cursor_EmptyPrompt(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("output"), nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	executor.Execute("/tmp/work", "")

	expectedArgs := []string{"--model", "auto", "--output-format", "stream-json", "-p", "", "--force"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_Execute_ClaudeCode_SpecialCharacters tests handling of special characters in prompt.
func TestExecutor_Execute_ClaudeCode_SpecialCharacters(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("output"), nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	specialPrompt := "test\nprompt\"with'quotes"
	executor.Execute("/tmp/work", specialPrompt)

	expectedArgs := []string{"-p", specialPrompt}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_ExecuteWithOptions_Cursor_Success tests successful cursor-agent execution with custom options.
func TestExecutor_ExecuteWithOptions_Cursor_Success(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Cursor agent executed")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	model := "claude-3-5-sonnet-20241022"
	allowWrite := true
	output, err := executor.ExecuteWithOptions(workDir, prompt, model, allowWrite)

	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("ExecuteWithOptions() output = %q, want %q", output, expectedOutput)
	}

	// Verify mock was called correctly with custom options
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
	if mockRunner.CommandName != "cursor-agent" {
		t.Errorf("Expected CommandName = 'cursor-agent', got %q", mockRunner.CommandName)
	}
	expectedArgs := []string{"--model", model, "--output-format", "stream-json", "-p", prompt, "--force"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
	if mockRunner.WorkDir != workDir {
		t.Errorf("Expected WorkDir = %q, got %q", workDir, mockRunner.WorkDir)
	}
}

// TestExecutor_ExecuteWithOptions_Cursor_WithoutForce tests cursor-agent execution without --force flag.
func TestExecutor_ExecuteWithOptions_Cursor_WithoutForce(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Cursor agent executed")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	model := "auto"
	allowWrite := false
	output, err := executor.ExecuteWithOptions(workDir, prompt, model, allowWrite)

	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("ExecuteWithOptions() output = %q, want %q", output, expectedOutput)
	}

	// Verify --force flag is not included when allowWrite is false
	expectedArgs := []string{"--model", model, "--output-format", "stream-json", "-p", prompt}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_ExecuteWithOptions_Cursor_CustomModel tests cursor-agent execution with custom model.
func TestExecutor_ExecuteWithOptions_Cursor_CustomModel(t *testing.T) {
	cleanup := setupEnvVar(t, "CURSOR_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Cursor agent executed")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	model := "claude-3-opus-20240229"
	allowWrite := true
	_, err := executor.ExecuteWithOptions(workDir, prompt, model, allowWrite)

	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v, want nil", err)
	}

	// Verify custom model is used
	expectedArgs := []string{"--model", model, "--output-format", "stream-json", "-p", prompt, "--force"}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
}

// TestExecutor_ExecuteWithOptions_Codex_Success tests successful codex execution with custom options.
func TestExecutor_ExecuteWithOptions_Codex_Success(t *testing.T) {
	cleanup := setupEnvVar(t, "CODEX_API_KEY", "test-key")
	defer cleanup()

	mockOutput := []byte("Codex agent executed")
	mockRunner := createMockRunner(mockOutput, nil)
	executor := NewExecutorWithRunner("codex", mockRunner)

	workDir := "/tmp/work"
	prompt := "test prompt"
	model := "gpt-5-codex"
	allowWrite := true
	output, err := executor.ExecuteWithOptions(workDir, prompt, model, allowWrite)

	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v, want nil", err)
	}

	expectedOutput := string(mockOutput)
	if output != expectedOutput {
		t.Errorf("ExecuteWithOptions() output = %q, want %q", output, expectedOutput)
	}

	// Verify mock was called correctly with custom options
	if mockRunner.CallCount != 1 {
		t.Errorf("Expected CallCount = 1, got %d", mockRunner.CallCount)
	}
	if mockRunner.CommandName != "codex" {
		t.Errorf("Expected CommandName = 'codex', got %q", mockRunner.CommandName)
	}
	expectedArgs := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--json", "-m", model, prompt}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
	if strings.Contains(strings.Join(mockRunner.Args, " "), "--sandbox") {
		t.Fatalf("write-enabled execution must not also pass --sandbox: %v", mockRunner.Args)
	}
	if mockRunner.WorkDir != workDir {
		t.Errorf("Expected WorkDir = %q, got %q", workDir, mockRunner.WorkDir)
	}
}

// TestExecutor_ExecuteWithOptions_Codex_ReadOnly tests codex execution with allowWrite=false.
func TestExecutor_ExecuteWithOptions_Codex_ReadOnly(t *testing.T) {
	cleanup := setupEnvVar(t, "CODEX_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("Codex agent executed"), nil)
	executor := NewExecutorWithRunner("codex", mockRunner)

	prompt := "test prompt"
	_, err := executor.ExecuteWithOptions("/tmp/work", prompt, "auto", false)

	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v, want nil", err)
	}

	// Verify read-only sandbox and that "auto" model omits -m
	expectedArgs := []string{"exec", "--sandbox", "read-only", "--json", prompt}
	if !reflect.DeepEqual(mockRunner.Args, expectedArgs) {
		t.Errorf("Expected Args = %v, got %v", expectedArgs, mockRunner.Args)
	}
	if strings.Contains(strings.Join(mockRunner.Args, " "), "dangerously-bypass") {
		t.Fatalf("read-only execution must not bypass approvals or sandbox: %v", mockRunner.Args)
	}
}

func TestExecutor_ExecuteWithOptions_Codex_StreamsJSONLAndRetainsStderr(t *testing.T) {
	cleanup := setupEnvVar(t, "CODEX_API_KEY", "test-key")
	defer cleanup()

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"item.started\"}' '{\"type\":\"item.completed\"}'\nprintf '%s\\n' 'diagnostic from codex' >&2\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var progress strings.Builder
	executor := NewExecutor("codex")
	executor.progressWriter = &progress
	output, err := executor.ExecuteWithOptions(t.TempDir(), "test prompt", "gpt-5.6-luna", true)
	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v, want nil", err)
	}

	wantStdout := "{\"type\":\"item.started\"}\n{\"type\":\"item.completed\"}\n"
	if !strings.HasPrefix(output, wantStdout) {
		t.Fatalf("returned output must preserve complete JSONL stdout; got %q", output)
	}
	if !strings.Contains(output, "diagnostic from codex\n") {
		t.Fatalf("returned output must retain stderr; got %q", output)
	}
	progressOutput := progress.String()
	if !strings.Contains(progressOutput, "[CODEX] {\"type\":\"item.started\"}\n") ||
		!strings.Contains(progressOutput, "[CODEX] {\"type\":\"item.completed\"}\n") {
		t.Fatalf("progress must stream every JSONL line; got %q", progressOutput)
	}
	if !strings.Contains(progressOutput, "diagnostic from codex\n") {
		t.Fatalf("progress must stream stderr; got %q", progressOutput)
	}
}

func TestExecutor_ExecuteWithOptions_Codex_ProgressErrorKillsProcess(t *testing.T) {
	cleanup := setupEnvVar(t, "CODEX_API_KEY", "test-key")
	defer cleanup()

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"item.started\"}'\nexec sleep 30\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	executor := NewExecutor("codex")
	executor.progressWriter = failingWriter{}
	done := make(chan error, 1)
	go func() {
		_, err := executor.ExecuteWithOptions(t.TempDir(), "test prompt", defaultCodexModel, true)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "progress sink failed") {
			t.Fatalf("expected progress writer error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Codex execution hung after progress writer failure")
	}
}

// TestExecutor_ExecuteWithOptions_Codex_MissingAPIKey tests error when no Codex API key is set.
func TestExecutor_ExecuteWithOptions_Codex_MissingAPIKey(t *testing.T) {
	cleanupCodex := restoreEnvVar(t, "CODEX_API_KEY")
	defer cleanupCodex()
	cleanupOpenAI := restoreEnvVar(t, "OPENAI_API_KEY")
	defer cleanupOpenAI()
	t.Setenv("HOME", t.TempDir())

	mockRunner := createMockRunner([]byte("should not be called"), nil)
	executor := NewExecutorWithRunner("codex", mockRunner)

	_, err := executor.ExecuteWithOptions("/tmp/work", "test prompt", "auto", true)

	if err == nil {
		t.Fatal("ExecuteWithOptions() error = nil, want error")
	}

	expectedError := "codex credentials are not configured"
	if err.Error() != expectedError {
		t.Errorf("ExecuteWithOptions() error = %q, want %q", err.Error(), expectedError)
	}

	// Verify command was not executed
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_ExecuteWithOptions_ClaudeCode_Error tests that ExecuteWithOptions returns error for claude-code.
func TestExecutor_ExecuteWithOptions_ClaudeCode_Error(t *testing.T) {
	cleanup := setupEnvVar(t, "ANTHROPIC_API_KEY", "test-key")
	defer cleanup()

	mockRunner := createMockRunner([]byte("output"), nil)
	executor := NewExecutorWithRunner("claude-code", mockRunner)

	_, err := executor.ExecuteWithOptions("/tmp/work", "test prompt", "auto", true)

	if err == nil {
		t.Fatal("ExecuteWithOptions() error = nil, want error")
	}

	expectedError := "ExecuteWithOptions is only supported for cursor-agent and codex, got: claude-code"
	if err.Error() != expectedError {
		t.Errorf("ExecuteWithOptions() error = %q, want %q", err.Error(), expectedError)
	}

	// Verify command was not executed
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

// TestExecutor_ExecuteWithOptions_Cursor_MissingAPIKey tests error when CURSOR_API_KEY is not set.
func TestExecutor_ExecuteWithOptions_Cursor_MissingAPIKey(t *testing.T) {
	cleanup := restoreEnvVar(t, "CURSOR_API_KEY")
	defer cleanup()

	mockRunner := createMockRunner([]byte("should not be called"), nil)
	executor := NewExecutorWithRunner("cursor-agent", mockRunner)

	_, err := executor.ExecuteWithOptions("/tmp/work", "test prompt", "auto", true)

	if err == nil {
		t.Fatal("ExecuteWithOptions() error = nil, want error")
	}

	expectedError := "CURSOR_API_KEY environment variable is not set"
	if err.Error() != expectedError {
		t.Errorf("ExecuteWithOptions() error = %q, want %q", err.Error(), expectedError)
	}

	// Verify command was not executed
	if mockRunner.CallCount != 0 {
		t.Errorf("Expected CallCount = 0, got %d", mockRunner.CallCount)
	}
}

// The real subprocess path (no cmdRunner mock) must propagate non-zero
// exits: a shadowed error would silently report success. Uses a fake
// claude-code executable on PATH.
func TestExecutor_Execute_ClaudeCode_SubprocessFailure(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude-code")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho fake-error >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("write fake claude-code: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	executor := NewExecutor("claude-code")
	_, err := executor.Execute(t.TempDir(), "prompt")
	if err == nil {
		t.Fatal("expected subprocess failure to propagate, got nil error")
	}
	if !strings.Contains(err.Error(), "claude-code execution failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
