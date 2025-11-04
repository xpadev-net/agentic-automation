package services

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/utils"
	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
)

// CIFailureResult represents the result of analyzing CI failure logs.
// It contains structured information extracted from failed check runs
// for use in AI retry prompts.
type CIFailureResult struct {
	Summary          string   // Error summary for AI prompt (max ErrSummaryMaxBytes)
	Excerpt          string   // Detailed error log excerpt (max LogExcerptMaxBytes)
	FailureType      string   // Failure type: "test_failure", "build_error", "lint_error", or "unknown"
	FailedCheckNames []string // List of failed check run names
	CombinedLogs     string   // Combined logs from all failed check runs (internal use)
}

// CIFailureAnalyzer provides methods to analyze CI failure logs from GitHub CheckRuns.
// It extracts error information and generates structured feedback for AI retry prompts.
type CIFailureAnalyzer struct {
	githubClient *clients.Client
	logger       *zap.Logger
}

// Failure type constants
const (
	FailureTypeTestFailure = "test_failure"
	FailureTypeBuildError  = "build_error"
	FailureTypeLintError   = "lint_error"
	FailureTypeUnknown     = "unknown"
)

// Regular expression patterns for detecting failure types
var (
	testFailurePattern = regexp.MustCompile(`(?i)(test.*fail|assertion.*fail|expected.*got|test.*error|failed.*tests?)`)
	buildErrorPattern  = regexp.MustCompile(`(?i)(build.*fail|compile.*error|syntax.*error|build.*error|compilation.*fail)`)
	lintErrorPattern   = regexp.MustCompile(`(?i)(lint.*fail|eslint|prettier|format.*error|linter.*error|style.*error)`)
)

