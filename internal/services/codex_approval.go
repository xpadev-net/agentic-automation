// Package services provides business logic services for the GitHub Agent Automation system.
package services

import (
	"agentic-automation/internal/config"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// codexApprovalPattern is a compiled regex pattern to detect Codex approval messages.
// Pattern matches "didn't find.*major issues" (case-insensitive).
var codexApprovalPattern = regexp.MustCompile(`(?i)didn't find.*major issues`)

// codexApprovalExactMatch is the exact approval message from Codex bot.
const codexApprovalExactMatch = "Codex Review: Didn't find any major issues."

// CodexApprovalDetector detects Codex bot approval patterns in review comments.
type CodexApprovalDetector struct {
	logger           *zap.Logger
	codexBotUsername string
}

// NewCodexApprovalDetector creates a new CodexApprovalDetector instance.
// If logger is nil, it uses config.GetLogger().
// The Codex bot username is read from CODEX_BOT_USERNAME environment variable (default: "codex-bot").
func NewCodexApprovalDetector(logger *zap.Logger) *CodexApprovalDetector {
	if logger == nil {
		logger = config.GetLogger()
	}

	return &CodexApprovalDetector{
		logger:           logger,
		codexBotUsername: config.GetEnv("CODEX_BOT_USERNAME", "codex-bot"),
	}
}

// DetectApproval checks if the review comment contains a Codex approval pattern.
// It validates that the reviewer is the Codex bot and checks for approval patterns:
// - Regex pattern: "didn't find.*major issues" (case-insensitive)
// - Exact match: "Codex Review: Didn't find any major issues." (case-insensitive)
//
// Returns true if approval is detected, false otherwise.
// Logs the detection result at Info level with approval_detected, reviewer_username,
// and review_body_preview fields. The review_body_preview is limited to the first 100 characters
// for security purposes.
func (s *CodexApprovalDetector) DetectApproval(reviewBody string, reviewerUsername string) bool {
	// 1. Validate reviewer username (case-insensitive)
	if !strings.EqualFold(reviewerUsername, s.codexBotUsername) {
		return false
	}

	// 2. Check for empty review body
	if reviewBody == "" {
		return false
	}

	// 3. Detect approval patterns
	// Pattern 1: Regex match
	regexMatch := codexApprovalPattern.MatchString(reviewBody)
	// Pattern 2: Exact match (case-insensitive)
	exactMatch := strings.EqualFold(strings.TrimSpace(reviewBody), codexApprovalExactMatch)

	detected := regexMatch || exactMatch

	// 4. Log the detection result
	// Create preview of review body (first 100 characters for security)
	preview := reviewBody
	if len(preview) > 100 {
		preview = preview[:100]
	}

	s.logger.Info("Codex approval detection result",
		zap.Bool("approval_detected", detected),
		zap.String("reviewer_username", reviewerUsername),
		zap.String("review_body_preview", preview),
		zap.String("service", "codex_approval"),
	)

	// 5. Return detection result
	return detected
}
