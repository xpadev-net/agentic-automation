package context

import (
	"encoding/json"
	"fmt"
	"strings"
)

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

// BuildPrompt builds the full prompt for the agent.
// It combines the original prompt with previous attempts and CI logs.
func BuildPrompt(prompt, previousAttempts, ciLogs string) string {
	var result strings.Builder

	// Original prompt
	result.WriteString(prompt)

	// Previous attempts
	if previousAttempts != "" {
		var attempts []PreviousAttempt
		if err := json.Unmarshal([]byte(previousAttempts), &attempts); err == nil && len(attempts) > 0 {
			result.WriteString("\n\nPrevious attempts:\n")
			for _, attempt := range attempts {
				result.WriteString(fmt.Sprintf("- Retry #%d: %s\n", attempt.RetryCount, attempt.Error))
				if attempt.CILogs != "" {
					result.WriteString(fmt.Sprintf("  CI Logs: %s\n", attempt.CILogs))
				}
			}
		}
	}

	// CI logs (if not already included in previous attempts)
	if ciLogs != "" {
		result.WriteString("\nCI failures:\n")
		result.WriteString(ciLogs)
	}

	return result.String()
}
