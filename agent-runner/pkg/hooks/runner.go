package hooks

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"agent-runner/pkg/config"
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

	// Buffer to store all output
	var outputBuf bytes.Buffer
	var outputMu sync.Mutex

	// Prefix for output lines
	prefix := fmt.Sprintf("[hook: %s] ", cmd.Name)

	// Channels for goroutine errors
	stdoutErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)

	// Goroutine to read and stream stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			lineWithPrefix := prefix + line + "\n"

			// Append to output buffer
			outputMu.Lock()
			outputBuf.WriteString(line + "\n")
			outputMu.Unlock()

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
	stderrBuf := &bytes.Buffer{}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			lineWithPrefix := prefix + line + "\n"

			// Append to stderr buffer
			stderrBuf.WriteString(line + "\n")

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

		// Wait for stderr reading to complete before accessing stderrBuf
		// This prevents data race where stderr goroutine may still be writing
		select {
		case stderrErr := <-stderrErrCh:
			// stderr goroutine completed (ignore error for now, we're handling stdout error)
			_ = stderrErr
		case <-time.After(5 * time.Second):
			// Timeout: stderr goroutine didn't complete, but process is killed
			// Continue anyway to avoid deadlock
		}

		// Get any output that was successfully read before the error
		outputMu.Lock()
		outputStr := outputBuf.String()
		outputMu.Unlock()

		if stderrBuf.Len() > 0 {
			outputStr += stderrBuf.String()
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

		// Get any output that was successfully read before the error
		outputMu.Lock()
		outputStr := outputBuf.String()
		outputMu.Unlock()

		if stderrBuf.Len() > 0 {
			outputStr += stderrBuf.String()
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

	// Get final output
	outputMu.Lock()
	outputStr := outputBuf.String()
	outputMu.Unlock()

	// Append stderr output if any
	if stderrBuf.Len() > 0 {
		outputStr += stderrBuf.String()
	}

	// If command execution failed, return HookError with output
	if cmdErr != nil {
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
				failedValidations = append(failedValidations, err.Error())
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
