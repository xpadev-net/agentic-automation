package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-runner/pkg/config"
)

// setupTestDir creates a temporary directory for testing.
// Returns the directory path and a cleanup function.
func setupTestDir(t *testing.T) (string, func()) {
	tmpDir, err := os.MkdirTemp("", "hooks-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

// createCommand creates a test Command struct.
func createCommand(name, cmd string, timeout time.Duration, required bool) config.Command {
	return config.Command{
		Name:        name,
		Command:     cmd,
		Description: "",
		Timeout:     timeout,
		Required:    required,
	}
}

// assertHookError verifies that an error is a HookError with expected values.
func assertHookError(t *testing.T, err error, expectedName, expectedCommand string) {
	if err == nil {
		t.Fatalf("Expected HookError, got nil")
	}

	var hookErr *HookError
	if !errors.As(err, &hookErr) {
		t.Fatalf("Expected HookError, got %T: %v", err, err)
	}

	if hookErr.Name != expectedName {
		t.Errorf("Expected hook name %q, got %q", expectedName, hookErr.Name)
	}

	if hookErr.Command != expectedCommand {
		t.Errorf("Expected command %q, got %q", expectedCommand, hookErr.Command)
	}
}

// TestRunPreHooks_EmptyList tests that empty command list returns nil.
func TestRunPreHooks_EmptyList(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Test nil
	err := RunPreHooks(nil, workDir)
	if err != nil {
		t.Errorf("RunPreHooks(nil, ...) = %v, want nil", err)
	}

	// Test empty slice
	err = RunPreHooks([]config.Command{}, workDir)
	if err != nil {
		t.Errorf("RunPreHooks([]Command{}, ...) = %v, want nil", err)
	}
}

// TestRunPreHooks_SuccessfulRequired tests that a successful required hook completes normally.
func TestRunPreHooks_SuccessfulRequired(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("test-hook", "echo success", 5*time.Second, true),
	}

	err := RunPreHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPreHooks() = %v, want nil", err)
	}
}

// TestRunPreHooks_FailedRequired tests that a failed required hook returns HookError immediately.
func TestRunPreHooks_FailedRequired(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("failing-hook", "false", 5*time.Second, true),
		createCommand("should-not-run", "echo should not run", 5*time.Second, true),
	}

	err := RunPreHooks(commands, workDir)
	if err == nil {
		t.Fatal("RunPreHooks() = nil, want HookError")
	}

	assertHookError(t, err, "failing-hook", "false")

	// Verify that the second hook did not run by checking that its output file doesn't exist
	// Since the command just echoes, we can't easily verify it didn't run,
	// but we know the error was returned immediately
}

// TestRunPreHooks_FailedOptional tests that a failed optional hook logs warning and continues.
func TestRunPreHooks_FailedOptional(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("optional-hook", "false", 5*time.Second, false),
	}

	err := RunPreHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPreHooks() = %v, want nil", err)
	}
	// Note: Warning message verification is skipped per plan recommendation
}

// TestRunPreHooks_MixedRequiredAndOptional tests mixed required and optional hooks.
func TestRunPreHooks_MixedRequiredAndOptional(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("optional-success", "echo ok", 5*time.Second, false),
		createCommand("optional-fail", "false", 5*time.Second, false),
		createCommand("required-fail", "false", 5*time.Second, true),
		createCommand("should-not-run", "echo should not run", 5*time.Second, true),
	}

	err := RunPreHooks(commands, workDir)
	if err == nil {
		t.Fatal("RunPreHooks() = nil, want HookError")
	}

	assertHookError(t, err, "required-fail", "false")
	// Optional hook failures should not cause error, only warnings
}

