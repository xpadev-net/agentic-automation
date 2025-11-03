package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsRunAgentTrigger(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Basic cases
		{
			name:     "ExactMatch",
			input:    "/run-agent",
			expected: true,
		},
		{
			name:     "AllUppercase",
			input:    "/RUN-AGENT",
			expected: true,
		},
		{
			name:     "AllLowercase",
			input:    "/run-agent",
			expected: true,
		},
		{
			name:     "MixedCase1",
			input:    "/Run-Agent",
			expected: true,
		},
		{
			name:     "MixedCase2",
			input:    "/RUN-agent",
			expected: true,
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: false,
		},
		{
			name:     "NotContained",
			input:    "hello world",
			expected: false,
		},
		{
			name:     "OnlyWhitespace",
			input:    "   ",
			expected: false,
		},
		// Context cases
		{
			name:     "InSentence",
			input:    "Please /run-agent this issue",
			expected: true,
		},
		{
			name:     "AtStart",
			input:    "/run-agent please",
			expected: true,
		},
		{
			name:     "AtEnd",
			input:    "please execute /run-agent",
			expected: true,
		},
		{
			name:     "WithWhitespaceAround",
			input:    "  /run-agent  ",
			expected: true,
		},
		{
			name:     "MultipleOccurrences",
			input:    "/run-agent and /run-agent again",
			expected: true,
		},
		{
			name:     "WithSpecialChars",
			input:    "Please /run-agent now!",
			expected: true,
		},
		{
			name:     "WithNewlines",
			input:    "/run-agent\nand more",
			expected: true,
		},
		// Edge cases
		{
			name:     "PrefixOnly",
			input:    "/run",
			expected: false,
		},
		{
			name:     "SuffixOnly",
			input:    "agent",
			expected: false,
		},
		{
			name:     "SimilarButDifferent",
			input:    "/run-agent-extra",
			expected: true, // partial match
		},
		{
			name:     "SimilarButDifferent2",
			input:    "pre/run-agent",
			expected: true, // partial match
		},
		{
			name:     "WithMarkdown",
			input:    "`/run-agent` in code",
			expected: true,
		},
		{
			name:     "LongComment",
			input:    strings.Repeat("text ", 1000) + "/run-agent",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsRunAgentTrigger(tt.input)
			assert.Equal(t, tt.expected, got,
				"ContainsRunAgentTrigger(%q) = %v, want %v",
				tt.input, got, tt.expected)
		})
	}
}

func TestContainsCodexReviewTrigger(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "ShouldReturnFalseForAnyInput",
			input:    "@codex review",
			expected: false, // Stub implementation always returns false
		},
		{
			name:     "ShouldReturnFalseForEmptyString",
			input:    "",
			expected: false,
		},
		{
			name:     "ShouldReturnFalseForOtherText",
			input:    "some other text",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsCodexReviewTrigger(tt.input)
			assert.Equal(t, tt.expected, got,
				"ContainsCodexReviewTrigger(%q) = %v, want %v (stub implementation)",
				tt.input, got, tt.expected)
		})
	}
}

