package utils

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"agentic-automation/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogEntry_System(t *testing.T) {
	json := `{"type":"system","subtype":"init","apiKeySource":"login","cwd":"/Users/xpadev/IdeaProjects/agentic-automation","session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","model":"Auto","permissionMode":"default"}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "system", entry.GetType())
	assert.Equal(t, "193b2dbb-03ce-444f-a307-4a80f5d4cab0", entry.GetSessionID())

	systemEntry, ok := entry.(*models.SystemEntry)
	require.True(t, ok)
	assert.Equal(t, "init", systemEntry.Subtype)
	assert.Equal(t, "login", systemEntry.APIKeySource)
	assert.Equal(t, "Auto", systemEntry.Model)
}

func TestParseLogEntry_User(t *testing.T) {
	json := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"specs/001-github-agent-automation/tasks.md と実装を確認し、取り組むべきタスクを教えて下さい"}]},"session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0"}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "user", entry.GetType())
	assert.Equal(t, "193b2dbb-03ce-444f-a307-4a80f5d4cab0", entry.GetSessionID())

	userEntry, ok := entry.(*models.UserEntry)
	require.True(t, ok)
	assert.Equal(t, "user", userEntry.Message.Role)
	assert.Len(t, userEntry.Message.Content, 1)
	assert.Equal(t, "text", userEntry.Message.Content[0].Type)
	assert.Contains(t, userEntry.Message.Content[0].Text, "tasks.md")
}

func TestParseLogEntry_Thinking(t *testing.T) {
	json := `{"type":"thinking","subtype":"delta","text":"","session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","timestamp_ms":1762319685226}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "thinking", entry.GetType())

	thinkingEntry, ok := entry.(*models.ThinkingEntry)
	require.True(t, ok)
	assert.Equal(t, "delta", thinkingEntry.Subtype)
	assert.Equal(t, int64(1762319685226), thinkingEntry.TimestampMS)
}

func TestParseLogEntry_ThinkingCompleted(t *testing.T) {
	json := `{"type":"thinking","subtype":"completed","session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","timestamp_ms":1762319686652}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "thinking", entry.GetType())

	thinkingEntry, ok := entry.(*models.ThinkingEntry)
	require.True(t, ok)
	assert.Equal(t, "completed", thinkingEntry.Subtype)
}

func TestParseLogEntry_Assistant(t *testing.T) {
	json := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"\nspecs/001-github-agent-automation/tasks.md と実装を確認し、取り組むべきタスクを整理します。\n"}]},"session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","model_call_id":"249b8740-a695-40d7-9f05-898e6a263ede","timestamp_ms":1762319687718}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "assistant", entry.GetType())

	assistantEntry, ok := entry.(*models.AssistantEntry)
	require.True(t, ok)
	assert.Equal(t, "assistant", assistantEntry.Message.Role)
	assert.Equal(t, "249b8740-a695-40d7-9f05-898e6a263ede", assistantEntry.ModelCallID)
}

func TestParseLogEntry_ToolCall_Started(t *testing.T) {
	json := `{"type":"tool_call","subtype":"started","call_id":"tool_d35d82bb-a1d8-4342-a07b-9e321edd96f","tool_call":{"readToolCall":{"args":{"path":"specs/001-github-agent-automation/tasks.md"}}},"model_call_id":"249b8740-a695-40d7-9f05-898e6a263ede","session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","timestamp_ms":1762319687718}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "tool_call", entry.GetType())

	toolCallEntry, ok := entry.(*models.ToolCallEntry)
	require.True(t, ok)
	assert.Equal(t, "started", toolCallEntry.Subtype)
	assert.Equal(t, "tool_d35d82bb-a1d8-4342-a07b-9e321edd96f", toolCallEntry.CallID)
	assert.NotNil(t, toolCallEntry.ToolCall)
	assert.Equal(t, "readToolCall", toolCallEntry.GetToolName())
}