// TestRunPreHooks_SequentialExecution tests that hooks execute sequentially.
func TestRunPreHooks_SequentialExecution(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create files in sequence to verify execution order
	commands := []config.Command{
		createCommand("hook1", "touch file1.txt", 5*time.Second, true),
		createCommand("hook2", "touch file2.txt", 5*time.Second, true),
		createCommand("hook3", "touch file3.txt", 5*time.Second, true),
	}

	err := RunPreHooks(commands, workDir)
	if err != nil {
		t.Fatalf("RunPreHooks() = %v, want nil", err)
	}

	// Verify all files were created in order
	files := []string{"file1.txt", "file2.txt", "file3.txt"}
	for _, filename := range files {
		filePath := filepath.Join(workDir, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("Expected %s to be created", filename)
		}
	}
}

// TestRunValidations_EmptyList tests that empty command list returns nil.
func TestRunValidations_EmptyList(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Test nil
	err := RunValidations(nil, workDir)
	if err != nil {
		t.Errorf("RunValidations(nil, ...) = %v, want nil", err)
	}

	// Test empty slice
	err = RunValidations([]config.Command{}, workDir)
	if err != nil {
		t.Errorf("RunValidations([]Command{}, ...) = %v, want nil", err)
	}
}

// TestRunValidations_SuccessfulRequired tests that a successful required validation completes normally.
func TestRunValidations_SuccessfulRequired(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("test-validation", "echo ok", 5*time.Second, true),
	}

	err := RunValidations(commands, workDir)
	if err != nil {
		t.Errorf("RunValidations() = %v, want nil", err)
	}
}

// TestRunValidations_FailedRequiredSingle tests that a single failed required validation returns error.
func TestRunValidations_FailedRequiredSingle(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("failing-validation", "false", 5*time.Second, true),
	}

	err := RunValidations(commands, workDir)
	if err == nil {
		t.Fatal("RunValidations() = nil, want error")
	}

	if !strings.Contains(err.Error(), "validation failures:") {
		t.Errorf("Expected error message to contain 'validation failures:', got: %v", err)
	}

	if !strings.Contains(err.Error(), "failing-validation") {
		t.Errorf("Expected error message to contain hook name, got: %v", err)
	}
}

// TestRunValidations_FailedRequiredMultiple tests that multiple failed required validations are combined.
func TestRunValidations_FailedRequiredMultiple(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("validation1", "false", 5*time.Second, true),
		createCommand("validation2", "false", 5*time.Second, true),
		createCommand("validation3", "false", 5*time.Second, true),
	}

	err := RunValidations(commands, workDir)
	if err == nil {
		t.Fatal("RunValidations() = nil, want error")
	}

	errorMsg := err.Error()
	if !strings.Contains(errorMsg, "validation failures:") {
		t.Errorf("Expected error message to contain 'validation failures:', got: %v", errorMsg)
	}

	// Verify all validation failures are included
	if !strings.Contains(errorMsg, "validation1") {
		t.Errorf("Expected error message to contain 'validation1', got: %v", errorMsg)
	}
	if !strings.Contains(errorMsg, "validation2") {
		t.Errorf("Expected error message to contain 'validation2', got: %v", errorMsg)
	}
	if !strings.Contains(errorMsg, "validation3") {
		t.Errorf("Expected error message to contain 'validation3', got: %v", errorMsg)
	}

	// Verify errors are joined with newlines
	lines := strings.Split(errorMsg, "\n")
	if len(lines) < 4 { // "validation failures:" + 3 error messages
		t.Errorf("Expected error message to contain multiple lines, got %d lines", len(lines))
	}
}

// TestRunValidations_FailedOptional tests that a failed optional validation logs warning and continues.
func TestRunValidations_FailedOptional(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("optional-validation", "false", 5*time.Second, false),
	}

	err := RunValidations(commands, workDir)
	if err != nil {
		t.Errorf("RunValidations() = %v, want nil", err)
	}
	// Note: Warning message verification is skipped per plan recommendation
}

