package context

import (
	"errors"
	"reflect"
	"testing"
)

// assertParseError checks if the error is a ParseError with the expected field
func assertParseError(t *testing.T, err error, expectedField string) {
	if err == nil {
		t.Fatalf("expected ParseError, got nil")
	}

	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected ParseError, got %T: %v", err, err)
	}

	if parseErr.Field != expectedField {
		t.Errorf("expected field %q, got %q", expectedField, parseErr.Field)
	}
}

// TestParseIssueContext_ValidInputs tests successful parsing with all valid inputs
func TestParseIssueContext_ValidInputs(t *testing.T) {
	tests := []struct {
		name                 string
		issueID              int
		repo                 string
		prompt               string
		previousAttemptsJSON string
		ciLogs               string
		wantIssue            *Issue
	}{
		{
			name:                 "all arguments valid with empty previous attempts",
			issueID:              42,
			repo:                 "owner/repo",
			prompt:               "Fix bug in parser",
			previousAttemptsJSON: "",
			ciLogs:               "",
			wantIssue: &Issue{
				ID:               42,
				Title:            "",
				Body:             "Fix bug in parser",
				Repo:             "owner/repo",
				Labels:           []string{},
				PreviousAttempts: nil,
				CILogs:           "",
			},
		},
		{
			name:                 "with ciLogs set",
			issueID:              123,
			repo:                 "test/repository",
			prompt:               "Implement feature",
			previousAttemptsJSON: "",
			ciLogs:               "Error: Test failure at line 42",
			wantIssue: &Issue{
				ID:               123,
				Title:            "",
				Body:             "Implement feature",
				Repo:             "test/repository",
				Labels:           []string{},
				PreviousAttempts: nil,
				CILogs:           "Error: Test failure at line 42",
			},
		},
		{
			name:                 "with valid previous attempts JSON",
			issueID:              456,
			repo:                 "org/project",
			prompt:               "Refactor code",
			previousAttemptsJSON: `[{"retry_count": 1, "error": "Lint failed", "ci_logs": "Error: line 42"}]`,
			ciLogs:               "",
			wantIssue: &Issue{
				ID:     456,
				Title:  "",
				Body:   "Refactor code",
				Repo:   "org/project",
				Labels: []string{},
				PreviousAttempts: []PreviousAttempt{
					{
						RetryCount: 1,
						Error:      "Lint failed",
						CILogs:     "Error: line 42",
					},
				},
				CILogs: "",
			},
		},
		{
			name:                 "with multiple previous attempts",
			issueID:              789,
			repo:                 "user/app",
			prompt:               "Fix multiple issues",
			previousAttemptsJSON: `[{"retry_count": 1, "error": "First error", "ci_logs": "Log1"}, {"retry_count": 2, "error": "Second error", "ci_logs": ""}]`,
			ciLogs:               "Current CI error",
			wantIssue: &Issue{
				ID:     789,
				Title:  "",
				Body:   "Fix multiple issues",
				Repo:   "user/app",
				Labels: []string{},
				PreviousAttempts: []PreviousAttempt{
					{
						RetryCount: 1,
						Error:      "First error",
						CILogs:     "Log1",
					},
					{
						RetryCount: 2,
						Error:      "Second error",
						CILogs:     "",
					},
				},
				CILogs: "Current CI error",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIssueContext(tt.issueID, tt.repo, tt.prompt, tt.previousAttemptsJSON, tt.ciLogs)
			if err != nil {
				t.Fatalf("ParseIssueContext() error = %v, want nil", err)
			}

			if !reflect.DeepEqual(got, tt.wantIssue) {
				t.Errorf("ParseIssueContext() = %+v, want %+v", got, tt.wantIssue)
			}
		})
	}
}

// TestParseIssueContext_InvalidIssueID tests validation errors for issueID
func TestParseIssueContext_InvalidIssueID(t *testing.T) {
	tests := []struct {
		name    string
		issueID int
	}{
		{
			name:    "zero issueID",
			issueID: 0,
		},
		{
			name:    "negative issueID",
			issueID: -1,
		},
		{
			name:    "large negative issueID",
			issueID: -100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseIssueContext(tt.issueID, "owner/repo", "Valid prompt", "", "")
			assertParseError(t, err, "issue-id")
		})
	}
}

// TestParseIssueContext_InvalidRepo tests validation errors for repo
func TestParseIssueContext_InvalidRepo(t *testing.T) {
	tests := []struct {
		name string
		repo string
	}{
		{
			name: "empty repo",
			repo: "",
		},
		{
			name: "owner only",
			repo: "owner",
		},
		{
			name: "repo only",
			repo: "/repo",
		},
		{
			name: "with space",
			repo: "owner repo",
		},
		{
			name: "with special characters",
			repo: "owner@repo",
		},
		{
			name: "invalid format",
			repo: "owner/repo/branch",
		},
		{
			name: "only slash",
			repo: "/",
		},
		{
			name: "multiple slashes",
			repo: "owner/repo/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseIssueContext(42, tt.repo, "Valid prompt", "", "")
			assertParseError(t, err, "repo")
		})
	}
}

