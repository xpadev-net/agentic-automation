// Package services provides business logic services for the GitHub Agent Automation system.
package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"go.uber.org/zap"
)

const (
	// AgentTypeClaudeCode is the agent type for Claude Code
	AgentTypeClaudeCode = "claude-code"
	// AgentTypeCursorAgents is the agent type for Cursor Agents
	AgentTypeCursorAgents = "cursor-agents"

	// LabelAgentClaudeCode is the label that indicates Claude Code agent should be used
	LabelAgentClaudeCode = "agent:claude-code"
	// LabelAgentCursorAgents is the label that indicates Cursor Agents should be used
	LabelAgentCursorAgents = "agent:cursor-agents"

	// EnvKeyAIAgentDefaultType is the environment variable key for default agent type
	EnvKeyAIAgentDefaultType = "AI_AGENT_DEFAULT_TYPE"
)

// AgentTypeDetectorService detects agent type from Issue labels.
type AgentTypeDetectorService struct {
	logger *zap.Logger
}

// NewAgentTypeDetectorService creates a new AgentTypeDetectorService instance.
// If logger is nil, it uses config.GetLogger().
func NewAgentTypeDetectorService(logger *zap.Logger) *AgentTypeDetectorService {
	if logger == nil {
		logger = config.GetLogger()
	}

	return &AgentTypeDetectorService{
		logger: logger,
	}
}

// parseLabels parses JSON string into []string.
// Returns empty slice and nil error if labelsJSON is empty.
// Returns error if JSON is invalid or not an array.
func parseLabels(labelsJSON string) ([]string, error) {
	if labelsJSON == "" {
		return []string{}, nil
	}

	// First check if it's null
	var rawValue interface{}
	if err := json.Unmarshal([]byte(labelsJSON), &rawValue); err != nil {
		return []string{}, err
	}

	// Check if it's null
	if rawValue == nil {
		return []string{}, nil
	}

	// Check if it's an array
	rawArray, ok := rawValue.([]interface{})
	if !ok {
		return []string{}, fmt.Errorf("labels must be an array, got %T", rawValue)
	}

	// Convert []interface{} to []string
	labels := make([]string, 0, len(rawArray))
	for i, item := range rawArray {
		str, ok := item.(string)
		if !ok {
			return []string{}, fmt.Errorf("labels[%d] must be a string, got %T", i, item)
		}
		labels = append(labels, str)
	}

	return labels, nil
}

// isValidAgentType checks if the agent type is valid.
func isValidAgentType(agentType string) bool {
	return agentType == AgentTypeClaudeCode || agentType == AgentTypeCursorAgents
}

// findAgentLabel searches for agent label in labels array.
// Returns agent type and true if found, empty string and false otherwise.
func findAgentLabel(labels []string) (string, bool) {
	for _, label := range labels {
		labelLower := strings.ToLower(label)
		switch labelLower {
		case strings.ToLower(LabelAgentClaudeCode):
			return AgentTypeClaudeCode, true
		case strings.ToLower(LabelAgentCursorAgents):
			return AgentTypeCursorAgents, true
		}
	}
	return "", false
}

// DetectAgentType detects agent type from Issue labels, environment variable, or default.
// Priority: 1) Issue Label, 2) Environment Variable, 3) Default (claude-code).
// Returns "claude-code" or "cursor-agents".
func (s *AgentTypeDetectorService) DetectAgentType(issue *models.Issue) string {
	// Priority 1: Check Issue labels
	if issue.Labels != "" {
		labels, err := parseLabels(issue.Labels)
		if err != nil {
			// Log parse error but continue to fallback
			labelsPreview := issue.Labels
			if len(labelsPreview) > 100 {
				labelsPreview = labelsPreview[:100]
			}
			s.logger.Warn("Failed to parse issue labels JSON, falling back to environment variable",
				zap.Error(err),
				zap.String("labels_json", labelsPreview),
				zap.Int("issue_id", issue.ID),
				zap.String("repo", issue.Repo),
				zap.Int("issue_number", issue.Number),
				zap.String("service", "agent_type_detector"),
			)
		} else {
			// Search for agent label
			agentType, found := findAgentLabel(labels)
			if found {
				// Find the matched label for logging
				var matchedLabel string
				for _, label := range labels {
					labelLower := strings.ToLower(label)
					if labelLower == strings.ToLower(LabelAgentClaudeCode) || labelLower == strings.ToLower(LabelAgentCursorAgents) {
						matchedLabel = label
						break
					}
				}
				s.logger.Info("Agent type detected from issue label",
					zap.String("detection_source", "label"),
					zap.String("detected_agent_type", agentType),
					zap.String("matched_label", matchedLabel),
					zap.Int("issue_id", issue.ID),
					zap.String("repo", issue.Repo),
					zap.Int("issue_number", issue.Number),
					zap.String("service", "agent_type_detector"),
				)
				return agentType
			}
		}
	}

	// Priority 2: Check environment variable
	envAgentType := config.GetEnv(EnvKeyAIAgentDefaultType, AgentTypeClaudeCode)
	if isValidAgentType(envAgentType) {
		s.logger.Info("Agent type detected from environment variable",
			zap.String("detection_source", "env_var"),
			zap.String("detected_agent_type", envAgentType),
			zap.Int("issue_id", issue.ID),
			zap.String("repo", issue.Repo),
			zap.Int("issue_number", issue.Number),
			zap.String("service", "agent_type_detector"),
		)
		return envAgentType
	}

	// Log warning if environment variable has invalid value
	if envAgentType != AgentTypeClaudeCode {
		s.logger.Warn("Invalid agent type in environment variable, using default",
			zap.String("invalid_env_value", envAgentType),
			zap.String("env_key", EnvKeyAIAgentDefaultType),
			zap.Int("issue_id", issue.ID),
			zap.String("repo", issue.Repo),
			zap.Int("issue_number", issue.Number),
			zap.String("service", "agent_type_detector"),
		)
	}

	// Priority 3: Default
	defaultAgentType := AgentTypeClaudeCode
	s.logger.Info("Agent type using default value",
		zap.String("detection_source", "default"),
		zap.String("detected_agent_type", defaultAgentType),
		zap.Int("issue_id", issue.ID),
		zap.String("repo", issue.Repo),
		zap.Int("issue_number", issue.Number),
		zap.String("service", "agent_type_detector"),
	)
	return defaultAgentType
}
