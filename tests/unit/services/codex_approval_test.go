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
		// Regex Pattern Matching
		{
			name:             "regex exact match",
			reviewBody:       "didn't find any major issues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "regex with capital D",
			reviewBody:       "Didn't find any major issues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "regex with text before",
			reviewBody:       "Some text didn't find any major issues here",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "regex with text after",
			reviewBody:       "didn't find any major issues in the code",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "regex all uppercase",
			reviewBody:       "DIDN'T FIND ANY MAJOR ISSUES",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "regex mixed case",
			reviewBody:       "DidN'T FiNd AnY MaJoR iSsUeS",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "regex not matching",
			reviewBody:       "found some issues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "regex partial match but wrong",
			reviewBody:       "didn't find any issues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false, // "major" がない
		},
		{
			name:             "regex with punctuation",
			reviewBody:       "I didn't find any major issues!",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		// Exact Match
		{
			name:             "exact match",
			reviewBody:       "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "exact match all uppercase",
			reviewBody:       "CODEX REVIEW: DIDN'T FIND ANY MAJOR ISSUES.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "exact match with leading whitespace",
			reviewBody:       "  Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "exact match with trailing whitespace",
			reviewBody:       "Codex Review: Didn't find any major issues.  ",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "exact match with both whitespaces",
			reviewBody:       "  Codex Review: Didn't find any major issues.  ",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "exact match not matching wrong message",
			reviewBody:       "Codex Review: Found some issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false,
		},
		{
			name:             "exact match not matching similar",
			reviewBody:       "Codex Review: Didn't find any issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false, // "major" がない
		},
		// Edge Cases
		{
			name:             "both regex and exact match",
			reviewBody:       "Codex Review: Didn't find any major issues. I didn't find any major issues either.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true, // 両方マッチ
		},
		{
			name:             "review body over 100 chars with regex",
			reviewBody:       strings.Repeat("x", 100) + "didn't find any major issues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "review body over 100 chars with exact",
			reviewBody:       strings.Repeat("x", 100) + "Codex Review: Didn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true,
		},
		{
			name:             "special characters in review body",
			reviewBody:       "Codex Review: Didn't find any major issues! 🎉",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true, // 特殊文字を含む
		},
		{
			name:             "newlines in review body with regex",
			reviewBody:       "didn't find\nany major\nissues",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         false, // 正規表現の .* は改行をマッチしないため
		},
		{
			name:             "newlines in review body with exact",
			reviewBody:       "Codex Review:\nDidn't find any major issues.",
			reviewerUsername: "chatgpt-codex-connector[bot]",
			reviewerUserID:   0,
			expected:         true, // 正規表現がマッチする（"didn't find" と "major issues" の間に改行がないため、.* がマッチ可能）
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
