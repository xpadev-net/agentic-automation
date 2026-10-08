package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent-runner/pkg/utils"
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

// lockedBuffer makes a bytes.Buffer safe for concurrent writes from the
// stdout/stderr copy goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Bytes()
}

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

const defaultCodexModel = "gpt-5.6-luna"

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
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

		// Stream stdout+stderr live into progressWriter (os.Stderr, which the
		// log shipper tails) while also capturing the combined output like
		// CombinedOutput did — claude runs are the longest-lived ones, so
		// buffering-until-exit would hide them from the live log feed.
		// perr must not reuse the outer err name: cmd.Wait() below writes to
		// the outer variable, and a shadowed err would leave it nil so a
		// failed subprocess reported success.
		stdout, perr := cmd.StdoutPipe()
		if perr != nil {
			return "", fmt.Errorf("failed to create claude-code stdout pipe: %w", perr)
		}
		stderr, perr := cmd.StderrPipe()
		if perr != nil {
			return "", fmt.Errorf("failed to create claude-code stderr pipe: %w", perr)
		}
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start claude-code: %w", err)
		}

		var out lockedBuffer
		progress := &synchronizedWriter{writer: e.progressWriter}
		readErrs := make(chan error, 2)
		go func() { readErrs <- streamCopy(stdout, &out, progress) }()
		go func() { readErrs <- streamCopy(stderr, &out, progress) }()
		// A read-side failure stops the child immediately — like the codex
		// path — so the abandoned pipe cannot fill and block the other
		// reader or cmd.Wait. A progress-writer failure alone keeps
		// draining (see streamCopy).
		var streamErr error
		for range 2 {
			if rerr := <-readErrs; rerr != nil {
				if streamErr == nil {
					streamErr = rerr
				}
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
		}
		err = cmd.Wait()
		output = out.Bytes()
		if streamErr != nil {
			return string(output), streamErr
		}
	}

	outputStr := string(output)

	// If command execution failed, wrap the error with only the output tail:
	// the streaming path already mirrored the full transcript to stderr, so
	// embedding it again would replay every line through the log shipper
	// when the caller prints this error.
	if err != nil {
		return outputStr, fmt.Errorf("claude-code execution failed: %w\nOutput (tail): %s", err, outputTail(outputStr))
	}

	return outputStr, nil
}

