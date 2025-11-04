package services

import (
	"context"
	"testing"

	"agentic-automation/internal/clients"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestNewCIFailureAnalyzer_Success tests successful creation of CIFailureAnalyzer
func TestNewCIFailureAnalyzer_Success(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}

	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	require.NotNil(t, analyzer)
	assert.Equal(t, githubClient, analyzer.githubClient)
	assert.Equal(t, logger, analyzer.logger)
}

// TestNewCIFailureAnalyzer_NilLogger tests that nil logger is handled gracefully
func TestNewCIFailureAnalyzer_NilLogger(t *testing.T) {
	githubClient := &clients.Client{}

	analyzer := NewCIFailureAnalyzer(githubClient, nil)

	require.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.logger) // Should use zap.NewNop()
}

// TestNewCIFailureAnalyzer_NilGitHubClient tests that nil githubClient causes panic
func TestNewCIFailureAnalyzer_NilGitHubClient(t *testing.T) {
	logger := zaptest.NewLogger(t)

	assert.Panics(t, func() {
		NewCIFailureAnalyzer(nil, logger)
	}, "expected panic when githubClient is nil")
}

// TestDetectFailureType_TestFailure tests detection of test failure patterns
func TestDetectFailureType_TestFailure(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name     string
		logs     string
		expected string
	}{
		{
			name:     "TestFail",
			logs:     "Test failed: assertion error",
			expected: FailureTypeTestFailure,
		},
		{
			name:     "AssertionFail",
			logs:     "Assertion failed: expected 'foo', got 'bar'",
			expected: FailureTypeTestFailure,
		},
		{
			name:     "ExpectedGot",
			logs:     "Expected 'value' but got 'other'",
			expected: FailureTypeTestFailure,
		},
		{
			name:     "TestError",
			logs:     "Test error occurred in test suite",
			expected: FailureTypeTestFailure,
		},
		{
			name:     "FailedTests",
			logs:     "Failed tests: 3 out of 10",
			expected: FailureTypeTestFailure,
		},
		{
			name:     "CaseInsensitive",
			logs:     "TEST FAILED: assertion error",
			expected: FailureTypeTestFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.detectFailureType(tt.logs)
			assert.Equal(t, tt.expected, result, "detectFailureType(%q) = %q, want %q", tt.logs, result, tt.expected)
		})
	}
}

// TestDetectFailureType_BuildError tests detection of build error patterns
func TestDetectFailureType_BuildError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name     string
		logs     string
		expected string
	}{
		{
			name:     "BuildFail",
			logs:     "Build failed: compilation error",
			expected: FailureTypeBuildError,
		},
		{
			name:     "CompileError",
			logs:     "Compile error: syntax error at line 42",
			expected: FailureTypeBuildError,
		},
		{
			name:     "SyntaxError",
			logs:     "Syntax error: unexpected token",
			expected: FailureTypeBuildError,
		},
		{
			name:     "BuildError",
			logs:     "Build error occurred during compilation",
			expected: FailureTypeBuildError,
		},
		{
			name:     "CompilationFail",
			logs:     "Compilation failed with errors",
			expected: FailureTypeBuildError,
		},
		{
			name:     "CaseInsensitive",
			logs:     "BUILD FAILED: compilation error",
			expected: FailureTypeBuildError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.detectFailureType(tt.logs)
			assert.Equal(t, tt.expected, result, "detectFailureType(%q) = %q, want %q", tt.logs, result, tt.expected)
		})
	}
}

// TestDetectFailureType_LintError tests detection of lint error patterns
func TestDetectFailureType_LintError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name     string
		logs     string
		expected string
	}{
		{
			name:     "LintFail",
			logs:     "Lint failed: style issues found",
			expected: FailureTypeLintError,
		},
		{
			name:     "ESLint",
			logs:     "ESLint found 5 errors",
			expected: FailureTypeLintError,
		},
		{
			name:     "Prettier",
			logs:     "Prettier formatting error",
			expected: FailureTypeLintError,
		},
		{
			name:     "FormatError",
			logs:     "Format error: indentation issue",
			expected: FailureTypeLintError,
		},
		{
			name:     "LinterError",
			logs:     "Linter error: unused variable",
			expected: FailureTypeLintError,
		},
		{
			name:     "StyleError",
			logs:     "Style error: line too long",
			expected: FailureTypeLintError,
		},
		{
			name:     "CaseInsensitive",
			logs:     "LINT FAILED: issues found",
			expected: FailureTypeLintError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.detectFailureType(tt.logs)
			assert.Equal(t, tt.expected, result, "detectFailureType(%q) = %q, want %q", tt.logs, result, tt.expected)
		})
	}
}

