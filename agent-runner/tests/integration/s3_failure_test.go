package integration

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-runner/pkg/storage"
)

// setupS3FailureEnv sets up environment variables with invalid S3 endpoint for failure testing
func setupS3FailureEnv(t *testing.T, maxRetries int) (cleanup func()) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>deterministic S3 fixture failure</Message></Error>`))
	}))
	t.Cleanup(server.Close)

	originalEnv := make(map[string]string)

	envVars := map[string]string{
		"S3_ENDPOINT":          server.URL,
		"S3_REGION":            "us-east-1",
		"S3_BUCKET":            "test-bucket",
		"S3_ACCESS_KEY_ID":     "test-key",
		"S3_SECRET_ACCESS_KEY": "test-secret",
		"S3_USE_PATH_STYLE":    "true",
		"S3_MAX_RETRIES":       strconv.Itoa(maxRetries),
	}

	for key, value := range envVars {
		if original, exists := os.LookupEnv(key); exists {
			originalEnv[key] = original
		}
		os.Setenv(key, value)
	}

	cleanup = func() {
		for key, value := range originalEnv {
			os.Setenv(key, value)
		}
		for key := range envVars {
			if _, exists := originalEnv[key]; !exists {
				os.Unsetenv(key)
			}
		}
	}

	t.Cleanup(cleanup)
	return cleanup
}

// verifyS3FailureReport verifies that Operator API mock received an appropriate failure report
func verifyS3FailureReport(t *testing.T, mock *OperatorAPIMock, expectedMessage string) {
	t.Helper()

	require.NotNil(t, mock, "OperatorAPIMock should not be nil")
	require.Greater(t, len(mock.ReceivedRequests), 0, "ReportFailure should be called")

	lastRequest := mock.ReceivedRequests[len(mock.ReceivedRequests)-1]
	assert.Equal(t, "failed", lastRequest.Status, "Status should be 'failed'")
	assert.Contains(t, lastRequest.ErrorMessage, expectedMessage, "ErrorMessage should contain expected message")
	assert.Contains(t, strings.ToLower(lastRequest.ErrorMessage), "s3", "ErrorMessage should contain 's3'")
}

// verifyS3ErrorType verifies that the error is related to S3 failure
func verifyS3ErrorType(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err, "Error should not be nil")

	errMsg := strings.ToLower(err.Error())

	// Check for MaxRetriesExceededError
	var maxRetriesErr *storage.MaxRetriesExceededError
	if errors.As(err, &maxRetriesErr) {
		assert.Contains(t, errMsg, "max retries", "Error should mention max retries")
		return
	}

	// Check for network errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// DNS error is acceptable for invalid endpoint
		return
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Connection error is acceptable for invalid endpoint
		return
	}

	// Check if error message contains S3-related keywords
	assert.True(t,
		strings.Contains(errMsg, "s3") ||
			strings.Contains(errMsg, "connection") ||
			strings.Contains(errMsg, "network") ||
			strings.Contains(errMsg, "max retries"),
		"Error should be related to S3, connection, network, or max retries: %v", err,
	)
}

// TestS3Failure_SaveSessionFailure tests SaveSession failure scenario
func TestS3Failure_SaveSessionFailure(t *testing.T) {
	// Setup environment with invalid S3 endpoint
	setupS3FailureEnv(t, 1) // MaxRetries=1 to speed up test

	// Test SaveSession directly with invalid S3 configuration
	err := storage.SaveSession(testAgentRunID, "claude-code")
	require.Error(t, err, "SaveSession should fail with invalid S3 endpoint")

	// Verify error type
	verifyS3ErrorType(t, err)

	// Verify error message contains session save context
	errMsg := strings.ToLower(err.Error())
	assert.True(t,
		strings.Contains(errMsg, "session") ||
			strings.Contains(errMsg, "save") ||
			strings.Contains(errMsg, "s3"),
		"Error message should mention session, save, or S3: %v", err,
	)
}

// TestS3Failure_SaveSessionFailure_Integration tests SaveSession failure in full integration scenario
func TestS3Failure_SaveSessionFailure_Integration(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		t.Skip("git functions not implemented yet")
		return
	}

	// Setup test repository
	repoDir, _ := setupTestRepo(t)

	// Create minimal manifest (no hooks/validations to simplify test)
	manifestYAML := `version: "1.0"
