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
		// Basic cases
		{
			name:     "ExactMatch",
			input:    "@codex review",
			expected: true,
		},
		{
			name:     "AllUppercase",
			input:    "@CODEX REVIEW",
			expected: true,
		},
		{
			name:     "AllLowercase",
			input:    "@codex review",
			expected: true,
		},
		{
			name:     "MixedCase1",
			input:    "@Codex Review",
			expected: true,
		},
		{
			name:     "MixedCase2",
			input:    "@CODEX review",
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
			input:    "Please @codex review this PR",
			expected: true,
		},
		{
			name:     "AtStart",
			input:    "@codex review please",
			expected: true,
		},
		{
			name:     "AtEnd",
			input:    "please check @codex review",
			expected: true,
		},
		{
			name:     "WithWhitespaceAround",
			input:    "  @codex review  ",
			expected: true,
		},
		{
			name:     "MultipleOccurrences",
			input:    "@codex review and @codex review again",
			expected: true,
		},
		{
			name:     "WithSpecialChars",
			input:    "Please @codex review now!",
			expected: true,
		},
		{
			name:     "WithNewlines",
			input:    "@codex review\nand more",
			expected: true,
		},
		// Edge cases
		{
			name:     "PrefixOnly",
			input:    "@codex",
			expected: false,
		},
		{
			name:     "SuffixOnly",
			input:    "review",
			expected: false,
		},
		{
			name:     "SimilarButDifferent",
			input:    "@codex review-extra",
			expected: true, // partial match
		},
		{
			name:     "SimilarButDifferent2",
			input:    "pre@codex review",
			expected: true, // partial match
		},
		{
			name:     "WithMarkdown",
			input:    "`@codex review` in code",
			expected: true,
		},
		{
			name:     "LongComment",
			input:    strings.Repeat("text ", 1000) + "@codex review",
			expected: true,
		},
		{
			name:     "WithUsernameMention",
			input:    "@user @codex review",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsCodexReviewTrigger(tt.input)
			assert.Equal(t, tt.expected, got,
				"ContainsCodexReviewTrigger(%q) = %v, want %v",
				tt.input, got, tt.expected)
		})
	}
}

func TestExtractInstructionFromComment(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Basic cases
		{
			name:     "ExactMatchWithInstruction",
			input:    "/run-agent テストが落ちているため修正して下さい",
			expected: "テストが落ちているため修正して下さい",
		},
		{
			name:     "AllUppercase",
			input:    "/RUN-AGENT fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "AllLowercase",
			input:    "/run-agent update the code",
			expected: "update the code",
		},
		{
			name:     "MixedCase",
			input:    "/Run-Agent update the code",
			expected: "update the code",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "NoTrigger",
			input:    "hello world",
			expected: "",
		},
		{
			name:     "TriggerOnly",
			input:    "/run-agent",
			expected: "",
		},
		{
			name:     "TriggerWithWhitespace",
			input:    "/run-agent   ",
			expected: "",
		},
		// Context cases
		{
			name:     "InSentence",
			input:    "Please /run-agent update the code",
			expected: "update the code",
		},
		{
			name:     "AtStart",
			input:    "/run-agent please fix",
			expected: "please fix",
		},
		{
			name:     "WithNewlines",
			input:    "/run-agent\nテストを修正",
			expected: "テストを修正",
		},
		{
			name:     "WithLeadingWhitespace",
			input:    "/run-agent   fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "WithLeadingNewlines",
			input:    "/run-agent\n\nfix the bug",
			expected: "fix the bug",
		},
		{
			name:     "WithSpecialChars",
			input:    "/run-agent fix the bug!",
			expected: "fix the bug!",
		},
		// Unicode cases - testing the fix for Unicode lowercase expansion bug
		{
			name:     "TurkishI_BeforeTrigger",
			input:    "İstanbul /run-agent fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "TurkishI_AtStart",
			input:    "İ/run-agent fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "GreekSigma_BeforeTrigger",
			input:    "Σ/run-agent fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "Cyrillic_BeforeTrigger",
			input:    "Я/run-agent fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "Japanese_BeforeTrigger",
			input:    "日本語 /run-agent テストを修正",
			expected: "テストを修正",
		},
		{
			name:     "Chinese_BeforeTrigger",
			input:    "中文 /run-agent 修复bug",
			expected: "修复bug",
		},
		{
			name:     "Korean_BeforeTrigger",
			input:    "한국어 /run-agent 버그 수정",
			expected: "버그 수정",
		},
		{
			name:     "Emoji_BeforeTrigger",
			input:    "🚀 /run-agent fix the bug",
			expected: "fix the bug",
		},
		{
			name:     "MultipleUnicodeChars_BeforeTrigger",
			input:    "İstanbul Σ Я 日本語 /run-agent fix all bugs",
			expected: "fix all bugs",
		},
		{
			name:     "UnicodeInInstruction",
			input:    "/run-agent İstanbul'da test yap",
			expected: "İstanbul'da test yap",
		},
		// Edge cases
		{
			name:     "MultipleTriggers_FirstOne",
			input:    "/run-agent first /run-agent second",
			expected: "first /run-agent second",
		},
		{
			name:     "TriggerInMiddle",
			input:    "prefix /run-agent suffix",
			expected: "suffix",
		},
		{
			name:     "LongComment",
			input:    strings.Repeat("text ", 100) + "/run-agent fix",
			expected: "fix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractInstructionFromComment(tt.input)
			assert.Equal(t, tt.expected, got,
				"ExtractInstructionFromComment(%q) = %q, want %q",
				tt.input, got, tt.expected)
		})
	}
}
