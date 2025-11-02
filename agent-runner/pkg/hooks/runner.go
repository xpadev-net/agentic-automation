package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"agent-runner/pkg/config"
)

// runCommand executes a single command with timeout and error handling.
// It returns a HookError if the command fails, or nil on success.
func runCommand(cmd config.Command, workDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmd.Timeout)
	defer cancel()

	execCmd := exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	execCmd.Dir = workDir
	execCmd.Env = os.Environ()

	output, err := execCmd.CombinedOutput()
	if err != nil {
		return &HookError{
			Name:    cmd.Name,
			Command: cmd.Command,
			Output:  string(output),
			Err:     err,
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
