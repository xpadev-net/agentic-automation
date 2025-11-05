package utils

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestSuppressDuplicateThinkingProcessingLogs(t *testing.T) {
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

	// Expect only one processing line and one completed line
	if strings.Count(out, "[THINKING] processing...") != 1 {
		t.Fatalf("expected 1 processing line, got:\n%s", out)
	}
	if strings.Count(out, "[THINKING] completed") != 1 {
		t.Fatalf("expected 1 completed line, got:\n%s", out)
	}
}
