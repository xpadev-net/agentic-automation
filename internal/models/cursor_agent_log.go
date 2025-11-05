package models

// LogEntry represents a cursor agent log entry.
// It can be one of: SystemEntry, UserEntry, ThinkingEntry, AssistantEntry, ToolCallEntry, or ResultEntry.
type LogEntry interface {
	GetType() string
	GetSessionID() string
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

// UserEntry represents a user message log entry.
type UserEntry struct {
	Type      string      `json:"type"`
	Message   UserMessage `json:"message"`
	SessionID string      `json:"session_id"`
}

func (e *UserEntry) GetType() string      { return e.Type }
func (e *UserEntry) GetSessionID() string { return e.SessionID }

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

// GetToolName returns the name of the tool being called.
// It returns the first key found in the tool_call map, or empty string if none.
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
