package services_test

import (
	"os"
	"strings"
	"testing"

	"agentic-automation/internal/services"

	"github.com/stretchr/testify/assert"
)

func TestNewCodexApprovalDetector(t *testing.T) {
	t.Run("with nil logger", func(t *testing.T) {
		// Setup: ensure a logger exists
		testLogger := setupTestLogger(t)
		defer teardownTestLogger()

		// Create service with nil logger
		svc := services.NewCodexApprovalDetector(nil)

		// Verify service is not nil
		assert.NotNil(t, svc)

		// The service should use the global logger from config when nil is passed
		_ = testLogger
	})

	t.Run("with provided logger", func(t *testing.T) {
		// Setup
		testLogger := setupTestLogger(t)
		defer teardownTestLogger()

		// Create service with provided logger
		svc := services.NewCodexApprovalDetector(testLogger)

		// Verify service is not nil
		assert.NotNil(t, svc)
	})

	t.Run("with custom CODEX_BOT_USERNAME env var", func(t *testing.T) {
		// Setup
		setupTestLogger(t)
		defer teardownTestLogger()

		// Save existing environment variable value
		oldUsername := os.Getenv("CODEX_BOT_USERNAME")
		oldUserID := os.Getenv("CODEX_BOT_USER_ID")
		defer func() {
			// Restore original values
			if oldUsername != "" {
				os.Setenv("CODEX_BOT_USERNAME", oldUsername)
			} else {
				os.Unsetenv("CODEX_BOT_USERNAME")
			}
			if oldUserID != "" {
				os.Setenv("CODEX_BOT_USER_ID", oldUserID)
			} else {
				os.Unsetenv("CODEX_BOT_USER_ID")
			}
		}()

		// Set custom environment variable
		os.Setenv("CODEX_BOT_USERNAME", "custom-bot")
		os.Unsetenv("CODEX_BOT_USER_ID") // Use default

		// Create service with nil logger (will use global logger)
		svc := services.NewCodexApprovalDetector(nil)

		// Verify service is not nil
		assert.NotNil(t, svc)
	})

	t.Run("with custom CODEX_BOT_USER_ID env var", func(t *testing.T) {
		// Setup
		setupTestLogger(t)
		defer teardownTestLogger()

		// Save existing environment variable value
		oldUsername := os.Getenv("CODEX_BOT_USERNAME")
		oldUserID := os.Getenv("CODEX_BOT_USER_ID")
		defer func() {
			// Restore original values
			if oldUsername != "" {
				os.Setenv("CODEX_BOT_USERNAME", oldUsername)
			} else {
				os.Unsetenv("CODEX_BOT_USERNAME")
			}
			if oldUserID != "" {
				os.Setenv("CODEX_BOT_USER_ID", oldUserID)
			} else {
				os.Unsetenv("CODEX_BOT_USER_ID")
			}
		}()

		// Set custom environment variable
		os.Setenv("CODEX_BOT_USER_ID", "999999999")
		os.Unsetenv("CODEX_BOT_USERNAME") // Use default

		// Create service with nil logger (will use global logger)
		svc := services.NewCodexApprovalDetector(nil)

		// Verify service is not nil
		assert.NotNil(t, svc)
	})

	t.Run("with default CODEX_BOT_USERNAME", func(t *testing.T) {
		// Setup
		setupTestLogger(t)
		defer teardownTestLogger()

		// Save existing environment variable value
		oldUsername := os.Getenv("CODEX_BOT_USERNAME")
		oldUserID := os.Getenv("CODEX_BOT_USER_ID")
		defer func() {
			// Restore original values
			if oldUsername != "" {
				os.Setenv("CODEX_BOT_USERNAME", oldUsername)
			} else {
				os.Unsetenv("CODEX_BOT_USERNAME")
			}
			if oldUserID != "" {
				os.Setenv("CODEX_BOT_USER_ID", oldUserID)
			} else {
				os.Unsetenv("CODEX_BOT_USER_ID")
			}
		}()

		// Clear environment variables
		os.Unsetenv("CODEX_BOT_USERNAME")
		os.Unsetenv("CODEX_BOT_USER_ID")

		// Create service with nil logger (will use global logger)
		svc := services.NewCodexApprovalDetector(nil)

		// Verify service is not nil
		assert.NotNil(t, svc)
	})
}