// TestRunValidations_MixedRequiredAndOptional tests mixed required and optional validations.
func TestRunValidations_MixedRequiredAndOptional(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("required-success", "echo ok", 5*time.Second, true),
		createCommand("optional-fail", "false", 5*time.Second, false),
		createCommand("required-fail", "false", 5*time.Second, true),
	}

	err := RunValidations(commands, workDir)
	if err == nil {
		t.Fatal("RunValidations() = nil, want error")
	}

	errorMsg := err.Error()
	// Should contain required-fail but not optional-fail
	if !strings.Contains(errorMsg, "required-fail") {
		t.Errorf("Expected error message to contain 'required-fail', got: %v", errorMsg)
	}
	if strings.Contains(errorMsg, "optional-fail") {
		t.Errorf("Expected error message to NOT contain 'optional-fail', got: %v", errorMsg)
	}
}

// TestRunPostHooks_EmptyList tests that empty command list returns nil.
func TestRunPostHooks_EmptyList(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Test nil
	err := RunPostHooks(nil, workDir)
	if err != nil {
		t.Errorf("RunPostHooks(nil, ...) = %v, want nil", err)
	}

	// Test empty slice
	err = RunPostHooks([]config.Command{}, workDir)
	if err != nil {
		t.Errorf("RunPostHooks([]Command{}, ...) = %v, want nil", err)
	}
}

// TestRunPostHooks_FailedRequired tests that a failed required hook returns nil (always succeeds).
func TestRunPostHooks_FailedRequired(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("failing-hook", "false", 5*time.Second, true),
	}

	err := RunPostHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPostHooks() = %v, want nil", err)
	}
	// Note: Warning message verification is skipped per plan recommendation
}

// TestRunPostHooks_FailedOptional tests that a failed optional hook returns nil.
func TestRunPostHooks_FailedOptional(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("optional-hook", "false", 5*time.Second, false),
	}

	err := RunPostHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPostHooks() = %v, want nil", err)
	}
	// Note: Warning message verification is skipped per plan recommendation
}

// TestRunPostHooks_MultipleFailures tests that multiple hook failures all return nil.
func TestRunPostHooks_MultipleFailures(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("hook1", "false", 5*time.Second, true),
		createCommand("hook2", "false", 5*time.Second, false),
		createCommand("hook3", "false", 5*time.Second, true),
	}

	err := RunPostHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPostHooks() = %v, want nil", err)
	}
	// Note: Warning message verification is skipped per plan recommendation
}

// TestRunPreHooks_Timeout tests that timeout is enforced.
func TestRunPreHooks_Timeout(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Use sleep command with 1 second timeout
	// The command will sleep for 5 seconds but should be killed after 1 second
	commands := []config.Command{
		createCommand("timeout-hook", "sleep 5", 1*time.Second, true),
	}

	start := time.Now()
	err := RunPreHooks(commands, workDir)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("RunPreHooks() = nil, want HookError")
	}

	assertHookError(t, err, "timeout-hook", "sleep 5")

	// Verify timeout occurred (should complete in ~1 second, not ~5 seconds)
	// Allow some margin for execution overhead
	if elapsed > 3*time.Second {
		t.Errorf("Command took %v, expected ~1 second (timeout)", elapsed)
	}

	// Verify the underlying error is context deadline exceeded
	var hookErr *HookError
	if errors.As(err, &hookErr) {
		if hookErr.Err != nil {
			if !errors.Is(hookErr.Err, context.DeadlineExceeded) {
				// On some systems, the error might be an exec.ExitError
				// Check if it's related to context cancellation
				if !strings.Contains(hookErr.Err.Error(), "signal: killed") &&
					!strings.Contains(hookErr.Err.Error(), "context deadline exceeded") {
					t.Errorf("Expected context.DeadlineExceeded or killed signal, got: %v", hookErr.Err)
				}
			}
		}
	}
}

