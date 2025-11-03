package utils

import (
	"strings"
)

const (
	// RunAgentTrigger is the trigger string that starts agent execution in GitHub Issue comments.
	RunAgentTrigger = "/run-agent"

	// CodexReviewTrigger is the trigger string for requesting Codex review in PR comments.
	// This will be implemented in T092 (User Story 3).
	CodexReviewTrigger = "@codex review"
)

// ContainsRunAgentTrigger checks if the comment body contains the "/run-agent" trigger string.
// The check is case-insensitive as per GitHub webhook contract.
// Returns false if commentBody is empty.
//
// Examples:
//   - ContainsRunAgentTrigger("/run-agent") -> true
//   - ContainsRunAgentTrigger("/RUN-AGENT") -> true
//   - ContainsRunAgentTrigger("Please /run-agent this") -> true
//   - ContainsRunAgentTrigger("hello world") -> false
//   - ContainsRunAgentTrigger("") -> false
func ContainsRunAgentTrigger(commentBody string) bool {
	if commentBody == "" {
		return false
	}
	return strings.Contains(strings.ToLower(commentBody), strings.ToLower(RunAgentTrigger))
}

// ContainsCodexReviewTrigger checks if the comment body contains the "@codex review" trigger string.
// This is a placeholder for future implementation (T092 - User Story 3).
// Currently returns false for all inputs.
//
// Examples:
//   - ContainsCodexReviewTrigger("@codex review") -> false (not yet implemented)
//   - ContainsCodexReviewTrigger("@codex please review") -> false (not yet implemented)
func ContainsCodexReviewTrigger(commentBody string) bool {
	// Stub implementation - returns false until T092
	return false
}
