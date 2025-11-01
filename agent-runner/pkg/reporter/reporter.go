package reporter

import "fmt"

// Config is the reporter configuration (alias for context.Config).
// Implementation will be added in T038.
type Config struct {
	OperatorAPIURL   string
	OperatorAPIToken string
	AgentRunID       int
	AgentType        string
}

// ReportSuccess reports successful execution to the Operator API.
// Implementation will be added in T038.
func ReportSuccess(cfg *Config, prNumber int, branch, commitSHA string) error {
	return fmt.Errorf("not implemented: T038")
}

// ReportFailure reports failed execution to the Operator API.
// Implementation will be added in T038.
func ReportFailure(cfg *Config, errorMsg string) error {
	return fmt.Errorf("not implemented: T038")
}
