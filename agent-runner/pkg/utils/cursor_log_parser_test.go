package utils

import (
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

	// Expect two processing lines: "..." and then "...." and one completed line
	if strings.Count(out, "[THINKING] processing...\n") != 1 {
		t.Fatalf("expected 1 base processing line (...), got:\n%s", out)
	}
	if strings.Count(out, "[THINKING] processing....\n") != 1 {
		t.Fatalf("expected 1 incremented processing line (....), got:\n%s", out)
	}
	if strings.Count(out, "[THINKING] completed\n") != 1 {
		t.Fatalf("expected 1 completed line, got:\n%s", out)
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

	// Expect dots to increase: ..., ...., ....., ......, .......
	want := []string{
		"[THINKING] processing...",
		"[THINKING] processing....",
		"[THINKING] processing.....",
		"[THINKING] processing......",
		"[THINKING] processing.......",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("expected to contain %q, got:\n%s", w, out)
		}
	}
	if strings.Count(out, "[THINKING] completed\n") != 1 {
		t.Fatalf("expected 1 completed line, got %d:\n%s", strings.Count(out, "[THINKING] completed"), out)
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

	// Expect two base processing (s1 first, s2 first) and one incremented for s1
	if strings.Count(out, "[THINKING] processing...\n") != 2 {
		t.Fatalf("expected 2 base processing lines (...), got %d:\n%s", strings.Count(out, "[THINKING] processing...\n"), out)
	}
	if strings.Count(out, "[THINKING] processing....\n") != 1 {
		t.Fatalf("expected 1 incremented processing line (....), got %d:\n%s", strings.Count(out, "[THINKING] processing....\n"), out)
	}
	if strings.Count(out, "[THINKING] completed\n") != 2 {
		t.Fatalf("expected 2 completed lines, got %d:\n%s", strings.Count(out, "[THINKING] completed\n"), out)
	}
}
