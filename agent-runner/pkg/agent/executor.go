package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"agent-runner/pkg/redact"
	"encoding/json"
)

// CommandRunner defines the interface for executing commands.
// This allows mocking command execution in tests.
type CommandRunner interface {
	Run(name string, args []string, workDir string) ([]byte, error)
}

// Executor executes AI agents (claude-code, cursor-agent, or codex).
type Executor struct {
	agentType      string
	cmdRunner      CommandRunner // Optional command runner for testing (nil uses exec.Command)
	progressWriter io.Writer
}

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

const defaultCodexModel = "gpt-5.6-luna"

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writer == nil {
		return len(p), nil
	}
	return w.writer.Write(p)
}

// NewExecutor creates a new agent executor for the specified agent type.
// Valid agent types are "claude-code", "cursor-agent", and "codex".
func NewExecutor(agentType string) *Executor {
	return &Executor{agentType: agentType, progressWriter: os.Stderr}
}

// NewExecutorWithRunner creates a new agent executor with a custom command runner.
// This is primarily used for testing to inject a mock command runner.
func NewExecutorWithRunner(agentType string, runner CommandRunner) *Executor {
	return &Executor{
		agentType:      agentType,
		cmdRunner:      runner,
		progressWriter: os.Stderr,
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
	case "codex":
		// Use default values for codex
		return e.executeCodex(workDir, prompt, defaultCodexModel, true)
	default:
		return "", fmt.Errorf("unknown agent type: %s", e.agentType)
	}
}

// ExecuteWithOptions runs the configured agent with additional options.
// This is used for cursor-agent and codex with custom model and allow-write settings.
func (e *Executor) ExecuteWithOptions(workDir, prompt, model string, allowWrite bool) (string, error) {
	switch e.agentType {
	case "cursor-agent":
		return e.executeCursor(workDir, prompt, model, allowWrite)
	case "codex":
		return e.executeCodex(workDir, prompt, model, allowWrite)
	default:
		return "", fmt.Errorf("ExecuteWithOptions is only supported for cursor-agent and codex, got: %s", e.agentType)
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
		output, err = cmd.Output()
	}

	outputStr := string(output)

	// If command execution failed, wrap the error with output context
	if err != nil {
		return outputStr, fmt.Errorf("claude-code execution failed: %w", redact.Error(err))
	}

	return outputStr, nil
}

// executeCodex executes the OpenAI Codex CLI (codex exec) in non-interactive mode.
// model selects the model via -m; empty uses gpt-5.6-luna and "auto" uses the CLI default.
// allowWrite enables unrestricted execution for implementation runs. Read-only
// runs keep the Codex sandbox and never bypass approvals.
func (e *Executor) executeCodex(workDir, prompt, model string, allowWrite bool) (string, error) {
	// Codex accepts either API-key environment authentication or its OAuth auth file.
	// Only check that the file is non-empty here; the CLI owns parsing and refreshing it.
	if os.Getenv("CODEX_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("codex credentials are not configured")
		}
		authInfo, err := os.Stat(filepath.Join(homeDir, ".codex", "auth.json"))
		if err != nil || !authInfo.Mode().IsRegular() || authInfo.Size() == 0 {
			return "", fmt.Errorf("codex credentials are not configured")
		}
	}

	args := []string{"exec"}
	if allowWrite {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		args = append(args, "--sandbox", "read-only")
	}
	args = append(args, "--json")
	if model == "" {
		model = defaultCodexModel
	}
	if model != "" && model != "auto" {
		args = append(args, "-m", model)
	}
	args = append(args, prompt)

	if e.cmdRunner != nil {
		// Use injected command runner (for testing)
		output, err := e.cmdRunner.Run("codex", args, workDir)
		return codexResult(string(output), err)
	}

	cmd := exec.Command("codex", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	output, err := e.runStreaming(cmd, "CODEX")
	return codexResult(output, err)
}

func codexResult(output string, err error) (string, error) {
	// If command execution failed, wrap the error with output context
	if err != nil {
		return output, fmt.Errorf("codex execution failed: %w", redact.Error(err))
	}
	return output, nil
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
			return outputStr, fmt.Errorf("cursor agent execution failed: %w", redact.Error(err))
		}
		return outputStr, nil
	}

	// Build command: cursor-agent --model <model> --output-format stream-json [--force] -p "<prompt>"
	cmd := exec.Command("cursor-agent", args...)
	cmd.Dir = workDir

	// Preserve existing environment and ensure CURSOR_API_KEY is set
	cmd.Env = os.Environ()

	outputStr, err := e.runStreaming(cmd, "CURSOR")
	if err != nil {
		return outputStr, fmt.Errorf("cursor agent execution failed: %w", redact.Error(err))
	}
	return outputStr, nil
}

// progressSummary emits only an allowlisted event name, never model text, tool
// output, commands, paths, or malformed raw input. Unknown data stays private.
func progressSummary(line string) string {
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if json.Unmarshal([]byte(line), &event) != nil {
		return "unrecognized output received"
	}
	switch event.Type {
	case "thread.started", "turn.started", "turn.completed", "turn.failed", "error", "system", "user", "assistant", "thinking", "tool_call", "result":
		return event.Type
	case "item.started", "item.updated", "item.completed":
		switch event.Item.Type {
		case "agent_message", "reasoning", "command_execution", "file_change", "mcp_tool_call", "web_search", "todo_list", "error":
			return event.Type + " (" + event.Item.Type + ")"
		default:
			return event.Type
		}
	default:
		return "event received"
	}
}

// runStreaming keeps stdout for parsing and stderr only as a byte count. Both
// streams are drained and joined before return, including progress sink failures.
func (e *Executor) runStreaming(cmd *exec.Cmd, label string) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", redact.Error(err)
	}
	var output bytes.Buffer
	progress := &synchronizedWriter{writer: e.progressWriter}
	results := make(chan error, 2)
	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				output.WriteString(line)
				if _, err := fmt.Fprintf(progress, "[%s] %s\n", label, progressSummary(line)); err != nil {
					results <- fmt.Errorf("progress sink failed: %w", redact.Error(err))
					return
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					readErr = nil
				}
				results <- readErr
				return
			}
		}
	}()
	go func() {
		n, err := io.Copy(io.Discard, stderr)
		if err == nil && n > 0 {
			_, err = fmt.Fprintf(progress, "[%s] stderr received (%d bytes suppressed)\n", label, n)
		}
		results <- err
	}()
	var streamErr error
	for range 2 {
		if err := <-results; err != nil {
			if streamErr == nil {
				streamErr = err
			}
			_ = cmd.Process.Kill()
			_ = stdout.Close()
			_ = stderr.Close()
		}
	}
	waitErr := cmd.Wait()
	if streamErr != nil {
		return output.String(), redact.Error(streamErr)
	}
	return output.String(), redact.Error(waitErr)
}