`
	require.NoError(t, createManifestFile(t, repoDir, manifestYAML))

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)
	githubServer, githubMock := setupMockGitHubAPI(t, 1)
	_ = githubServer // Will be used when git functions support configurable API URL

	// Setup environment with invalid S3 endpoint
	setupS3FailureEnv(t, 1) // MaxRetries=1 to speed up test
	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// Verify setup
	assert.NotNil(t, operatorMock)
	assert.NotNil(t, githubMock)
	assert.DirExists(t, repoDir)

	// TODO: Execute Run() function once main package can be imported
	// Note: Go doesn't allow importing main packages directly, so we need one of:
	// 1. Move Run() to an internal/runner package
	// 2. Use os/exec to run the binary and check exit code
	// 3. Create a main_test.go in the main package
	//
	// For now, we test SaveSession directly which verifies the S3 failure behavior.
	// The full integration test with Run() will be added once the package structure allows it.
	//
	// Expected behavior when Run() is called:
	// - Run() should fail at SaveSession step (step 13)
	// - Error should contain "session save failed"
	// - ReportFailure should be called with appropriate error message
	//
	// Test SaveSession directly to verify S3 failure behavior
	err := storage.SaveSession(testAgentRunID, "claude-code")
	require.Error(t, err, "SaveSession should fail with invalid S3 endpoint")
	verifyS3ErrorType(t, err)
}

// TestS3Failure_RestoreSessionFailure tests RestoreSession failure scenario
func TestS3Failure_RestoreSessionFailure(t *testing.T) {
	// Setup environment with invalid S3 endpoint
	setupS3FailureEnv(t, 1) // MaxRetries=1 to speed up test

	// Test RestoreSession directly with invalid S3 configuration
	err := storage.RestoreSession(testAgentRunID, 1)
	require.Error(t, err, "RestoreSession should fail with invalid S3 endpoint")

	// Verify error type
	verifyS3ErrorType(t, err)

	// Verify error message contains session restore context
	errMsg := strings.ToLower(err.Error())
	assert.True(t,
		strings.Contains(errMsg, "session") ||
			strings.Contains(errMsg, "restore") ||
			strings.Contains(errMsg, "s3"),
		"Error message should mention session, restore, or S3: %v", err,
	)
}

// TestS3Failure_RestoreSessionFailure_Integration tests RestoreSession failure in full integration scenario
func TestS3Failure_RestoreSessionFailure_Integration(t *testing.T) {
	// Check if git functions are implemented
	if !checkGitFunctionsImplemented(t) {
		t.Skip("git functions not implemented yet")
		return
	}

	// Setup test repository
	repoDir, _ := setupTestRepo(t)

	// Setup mock APIs
	operatorServer, operatorMock := setupMockOperatorAPI(t)
	githubServer, githubMock := setupMockGitHubAPI(t, 1)
	_ = githubServer

	// Setup environment with invalid S3 endpoint
	setupS3FailureEnv(t, 1) // MaxRetries=1 to speed up test

	// Set RETRY_COUNT > 0 to trigger RestoreSession
	originalRetryCount := os.Getenv("RETRY_COUNT")
	os.Setenv("RETRY_COUNT", "1")
	t.Cleanup(func() {
		if originalRetryCount != "" {
			os.Setenv("RETRY_COUNT", originalRetryCount)
		} else {
			os.Unsetenv("RETRY_COUNT")
		}
	})

	setupTestEnv(t, operatorServer.URL, testOperatorToken, "claude-code", repoDir)

	// Verify setup
	assert.NotNil(t, operatorMock)
	assert.NotNil(t, githubMock)
	assert.DirExists(t, repoDir)

	// TODO: Execute Run() function once main package can be imported
	// See TestS3Failure_SaveSessionFailure_Integration for details on limitations
	//
	// Expected behavior when Run() is called with RETRY_COUNT > 0:
	// - Run() should fail at RestoreSession step (step 5)
	// - Error should contain "session restore failed"
	// - ReportFailure should be called with appropriate error message
	//
	// Test RestoreSession directly to verify S3 failure behavior
	err := storage.RestoreSession(testAgentRunID, 1)
	require.Error(t, err, "RestoreSession should fail with invalid S3 endpoint")
	verifyS3ErrorType(t, err)
}

// TestS3Failure_RestoreSessionSkippedOnInitialRun tests that RestoreSession is skipped on initial run
func TestS3Failure_RestoreSessionSkippedOnInitialRun(t *testing.T) {
	// Setup environment with invalid S3 endpoint
	setupS3FailureEnv(t, 1)

	// Test RestoreSession with RETRY_COUNT=0
	// It should return nil (skip) even with invalid S3 endpoint
	err := storage.RestoreSession(testAgentRunID, 0)
	assert.NoError(t, err, "RestoreSession should skip (return nil) when retry_count is 0")
}