// NewCIFailureAnalyzer creates a new CIFailureAnalyzer instance.
// It requires a GitHub client and logger as dependencies.
//
// Parameters:
//   - githubClient: GitHub API client (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *CIFailureAnalyzer: Initialized analyzer instance
func NewCIFailureAnalyzer(githubClient *clients.Client, logger *zap.Logger) *CIFailureAnalyzer {
	if githubClient == nil {
		panic("githubClient is required for CIFailureAnalyzer")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &CIFailureAnalyzer{
		githubClient: githubClient,
		logger:       logger,
	}
}

// AnalyzeCIFailure analyzes CI failure logs from a check suite.
// It retrieves all check runs for the given check suite, filters failed ones,
// extracts logs, and generates structured failure information.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - checkSuiteID: GitHub check suite ID
//
// Returns:
//   - *CIFailureResult: Analyzed failure information, or nil on error
//   - error: Error if check runs cannot be retrieved or no failed runs found
func (a *CIFailureAnalyzer) AnalyzeCIFailure(
	ctx context.Context,
	owner string,
	repo string,
	checkSuiteID int64,
) (*CIFailureResult, error) {
	a.logger.Info("Analyzing CI failure",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("check_suite_id", checkSuiteID),
	)

	// Retrieve all check runs for the check suite
	checkRuns, err := a.githubClient.ListCheckRunsForCheckSuite(ctx, owner, repo, checkSuiteID)
	if err != nil {
		a.logger.Error("Failed to retrieve check runs",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int64("check_suite_id", checkSuiteID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to retrieve check runs: %w", err)
	}

	// Filter failed check runs
	var failedCheckRuns []*github.CheckRun
	for _, checkRun := range checkRuns {
		if checkRun != nil && checkRun.GetConclusion() == "failure" {
			failedCheckRuns = append(failedCheckRuns, checkRun)
		}
	}

	if len(failedCheckRuns) == 0 {
		a.logger.Warn("No failed check runs found",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int64("check_suite_id", checkSuiteID),
		)
		return nil, fmt.Errorf("no failed check runs found")
	}

	// Analyze each failed check run
	var combinedLogs strings.Builder
	var failedCheckNames []string
	var failureTypes []string

	for _, checkRun := range failedCheckRuns {
		logs, failureType := a.analyzeCheckRunLogs(checkRun)

		if name := checkRun.GetName(); name != "" {
			failedCheckNames = append(failedCheckNames, name)
		}

		if logs != "" {
			if combinedLogs.Len() > 0 {
				combinedLogs.WriteString("\n\n")
			}
			combinedLogs.WriteString(fmt.Sprintf("=== %s ===\n%s", checkRun.GetName(), logs))
		}

		if failureType != FailureTypeUnknown {
			failureTypes = append(failureTypes, failureType)
		}
	}

	// Determine overall failure type (priority: test_failure > build_error > lint_error > unknown)
	overallFailureType := a.determineOverallFailureType(failureTypes)

	// Extract error summary using existing utility
	combinedLogsStr := combinedLogs.String()
	summary, excerpt := utils.ExtractErrorSummary(
		combinedLogsStr,
		utils.ErrSummaryMaxLines,
		utils.ErrSummaryMaxBytes,
	)

	result := &CIFailureResult{
		Summary:          summary,
		Excerpt:          excerpt,
		FailureType:      overallFailureType,
		FailedCheckNames: failedCheckNames,
		CombinedLogs:     combinedLogsStr,
	}

	a.logger.Info("CI failure analysis completed",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("check_suite_id", checkSuiteID),
		zap.String("failure_type", overallFailureType),
		zap.Int("failed_check_count", len(failedCheckNames)),
		zap.Int("summary_length", len(summary)),
	)

	return result, nil
}

// analyzeCheckRunLogs extracts logs and determines failure type from a single check run.
// It is a private method used internally by AnalyzeCIFailure.
//
// Parameters:
//   - checkRun: GitHub CheckRun instance (must not be nil)
//
// Returns:
//   - string: Extracted logs from CheckRun.Output
//   - string: Detected failure type
func (a *CIFailureAnalyzer) analyzeCheckRunLogs(checkRun *github.CheckRun) (string, string) {
	logs := a.getCheckRunLogsFromOutput(checkRun)
	failureType := a.detectFailureType(logs)
	return logs, failureType
}

// getCheckRunLogsFromOutput extracts logs from CheckRun.Output field.
// It prioritizes Output.Text over Output.Summary, and handles nil/empty cases.
//
// Parameters:
//   - checkRun: GitHub CheckRun instance (must not be nil)
//
// Returns:
//   - string: Extracted logs, or empty string if none available
func (a *CIFailureAnalyzer) getCheckRunLogsFromOutput(checkRun *github.CheckRun) string {
	if checkRun == nil || checkRun.Output == nil {
		return ""
	}

	output := checkRun.Output

	// Prioritize Text over Summary
	if output.Text != nil && strings.TrimSpace(*output.Text) != "" {
		return strings.TrimSpace(*output.Text)
	}

	if output.Summary != nil && strings.TrimSpace(*output.Summary) != "" {
		return strings.TrimSpace(*output.Summary)
	}

	return ""
}

// detectFailureType determines the failure type from log content using pattern matching.
// It checks patterns in priority order: test_failure > build_error > lint_error.
//
// Parameters:
//   - logs: Log content to analyze
//
// Returns:
//   - string: Detected failure type, or "unknown" if no pattern matches
func (a *CIFailureAnalyzer) detectFailureType(logs string) string {
	if logs == "" {
		return FailureTypeUnknown
	}

	// Check patterns in priority order
	if testFailurePattern.MatchString(logs) {
		return FailureTypeTestFailure
	}

	if buildErrorPattern.MatchString(logs) {
		return FailureTypeBuildError
	}

	if lintErrorPattern.MatchString(logs) {
		return FailureTypeLintError
	}

	return FailureTypeUnknown
}

// determineOverallFailureType determines the overall failure type from multiple failure types.
// It uses priority: test_failure > build_error > lint_error > unknown.
//
// Parameters:
//   - failureTypes: Slice of failure types to aggregate
//
// Returns:
//   - string: Overall failure type based on priority
func (a *CIFailureAnalyzer) determineOverallFailureType(failureTypes []string) string {
	if len(failureTypes) == 0 {
		return FailureTypeUnknown
	}

	// Check in priority order
	for _, ft := range failureTypes {
		if ft == FailureTypeTestFailure {
			return FailureTypeTestFailure
		}
	}

	for _, ft := range failureTypes {
		if ft == FailureTypeBuildError {
			return FailureTypeBuildError
		}
	}

	for _, ft := range failureTypes {
		if ft == FailureTypeLintError {
			return FailureTypeLintError
		}
	}

	return FailureTypeUnknown
}
