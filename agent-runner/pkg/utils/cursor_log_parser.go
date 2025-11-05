package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LogEntry represents a cursor agent log entry.
type LogEntry interface {
	GetType() string
	GetSessionID() string
	Format() string
}

// SystemEntry represents a system initialization log entry.
type SystemEntry struct {
	Type           string `json:"type"`
	Subtype        string `json:"subtype"`
	APIKeySource   string `json:"apiKeySource,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	SessionID      string `json:"session_id"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
}

func (e *SystemEntry) GetType() string      { return e.Type }
func (e *SystemEntry) GetSessionID() string { return e.SessionID }
func (e *SystemEntry) Format() string {
	return fmt.Sprintf("[SYSTEM] %s: model=%s, cwd=%s", e.Subtype, e.Model, e.CWD)
}

// UserEntry represents a user message log entry.
type UserEntry struct {
	Type      string      `json:"type"`
	Message   UserMessage `json:"message"`
	SessionID string      `json:"session_id"`
}

func (e *UserEntry) GetType() string      { return e.Type }
func (e *UserEntry) GetSessionID() string { return e.SessionID }
func (e *UserEntry) Format() string {
	var texts []string
	for _, content := range e.Message.Content {
		if content.Type == "text" && content.Text != "" {
			texts = append(texts, content.Text)
		}
	}
	if len(texts) > 0 {
		return fmt.Sprintf("[USER] %s", strings.Join(texts, " "))
	}
	return "[USER] (empty message)"
}

// UserMessage represents the message content in a UserEntry.
type UserMessage struct {
	Role    string           `json:"role"`
	Content []MessageContent `json:"content"`
}

// MessageContent represents a content item in a message.
type MessageContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ThinkingEntry represents a thinking process log entry.
type ThinkingEntry struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Text        string `json:"text,omitempty"`
	SessionID   string `json:"session_id"`
	TimestampMS int64  `json:"timestamp_ms,omitempty"`
}

func (e *ThinkingEntry) GetType() string      { return e.Type }
func (e *ThinkingEntry) GetSessionID() string { return e.SessionID }
func (e *ThinkingEntry) Format() string {
	if e.Subtype == "completed" {
		return "[THINKING] completed"
	}
	return "[THINKING] processing..."
}

// AssistantEntry represents an assistant response log entry.
type AssistantEntry struct {
	Type        string           `json:"type"`
	Message     AssistantMessage `json:"message"`
	SessionID   string           `json:"session_id"`
	ModelCallID string           `json:"model_call_id,omitempty"`
	TimestampMS int64            `json:"timestamp_ms,omitempty"`
}

func (e *AssistantEntry) GetType() string      { return e.Type }
func (e *AssistantEntry) GetSessionID() string { return e.SessionID }
func (e *AssistantEntry) Format() string {
	var texts []string
	for _, content := range e.Message.Content {
		if content.Type == "text" && content.Text != "" {
			// Truncate long messages
			text := content.Text
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			texts = append(texts, text)
		}
	}
	if len(texts) > 0 {
		return fmt.Sprintf("[ASSISTANT] %s", strings.Join(texts, " "))
	}
	return "[ASSISTANT] (empty response)"
}

// AssistantMessage represents the message content in an AssistantEntry.
type AssistantMessage struct {
	Role    string           `json:"role"`
	Content []MessageContent `json:"content"`
}

// ToolCallEntry represents a tool call log entry.
type ToolCallEntry struct {
	Type        string                 `json:"type"`
	Subtype     string                 `json:"subtype"`
	CallID      string                 `json:"call_id"`
	ToolCall    map[string]interface{} `json:"tool_call"`
	ModelCallID string                 `json:"model_call_id,omitempty"`
	SessionID   string                 `json:"session_id"`
	TimestampMS int64                  `json:"timestamp_ms,omitempty"`
}

func (e *ToolCallEntry) GetType() string      { return e.Type }
func (e *ToolCallEntry) GetSessionID() string { return e.SessionID }
func (e *ToolCallEntry) Format() string {
	toolName := e.GetToolName()
	if toolName != "" {
		return fmt.Sprintf("[TOOL] %s %s (id: %s)", toolName, e.Subtype, e.CallID)
	}
	return fmt.Sprintf("[TOOL] %s (id: %s)", e.Subtype, e.CallID)
}

// GetToolName returns the name of the tool being called.
func (e *ToolCallEntry) GetToolName() string {
	if e.ToolCall == nil {
		return ""
	}
	// tool_call is a map with a single key representing the tool name
	for key := range e.ToolCall {
		return key
	}
	return ""
}

// ResultEntry represents a result log entry.
type ResultEntry struct {
	Type          string `json:"type"`
	Subtype       string `json:"subtype"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	DurationAPIMS int64  `json:"duration_api_ms,omitempty"`
	IsError       bool   `json:"is_error,omitempty"`
	Result        string `json:"result,omitempty"`
	SessionID     string `json:"session_id"`
	RequestID     string `json:"request_id,omitempty"`
}

func (e *ResultEntry) GetType() string      { return e.Type }
func (e *ResultEntry) GetSessionID() string { return e.SessionID }
func (e *ResultEntry) Format() string {
	status := "success"
	if e.IsError {
		status = "error"
	}
	duration := ""
	if e.DurationMS > 0 {
		duration = fmt.Sprintf(" (duration: %dms)", e.DurationMS)
	}
	result := ""
	if e.Result != "" {
		truncated := e.Result
		if len(truncated) > 100 {
			truncated = truncated[:100] + "..."
		}
		result = fmt.Sprintf(": %s", truncated)
	}
	return fmt.Sprintf("[RESULT] %s%s%s", status, duration, result)
}

// ParseLogEntry parses a single line of JSON log entry.
// Returns a LogEntry interface and an error if parsing fails.
func ParseLogEntry(line []byte) (LogEntry, error) {
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
		var entry SystemEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse system entry: %w", err)
		}
		return &entry, nil

	case "user":
		var entry UserEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse user entry: %w", err)
		}
		return &entry, nil

	case "thinking":
		var entry ThinkingEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse thinking entry: %w", err)
		}
		return &entry, nil

	case "assistant":
		var entry AssistantEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse assistant entry: %w", err)
		}
		return &entry, nil

	case "tool_call":
		var entry ToolCallEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse tool_call entry: %w", err)
		}
		return &entry, nil

	case "result":
		var entry ResultEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse result entry: %w", err)
		}
		return &entry, nil

	default:
		return nil, fmt.Errorf("unknown entry type: %s", typeStr)
	}
}

// ParseAndFormatOutput parses the stream-json output and formats it for human-readable logging.
// It processes each line and writes formatted output to stderr.
func ParseAndFormatOutput(output string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := ParseLogEntry([]byte(line))
		if err != nil {
			// If parsing fails, output the raw line with a warning
			fmt.Fprintf(os.Stderr, "[PARSE ERROR] %v: %s\n", err, line)
			continue
		}

		// Format and output the parsed entry
		fmt.Fprintf(os.Stderr, "%s\n", entry.Format())
	}
}
