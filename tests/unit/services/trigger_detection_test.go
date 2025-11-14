package services_test

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/services"
	"testing"

	"github.com/stretchr/testify/assert"
)

// setupTestLogger creates a test logger and sets it up for testing
func setupTestLogger(t *testing.T) *config.AppLogger {
	testLogger := config.NewNopLogger()
	config.SetLoggerForTesting(testLogger)
	return testLogger
}

// teardownTestLogger resets the logger after testing
func teardownTestLogger() {
	config.ResetLoggerForTesting()
}

func TestNewTriggerDetectionService(t *testing.T) {
	t.Run("with nil logger", func(t *testing.T) {
		// Setup: ensure a logger exists
		testLogger := setupTestLogger(t)
		defer teardownTestLogger()

		// Create service with nil logger
		svc := services.NewTriggerDetectionService(nil)

		// Verify service is not nil
		assert.NotNil(t, svc)

		// The service should use the global logger from config when nil is passed
		// We can't directly test this without exposing internal state,
		// but we can verify the service works correctly
		_ = testLogger
	})

	t.Run("with provided logger", func(t *testing.T) {
		// Setup
		testLogger := setupTestLogger(t)
		defer teardownTestLogger()

		// Create service with provided logger
		svc := services.NewTriggerDetectionService(testLogger)

		// Verify service is not nil
		assert.NotNil(t, svc)
	})
}

func TestDetectRunAgentTrigger(t *testing.T) {
	// Setup
	testLogger := setupTestLogger(t)
	defer teardownTestLogger()

	svc := services.NewTriggerDetectionService(testLogger)

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Basic cases - matching comment_parser_test.go
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
			expected: true, // partial match - contains the trigger
		},
		{
			name:     "SimilarButDifferent2",
			input:    "pre/run-agent",
			expected: true, // partial match - contains the trigger
		},
		{
			name:     "WithMarkdown",
			input:    "`/run-agent` in code",
			expected: true,
		},
		// Additional edge/locale/length cases
		{
			name:     "JapaneseContext",
			input:    "このIssueを処理してください。/run-agent を実行",
			expected: true,
		},
		{
			name:     "MixedFullHalfSpaces",
			input:    "\u3000/run-agent\t 実行",
			expected: true,
		},
		{
			name:     "HTMLEscapedLike",
			input:    "&sol;run-agent should not be detected",
			expected: false,
		},
		{
			name: "VeryLongInputWithTrigger",
			input: func() string {
				s := "/run-agent"
				for i := 0; i < 10000; i++ {
					s += "x"
				}
				return s
			}(),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.DetectRunAgentTrigger(tt.input)
			assert.Equal(t, tt.expected, got,
				"DetectRunAgentTrigger(%q) = %v, want %v",
				tt.input, got, tt.expected)
		})
	}
}
