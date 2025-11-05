package utils

import (
	"encoding/json"
	"fmt"

	"agentic-automation/internal/models"
)

// ParseLogEntry parses a single line of JSON log entry.
// Returns a LogEntry interface and an error if parsing fails.
func ParseLogEntry(line []byte) (models.LogEntry, error) {
	// First, parse into a generic map to determine the type
	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	typeStr, ok := raw["type"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'type' field")
	}

	// Parse based on type
	switch typeStr {
	case "system":
		var entry models.SystemEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse system entry: %w", err)
		}
		return &entry, nil

	case "user":
		var entry models.UserEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse user entry: %w", err)
		}
		return &entry, nil

	case "thinking":
		var entry models.ThinkingEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse thinking entry: %w", err)
		}
		return &entry, nil

	case "assistant":
		var entry models.AssistantEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse assistant entry: %w", err)
		}
		return &entry, nil

	case "tool_call":
		var entry models.ToolCallEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse tool_call entry: %w", err)
		}
		return &entry, nil

	case "result":
		var entry models.ResultEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse result entry: %w", err)
		}
		return &entry, nil

	default:
		return nil, fmt.Errorf("unknown entry type: %s", typeStr)
	}
}

// ParseLogFile parses all lines from a log file.
// Returns a slice of LogEntry and an error if any line fails to parse.
func ParseLogFile(lines [][]byte) ([]models.LogEntry, error) {
	entries := make([]models.LogEntry, 0, len(lines))
	var parseErrors []error

	for i, line := range lines {
		if len(line) == 0 {
			continue // Skip empty lines
		}

		entry, err := ParseLogEntry(line)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("line %d: %w", i+1, err))
			continue
		}

		entries = append(entries, entry)
	}

	if len(parseErrors) > 0 {
		return entries, fmt.Errorf("encountered %d parse errors: %v", len(parseErrors), parseErrors)
	}

	return entries, nil
}

// CountByType counts log entries by their type.
func CountByType(entries []models.LogEntry) map[string]int {
	counts := make(map[string]int)
	for _, entry := range entries {
		counts[entry.GetType()]++
	}
	return counts
}
