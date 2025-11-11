package context

// Config holds the agent-runner configuration from environment variables.
// Note: This type is kept for backward compatibility with reporter.Config alias.
type Config struct {
	OperatorAPIURL   string
	OperatorAPIToken string
	AgentRunID       int
	AgentType        string
	GitHubToken      string
	RetryCount       int
	WorkDir          string
}
