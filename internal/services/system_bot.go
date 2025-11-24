// Package services provides business logic services for the GitHub Agent Automation system.
package services

import (
	"agentic-automation/internal/config"
	"strconv"
	"strings"
)

// SystemBotDetector detects if a user is the system bot.
type SystemBotDetector struct {
	logger            *config.AppLogger
	systemBotUsername string
	systemBotUserID   int64
}

// NewSystemBotDetector creates a new SystemBotDetector instance.
// If logger is nil, it uses config.GetLogger().
// The system bot username is read from SYSTEM_BOT_USERNAME environment variable (default: "").
// The system bot user ID is read from SYSTEM_BOT_USER_ID environment variable (default: 0).
func NewSystemBotDetector(logger *config.AppLogger) *SystemBotDetector {
	if logger == nil {
		logger = config.GetLogger()
	}

	// Parse user ID from environment variable
	userIDStr := config.GetEnv("SYSTEM_BOT_USER_ID", "0")
	userID := int64(0) // default value
	if parsedID, err := strconv.ParseInt(userIDStr, 10, 64); err == nil {
		userID = parsedID
	}

	return &SystemBotDetector{
		logger:            logger,
		systemBotUsername: config.GetEnv("SYSTEM_BOT_USERNAME", ""),
		systemBotUserID:   userID,
	}
}

// IsSystemBot checks if the given username and user ID match the system bot.
// It matches by username (case-insensitive, with [bot] suffix handling) or by user ID.
// This follows the same pattern as CodexApprovalDetector.isCodexBot.
func (s *SystemBotDetector) IsSystemBot(username string, userID int64) bool {
	// If both username and userID are not configured, return false
	if s.systemBotUsername == "" && s.systemBotUserID == 0 {
		return false
	}

	// Check by user ID first (exact match)
	if userID != 0 && userID == s.systemBotUserID {
		return true
	}

	// Check by username (case-insensitive)
	// Handle [bot] suffix: match both "system-bot[bot]" and "system-bot"
	usernameNormalized := strings.ToLower(strings.TrimSpace(username))
	expectedUsernameNormalized := strings.ToLower(strings.TrimSpace(s.systemBotUsername))

	// Direct match
	if usernameNormalized == expectedUsernameNormalized && expectedUsernameNormalized != "" {
		return true
	}

	// Match without [bot] suffix
	// Remove [bot] suffix from both if present
	removeBotSuffix := func(s string) string {
		s = strings.TrimSuffix(s, "[bot]")
		return strings.TrimSpace(s)
	}

	usernameWithoutSuffix := removeBotSuffix(usernameNormalized)
	expectedUsernameWithoutSuffix := removeBotSuffix(expectedUsernameNormalized)

	if usernameWithoutSuffix == expectedUsernameWithoutSuffix && usernameWithoutSuffix != "" {
		return true
	}

	return false
}
