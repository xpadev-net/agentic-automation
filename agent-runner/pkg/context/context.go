package context

// Config holds the agent-runner configuration from environment variables.
// Implementation will be added in T037.
type Config struct {
	OperatorAPIURL   string
	OperatorAPIToken string
	AgentRunID       int
	AgentType        string
	GitHubToken      string
	RetryCount       int
	WorkDir          string
}

// LoadConfig loads configuration from environment variables.
// Implementation will be added in T037.
func LoadConfig() *Config {
	return &Config{} // TODO: implement in T037
}

// BuildPrompt builds the full prompt for the agent.
// Implementation will be added in T037.
func BuildPrompt(prompt, previousAttempts, ciLogs string) string {
	return prompt // TODO: implement in T037
}
