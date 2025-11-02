package agent

import (
	"fmt"
	"os"
	"os/exec"
)

// CommandRunner defines the interface for executing commands.
// This allows mocking command execution in tests.
type CommandRunner interface {
	Run(name string, args []string, workDir string) ([]byte, error)
}

// Executor executes AI agents (claude-code or cursor-agents).
type Executor struct {
	agentType string
	cmdRunner CommandRunner // Optional command runner for testing (nil uses exec.Command)
}

// NewExecutor creates a new agent executor for the specified agent type.
// Valid agent types are "claude-code" and "cursor-agents".
func NewExecutor(agentType string) *Executor {
	return &Executor{agentType: agentType}
}

// NewExecutorWithRunner creates a new agent executor with a custom command runner.
// This is primarily used for testing to inject a mock command runner.
func NewExecutorWithRunner(agentType string, runner CommandRunner) *Executor {
	return &Executor{
		agentType: agentType,
		cmdRunner: runner,
	}
}

// Execute runs the configured agent with the given prompt in the specified working directory.
// It returns the combined output (stdout + stderr) and any error that occurred during execution.
func (e *Executor) Execute(workDir, prompt string) (string, error) {
	switch e.agentType {
	case "claude-code":
		return e.executeClaudeCode(workDir, prompt)
	case "cursor-agents":
		return e.executeCursor(workDir, prompt)
	default:
		return "", fmt.Errorf("unknown agent type: %s", e.agentType)
	}
}

// executeClaudeCode executes the claude-code agent.
func (e *Executor) executeClaudeCode(workDir, prompt string) (string, error) {
	// Check if ANTHROPIC_API_KEY is set
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY environment variable is not set")
	}

	var output []byte
	var err error

	if e.cmdRunner != nil {
		// Use injected command runner (for testing)
		output, err = e.cmdRunner.Run("claude-code", []string{"-p", prompt}, workDir)
	} else {
		// Build command: claude-code -p "<prompt>"
		// Additional flags for workspace and non-interactive mode may be needed
		cmd := exec.Command("claude-code", "-p", prompt)
		cmd.Dir = workDir

		// Preserve existing environment and ensure ANTHROPIC_API_KEY is set
		cmd.Env = os.Environ()

		// Execute and capture combined output (stdout + stderr)
		output, err = cmd.CombinedOutput()
	}

	outputStr := string(output)

	// If command execution failed, wrap the error with output context
	if err != nil {
		return outputStr, fmt.Errorf("claude-code execution failed: %w\nOutput: %s", err, outputStr)
	}

	return outputStr, nil
}

// executeCursor executes the cursor-agents agent.
func (e *Executor) executeCursor(workDir, prompt string) (string, error) {
	// Check if CURSOR_API_KEY is set
	if os.Getenv("CURSOR_API_KEY") == "" {
		return "", fmt.Errorf("CURSOR_API_KEY environment variable is not set")
	}

	var output []byte
	var err error

	if e.cmdRunner != nil {
		// Use injected command runner (for testing)
		output, err = e.cmdRunner.Run("cursor", []string{"agent", "-p", prompt}, workDir)
	} else {
		// Build command: cursor agent -p "<prompt>"
		// Additional flags for working directory and headless mode may be needed
		cmd := exec.Command("cursor", "agent", "-p", prompt)
		cmd.Dir = workDir

		// Preserve existing environment and ensure CURSOR_API_KEY is set
		cmd.Env = os.Environ()

		// Execute and capture combined output (stdout + stderr)
		output, err = cmd.CombinedOutput()
	}

	outputStr := string(output)

	// If command execution failed, wrap the error with output context
	if err != nil {
		return outputStr, fmt.Errorf("cursor agent execution failed: %w\nOutput: %s", err, outputStr)
	}

	return outputStr, nil
}
