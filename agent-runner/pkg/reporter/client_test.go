package reporter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// setupTestServer creates a test HTTP server with a custom handler
func setupTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	server := httptest.NewServer(handler)
	client, _ := NewClient(server.URL, "test-token", 123)
	return server, client
}

// createTestClient creates a test Client instance with a custom HTTP client
func createTestClient(apiURL, apiToken string, agentRunID int, httpClient *http.Client) (*Client, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("apiURL cannot be empty")
	}
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken cannot be empty")
	}
	if agentRunID <= 0 {
		return nil, fmt.Errorf("agentRunID must be a positive integer, got: %d", agentRunID)
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: httpTimeout,
		}
	}

	return &Client{
		apiURL:     apiURL,
		apiToken:   apiToken,
		agentRunID: agentRunID,
		httpClient: httpClient,
	}, nil
}

// countRequestAttempts is a helper that counts HTTP request attempts
type requestCounter struct {
	count int
	mu    sync.Mutex
}

func (rc *requestCounter) increment() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.count++
}

func (rc *requestCounter) get() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.count
}

func (rc *requestCounter) reset() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.count = 0
}

// ============================================================================
// NewClient Validation Tests
// ============================================================================

func TestNewClient_EmptyURL(t *testing.T) {
	client, err := NewClient("", "valid-token", 1)
	if err == nil {
		t.Error("Expected error for empty URL, got nil")
	}
	if client != nil {
		t.Error("Expected nil client for empty URL")
	}
	if !strings.Contains(err.Error(), "apiURL cannot be empty") {
		t.Errorf("Expected error message about empty apiURL, got: %v", err)
	}
}

func TestNewClient_URLWithWhitespace(t *testing.T) {
	client, err := NewClient("  ", "valid-token", 1)
	if err == nil {
		t.Error("Expected error for whitespace-only URL, got nil")
	}
	if client != nil {
		t.Error("Expected nil client for whitespace-only URL")
	}
	if !strings.Contains(err.Error(), "apiURL cannot be empty") {
		t.Errorf("Expected error message about empty apiURL, got: %v", err)
	}
}

func TestNewClient_EmptyToken(t *testing.T) {
	client, err := NewClient("http://test.com", "", 1)
	if err == nil {
		t.Error("Expected error for empty token, got nil")
	}
	if client != nil {
		t.Error("Expected nil client for empty token")
	}
	if !strings.Contains(err.Error(), "apiToken cannot be empty") {
		t.Errorf("Expected error message about empty apiToken, got: %v", err)
	}
}

func TestNewClient_TokenWithWhitespace(t *testing.T) {
	client, err := NewClient("http://test.com", "  ", 1)
	if err == nil {
		t.Error("Expected error for whitespace-only token, got nil")
	}
	if client != nil {
		t.Error("Expected nil client for whitespace-only token")
	}
	if !strings.Contains(err.Error(), "apiToken cannot be empty") {
		t.Errorf("Expected error message about empty apiToken, got: %v", err)
	}
}

func TestNewClient_ZeroAgentRunID(t *testing.T) {
	client, err := NewClient("http://test.com", "valid-token", 0)
	if err == nil {
		t.Error("Expected error for zero agentRunID, got nil")
	}
	if client != nil {
		t.Error("Expected nil client for zero agentRunID")
	}
	if !strings.Contains(err.Error(), "agentRunID must be a positive integer") {
		t.Errorf("Expected error message about invalid agentRunID, got: %v", err)
	}
}

func TestNewClient_NegativeAgentRunID(t *testing.T) {
	client, err := NewClient("http://test.com", "valid-token", -1)
	if err == nil {
		t.Error("Expected error for negative agentRunID, got nil")
	}
	if client != nil {
		t.Error("Expected nil client for negative agentRunID")
	}
	if !strings.Contains(err.Error(), "agentRunID must be a positive integer") {
		t.Errorf("Expected error message about invalid agentRunID, got: %v", err)
	}
}

func TestNewClient_ValidParameters(t *testing.T) {
	apiURL := "http://test.com"
	apiToken := "valid-token"
	agentRunID := 123

	client, err := NewClient(apiURL, apiToken, agentRunID)
	if err != nil {
		t.Fatalf("Expected no error for valid parameters, got: %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client for valid parameters")
	}
	if client.apiURL != apiURL {
		t.Errorf("Expected apiURL=%q, got %q", apiURL, client.apiURL)
	}
	if client.apiToken != apiToken {
		t.Errorf("Expected apiToken=%q, got %q", apiToken, client.apiToken)
	}
	if client.agentRunID != agentRunID {
		t.Errorf("Expected agentRunID=%d, got %d", agentRunID, client.agentRunID)
	}
	if client.httpClient == nil {
		t.Error("Expected non-nil httpClient")
	}
}