func TestParseLogEntry_ToolCall_Completed(t *testing.T) {
	json := `{"type":"tool_call","subtype":"completed","call_id":"tool_d35d82bb-a1d8-4342-a07b-9e321edd96f","tool_call":{"readToolCall":{"args":{"path":"specs/001-github-agent-automation/tasks.md"},"result":{"success":{"content":"# Tasks: GitHub Agent Automation\n\n**Input**: Design documents","isEmpty":false,"exceededLimit":false,"totalLines":514,"fileSize":29371,"path":"specs/001-github-agent-automation/tasks.md","readRange":{"startLine":1,"endLine":514}}}}},"model_call_id":"249b8740-a695-40d7-9f05-898e6a263ede","session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","timestamp_ms":1762319688251}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "tool_call", entry.GetType())

	toolCallEntry, ok := entry.(*models.ToolCallEntry)
	require.True(t, ok)
	assert.Equal(t, "completed", toolCallEntry.Subtype)
	assert.NotNil(t, toolCallEntry.ToolCall)
	assert.Equal(t, "readToolCall", toolCallEntry.GetToolName())
}

func TestParseLogEntry_Result(t *testing.T) {
	json := `{"type":"result","subtype":"success","duration_ms":81383,"duration_api_ms":81383,"is_error":false,"result":"\nspecs/001-github-agent-automation/tasks.md と実装を確認し、取り組むべきタスクを整理します。\n","session_id":"193b2dbb-03ce-444f-a307-4a80f5d4cab0","request_id":"2ad1642d-8dda-478d-9b39-5e070427a0de"}`

	entry, err := ParseLogEntry([]byte(json))
	require.NoError(t, err)
	assert.Equal(t, "result", entry.GetType())

	resultEntry, ok := entry.(*models.ResultEntry)
	require.True(t, ok)
	assert.Equal(t, "success", resultEntry.Subtype)
	assert.Equal(t, int64(81383), resultEntry.DurationMS)
	assert.False(t, resultEntry.IsError)
}

func TestParseLogEntry_InvalidJSON(t *testing.T) {
	invalidJSON := `{"type":"system","subtype":}`

	_, err := ParseLogEntry([]byte(invalidJSON))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal JSON")
}

func TestParseLogEntry_MissingType(t *testing.T) {
	json := `{"subtype":"init","session_id":"123"}`

	_, err := ParseLogEntry([]byte(json))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing or invalid 'type' field")
}

func TestParseLogEntry_UnknownType(t *testing.T) {
	json := `{"type":"unknown_type","session_id":"123"}`

	_, err := ParseLogEntry([]byte(json))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown entry type")
}

func TestParseLogFile_AllLines(t *testing.T) {
	// Get the path to the sample log file
	logPath := filepath.Join("..", "..", "resources", "cursor-agent-output-sample.log")

	file, err := os.Open(logPath)
	require.NoError(t, err, "failed to open log file")
	defer file.Close()

	var lines [][]byte
	scanner := bufio.NewScanner(file)
	// Increase buffer size to handle long lines (up to 10MB per line)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		// Create a copy of the line to avoid issues with scanner reusing buffer
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)
		lines = append(lines, lineCopy)
	}
	require.NoError(t, scanner.Err())

	// Parse all lines
	entries, err := ParseLogFile(lines)

	// We expect all lines to parse successfully
	require.NoError(t, err, "failed to parse log file")

	// Verify we got the expected number of entries
	assert.Greater(t, len(entries), 0, "expected at least one entry")

	// Count entries by type
	counts := CountByType(entries)

	// Verify we have entries of each expected type
	assert.Greater(t, counts["system"], 0, "expected at least one system entry")
	assert.Greater(t, counts["user"], 0, "expected at least one user entry")
	assert.Greater(t, counts["thinking"], 0, "expected at least one thinking entry")
	assert.Greater(t, counts["assistant"], 0, "expected at least one assistant entry")
	assert.Greater(t, counts["tool_call"], 0, "expected at least one tool_call entry")
	assert.Greater(t, counts["result"], 0, "expected at least one result entry")

	t.Logf("Parsed %d entries successfully", len(entries))
	t.Logf("Entry counts by type: %v", counts)
}

func TestParseLogFile_EmptyLines(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"type":"system","subtype":"init","session_id":"123"}`),
		[]byte(``), // Empty line
		[]byte(`{"type":"user","message":{"role":"user","content":[]},"session_id":"123"}`),
	}

	entries, err := ParseLogFile(lines)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "should skip empty lines")
}

func TestCountByType(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"type":"system","subtype":"init","session_id":"123"}`),
		[]byte(`{"type":"user","message":{"role":"user","content":[]},"session_id":"123"}`),
		[]byte(`{"type":"system","subtype":"init","session_id":"456"}`),
		[]byte(`{"type":"thinking","subtype":"delta","session_id":"123"}`),
	}

	entries, err := ParseLogFile(lines)
	require.NoError(t, err)

	counts := CountByType(entries)
	assert.Equal(t, 2, counts["system"])
	assert.Equal(t, 1, counts["user"])
	assert.Equal(t, 1, counts["thinking"])
	assert.Equal(t, 0, counts["assistant"])
}

func TestToolCallEntry_GetToolName(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected string
	}{
		{
			name:     "readToolCall",
			json:     `{"type":"tool_call","subtype":"started","call_id":"test","tool_call":{"readToolCall":{}},"session_id":"123"}`,
			expected: "readToolCall",
		},
		{
			name:     "lsToolCall",
			json:     `{"type":"tool_call","subtype":"started","call_id":"test","tool_call":{"lsToolCall":{}},"session_id":"123"}`,
			expected: "lsToolCall",
		},
		{
			name:     "grepToolCall",
			json:     `{"type":"tool_call","subtype":"started","call_id":"test","tool_call":{"grepToolCall":{}},"session_id":"123"}`,
			expected: "grepToolCall",
		},
		{
			name:     "globToolCall",
			json:     `{"type":"tool_call","subtype":"started","call_id":"test","tool_call":{"globToolCall":{}},"session_id":"123"}`,
			expected: "globToolCall",
		},
		{
			name:     "semSearchToolCall",
			json:     `{"type":"tool_call","subtype":"started","call_id":"test","tool_call":{"semSearchToolCall":{}},"session_id":"123"}`,
			expected: "semSearchToolCall",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := ParseLogEntry([]byte(tt.json))
			require.NoError(t, err)

			toolCallEntry, ok := entry.(*models.ToolCallEntry)
			require.True(t, ok)
			assert.Equal(t, tt.expected, toolCallEntry.GetToolName())
		})
	}
}