// TestParseIssueContext_InvalidPrompt tests validation errors for prompt
func TestParseIssueContext_InvalidPrompt(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
	}{
		{
			name:   "empty prompt",
			prompt: "",
		},
		{
			name:   "whitespace only",
			prompt: "   ",
		},
		{
			name:   "tab only",
			prompt: "\t",
		},
		{
			name:   "newline only",
			prompt: "\n",
		},
		{
			name:   "mixed whitespace",
			prompt: " \t\n ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseIssueContext(42, "owner/repo", tt.prompt, "", "")
			assertParseError(t, err, "prompt")
		})
	}
}

// TestParseIssueContext_InvalidPreviousAttempts tests validation errors for previousAttempts JSON
func TestParseIssueContext_InvalidPreviousAttempts(t *testing.T) {
	tests := []struct {
		name                 string
		previousAttemptsJSON string
	}{
		{
			name:                 "invalid JSON",
			previousAttemptsJSON: "{invalid}",
		},
		{
			name:                 "object instead of array",
			previousAttemptsJSON: `{"retry_count": 1}`,
		},
		{
			name:                 "string instead of array",
			previousAttemptsJSON: `"not an array"`,
		},
		{
			name:                 "number instead of array",
			previousAttemptsJSON: `123`,
		},
		{
			name:                 "boolean instead of array",
			previousAttemptsJSON: `true`,
		},
		{
			name:                 "null instead of array",
			previousAttemptsJSON: `null`,
		},
		{
			name:                 "malformed JSON",
			previousAttemptsJSON: `[{"retry_count": 1, "error": "test"`, // missing closing bracket
		},
		{
			name:                 "invalid array element",
			previousAttemptsJSON: `[{"invalid": "field"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseIssueContext(42, "owner/repo", "Valid prompt", tt.previousAttemptsJSON, "")
			assertParseError(t, err, "previous-attempts")
		})
	}
}

// TestParseIssueContext_NegativeRetryCount tests validation for negative retry_count
func TestParseIssueContext_NegativeRetryCount(t *testing.T) {
	tests := []struct {
		name                 string
		previousAttemptsJSON string
	}{
		{
			name:                 "negative retry_count",
			previousAttemptsJSON: `[{"retry_count": -1, "error": "test"}]`,
		},
		{
			name:                 "negative retry_count in multiple attempts",
			previousAttemptsJSON: `[{"retry_count": 1, "error": "test"}, {"retry_count": -5, "error": "test2"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseIssueContext(42, "owner/repo", "Valid prompt", tt.previousAttemptsJSON, "")
			assertParseError(t, err, "previous-attempts")
		})
	}
}

// TestParseIssueContext_EmptyPreviousAttempts tests that empty string is handled correctly
func TestParseIssueContext_EmptyPreviousAttempts(t *testing.T) {
	issue, err := ParseIssueContext(42, "owner/repo", "Valid prompt", "", "")
	if err != nil {
		t.Fatalf("ParseIssueContext() error = %v, want nil", err)
	}

	if issue.PreviousAttempts != nil && len(issue.PreviousAttempts) > 0 {
		t.Errorf("ParseIssueContext() PreviousAttempts = %v, want nil", issue.PreviousAttempts)
	}
}

// TestParseError_Error tests ParseError.Error() method
func TestParseError_Error(t *testing.T) {
	tests := []struct {
		name    string
		err     *ParseError
		wantMsg string
	}{
		{
			name: "error without wrapped error",
			err: &ParseError{
				Field:   "test-field",
				Message: "test message",
			},
			wantMsg: "invalid test-field: test message",
		},
		{
			name: "error with wrapped error",
			err: &ParseError{
				Field:   "test-field",
				Message: "test message",
				Err:     errors.New("wrapped error"),
			},
			wantMsg: "invalid test-field: test message: wrapped error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.wantMsg {
				t.Errorf("ParseError.Error() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

// TestParseError_Unwrap tests ParseError.Unwrap() method
func TestParseError_Unwrap(t *testing.T) {
	wrappedErr := errors.New("original error")
	err := &ParseError{
		Field:   "test-field",
		Message: "test message",
		Err:     wrappedErr,
	}

	got := err.Unwrap()
	if got != wrappedErr {
		t.Errorf("ParseError.Unwrap() = %v, want %v", got, wrappedErr)
	}

	// Test nil wrapped error
	errNoWrap := &ParseError{
		Field:   "test-field",
		Message: "test message",
		Err:     nil,
	}

	gotNil := errNoWrap.Unwrap()
	if gotNil != nil {
		t.Errorf("ParseError.Unwrap() = %v, want nil", gotNil)
	}
}