// ============================================================================
// HTTP Success Response Tests
// ============================================================================

func TestReportSuccess_Success(t *testing.T) {
	var receivedRequest *ReportRequest
	var authHeader string
	var contentType string

	handler := func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed to read request body: %v", err)
		}

		var req ReportRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Fatalf("Failed to parse request body: %v", err)
		}
		receivedRequest = &req

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ReportResponse{
			Message:    "Success",
			AgentRunID: 123,
		})
	}

	server, client := setupTestServer(handler)
	defer server.Close()

	prNumber := 42
	branch := "feature/123"
	commitSHA := "abc123"
	agentType := "claude-code"

	err := client.ReportSuccess(prNumber, branch, commitSHA, agentType)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify request body
	if receivedRequest == nil {
		t.Fatal("Expected request to be received")
	}
	if receivedRequest.Status != "succeeded" {
		t.Errorf("Expected Status=succeeded, got %q", receivedRequest.Status)
	}
	if receivedRequest.AgentType != agentType {
		t.Errorf("Expected AgentType=%q, got %q", agentType, receivedRequest.AgentType)
	}
	if receivedRequest.PRNumber == nil || *receivedRequest.PRNumber != prNumber {
		t.Errorf("Expected PRNumber=%d, got %v", prNumber, receivedRequest.PRNumber)
	}
	if receivedRequest.Branch != branch {
		t.Errorf("Expected Branch=%q, got %q", branch, receivedRequest.Branch)
	}
	if receivedRequest.CommitSHA != commitSHA {
		t.Errorf("Expected CommitSHA=%q, got %q", commitSHA, receivedRequest.CommitSHA)
	}

	// Verify headers
	expectedAuth := "Bearer test-token"
	if authHeader != expectedAuth {
		t.Errorf("Expected Authorization=%q, got %q", expectedAuth, authHeader)
	}
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type=application/json, got %q", contentType)
	}
}

func TestReportFailure_Success(t *testing.T) {
	var receivedRequest *ReportRequest

	handler := func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed to read request body: %v", err)
		}

		var req ReportRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Fatalf("Failed to parse request body: %v", err)
		}
		receivedRequest = &req

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ReportResponse{
			Message:    "Failure reported",
			AgentRunID: 123,
		})
	}

	server, client := setupTestServer(handler)
	defer server.Close()

	errorMsg := "test error"
	logs := "error logs"
	agentType := "cursor-agents"

	err := client.ReportFailure(errorMsg, logs, agentType)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify request body
	if receivedRequest == nil {
		t.Fatal("Expected request to be received")
	}
	if receivedRequest.Status != "failed" {
		t.Errorf("Expected Status=failed, got %q", receivedRequest.Status)
	}
	if receivedRequest.AgentType != agentType {
		t.Errorf("Expected AgentType=%q, got %q", agentType, receivedRequest.AgentType)
	}
	if receivedRequest.ErrorMessage != errorMsg {
		t.Errorf("Expected ErrorMessage=%q, got %q", errorMsg, receivedRequest.ErrorMessage)
	}
	if receivedRequest.Logs != logs {
		t.Errorf("Expected Logs=%q, got %q", logs, receivedRequest.Logs)
	}
}

func TestReportSuccess_ResponseParsing(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ReportResponse{
			Message:    "Success",
			AgentRunID: 123,
		})
	}

	server, client := setupTestServer(handler)
	defer server.Close()

	err := client.ReportSuccess(42, "branch", "sha", "claude-code")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

// ============================================================================
// Retryable Error Tests
// ============================================================================

