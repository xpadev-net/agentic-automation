package config

import (
	"fmt"
	"strings"
)

// PublicURLEnvKey is the environment variable that configures the externally
// reachable base URL of the Operator WebUI (e.g. "https://agents.example.com").
const PublicURLEnvKey = "PUBLIC_URL"

// PublicURL returns the externally reachable base URL configured via
// PUBLIC_URL, normalized without trailing slashes. Returns "" when unset.
func PublicURL() string {
	return strings.TrimRight(GetEnv(PublicURLEnvKey, ""), "/")
}

// AgentRunURL returns the WebUI deep link for an AgentRun detail page, or ""
// when PUBLIC_URL is not configured.
func AgentRunURL(agentRunID int) string {
	base := PublicURL()
	if base == "" || agentRunID <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/runs/%d", base, agentRunID)
}
