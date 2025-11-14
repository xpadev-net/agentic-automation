// Package services provides business logic services for the GitHub Agent Automation system.
package services

import (
	"agentic-automation/internal/config"
	"strconv"
	"strings"
)

// codexApprovalExactMatch is the exact approval message from Codex bot.
const codexApprovalExactMatch = "Codex Review: Didn't find any major issues."

// CodexApprovalDetector detects Codex bot approval patterns in review comments.
type CodexApprovalDetector struct {
	logger           *config.AppLogger
	codexBotUsername string
	codexBotUserID   int64
}

// NewCodexApprovalDetector creates a new CodexApprovalDetector instance.
// If logger is nil, it uses config.GetLogger().
// The Codex bot username is read from CODEX_BOT_USERNAME environment variable (default: "chatgpt-codex-connector[bot]").
// The Codex bot user ID is read from CODEX_BOT_USER_ID environment variable (default: 199175422).
func NewCodexApprovalDetector(logger *config.AppLogger) *CodexApprovalDetector {
	if logger == nil {
		logger = config.GetLogger()
	}

	// Parse user ID from environment variable
	userIDStr := config.GetEnv("CODEX_BOT_USER_ID", "199175422")
	userID := int64(199175422) // default value
	if parsedID, err := strconv.ParseInt(userIDStr, 10, 64); err == nil {
		userID = parsedID
	}

	return &CodexApprovalDetector{
		logger:           logger,
		codexBotUsername: config.GetEnv("CODEX_BOT_USERNAME", "chatgpt-codex-connector[bot]"),
		codexBotUserID:   userID,
	}
}

// DetectApproval checks if the review comment contains a Codex approval pattern.
// It validates that the reviewer is the Codex bot and checks for approval patterns:
// - Regex pattern: "didn't find.*major issues" (case-insensitive)
// - Exact match: "Codex Review: Didn't find any major issues." (case-insensitive)
//
// The reviewer is identified by either username or user ID:
// - Username matching is case-insensitive and handles [bot] suffix variations
// - User ID matching is exact
//
// Returns true if approval is detected, false otherwise.
// Logs the detection result at Info level with approval_detected, reviewer_username,
// reviewer_user_id, and review_body_preview fields. The review_body_preview is limited to the first 100 characters
// for security purposes.
func (s *CodexApprovalDetector) DetectApproval(reviewBody string, reviewerUsername string, reviewerUserID int64) bool {
	// 1. Validate reviewer (by username or user ID)
	if !s.isCodexBot(reviewerUsername, reviewerUserID) {
		return false
	}

	// 2. Check for empty review body
	if reviewBody == "" {
		return false
	}

	// 3. Detect approval pattern (case sensitive, prefix match)
	detected := strings.HasPrefix(reviewBody, codexApprovalExactMatch)

	// 4. Log the detection result
	// Create preview of review body (first 100 characters for security)
	preview := reviewBody
	if len(preview) > 100 {
		preview = preview[:100]
	}

	s.logger.Info("Codex approval detection result",
		config.Bool("approval_detected", detected),
		config.String("reviewer_username", reviewerUsername),
		config.Int64("reviewer_user_id", reviewerUserID),
		config.String("review_body_preview", preview),
		config.String("service", "codex_approval"),
	)

	// 5. Return detection result
	return detected
}

// IsCodexBot checks if the given username and user ID match the Codex bot.
// This is a public wrapper around the private isCodexBot method.
// 注意: 通常はDetectApproval()を使用すれば内部でbot判定が行われるため、
// このメソッドは情報取得目的など、特別な理由がある場合にのみ使用すること
func (s *CodexApprovalDetector) IsCodexBot(username string, userID int64) bool {
	return s.isCodexBot(username, userID)
}

// isCodexBot checks if the given username and user ID match the Codex bot.
// It matches by username (case-insensitive, with [bot] suffix handling) or by user ID.
func (s *CodexApprovalDetector) isCodexBot(username string, userID int64) bool {
	// Check by user ID first (exact match)
	if userID != 0 && userID == s.codexBotUserID {
		return true
	}

	// Check by username (case-insensitive)
	// Handle [bot] suffix: match both "chatgpt-codex-connector[bot]" and "chatgpt-codex-connector"
	usernameNormalized := strings.ToLower(strings.TrimSpace(username))
	expectedUsernameNormalized := strings.ToLower(strings.TrimSpace(s.codexBotUsername))

	// Direct match
	if usernameNormalized == expectedUsernameNormalized {
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