func TestCodexApprovalDetector_DetectApproval(t *testing.T) {
	// Setup
	testLogger := setupTestLogger(t)
	defer teardownTestLogger()

	detector := services.NewCodexApprovalDetector(testLogger)

	tests := []struct {
		name             string
		reviewBody       string
		reviewerUsername string
		reviewerUserID   int64
		expected         bool
	}{
		// Reviewer Username 検証（デフォルト: chatgpt-codex-connector[bot]）
		{
			name:             "correct username with [bot] suffix",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "correct username without [bot] suffix",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "correct username uppercase",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "CHATGPT-CODEX-CONNECTOR[BOT]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "correct username mixed case",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "ChatGPT-Codex-Connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "wrong username",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "other-user",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "empty username",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "",
			reviewerUserID:   0,
			expected:         false,
		},
		// Reviewer User ID 検証
		{
			name:             "correct user ID",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "other-user",
			reviewerUserID:   199175422,
			expected:         true,
		},
		{
			name:             "wrong user ID",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "other-user",
			reviewerUserID:   123456789,
			expected:         false,
		},
		{
			name:             "zero user ID (username match required)",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		// Empty Review Body 検証
		{
			name:             "empty review body",
			reviewBody:       "",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "whitespace only review body",
			reviewBody:       "   ",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		// Review Body Content 検証
		{
			name:             "review body matches exact message",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "review body starts with exact message and additional text",
			reviewBody:       "Codex Review: Didn't find any major issues. Additional context provided.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "review body with trailing whitespace",
			reviewBody:       "Codex Review: Didn't find any major issues.  ",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "review body with emoji after message",
			reviewBody:       "Codex Review: Didn't find any major issues. 🎉",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "review body does not start with message",
			reviewBody:       "Some text Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body lowercase message",
			reviewBody:       "codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body uppercase message",
			reviewBody:       "CODEX REVIEW: DIDN'T FIND ANY MAJOR ISSUES.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body mixed case phrase only",
			reviewBody:       "didn't find any major issues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body with leading whitespace",
			reviewBody:       "  Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body with newline after colon",
			reviewBody:       "Codex Review:\nDidn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body long but starts with message",
			reviewBody:       "Codex Review: Didn't find any major issues." + strings.Repeat("x", 120),
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "review body long but does not start with message",
			reviewBody:       strings.Repeat("x", 100) + "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body wrong message",
			reviewBody:       "Codex Review: Found some issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "review body similar but missing major",
			reviewBody:       "Codex Review: Didn't find any issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false, // "major" がない
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detector.DetectApproval(tt.reviewBody, tt.reviewerUsername, tt.reviewerUserID)
			assert.Equal(t, tt.expected, got,
				"DetectApproval(%q, %q, %d) = %v, want %v",
				tt.reviewBody, tt.reviewerUsername, tt.reviewerUserID, got, tt.expected)
		})
	}
}

func TestCodexApprovalDetector_DetectApproval_WithCustomUsername(t *testing.T) {
	// Setup
	testLogger := setupTestLogger(t)
	defer teardownTestLogger()

	// Save existing environment variable value
	oldUsername := os.Getenv("CODEX_BOT_USERNAME")
	oldUserID := os.Getenv("CODEX_BOT_USER_ID")
	defer func() {
		// Restore original values
		if oldUsername != "" {
			os.Setenv("CODEX_BOT_USERNAME", oldUsername)
		} else {
			os.Unsetenv("CODEX_BOT_USERNAME")
		}
		if oldUserID != "" {
			os.Setenv("CODEX_BOT_USER_ID", oldUserID)
		} else {
			os.Unsetenv("CODEX_BOT_USER_ID")
		}
	}()

	// Set custom environment variable
	os.Setenv("CODEX_BOT_USERNAME", "custom-bot-name")
	os.Unsetenv("CODEX_BOT_USER_ID") // Use default

	// Create detector with custom username
	detector := services.NewCodexApprovalDetector(testLogger)

	t.Run("custom username matches", func(t *testing.T) {
		got := detector.DetectApproval("Codex Review: Didn't find any major issues.", "custom-bot-name", 0)
		assert.True(t, got, "Should detect approval with custom username")
	})

	t.Run("default username does not match", func(t *testing.T) {
		got := detector.DetectApproval("Codex Review: Didn't find any major issues.", "chatgpt-codex-connector[bot]", 0)
		assert.False(t, got, "Should not detect approval with default username when custom username is set")
	})
}

func TestCodexApprovalDetector_DetectApproval_WithCustomUserID(t *testing.T) {
	// Setup
	testLogger := setupTestLogger(t)
	defer teardownTestLogger()

	// Save existing environment variable value
	oldUsername := os.Getenv("CODEX_BOT_USERNAME")
	oldUserID := os.Getenv("CODEX_BOT_USER_ID")
	defer func() {
		// Restore original values
		if oldUsername != "" {
			os.Setenv("CODEX_BOT_USERNAME", oldUsername)
		} else {
			os.Unsetenv("CODEX_BOT_USERNAME")
		}
		if oldUserID != "" {
			os.Setenv("CODEX_BOT_USER_ID", oldUserID)
		} else {
			os.Unsetenv("CODEX_BOT_USER_ID")
		}
	}()

	// Set custom environment variable
	os.Setenv("CODEX_BOT_USER_ID", "999999999")
	os.Unsetenv("CODEX_BOT_USERNAME") // Use default

	// Create detector with custom user ID
	detector := services.NewCodexApprovalDetector(testLogger)

	t.Run("custom user ID matches", func(t *testing.T) {
		got := detector.DetectApproval("Codex Review: Didn't find any major issues.", "other-user", 999999999)
		assert.True(t, got, "Should detect approval with custom user ID")
	})

	t.Run("default user ID does not match", func(t *testing.T) {
		got := detector.DetectApproval("Codex Review: Didn't find any major issues.", "other-user", 199175422)
		assert.False(t, got, "Should not detect approval with default user ID when custom user ID is set")
	})
}