// TestDetectFailureType_Unknown tests detection of unknown failure types
func TestDetectFailureType_Unknown(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name     string
		logs     string
		expected string
	}{
		{
			name:     "EmptyString",
			logs:     "",
			expected: FailureTypeUnknown,
		},
		{
			name:     "GenericMessage",
			logs:     "Something went wrong",
			expected: FailureTypeUnknown,
		},
		{
			name:     "NoErrorKeywords",
			logs:     "Process completed successfully",
			expected: FailureTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.detectFailureType(tt.logs)
			assert.Equal(t, tt.expected, result, "detectFailureType(%q) = %q, want %q", tt.logs, result, tt.expected)
		})
	}
}

// TestDetectFailureType_Priority tests priority order when multiple patterns match
func TestDetectFailureType_Priority(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name     string
		logs     string
		expected string
	}{
		{
			name:     "TestFailurePriority",
			logs:     "Test failed and build failed",
			expected: FailureTypeTestFailure, // test_failure has highest priority
		},
		{
			name:     "BuildErrorPriority",
			logs:     "Build failed and lint failed",
			expected: FailureTypeBuildError, // build_error > lint_error
		},
		{
			name:     "LintErrorPriority",
			logs:     "Lint failed and unknown error",
			expected: FailureTypeLintError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.detectFailureType(tt.logs)
			assert.Equal(t, tt.expected, result, "detectFailureType(%q) = %q, want %q", tt.logs, result, tt.expected)
		})
	}
}

// TestAnalyzeCheckRunLogs_WithText tests extraction of logs from Output.Text
func TestAnalyzeCheckRunLogs_WithText(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	text := "Test failed: assertion error at line 42"
	checkRun := &github.CheckRun{
		Name: github.String("test-check"),
		Output: &github.CheckRunOutput{
			Text:    github.String(text),
			Summary: github.String("Summary text"),
		},
	}

	logs, failureType := analyzer.analyzeCheckRunLogs(checkRun)

	assert.Equal(t, text, logs, "should extract Text field")
	assert.Equal(t, FailureTypeTestFailure, failureType, "should detect test failure")
}

// TestAnalyzeCheckRunLogs_WithSummaryOnly tests extraction of logs from Output.Summary when Text is empty
func TestAnalyzeCheckRunLogs_WithSummaryOnly(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	summary := "Build failed: compilation error"
	checkRun := &github.CheckRun{
		Name: github.String("build-check"),
		Output: &github.CheckRunOutput{
			Text:    nil,
			Summary: github.String(summary),
		},
	}

	logs, failureType := analyzer.analyzeCheckRunLogs(checkRun)

	assert.Equal(t, summary, logs, "should extract Summary field when Text is nil")
	assert.Equal(t, FailureTypeBuildError, failureType, "should detect build error")
}

// TestAnalyzeCheckRunLogs_EmptyOutput tests handling of nil or empty Output
func TestAnalyzeCheckRunLogs_EmptyOutput(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name     string
		checkRun *github.CheckRun
	}{
		{
			name:     "NilOutput",
			checkRun: &github.CheckRun{Name: github.String("test-check")},
		},
		{
			name: "EmptyTextAndSummary",
			checkRun: &github.CheckRun{
				Name: github.String("test-check"),
				Output: &github.CheckRunOutput{
					Text:    github.String(""),
					Summary: github.String(""),
				},
			},
		},
		{
			name: "NilTextAndSummary",
			checkRun: &github.CheckRun{
				Name: github.String("test-check"),
				Output: &github.CheckRunOutput{
					Text:    nil,
					Summary: nil,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs, failureType := analyzer.analyzeCheckRunLogs(tt.checkRun)

			assert.Empty(t, logs, "should return empty logs")
			assert.Equal(t, FailureTypeUnknown, failureType, "should return unknown failure type")
		})
	}
}

// TestAnalyzeCheckRunLogs_TestFailure tests detection of test failure in check run logs
func TestAnalyzeCheckRunLogs_TestFailure(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	checkRun := &github.CheckRun{
		Name: github.String("test-check"),
		Output: &github.CheckRunOutput{
			Text: github.String("Test failed: expected 'foo', got 'bar'"),
		},
	}

	logs, failureType := analyzer.analyzeCheckRunLogs(checkRun)

	assert.NotEmpty(t, logs)
	assert.Equal(t, FailureTypeTestFailure, failureType)
}

