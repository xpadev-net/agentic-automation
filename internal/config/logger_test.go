package config

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestAny_WithPointers(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{
			name:     "nil pointer to int",
			value:    (*int)(nil),
			expected: "nil",
		},
		{
			name:     "non-nil pointer to int",
			value:    intPtr(42),
			expected: "42",
		},
		{
			name:     "nil pointer to string",
			value:    (*string)(nil),
			expected: "nil",
		},
		{
			name:     "non-nil pointer to string",
			value:    stringPtr("test-value"),
			expected: "test-value",
		},
		{
			name:     "non-pointer int",
			value:    123,
			expected: "123",
		},
		{
			name:     "non-pointer string",
			value:    "hello",
			expected: "hello",
		},
		{
			name:     "nil interface",
			value:    nil,
			expected: "nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := Any("test_key", tt.value)
			if !field.valid {
				if tt.expected == "" {
					return // Expected to be invalid
				}
				t.Fatalf("Field should be valid")
			}

			formatted, ok := field.format()
			if !ok {
				t.Fatalf("Field format should succeed")
			}

			// Extract the value part from [key=value]
			parts := strings.Split(formatted, "=")
			if len(parts) != 2 {
				t.Fatalf("Expected format [key=value], got %s", formatted)
			}
			valuePart := strings.TrimSuffix(parts[1], "]")
			if valuePart != tt.expected {
				t.Errorf("Expected value %q, got %q", tt.expected, valuePart)
			}
		})
	}
}

func TestAny_LoggingWithPointers(t *testing.T) {
	// Create a buffer to capture log output
	var buf bytes.Buffer
	testLogger := log.New(&buf, "", 0)
	logger := FromStdLogger(testLogger)

	// Test with pointer values
	prID := intPtr(12345)
	commitSHA := stringPtr("abc123def456")
	var nilInt *int = nil
	var nilString *string = nil

	logger.Info("Test message",
		Any("pr_id", prID),
		Any("commit_sha", commitSHA),
		Any("nil_int", nilInt),
		Any("nil_string", nilString),
	)

	output := buf.String()

	// Verify that pointer values are dereferenced, not showing memory addresses
	if strings.Contains(output, "0x") {
		t.Errorf("Log output should not contain memory addresses, got: %s", output)
	}

	// Verify actual values are present
	if !strings.Contains(output, "[pr_id=12345]") {
		t.Errorf("Log output should contain pr_id=12345, got: %s", output)
	}
	if !strings.Contains(output, "[commit_sha=abc123def456]") {
		t.Errorf("Log output should contain commit_sha=abc123def456, got: %s", output)
	}
	if !strings.Contains(output, "[nil_int=nil]") {
		t.Errorf("Log output should contain nil_int=nil, got: %s", output)
	}
	if !strings.Contains(output, "[nil_string=nil]") {
		t.Errorf("Log output should contain nil_string=nil, got: %s", output)
	}
}

// Helper functions
func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
