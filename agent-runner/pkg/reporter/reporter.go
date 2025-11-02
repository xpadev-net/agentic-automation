package reporter

// Config is the reporter configuration (alias for context.Config).
type Config struct {
	OperatorAPIURL   string
	OperatorAPIToken string
	AgentRunID       int
	AgentType        string
}

// ReportSuccess reports successful execution to the Operator API.
// It creates a new Client from the Config and sends a success report.
func ReportSuccess(cfg *Config, prNumber int, branch, commitSHA string) error {
	client, err := NewClient(cfg.OperatorAPIURL, cfg.OperatorAPIToken, cfg.AgentRunID)
	if err != nil {
		return err
	}
	return client.ReportSuccess(prNumber, branch, commitSHA, cfg.AgentType)
}

// ReportFailure reports failed execution to the Operator API.
// It creates a new Client from the Config and sends a failure report.
func ReportFailure(cfg *Config, errorMsg, logs string) error {
	client, err := NewClient(cfg.OperatorAPIURL, cfg.OperatorAPIToken, cfg.AgentRunID)
	if err != nil {
		return err
	}
	return client.ReportFailure(errorMsg, logs, cfg.AgentType)
}