// TestAnalyzeCheckRunLogs_BuildError tests detection of build error in check run logs
func TestAnalyzeCheckRunLogs_BuildError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	checkRun := &github.CheckRun{
		Name: github.String("build-check"),
		Output: &github.CheckRunOutput{
			Text: github.String("Build failed: compilation error at file.go:42"),
		},
	}

	logs, failureType := analyzer.analyzeCheckRunLogs(checkRun)

	assert.NotEmpty(t, logs)
	assert.Equal(t, FailureTypeBuildError, failureType)
}

// TestAnalyzeCheckRunLogs_LintError tests detection of lint error in check run logs
func TestAnalyzeCheckRunLogs_LintError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	checkRun := &github.CheckRun{
		Name: github.String("lint-check"),
		Output: &github.CheckRunOutput{
			Text: github.String("Lint failed: ESLint found 5 errors"),
		},
	}

	logs, failureType := analyzer.analyzeCheckRunLogs(checkRun)

	assert.NotEmpty(t, logs)
	assert.Equal(t, FailureTypeLintError, failureType)
}

// TestAnalyzeCIFailure_Success tests successful analysis of CI failure
// Note: This test requires proper GitHub client mocking or integration test setup
func TestAnalyzeCIFailure_Success(t *testing.T) {
	t.Skip("Requires mock GitHub client or integration test setup - test core logic separately")

	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	ctx := context.Background()

	_, err := analyzer.AnalyzeCIFailure(ctx, "owner", "repo", 123)
	_ = err
}

// TestAnalyzeCIFailure_NoFailedRuns tests error handling when no failed runs exist
func TestAnalyzeCIFailure_NoFailedRuns(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	ctx := context.Background()

	// This test will fail without proper mocking
	// We'll test the logic separately
	t.Skip("Requires mock GitHub client or integration test setup")

	_, err := analyzer.AnalyzeCIFailure(ctx, "owner", "repo", 123)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no failed check runs found")
}

// TestAnalyzeCIFailure_EmptyLogs tests handling of empty logs
func TestAnalyzeCIFailure_EmptyLogs(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	ctx := context.Background()

	t.Skip("Requires mock GitHub client or integration test setup")

	_, err := analyzer.AnalyzeCIFailure(ctx, "owner", "repo", 123)

	_ = err
}

// TestAnalyzeCIFailure_MultipleFailureTypes tests priority handling of multiple failure types
func TestAnalyzeCIFailure_MultipleFailureTypes(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	ctx := context.Background()

	t.Skip("Requires mock GitHub client or integration test setup")

	_, err := analyzer.AnalyzeCIFailure(ctx, "owner", "repo", 123)

	_ = err
}

// TestAnalyzeCIFailure_GitHubAPIError tests error handling for GitHub API failures
func TestAnalyzeCIFailure_GitHubAPIError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	ctx := context.Background()

	t.Skip("Requires mock GitHub client or integration test setup")

	_, err := analyzer.AnalyzeCIFailure(ctx, "owner", "repo", 123)

	_ = err
}

// TestDetermineOverallFailureType tests the priority logic for determining overall failure type
func TestDetermineOverallFailureType(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	analyzer := NewCIFailureAnalyzer(githubClient, logger)

	tests := []struct {
		name         string
		failureTypes []string
		expected     string
	}{
		{
			name:         "TestFailurePriority",
			failureTypes: []string{FailureTypeTestFailure, FailureTypeBuildError},
			expected:     FailureTypeTestFailure,
		},
		{
			name:         "BuildErrorPriority",
			failureTypes: []string{FailureTypeBuildError, FailureTypeLintError},
			expected:     FailureTypeBuildError,
		},
		{
			name:         "LintErrorOnly",
			failureTypes: []string{FailureTypeLintError},
			expected:     FailureTypeLintError,
		},
		{
			name:         "EmptyList",
			failureTypes: []string{},
			expected:     FailureTypeUnknown,
		},
		{
			name:         "OnlyUnknown",
			failureTypes: []string{FailureTypeUnknown},
			expected:     FailureTypeUnknown,
		},
		{
			name:         "AllThreeTypes",
			failureTypes: []string{FailureTypeLintError, FailureTypeBuildError, FailureTypeTestFailure},
			expected:     FailureTypeTestFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.determineOverallFailureType(tt.failureTypes)
			assert.Equal(t, tt.expected, result, "determineOverallFailureType(%v) = %q, want %q", tt.failureTypes, result, tt.expected)
		})
	}
}