// streamCopy drains r into buf while mirroring to progress. A mirror
// failure (e.g. closed stderr) only drops the progress writer — the pipe
// keeps being drained so the subprocess never blocks on it. A read
// failure is returned so the caller can terminate the subprocess.
func streamCopy(r io.Reader, buf *lockedBuffer, progress io.Writer) error {
	p := make([]byte, 32*1024)
	mirror := true
	for {
		n, rerr := r.Read(p)
		if n > 0 {
			chunk := p[:n]
			_, _ = buf.Write(chunk)
			if mirror {
				if _, werr := progress.Write(chunk); werr != nil {
					mirror = false
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create codex stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create codex stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start codex: %w", err)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	progressWriter := &synchronizedWriter{writer: e.progressWriter}
	type readerResult struct {
		stream string
		err    error
	}
	readerResults := make(chan readerResult, 2)

	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				stdoutBuf.WriteString(line)
				_, writeErr := fmt.Fprintf(progressWriter, "[CODEX] %s", line)
				if !strings.HasSuffix(line, "\n") {
					_, newlineErr := fmt.Fprintln(progressWriter)
					if writeErr == nil {
						writeErr = newlineErr
					}
				}
				if writeErr != nil {
					readerResults <- readerResult{stream: "stdout", err: fmt.Errorf("error streaming codex stdout: %w", writeErr)}
					return
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					readerResults <- readerResult{stream: "stdout"}
				} else {
					readerResults <- readerResult{stream: "stdout", err: fmt.Errorf("error reading codex stdout: %w", readErr)}
				}
				return
			}
		}
	}()

	go func() {
		_, copyErr := io.Copy(io.MultiWriter(&stderrBuf, progressWriter), stderr)
		if copyErr != nil {
			readerResults <- readerResult{stream: "stderr", err: fmt.Errorf("error reading codex stderr: %w", copyErr)}
			return
		}
		readerResults <- readerResult{stream: "stderr"}
	}()

	var streamErr error
	for range 2 {
		result := <-readerResults
		if result.err != nil {
			if streamErr == nil {
				streamErr = result.err
			}
			// Stop the child immediately on the first reader/writer error. This
			// closes both pipes so the other goroutine can finish and be joined.
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
	cmdErr := cmd.Wait()

	outputStr := stdoutBuf.String() + stderrBuf.String()
	if streamErr != nil {
		return outputStr, streamErr
	}
	return codexResult(outputStr, cmdErr)
}

// outputTail bounds the output embedded in an execution error. Streaming
// executors already mirror their full transcript to stderr (and thus the
// log shipper), so embedding it again would replay thousands of lines when
// the caller prints this error after run failure.
func outputTail(output string) string {
	const maxTailBytes = 4096
	if len(output) <= maxTailBytes {
		return output
	}
	cut := len(output) - maxTailBytes
	// Advance the cut only past UTF-8 continuation bytes so we don't
	// split a rune at the boundary. Subprocess stderr is not guaranteed
	// valid UTF-8, so sanitize — not delete — any remaining invalid
	// bytes: walking forward until the whole tail validates would drop
	// every diagnostic byte in front of a single bad byte.
	for cut < len(output) && output[cut]&0xC0 == 0x80 {
		cut++
	}
	return "…[truncated]" + strings.ToValidUTF8(output[cut:], "�")
}

func codexResult(output string, err error) (string, error) {
	// If command execution failed, wrap the error with only the output tail.
	if err != nil {
		return output, fmt.Errorf("codex execution failed: %w\nOutput (tail): %s", err, outputTail(output))
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
			return outputStr, fmt.Errorf("cursor agent execution failed: %w\nOutput (tail): %s", err, outputTail(outputStr))
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

	// Create log formatter for suppressing duplicate thinking logs
	logFormatter := utils.NewLogFormatter()

	// Channels for goroutine errors
	stdoutErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)

	// Goroutine to read and parse stdout stream
	go func() {
		scanner := bufio.NewScanner(stdout)
		// Set buffer size to handle large JSON lines (initial 1MB, max 10MB)
		// This prevents ErrTooLong errors when cursor-agent outputs large messages
		buf := make([]byte, 0, 1024*1024) // 1MB initial buffer
		scanner.Buffer(buf, 10*1024*1024) // 10MB max buffer
		inProgress := false
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
				// Format and output the parsed entry with suppression of duplicate thinking progress logs
				formatted, shouldOutput := logFormatter.FormatAndOutput(entry)
				if shouldOutput {
					isProcessingPiece := strings.HasPrefix(formatted, "[THINKING] processing") || formatted == "."
					if isProcessingPiece {
						// processing 系は改行しない（同一行で進捗を更新）
						fmt.Fprintf(os.Stderr, "%s", formatted)
						os.Stderr.Sync()
						inProgress = true
					} else {
						if inProgress {
							// 直前が進捗連結中なら行を確定
							fmt.Fprintf(os.Stderr, "\n")
							inProgress = false
						}
						fmt.Fprintf(os.Stderr, "%s\n", formatted)
					}
				}
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

	// Helper function to kill process and wait with timeout
	killAndWait := func() error {
		if cmd.Process != nil {
			// Kill the process to prevent deadlock
			if killErr := cmd.Process.Kill(); killErr != nil {
				return fmt.Errorf("failed to kill process: %w", killErr)
			}
		}

		// Wait for process to exit with timeout to prevent hanging
		waitDone := make(chan error, 1)
		go func() {
			waitDone <- cmd.Wait()
		}()

		select {
		case err := <-waitDone:
			return err
		case <-time.After(5 * time.Second):
			// Process didn't exit within 5 seconds, but we already killed it
			// This shouldn't happen, but we return an error to be safe
			return fmt.Errorf("process did not exit within timeout after kill")
		}
	}

	// Wait for stdout reading to complete
	stdoutErr := <-stdoutErrCh
	if stdoutErr != nil {
		// Kill the process to prevent deadlock from pipe filling up
		killErr := killAndWait()

		// Get any output that was successfully read before the error
		outputMu.Lock()
		outputStr := outputBuf.String()
		outputMu.Unlock()

		if stderrBuf.Len() > 0 {
			outputStr += stderrBuf.String()
		}

		if killErr != nil {
			return outputStr, fmt.Errorf("%v (process kill failed: %v)", stdoutErr, killErr)
		}
		return outputStr, fmt.Errorf("%v (process killed due to stream error)", stdoutErr)
	}

	// Wait for stderr reading to complete
	stderrErr := <-stderrErrCh
	if stderrErr != nil {
		// Kill the process to prevent deadlock from pipe filling up
		killErr := killAndWait()

		// Get any output that was successfully read before the error
		outputMu.Lock()
		outputStr := outputBuf.String()
		outputMu.Unlock()

		if stderrBuf.Len() > 0 {
			outputStr += stderrBuf.String()
		}

		if killErr != nil {
			return outputStr, fmt.Errorf("%v (process kill failed: %v)", stderrErr, killErr)
		}
		return outputStr, fmt.Errorf("%v (process killed due to stream error)", stderrErr)
	}

	// Wait for command to complete with timeout to prevent hanging
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var cmdErr error
	select {
	case cmdErr = <-waitDone:
		// Process completed normally
	case <-time.After(1 * time.Hour):
		// This is a very long timeout for safety, but if it happens something is wrong
		// Kill the process and return error
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmdErr = fmt.Errorf("command did not complete within 1 hour timeout")
	}

	// Get final output
	outputMu.Lock()
	outputStr := outputBuf.String()
	outputMu.Unlock()

	// Append stderr output if any
	if stderrBuf.Len() > 0 {
		outputStr += stderrBuf.String()
		fmt.Fprintf(os.Stderr, "[STDERR] %s", stderrBuf.String())
	}

	// If command execution failed, wrap the error with only the output tail.
	if cmdErr != nil {
		return outputStr, fmt.Errorf("cursor agent execution failed: %w\nOutput (tail): %s", cmdErr, outputTail(outputStr))
	}

	return outputStr, nil
}
