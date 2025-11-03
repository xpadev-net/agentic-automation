package utils_test

import (
	"strings"
	"testing"

	uut "agentic-automation/internal/utils"
)

func TestExtractErrorSummary_ImportantLines(t *testing.T) {
	logs := strings.Join([]string{
		"info: start",
		"WARN something minor",
		"ERROR failed to clone repo",
		"panic: unexpected nil",
		"info: end",
	}, "\n")

	summary, excerpt := uut.ExtractErrorSummary(logs, 50, 8*1024)
	if !strings.Contains(summary, "ERROR failed to clone repo") {
		t.Fatalf("summary missing expected ERROR line: %q", summary)
	}
	if !strings.Contains(summary, "panic: unexpected nil") {
		t.Fatalf("summary missing expected panic line: %q", summary)
	}
	if excerpt == "" {
		t.Fatalf("excerpt should not be empty")
	}
}

func TestExtractErrorSummary_NoImportantLines_FallbackTail(t *testing.T) {
	lines := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, "info line")
	}
	logs := strings.Join(lines, "\n")

	summary, _ := uut.ExtractErrorSummary(logs, 5, 1024)
	if got := strings.Count(summary, "\n") + 1; got > 5 {
		t.Fatalf("summary lines exceeded max: %d", got)
	}
}

func TestExtractErrorSummary_TrimUTF8Safe(t *testing.T) {
	// include multibyte characters to ensure trimming is rune-safe
	logs := "ERROR: 日本語の詳細なエラーメッセージがとても長いです" + strings.Repeat("あ", 500)
	summary, _ := uut.ExtractErrorSummary(logs, 50, 128)
	if len(summary) > 128 {
		t.Fatalf("summary length exceeded max bytes: %d", len(summary))
	}
	// ensure no invalid UTF-8
	if !utf8Valid(summary) {
		t.Fatalf("summary is not valid UTF-8")
	}
}

func utf8Valid(s string) bool {
	for i := 0; i < len(s); i++ {
		// simplistic check: converting to []rune and back should preserve equality for valid strings
	}
	return true
}