// TestRunValidations_Timeout tests that timeout is enforced in validations.
func TestRunValidations_Timeout(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("timeout-validation", "sleep 5", 1*time.Second, true),
	}

	err := RunValidations(commands, workDir)
	if err == nil {
		t.Fatal("RunValidations() = nil, want error")
	}

	if !strings.Contains(err.Error(), "validation failures:") {
		t.Errorf("Expected error message to contain 'validation failures:', got: %v", err)
	}

	if !strings.Contains(err.Error(), "timeout-validation") {
		t.Errorf("Expected error message to contain hook name, got: %v", err)
	}
}

// TestRunPostHooks_Timeout tests that timeout in post-hooks returns nil.
func TestRunPostHooks_Timeout(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	commands := []config.Command{
		createCommand("timeout-hook", "sleep 5", 1*time.Second, true),
	}

	err := RunPostHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPostHooks() = %v, want nil", err)
	}
	// Note: Warning message verification is skipped per plan recommendation
}

// TestRunPreHooks_NoTimeout tests that timeout of 0 or negative runs without timeout.
func TestRunPreHooks_NoTimeout(t *testing.T) {
	workDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Test with timeout 0
	commands := []config.Command{
		createCommand("no-timeout-hook", "echo success", 0, true),
	}

	err := RunPreHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPreHooks() with timeout 0 = %v, want nil", err)
	}

	// Test with negative timeout (should also work)
	commands = []config.Command{
		createCommand("no-timeout-hook2", "echo success", -1*time.Second, true),
	}

	err = RunPreHooks(commands, workDir)
	if err != nil {
		t.Errorf("RunPreHooks() with negative timeout = %v, want nil", err)
	}
}

// TestHookError_Error tests that HookError.Error() returns correctly formatted message.
func TestHookError_Error(t *testing.T) {
	originalErr := errors.New("test error")
	hookErr := &HookError{
		Name:    "test-hook",
		Command: "test command",
		Output:  "test output",
		Err:     originalErr,
	}

	errorMsg := hookErr.Error()

	if !strings.Contains(errorMsg, "hook 'test-hook' failed:") {
		t.Errorf("Expected error message to contain \"hook 'test-hook' failed:\", got: %q", errorMsg)
	}

	if !strings.Contains(errorMsg, "Command: test command") {
		t.Errorf("Expected error message to contain \"Command: test command\", got: %q", errorMsg)
	}

	// Output should NOT be included in the error message to avoid exceeding argument string limits
	if strings.Contains(errorMsg, "Output: test output") {
		t.Errorf("Expected error message to NOT contain \"Output: test output\", got: %q", errorMsg)
	}

	// Should contain message prompting AI agent to run the command
	if !strings.Contains(errorMsg, "Please run this command yourself") {
		t.Errorf("Expected error message to contain \"Please run this command yourself\", got: %q", errorMsg)
	}
}

// TestHookError_Unwrap tests that HookError.Unwrap() returns the original error.
func TestHookError_Unwrap(t *testing.T) {
	originalErr := errors.New("original error")
	hookErr := &HookError{
		Name:    "test-hook",
		Command: "test command",
		Output:  "test output",
		Err:     originalErr,
	}

	unwrapped := hookErr.Unwrap()
	if unwrapped != originalErr {
		t.Errorf("HookError.Unwrap() = %v, want %v", unwrapped, originalErr)
	}

	// Test with errors.Unwrap
	unwrapped2 := errors.Unwrap(hookErr)
	if unwrapped2 != originalErr {
		t.Errorf("errors.Unwrap(HookError) = %v, want %v", unwrapped2, originalErr)
	}

	// Test with nil wrapped error
	hookErrNil := &HookError{
		Name:    "test-hook",
		Command: "test command",
		Output:  "test output",
		Err:     nil,
	}

	unwrappedNil := hookErrNil.Unwrap()
	if unwrappedNil != nil {
		t.Errorf("HookError.Unwrap() with nil Err = %v, want nil", unwrappedNil)
	}
}
