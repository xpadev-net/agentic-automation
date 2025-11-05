package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"agent-runner/pkg/utils"
)

// CommandRunner defines the interface for executing commands.
// This allows mocking command execution in tests.
type CommandRunner interface {
	Run(name string, args []string, workDir string) ([]byte, error)
}

// Executor executes AI agents (claude-code or cursor-agent).
type Executor struct {
	agentType string
	cmdRunner CommandRunner // Optional command runner for testing (nil uses exec.Command)
}

// NewExecutor creates a new agent executor for the specified agent type.
// Valid agent types are "claude-code" and "cursor-agent".
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
	case "cursor-agent":
		// Use default values for cursor-agent
		return e.executeCursor(workDir, prompt, "auto", true)
	default:
		return "", fmt.Errorf("unknown agent type: %s", e.agentType)
	}
}

// ExecuteWithOptions runs the configured agent with additional options.
// This is used for cursor-agent with custom model and allow-write settings.
func (e *Executor) ExecuteWithOptions(workDir, prompt, model string, allowWrite bool) (string, error) {
	switch e.agentType {
	case "cursor-agent":
		return e.executeCursor(workDir, prompt, model, allowWrite)
	default:
		return "", fmt.Errorf("ExecuteWithOptions is only supported for cursor-agent, got: %s", e.agentType)
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

// executeCursor executes the cursor-agent agent.
func (e *Executor) executeCursor(workDir, prompt, model string, allowWrite bool) (string, error) {
	// Check if CURSOR_API_KEY is set
	if os.Getenv("CURSOR_API_KEY") == "" {
		return "", fmt.Errorf("CURSOR_API_KEY environment variable is not set")
	}

	// Build command arguments
	args := []string{
		"--model", model,
		"--output-format", "stream-json",
		"-p", prompt,
	}

	// Add --force flag if allowWrite is true
	if allowWrite {
		args = append(args, "--force")
	}

	var output []byte
	var err error

	if e.cmdRunner != nil {
		// Use injected command runner (for testing)
		output, err = e.cmdRunner.Run("cursor-agent", args, workDir)
		outputStr := string(output)
		if err != nil {
			return outputStr, fmt.Errorf("cursor agent execution failed: %w\nOutput: %s", err, outputStr)
		}
		return outputStr, nil
	}

	// Build command: cursor-agent --model <model> --output-format stream-json [--force] -p "<prompt>"
	cmd := exec.Command("cursor-agent", args...)
	cmd.Dir = workDir

	// Preserve existing environment and ensure CURSOR_API_KEY is set
	cmd.Env = os.Environ()

	// Get stdout and stderr pipes for streaming
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start cursor-agent: %w", err)
	}

	// Buffer to store all output
	var outputBuf bytes.Buffer
	var outputMu sync.Mutex

	// Channels for goroutine errors
	stdoutErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)

	// Goroutine to read and parse stdout stream
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Bytes()
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)

			// Append to output buffer
			outputMu.Lock()
			outputBuf.Write(lineCopy)
			outputBuf.WriteByte('\n')
			outputMu.Unlock()

			// Parse and format the line in real-time
			entry, parseErr := utils.ParseLogEntry(lineCopy)
			if parseErr != nil {
				// If parsing fails, output the raw line with a warning
				fmt.Fprintf(os.Stderr, "[PARSE ERROR] %v: %s\n", parseErr, string(lineCopy))
			} else {
				// Format and output the parsed entry
				fmt.Fprintf(os.Stderr, "%s\n", entry.Format())
			}
		}
		if err := scanner.Err(); err != nil {
			stdoutErrCh <- fmt.Errorf("error reading stdout: %w", err)
		} else {
			stdoutErrCh <- nil
		}
	}()

	// Goroutine to read stderr
	stderrBuf := &bytes.Buffer{}
	go func() {
		_, copyErr := io.Copy(stderrBuf, stderr)
		if copyErr != nil {
			stderrErrCh <- fmt.Errorf("error reading stderr: %w", copyErr)
		} else {
			stderrErrCh <- nil
		}
	}()

	// Wait for stdout reading to complete
	stdoutErr := <-stdoutErrCh
	if stdoutErr != nil {
		cmd.Wait() // Clean up
		return "", stdoutErr
	}

	// Wait for stderr reading to complete
	stderrErr := <-stderrErrCh
	if stderrErr != nil {
		cmd.Wait() // Clean up
		return "", stderrErr
	}

	// Wait for command to complete
	cmdErr := cmd.Wait()

	// Get final output
	outputMu.Lock()
	outputStr := outputBuf.String()
	outputMu.Unlock()

	// Append stderr output if any
	if stderrBuf.Len() > 0 {
		outputStr += stderrBuf.String()
		fmt.Fprintf(os.Stderr, "[STDERR] %s", stderrBuf.String())
	}

	// If command execution failed, wrap the error with output context
	if cmdErr != nil {
		return outputStr, fmt.Errorf("cursor agent execution failed: %w\nOutput: %s", cmdErr, outputStr)
	}

	return outputStr, nil
}
