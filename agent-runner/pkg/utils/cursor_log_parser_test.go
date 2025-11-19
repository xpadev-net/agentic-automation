package utils

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestProgressDotsIncrementOnConsecutiveProcessing(t *testing.T) {
	// Prepare stream-json like lines for the same session
	lines := []string{
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`, // duplicate processing
		`{"type":"thinking","subtype":"completed","session_id":"s1"}`,
	}
	input := strings.Join(lines, "\n")

	// Capture stderr
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	// Run
	ParseAndFormatOutput(input)

	// Restore stderr and read output
	_ = w.Close()
	os.Stderr = oldStderr
	outBytes, _ := io.ReadAll(r)
	_ = r.Close()
	out := string(outBytes)

	// Expect first processing then one dot, then a newline before completed
	if !strings.Contains(out, "[THINKING] processing.") {
		t.Fatalf("expected '[THINKING] processing.' sequence, got:\n%s", out)
	}
	if !strings.Contains(out, "\n[THINKING] completed") {
		t.Fatalf("expected newline before completed, got:\n%s", out)
	}
}

func TestProgressDotsKeepIncreasingWithMoreProcessing(t *testing.T) {
	// Test with many consecutive duplicate processing logs
	lines := []string{
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"completed","session_id":"s1"}`,
	}
	input := strings.Join(lines, "\n")

	// Capture stderr
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	// Run
	ParseAndFormatOutput(input)

	// Restore stderr and read output
	_ = w.Close()
	os.Stderr = oldStderr
	outBytes, _ := io.ReadAll(r)
	_ = r.Close()
	out := string(outBytes)

	// Expect first processing then four dots (total 5 processing events)
	if !strings.Contains(out, "[THINKING] processing....") {
		t.Fatalf("expected '[THINKING] processing....', got:\n%s", out)
	}
	if !strings.Contains(out, "[THINKING] completed") {
		t.Fatalf("expected completed, got:\n%s", out)
	}
}

func TestDifferentSessionsDotsManagedIndependently(t *testing.T) {
	// Test that different sessions don't suppress each other
	lines := []string{
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s2"}`,
		`{"type":"thinking","subtype":"progress","session_id":"s1"}`,
		`{"type":"thinking","subtype":"completed","session_id":"s1"}`,
		`{"type":"thinking","subtype":"completed","session_id":"s2"}`,
	}
	input := strings.Join(lines, "\n")

	// Capture stderr
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	// Run
	ParseAndFormatOutput(input)

	// Restore stderr and read output
	_ = w.Close()
	os.Stderr = oldStderr
	outBytes, _ := io.ReadAll(r)
	_ = r.Close()
	out := string(outBytes)

	// Expect concatenated order: s1 base + s2 base + s1 dot
	prefix := "[THINKING] processing[THINKING] processing."
	if !strings.Contains(out, prefix) {
		t.Fatalf("expected concatenated processing sequence per session, got:\n%s", out)
	}
	if strings.Count(out, "[THINKING] completed") != 2 {
		t.Fatalf("expected 2 completed logs, got %d:\n%s", strings.Count(out, "[THINKING] completed"), out)
	}
}

func TestGetShellCommand_StringArray(t *testing.T) {
	entry := &ToolCallEntry{
		ToolCall: map[string]interface{}{
			"shellToolCall": map[string]interface{}{
				"args": map[string]interface{}{
					"command": []interface{}{"ls", "-la", "/tmp"},
				},
			},
		},
	}

	command := entry.GetShellCommand()
	expected := "ls -la /tmp"
	if command != expected {
		t.Errorf("expected %q, got %q", expected, command)
	}
}

func TestGetShellCommand_String(t *testing.T) {
	entry := &ToolCallEntry{
		ToolCall: map[string]interface{}{
			"shellToolCall": map[string]interface{}{
				"args": map[string]interface{}{
					"command": "ls -la",
				},
			},
		},
	}

	command := entry.GetShellCommand()
	expected := "ls -la"
	if command != expected {
		t.Errorf("expected %q, got %q", expected, command)
	}
}

func TestGetShellCommand_NoCommand(t *testing.T) {
	entry := &ToolCallEntry{
		ToolCall: map[string]interface{}{
			"shellToolCall": map[string]interface{}{
				"args": map[string]interface{}{},
			},
		},
	}

	command := entry.GetShellCommand()
	if command != "" {
		t.Errorf("expected empty string, got %q", command)
	}
}

func TestGetShellCommand_NonShellToolCall(t *testing.T) {
	entry := &ToolCallEntry{
		ToolCall: map[string]interface{}{
			"otherTool": map[string]interface{}{
				"args": map[string]interface{}{
					"command": "some command",
				},
			},
		},
	}

	command := entry.GetShellCommand()
	if command != "" {
		t.Errorf("expected empty string for non-shellToolCall, got %q", command)
	}
}

func TestGetShellCommand_NilToolCall(t *testing.T) {
	entry := &ToolCallEntry{
		ToolCall: nil,
	}

	command := entry.GetShellCommand()
	if command != "" {
		t.Errorf("expected empty string for nil ToolCall, got %q", command)
	}
}

func TestFormat_ShellToolCallWithCommand(t *testing.T) {
	entry := &ToolCallEntry{
		Subtype: "started",
		CallID:  "abc123",
		ToolCall: map[string]interface{}{
			"shellToolCall": map[string]interface{}{
				"args": map[string]interface{}{
					"command": "ls -la",
				},
			},
		},
	}

	formatted := entry.Format()
	expected := `[TOOL] shellToolCall started (id: abc123) command: "ls -la"`
	if formatted != expected {
		t.Errorf("expected %q, got %q", expected, formatted)
	}
}

func TestFormat_ShellToolCallWithLongCommand(t *testing.T) {
	longCommand := strings.Repeat("a", 250)
	entry := &ToolCallEntry{
		Subtype: "started",
		CallID:  "abc123",
		ToolCall: map[string]interface{}{
			"shellToolCall": map[string]interface{}{
				"args": map[string]interface{}{
					"command": longCommand,
				},
			},
		},
	}

	formatted := entry.Format()
	expectedTruncated := longCommand[:200] + "..."
	expected := fmt.Sprintf(`[TOOL] shellToolCall started (id: abc123) command: %q`, expectedTruncated)
	if formatted != expected {
		t.Errorf("expected %q, got %q", expected, formatted)
	}
}

func TestFormat_NonShellToolCall(t *testing.T) {
	entry := &ToolCallEntry{
		Subtype: "started",
		CallID:  "abc123",
		ToolCall: map[string]interface{}{
			"otherTool": map[string]interface{}{
				"args": map[string]interface{}{},
			},
		},
	}

	formatted := entry.Format()
	expected := "[TOOL] otherTool started (id: abc123)"
	if formatted != expected {
		t.Errorf("expected %q, got %q", expected, formatted)
	}
}
