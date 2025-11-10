package hooks

import "fmt"

// HookError represents an error that occurred during hook or validation execution.
// It captures the hook name, command, output, and the underlying error.
type HookError struct {
	Name    string // Hook name from config.Command.Name
	Command string // Command string that was executed
	Output  string // Combined stdout/stderr output from the command
	Err     error  // Original error from exec.CommandContext
}

// Error returns a formatted error message containing hook name and command.
// Output is excluded to avoid exceeding argument string limits when test logs are included.
// The AI agent should run the command itself to see the full error output.
func (e *HookError) Error() string {
	return fmt.Sprintf("hook '%s' failed: %v\nCommand: %s\n\nPlease run this command yourself to see the full error output and fix the issues.", e.Name, e.Err, e.Command)
}

// Unwrap returns the underlying error, supporting error wrapping and error chains.
func (e *HookError) Unwrap() error {
	return e.Err
}
