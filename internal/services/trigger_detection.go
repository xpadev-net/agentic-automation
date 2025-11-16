// Package services provides business logic services for the GitHub Agent Automation system.
package services

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/utils"
)

// TriggerDetectionService detects trigger strings in GitHub issue comments.
type TriggerDetectionService struct {
	logger *config.AppLogger
}

// NewTriggerDetectionService creates a new TriggerDetectionService instance.
// If logger is nil, it uses config.GetLogger().
func NewTriggerDetectionService(logger *config.AppLogger) *TriggerDetectionService {
	if logger == nil {
		logger = config.GetLogger()
	}

	return &TriggerDetectionService{
		logger: logger,
	}
}

// DetectRunAgentTrigger checks if the comment body contains the "/run-agent" trigger string (case-insensitive).
// Returns true if detected, false otherwise.
// Logs the detection result at Info level with trigger_detected and comment_body_preview fields.
// The comment_body_preview is limited to the first 100 characters for security purposes.
func (s *TriggerDetectionService) DetectRunAgentTrigger(commentBody string) bool {
	// Use the existing utility function for detection
	detected := utils.ContainsRunAgentTrigger(commentBody)

	// Create preview of comment body (first 100 characters for security)
	preview := commentBody
	if len(preview) > 100 {
		preview = preview[:100]
	}

	// Log the detection result
	s.logger.Info("Trigger detection result",
		config.Bool("trigger_detected", detected),
		config.String("comment_body_preview", preview),
		config.String("service", "trigger_detection"),
	)

	return detected
}
