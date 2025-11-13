package hooks

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"agent-runner/pkg/config"
)

const (
	// maxScannerBufferSize is the maximum buffer size for bufio.Scanner.
	// This handles very long lines that might exceed the default 64KB buffer.
	maxScannerBufferSize = 1024 * 1024 // 1MB

	// maxErrorOutputLines is the maximum number of lines to keep for error reporting.
	// Since output is already streamed to stdout/stderr, we only need to keep
	// the last few lines for error context. This prevents excessive memory consumption.
	maxErrorOutputLines = 100
)

// runCommand executes a single command with timeout and error handling.
// It returns a HookError if the command fails, or nil on success.
// If cmd.Timeout is zero or negative, the command runs without timeout.
// Output is streamed in real-time to os.Stdout/os.Stderr with [hook: {name}] prefix.
func runCommand(cmd config.Command, workDir string) error {
	var ctx context.Context
	var cancel context.CancelFunc

	if cmd.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), cmd.Timeout)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	execCmd := exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	execCmd.Dir = workDir
	execCmd.Env = os.Environ()

	// Get stdout and stderr pipes for streaming
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  "",
			Err:     fmt.Errorf("failed to create stdout pipe: %w", err),
		}
	}

	stderr, err := execCmd.StderrPipe()
	if err != nil {
		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  "",
			Err:     fmt.Errorf("failed to create stderr pipe: %w", err),
		}
	}

	// Start the command
	if err := execCmd.Start(); err != nil {
		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  "",
			Err:     fmt.Errorf("failed to start command: %w", err),
		}
	}

	// Ring buffer to store only the last N lines for error reporting.
	// Since output is already streamed to stdout/stderr, we don't need to keep all output.
	// This prevents excessive memory consumption when commands produce large amounts of output.
	type lineBuffer struct {
		lines []string
		mu    sync.Mutex
	}

	outputLines := &lineBuffer{lines: make([]string, 0, maxErrorOutputLines)}
	stderrLines := &lineBuffer{lines: make([]string, 0, maxErrorOutputLines)}

	// Helper function to append line to ring buffer (keeps only last N lines)
	appendToRingBuffer := func(buf *lineBuffer, line string) {
		buf.mu.Lock()
		defer buf.mu.Unlock()
		buf.lines = append(buf.lines, line)
		if len(buf.lines) > maxErrorOutputLines {
			// Remove oldest line (ring buffer behavior)
			buf.lines = buf.lines[1:]
		}
	}

	// Helper function to get all lines from ring buffer as string
	getBufferString := func(buf *lineBuffer) string {
		buf.mu.Lock()
		defer buf.mu.Unlock()
		if len(buf.lines) == 0 {
			return ""
		}
		return strings.Join(buf.lines, "\n") + "\n"
	}

	// Prefix for output lines
	prefix := fmt.Sprintf("[hook: %s] ", cmd.Name)

	// Channels for goroutine errors
	stdoutErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)

	// Goroutine to read and stream stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		// Increase buffer size to handle very long lines
		buf := make([]byte, 0, 64*1024) // Start with 64KB
		scanner.Buffer(buf, maxScannerBufferSize)

		for scanner.Scan() {
			line := scanner.Text()
			lineWithPrefix := prefix + line + "\n"

			// Append to ring buffer (keeps only last N lines for error reporting)
			appendToRingBuffer(outputLines, line)

			// Stream to os.Stdout in real-time
			fmt.Fprint(os.Stdout, lineWithPrefix)
			os.Stdout.Sync()
		}
		if err := scanner.Err(); err != nil {
			stdoutErrCh <- fmt.Errorf("error reading stdout: %w", err)
		} else {
			stdoutErrCh <- nil
		}
	}()

	// Goroutine to read and stream stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		// Increase buffer size to handle very long lines
		buf := make([]byte, 0, 64*1024) // Start with 64KB
		scanner.Buffer(buf, maxScannerBufferSize)

		for scanner.Scan() {
			line := scanner.Text()
			lineWithPrefix := prefix + line + "\n"

			// Append to ring buffer (keeps only last N lines for error reporting)
			appendToRingBuffer(stderrLines, line)

			// Stream to os.Stderr in real-time
			fmt.Fprint(os.Stderr, lineWithPrefix)
			os.Stderr.Sync()
		}
		if err := scanner.Err(); err != nil {
			stderrErrCh <- fmt.Errorf("error reading stderr: %w", err)
		} else {
			stderrErrCh <- nil
		}
	}()

	// Helper function to kill process and wait with timeout
	killAndWait := func() error {
		if execCmd.Process != nil {
			// Kill the process to prevent deadlock
			if killErr := execCmd.Process.Kill(); killErr != nil {
				return fmt.Errorf("failed to kill process: %w", killErr)
			}
		}

		// Wait for process to exit with timeout to prevent hanging
		waitDone := make(chan error, 1)
		go func() {
			waitDone <- execCmd.Wait()
		}()

		select {
		case err := <-waitDone:
			return err
		case <-time.After(5 * time.Second):
			// Process didn't exit within 5 seconds, but we already killed it
			return fmt.Errorf("process did not exit within timeout after kill")
		}
	}

	// Wait for stdout reading to complete
	stdoutErr := <-stdoutErrCh
	if stdoutErr != nil {
		// Kill the process to prevent deadlock from pipe filling up
		killErr := killAndWait()

		// Wait for stderr reading to complete before accessing stderrLines
		// This prevents data race where stderr goroutine may still be writing
		select {
		case stderrErr := <-stderrErrCh:
			// stderr goroutine completed (ignore error for now, we're handling stdout error)
			_ = stderrErr
		case <-time.After(5 * time.Second):
			// Timeout: stderr goroutine didn't complete, but process is killed
			// Continue anyway to avoid deadlock
		}

		// Get last N lines from ring buffers for error reporting
		outputStr := getBufferString(outputLines)
		stderrStr := getBufferString(stderrLines)
		if stderrStr != "" {
			if outputStr != "" {
				outputStr += stderrStr
			} else {
				outputStr = stderrStr
			}
		}

		// Add notice that only last N lines are shown (output is already streamed)
		if outputStr != "" {
			outputStr = fmt.Sprintf("[Showing last %d lines of output for error context. Full output was streamed to stdout/stderr.]\n\n%s",
				maxErrorOutputLines, outputStr)
		}

		if killErr != nil {
			return &HookError{
				Name:    cmd.Name,
				Command: cmd.Command,
				Output:  outputStr,
				Err:     fmt.Errorf("%v (process kill failed: %v)", stdoutErr, killErr),
			}
		}
		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  outputStr,
			Err:     fmt.Errorf("%v (process killed due to stream error)", stdoutErr),
		}
	}

	// Wait for stderr reading to complete
	stderrErr := <-stderrErrCh
	if stderrErr != nil {
		// Kill the process to prevent deadlock from pipe filling up
		killErr := killAndWait()

		// Get last N lines from ring buffers for error reporting
		outputStr := getBufferString(outputLines)
		stderrStr := getBufferString(stderrLines)
		if stderrStr != "" {
			if outputStr != "" {
				outputStr += stderrStr
			} else {
				outputStr = stderrStr
			}
		}

		// Add notice that only last N lines are shown (output is already streamed)
		if outputStr != "" {
			outputStr = fmt.Sprintf("[Showing last %d lines of output for error context. Full output was streamed to stdout/stderr.]\n\n%s",
				maxErrorOutputLines, outputStr)
		}

		if killErr != nil {
			return &HookError{
				Name:    cmd.Name,
				Command: cmd.Command,
				Output:  outputStr,
				Err:     fmt.Errorf("%v (process kill failed: %v)", stderrErr, killErr),
			}
		}
		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  outputStr,
			Err:     fmt.Errorf("%v (process killed due to stream error)", stderrErr),
		}
	}

	// Wait for command to complete
	cmdErr := execCmd.Wait()

	// If command execution failed, get last N lines from ring buffers for error reporting
	if cmdErr != nil {
		outputStr := getBufferString(outputLines)
		stderrStr := getBufferString(stderrLines)
		if stderrStr != "" {
			if outputStr != "" {
				outputStr += stderrStr
			} else {
				outputStr = stderrStr
			}
		}

		// Add notice that only last N lines are shown (output is already streamed)
		if outputStr != "" {
			outputStr = fmt.Sprintf("[Showing last %d lines of output for error context. Full output was streamed to stdout/stderr.]\n\n%s",
				maxErrorOutputLines, outputStr)
		}

		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  outputStr,
			Err:     cmdErr,
		}
	}

	return nil
}