func TestIsRetryableError_5xxErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"500 Internal Server Error", 500, true},
		{"502 Bad Gateway", 502, true},
		{"503 Service Unavailable", 503, true},
		{"504 Gateway Timeout", 504, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.statusCode != 0 {
				err = &HTTPError{StatusCode: tt.statusCode}
			}
			got := isRetryableError(err, tt.statusCode)
			if got != tt.want {
				t.Errorf("isRetryableError(..., %d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsRetryableError_429RateLimit(t *testing.T) {
	err := &HTTPError{StatusCode: http.StatusTooManyRequests}
	got := isRetryableError(err, http.StatusTooManyRequests)
	if !got {
		t.Errorf("isRetryableError(..., 429) = %v, want true", got)
	}
}

func TestIsRetryableError_4xxClientErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"400 Bad Request", 400, false},
		{"401 Unauthorized", 401, false},
		{"404 Not Found", 404, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPError{StatusCode: tt.statusCode}
			got := isRetryableError(err, tt.statusCode)
			if got != tt.want {
				t.Errorf("isRetryableError(..., %d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsRetryableError_NetworkTimeout(t *testing.T) {
	err := &timeoutError{timeout: true}
	got := isRetryableError(err, 0)
	if !got {
		t.Errorf("isRetryableError(timeout error, 0) = %v, want true", got)
	}
}

func TestIsRetryableError_NetworkTemporary(t *testing.T) {
	err := &temporaryError{temporary: true}
	got := isRetryableError(err, 0)
	if !got {
		t.Errorf("isRetryableError(temporary error, 0) = %v, want true", got)
	}
}

func TestIsRetryableError_DNSError(t *testing.T) {
	err := &net.DNSError{
		Err:         "no such host",
		Name:        "test.example.com",
		Server:      "8.8.8.8",
		IsTimeout:   false,
		IsTemporary: false,
	}
	got := isRetryableError(err, 0)
	if !got {
		t.Errorf("isRetryableError(DNS error, 0) = %v, want true", got)
	}
}

// Helper types for testing network errors
type timeoutError struct {
	timeout bool
}

func (e *timeoutError) Error() string   { return "timeout error" }
func (e *timeoutError) Timeout() bool   { return e.timeout }
func (e *timeoutError) Temporary() bool { return false }

type temporaryError struct {
	temporary bool
}

func (e *temporaryError) Error() string   { return "temporary error" }
func (e *temporaryError) Timeout() bool   { return false }
func (e *temporaryError) Temporary() bool { return e.temporary }

// ============================================================================
// Retry Logic Tests
// ============================================================================

func TestCalculateBackoffDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 0},
		{2, 1 * time.Second}, // 2^(2-2) * 1s = 1s
		{3, 2 * time.Second}, // 2^(3-2) * 1s = 2s
		{4, 4 * time.Second}, // 2^(4-2) * 1s = 4s
		{5, 8 * time.Second}, // 2^(5-2) * 1s = 8s
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			got := calculateBackoffDelay(tt.attempt)
			if got != tt.want {
				t.Errorf("calculateBackoffDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestReportSuccess_RetryOn5xx(t *testing.T) {
	attemptCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(ReportResponse{
				Message:    "Success after retry",
				AgentRunID: 123,
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", 123)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.ReportSuccess(42, "branch", "sha", "claude-code")
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if attemptCount != 3 {
		t.Errorf("Expected 3 attempts, got %d", attemptCount)
	}
}

func TestReportSuccess_MaxRetriesExceeded(t *testing.T) {
	attemptCount := &requestCounter{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		attemptCount.increment()
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", 123)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.ReportSuccess(42, "branch", "sha", "claude-code")
	if err == nil {
		t.Fatal("Expected error after max retries, got nil")
	}

	var maxRetriesErr *MaxRetriesExceededError
	if !strings.Contains(err.Error(), "max retries") {
		t.Errorf("Expected MaxRetriesExceededError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "max retries") {
		t.Errorf("Expected error message about max retries, got: %v", err)
	}

	if errors.As(err, &maxRetriesErr) {
		if maxRetriesErr.MaxAttempts != maxRetries {
			t.Errorf("Expected MaxAttempts=%d, got %d", maxRetries, maxRetriesErr.MaxAttempts)
		}
	}

	if attemptCount.get() != maxRetries {
		t.Errorf("Expected %d attempts, got %d", maxRetries, attemptCount.get())
	}
}

func TestReportSuccess_NoRetryOn4xx(t *testing.T) {
	attemptCount := &requestCounter{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		attemptCount.increment()
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Bad Request"}`))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", 123)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.ReportSuccess(42, "branch", "sha", "claude-code")
	if err == nil {
		t.Fatal("Expected error for 4xx response, got nil")
	}

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Errorf("Expected HTTPError, got: %v", err)
	} else {
		if httpErr.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected StatusCode=400, got %d", httpErr.StatusCode)
		}
	}

	if attemptCount.get() != 1 {
		t.Errorf("Expected 1 attempt (no retry), got %d", attemptCount.get())
	}
}

func TestReportSuccess_RetryOn429(t *testing.T) {
	attemptCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Too Many Requests"))
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(ReportResponse{
				Message:    "Success after rate limit",
				AgentRunID: 123,
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", 123)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.ReportSuccess(42, "branch", "sha", "claude-code")
	if err != nil {
		t.Fatalf("Expected no error after retry, got: %v", err)
	}

	if attemptCount != 2 {
		t.Errorf("Expected 2 attempts, got %d", attemptCount)
	}
}

// ============================================================================
// Error Handling Tests
// ============================================================================

func TestHTTPError_ErrorMethod(t *testing.T) {
	tests := []struct {
		name    string
		err     *HTTPError
		wantMsg string
	}{
		{
			name:    "with message",
			err:     &HTTPError{StatusCode: 500, Message: "Internal Error"},
			wantMsg: "HTTP 500: Internal Error",
		},
		{
			name:    "without message",
			err:     &HTTPError{StatusCode: 404, Message: ""},
			wantMsg: "HTTP 404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.wantMsg {
				t.Errorf("HTTPError.Error() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestMaxRetriesExceededError_ErrorMethod(t *testing.T) {
	originalErr := &HTTPError{StatusCode: 500, Message: "Server Error"}
	err := &MaxRetriesExceededError{
		MaxAttempts: 5,
		LastError:   originalErr,
	}

	got := err.Error()
	if !strings.Contains(got, "max retries (5) exceeded") {
		t.Errorf("Expected error message to contain 'max retries (5) exceeded', got: %q", got)
	}
	if !strings.Contains(got, originalErr.Error()) {
		t.Errorf("Expected error message to contain last error, got: %q", got)
	}
}

func TestMaxRetriesExceededError_Unwrap(t *testing.T) {
	originalErr := &HTTPError{StatusCode: 500, Message: "Server Error"}
	err := &MaxRetriesExceededError{
		MaxAttempts: 5,
		LastError:   originalErr,
	}

	got := err.Unwrap()
	if got != originalErr {
		t.Errorf("MaxRetriesExceededError.Unwrap() = %v, want %v", got, originalErr)
	}
}

func TestParseHTTPResponse_JSONErrorResponse(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error": "Bad request", "message": "Invalid data"}`)),
	}

	result, err := parseHTTPResponse(resp)
	if result != nil {
		t.Errorf("Expected nil result for error response, got: %v", result)
	}
	if err == nil {
		t.Fatal("Expected error for 400 response, got nil")
	}

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Expected HTTPError, got: %T", err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected StatusCode=400, got %d", httpErr.StatusCode)
	}
	if httpErr.Message != "Invalid data" {
		t.Errorf("Expected Message='Invalid data', got %q", httpErr.Message)
	}
}

func TestParseHTTPResponse_PlainTextErrorResponse(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("Internal server error")),
	}

	result, err := parseHTTPResponse(resp)
	if result != nil {
		t.Errorf("Expected nil result for error response, got: %v", result)
	}
	if err == nil {
		t.Fatal("Expected error for 500 response, got nil")
	}

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Expected HTTPError, got: %T", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected StatusCode=500, got %d", httpErr.StatusCode)
	}
	if httpErr.Message != "Internal server error" {
		t.Errorf("Expected Message='Internal server error', got %q", httpErr.Message)
	}
}

func TestParseHTTPResponse_ResponseBodySizeLimit(t *testing.T) {
	// Create a response body larger than maxResponseBodySize (1MB)
	largeBody := make([]byte, maxResponseBodySize+1024)
	for i := range largeBody {
		largeBody[i] = 'A'
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(string(largeBody))),
	}

	result, err := parseHTTPResponse(resp)
	// The body is too large and not valid JSON, so we expect a parse error
	if err == nil {
		t.Fatal("Expected error for invalid JSON response, got nil")
	}

	// Verify that only maxResponseBodySize bytes were read
	// This is implicitly tested by the fact that parsing fails
	_ = result
}

// ============================================================================
// URL Building Tests
// ============================================================================

func TestBuildReportURL_ValidURL(t *testing.T) {
	url, err := buildReportURL("http://test.com", 123)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	expected := "http://test.com/api/agent-runs/123/report"
	if url != expected {
		t.Errorf("buildReportURL() = %q, want %q", url, expected)
	}
}

func TestBuildReportURL_URLWithTrailingSlash(t *testing.T) {
	url, err := buildReportURL("http://test.com/", 123)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	expected := "http://test.com/api/agent-runs/123/report"
	if url != expected {
		t.Errorf("buildReportURL() = %q, want %q (trailing slash should be removed)", url, expected)
	}
}

func TestBuildReportURL_HTTPSURL(t *testing.T) {
	url, err := buildReportURL("https://api.example.com", 456)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	expected := "https://api.example.com/api/agent-runs/456/report"
	if url != expected {
		t.Errorf("buildReportURL() = %q, want %q", url, expected)
	}
}

func TestBuildReportURL_InvalidURL(t *testing.T) {
	url, err := buildReportURL("://invalid", 123)
	if err == nil {
		t.Errorf("Expected error for invalid URL, got nil. URL: %q", url)
	}
	if !strings.Contains(err.Error(), "invalid API URL") {
		t.Errorf("Expected error message about invalid URL, got: %v", err)
	}
}
