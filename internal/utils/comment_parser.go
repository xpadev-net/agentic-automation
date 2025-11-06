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
// The check is case-insensitive as per GitHub webhook contract.
// Returns false if commentBody is empty.
//
// Examples:
//   - ContainsCodexReviewTrigger("@codex review") -> true
//   - ContainsCodexReviewTrigger("@CODEX REVIEW") -> true
//   - ContainsCodexReviewTrigger("Please @codex review this PR") -> true
//   - ContainsCodexReviewTrigger("hello world") -> false
//   - ContainsCodexReviewTrigger("") -> false
func ContainsCodexReviewTrigger(commentBody string) bool {
	if commentBody == "" {
		return false
	}
	return strings.Contains(strings.ToLower(commentBody), strings.ToLower(CodexReviewTrigger))
}

// ExtractInstructionFromComment extracts the instruction text after "/run-agent" from a comment body.
// The extraction is case-insensitive. Returns empty string if trigger is not found or no instruction follows.
//
// This function scans the original string byte-by-byte to find the trigger, ensuring that
// Unicode characters whose lowercase form expands to multiple runes (e.g., İ → i̇) do not
// cause index mismatches that could lead to panics.
//
// Examples:
//   - ExtractInstructionFromComment("/run-agent テストが落ちているため修正して下さい") -> "テストが落ちているため修正して下さい"
//   - ExtractInstructionFromComment("/RUN-AGENT fix the bug") -> "fix the bug"
//   - ExtractInstructionFromComment("Please /run-agent update the code") -> "update the code"
//   - ExtractInstructionFromComment("/run-agent") -> ""
//   - ExtractInstructionFromComment("/run-agent\nテストを修正") -> "テストを修正"
//   - ExtractInstructionFromComment("hello world") -> ""
func ExtractInstructionFromComment(commentBody string) string {
	if commentBody == "" {
		return ""
	}

	triggerLen := len(RunAgentTrigger)
	bodyLen := len(commentBody)

	// Scan the original string byte-by-byte to find the trigger
	// This ensures indices always correspond to the original bytes, preventing
	// panics when Unicode characters whose lowercase form expands to multiple
	// runes (e.g., İ → i̇) appear before the trigger.
	for i := 0; i <= bodyLen-triggerLen; i++ {
		// Extract candidate substring at current position
		candidate := commentBody[i : i+triggerLen]

		// Case-insensitive comparison using strings.EqualFold
		// This handles Unicode correctly without changing string length
		if strings.EqualFold(candidate, RunAgentTrigger) {
			// Found the trigger, extract instruction after it
			afterTrigger := commentBody[i+triggerLen:]

			// Trim leading whitespace and newlines
			instruction := strings.TrimSpace(afterTrigger)

			return instruction
		}
	}

	return ""
}