// RunPreHooks executes pre-execution hooks sequentially.
// If a required hook fails, returns error immediately and aborts execution.
// Optional hooks that fail are logged as warnings but execution continues.
func RunPreHooks(commands []config.Command, workDir string) error {
	if len(commands) == 0 {
		return nil
	}

	for _, cmd := range commands {
		if err := runCommand(cmd, workDir); err != nil {
			if cmd.Required {
				return err
			}
			// Optional hook failed - log warning and continue
			fmt.Fprintf(os.Stderr, "WARNING: Optional pre-hook '%s' failed: %v\n", cmd.Name, err)
		}
	}

	return nil
}

// RunValidations executes validation commands sequentially.
// All required validation failures are collected and returned as a combined error
// for agent retry. Optional validation failures are logged as warnings.
func RunValidations(commands []config.Command, workDir string) error {
	if len(commands) == 0 {
		return nil
	}

	var failedValidations []string

	for _, cmd := range commands {
		if err := runCommand(cmd, workDir); err != nil {
			if cmd.Required {
				errorMsg := err.Error()
				// HookErrorの場合はOutputも含める
				if hookErr, ok := err.(*HookError); ok && hookErr.Output != "" {
					errorMsg += "\n\nOutput:\n" + hookErr.Output
				}
				failedValidations = append(failedValidations, errorMsg)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: Optional validation '%s' failed: %v\n", cmd.Name, err)
			}
		}
	}

	if len(failedValidations) > 0 {
		return fmt.Errorf("validation failures:\n%s", strings.Join(failedValidations, "\n"))
	}

	return nil
}

// RunPostHooks executes post-execution hooks sequentially.
// Failures are logged as warnings but do not abort execution (PR already created).
// Always returns nil regardless of failures.
func RunPostHooks(commands []config.Command, workDir string) error {
	if len(commands) == 0 {
		return nil
	}

	for _, cmd := range commands {
		if err := runCommand(cmd, workDir); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Post-hook '%s' failed: %v\n", cmd.Name, err)
		}
	}

	return nil
}
