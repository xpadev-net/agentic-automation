package reporter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	redact "agent-runner/pkg/redact"
)

const (
	// maxRetries is the maximum number of retry attempts
	maxRetries = 5
	// httpTimeout is the timeout for HTTP requests
	httpTimeout = 30 * time.Second
	// maxResponseBodySize is the maximum size of response body to read (1MB)
	maxResponseBodySize = 1024 * 1024
)

// HTTPError represents an HTTP error response from the Operator API
type HTTPError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// MaxRetriesExceededError indicates that the maximum number of retry attempts was exceeded
type MaxRetriesExceededError struct {
	MaxAttempts int
	LastError   error
}

func (e *MaxRetriesExceededError) Error() string {
	if e.LastError != nil {
		return fmt.Sprintf("max retries (%d) exceeded: %v", e.MaxAttempts, e.LastError)
	}
	return fmt.Sprintf("max retries (%d) exceeded", e.MaxAttempts)
}

func (e *MaxRetriesExceededError) Unwrap() error {
	return e.LastError
}

// ReportRequest represents the request body for agent execution report
type ReportRequest struct {
	Status       string `json:"status"`                  // "succeeded" | "failed"
	AgentType    string `json:"agent_type"`              // "claude-code" | "cursor-agent" | "codex"
	PRNumber     *int   `json:"pr_number,omitempty"`     // Optional: PR number if succeeded
	Branch       string `json:"branch,omitempty"`        // Optional: Git branch name
	CommitSHA    string `json:"commit_sha,omitempty"`    // Optional: Git commit SHA
	ErrorMessage string `json:"error_message,omitempty"` // Optional: Error details if failed
	Logs         string `json:"logs,omitempty"`          // Optional: Agent execution logs
}

// ReportResponse represents the response for successful report
type ReportResponse struct {
	Message    string `json:"message"`
	AgentRunID int    `json:"agent_run_id"`
}

// PlanReportRequest represents the request body for plan creation/execution results.
type PlanReportRequest struct {
	Status          string `json:"status"` // "plan_created" | "plan_rejected"
	AgentType       string `json:"agent_type"`
	PlanContent     string `json:"plan_content,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
	Logs            string `json:"logs,omitempty"`
}

// Client is the Operator API client
type Client struct {
	apiURL     string
	apiToken   string
	agentRunID int
	httpClient *http.Client
}

// NewClient creates a new Operator API client
func NewClient(apiURL, apiToken string, agentRunID int) (*Client, error) {
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" {
		return nil, fmt.Errorf("apiURL cannot be empty")
	}

	// Validate API token
	apiToken = strings.TrimSpace(apiToken)
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken cannot be empty")
	}

	// Validate agent run ID
	if agentRunID <= 0 {
		return nil, fmt.Errorf("agentRunID must be a positive integer, got: %d", agentRunID)
	}

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: httpTimeout,
	}

	return &Client{
		apiURL:     apiURL,
		apiToken:   apiToken,
		agentRunID: agentRunID,
		httpClient: httpClient,
	}, nil
}

func validatePlanReportRequest(req *PlanReportRequest) error {
	if req.Status != "plan_created" && req.Status != "plan_rejected" {
		return fmt.Errorf("status must be 'plan_created' or 'plan_rejected', got: %q", req.Status)
	}
	if req.AgentType != "claude-code" && req.AgentType != "cursor-agent" && req.AgentType != "codex" {
		return fmt.Errorf("agent_type must be 'claude-code', 'cursor-agent', or 'codex', got: %q", req.AgentType)
	}
	if req.Status == "plan_created" && strings.TrimSpace(req.PlanContent) == "" {
		return fmt.Errorf("plan_content is required when status is 'plan_created'")
	}
	if req.Status == "plan_rejected" && strings.TrimSpace(req.RejectionReason) == "" {
		return fmt.Errorf("rejection_reason is required when status is 'plan_rejected'")
	}
	return nil
}

func sanitizeLogs(logs string) string {
	return redact.String(logs)
}

// validateReportRequest validates the report request
func validateReportRequest(req *ReportRequest) error {
	if req.Status != "succeeded" && req.Status != "failed" {
		return fmt.Errorf("status must be 'succeeded' or 'failed', got: %q", req.Status)
	}

	if req.AgentType != "claude-code" && req.AgentType != "cursor-agent" && req.AgentType != "codex" {
		return fmt.Errorf("agent_type must be 'claude-code', 'cursor-agent', or 'codex', got: %q", req.AgentType)
	}

	return nil
}

// buildReportURL builds the report endpoint URL
func buildReportURL(apiURL string, agentRunID int) (string, error) {
	// Remove trailing slash from API URL
	apiURL = strings.TrimSuffix(apiURL, "/")

	// Build the path
	path := fmt.Sprintf("/api/agent-runs/%d/report", agentRunID)

	// Parse the base URL to ensure it's valid
	baseURL, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("invalid API URL: %w", err)
	}

	// Construct the full URL
	fullURL := baseURL.ResolveReference(&url.URL{Path: path})

	return fullURL.String(), nil
}

// isRetryableError determines if an error should be retried
func isRetryableError(err error, statusCode int) bool {
	// Check for HTTP status codes
	if statusCode != 0 {
		// Retry on server errors (5xx) and rate limit (429)
		if statusCode == http.StatusTooManyRequests || (statusCode >= 500 && statusCode < 600) {
			return true
		}
		// Don't retry on client errors (4xx)
		if statusCode >= 400 && statusCode < 500 {
			return false
		}
	}

	// Check for network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Retry on timeouts and temporary network errors
		if netErr.Timeout() {
			return true
		}
		// Check Temporary() for compatibility
		if tempErr, ok := err.(interface {
			Temporary() bool
		}); ok && tempErr.Temporary() {
			return true
		}
	}

	// Check for DNS errors (network-related)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Default: don't retry unknown errors
	return false
}

// calculateBackoffDelay calculates the backoff delay for a given attempt number
func calculateBackoffDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 0 // First attempt is immediate
	}

	// Exponential backoff: 2^(attempt-2) * 1 second
	// attempt=2: 2^0 * 1s = 1s
	// attempt=3: 2^1 * 1s = 2s
	// attempt=4: 2^2 * 1s = 4s
	// attempt=5: 2^3 * 1s = 8s
	delay := time.Duration(math.Pow(2, float64(attempt-2))) * time.Second

	return delay
}

// buildHTTPRequest builds an HTTP POST request with the report data
func buildHTTPRequest(url string, req *ReportRequest, token string) (*http.Request, error) {
	// Encode request body as JSON
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request body: %w", err)
	}

	// Create POST request
	httpReq, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	httpReq.Header.Set("Content-Type", "application/json")

	return httpReq, nil
}

func buildPlanHTTPRequest(url string, req *PlanReportRequest, token string) (*http.Request, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode plan report body: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("failed to create plan HTTP request: %w", err)
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

// parseHTTPResponse parses the HTTP response and returns either a ReportResponse or an error
func parseHTTPResponse(resp *http.Response) (*ReportResponse, error) {
	// Read response body with size limit
	bodyReader := io.LimitReader(resp.Body, maxResponseBodySize)
	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	resp.Body.Close()

	bodyStr := string(bodyBytes)

	// Check status code
	if resp.StatusCode == http.StatusOK {
		// Success - parse response
		var reportResp ReportResponse
		if err := json.Unmarshal(bodyBytes, &reportResp); err != nil {
			return nil, fmt.Errorf("failed to decode response body: %v (body: %s)", err, bodyStr)
		}
		return &reportResp, nil
	}

	// Error response - extract error message from body
	errorMsg := bodyStr
	// Try to parse as JSON error response
	var errorResp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(bodyBytes, &errorResp) == nil {
		if errorResp.Message != "" {
			errorMsg = errorResp.Message
		} else if errorResp.Error != "" {
			errorMsg = errorResp.Error
		}
	}

	return nil, &HTTPError{
		StatusCode: resp.StatusCode,
		Message:    errorMsg,
		Body:       bodyStr,
	}
}

// sendReport sends the report request with retry logic
func sendReport(client *Client, req *ReportRequest) error {
	// Validate request
	if err := validateReportRequest(req); err != nil {
		return fmt.Errorf("invalid report request: %w", err)
	}

	// Build URL
	reportURL, err := buildReportURL(client.apiURL, client.agentRunID)
	if err != nil {
		return fmt.Errorf("failed to build report URL: %w", err)
	}

	var lastErr error

	// Retry loop (max 5 attempts)
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Build HTTP request
		httpReq, err := buildHTTPRequest(reportURL, req, client.apiToken)
		if err != nil {
			// Request building error is not retryable
			return fmt.Errorf("failed to build HTTP request: %w", err)
		}

		// Send request
		resp, err := client.httpClient.Do(httpReq)
		if err != nil {
			// Network error - check if retryable
			statusCode := 0
			var httpErr *HTTPError
			if errors.As(err, &httpErr) {
				statusCode = httpErr.StatusCode
			}

			if isRetryableError(err, statusCode) {
				lastErr = err
				// Log retry attempt
				fmt.Fprintf(os.Stderr, "Retry attempt %d/%d failed: %v\n", attempt, maxRetries, err)

				// Don't wait after the last attempt
				if attempt >= maxRetries {
					break
				}

				// Calculate backoff delay
				backoff := calculateBackoffDelay(attempt + 1) // +1 for next attempt
				fmt.Fprintf(os.Stderr, "Waiting %v before retry...\n", backoff)
				time.Sleep(backoff)

				continue
			}

			// Non-retryable error
			return fmt.Errorf("non-retryable error: %w", err)
		}

		// Parse response
		reportResp, err := parseHTTPResponse(resp)
		if err != nil {
			// Check if it's an HTTP error
			var httpErr *HTTPError
			if errors.As(err, &httpErr) {
				// Check if retryable
				if isRetryableError(httpErr, httpErr.StatusCode) {
					lastErr = httpErr
					// Log retry attempt
					fmt.Fprintf(os.Stderr, "Retry attempt %d/%d failed: %v\n", attempt, maxRetries, httpErr)

					// Don't wait after the last attempt
					if attempt >= maxRetries {
						break
					}

					// Calculate backoff delay
					backoff := calculateBackoffDelay(attempt + 1) // +1 for next attempt
					fmt.Fprintf(os.Stderr, "Waiting %v before retry...\n", backoff)
					time.Sleep(backoff)

					continue
				}

				// Non-retryable HTTP error
				return httpErr
			}

			// Other parsing errors are not retryable
			return fmt.Errorf("failed to parse response: %w", err)
		}

		// Success
		if attempt > 1 {
			fmt.Fprintf(os.Stderr, "Report sent successfully after %d attempts\n", attempt)
		}
		fmt.Fprintf(os.Stderr, "Report received: %s (AgentRun ID: %d)\n", reportResp.Message, reportResp.AgentRunID)
		return nil
	}

	// All retries exhausted
	return &MaxRetriesExceededError{
		MaxAttempts: maxRetries,
		LastError:   lastErr,
	}
}

func sendPlanReport(client *Client, req *PlanReportRequest) error {
	if err := validatePlanReportRequest(req); err != nil {
		return fmt.Errorf("invalid plan report request: %w", err)
	}
	reportURL, err := buildReportURL(client.apiURL, client.agentRunID)
	if err != nil {
		return fmt.Errorf("failed to build report URL: %w", err)
	}
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		httpReq, err := buildPlanHTTPRequest(reportURL, req, client.apiToken)
		if err != nil {
			return fmt.Errorf("failed to build plan HTTP request: %w", err)
		}
		resp, err := client.httpClient.Do(httpReq)
		if err != nil {
			statusCode := 0
			var httpErr *HTTPError
			if errors.As(err, &httpErr) {
				statusCode = httpErr.StatusCode
			}
			if isRetryableError(err, statusCode) {
				lastErr = err
				fmt.Fprintf(os.Stderr, "Plan report retry attempt %d/%d failed: %v\n", attempt, maxRetries, err)
				if attempt >= maxRetries {
					break
				}
				backoff := calculateBackoffDelay(attempt + 1)
				fmt.Fprintf(os.Stderr, "Waiting %v before retry...\n", backoff)
				time.Sleep(backoff)
				continue
			}
			return fmt.Errorf("non-retryable error: %w", err)
		}
		reportResp, err := parseHTTPResponse(resp)
		if err != nil {
			var httpErr *HTTPError
			if errors.As(err, &httpErr) {
				if isRetryableError(httpErr, httpErr.StatusCode) {
					lastErr = httpErr
					fmt.Fprintf(os.Stderr, "Plan report retry attempt %d/%d failed: %v\n", attempt, maxRetries, httpErr)
					if attempt >= maxRetries {
						break
					}
					backoff := calculateBackoffDelay(attempt + 1)
					fmt.Fprintf(os.Stderr, "Waiting %v before retry...\n", backoff)
					time.Sleep(backoff)
					continue
				}
				return httpErr
			}
			return fmt.Errorf("failed to parse plan report response: %w", err)
		}
		if attempt > 1 {
			fmt.Fprintf(os.Stderr, "Plan report sent successfully after %d attempts\n", attempt)
		}
		fmt.Fprintf(os.Stderr, "Plan report received: %s (AgentRun ID: %d)\n", reportResp.Message, reportResp.AgentRunID)
		return nil
	}
	return &MaxRetriesExceededError{MaxAttempts: maxRetries, LastError: lastErr}
}

// ReportSuccess reports successful execution to the Operator API
func (c *Client) ReportSuccess(prNumber int, branch, commitSHA, agentType string) error {
	req := &ReportRequest{
		Status:    "succeeded",
		AgentType: agentType,
		PRNumber:  &prNumber,
		Branch:    branch,
		CommitSHA: commitSHA,
	}

	if err := sendReport(c, req); err != nil {
		return fmt.Errorf("failed to report success: %w", err)
	}

	return nil
}

// ReportFailure reports failed execution to the Operator API
func (c *Client) ReportFailure(errorMsg, logs, agentType string) error {
	req := &ReportRequest{
		Status:       "failed",
		AgentType:    agentType,
		ErrorMessage: errorMsg,
		Logs:         logs,
	}

	if err := sendReport(c, req); err != nil {
		return fmt.Errorf("failed to report failure: %w", err)
	}

	return nil
}

// ReportPlanCreation reports a successfully generated plan to the Operator API.
func (c *Client) ReportPlanCreation(planContent, agentType, logs string) error {
	preview := strings.TrimSpace(planContent)
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	req := &PlanReportRequest{
		Status:      "plan_created",
		AgentType:   agentType,
		PlanContent: planContent,
		Logs:        sanitizeLogs(logs),
	}
	if err := sendPlanReport(c, req); err != nil {
		return fmt.Errorf("failed to report plan creation (preview: %s): %w", preview, err)
	}
	return nil
}

// ReportPlanRejection reports a rejected plan to the Operator API.
func (c *Client) ReportPlanRejection(reason, agentType, logs string) error {
	req := &PlanReportRequest{
		Status:          "plan_rejected",
		AgentType:       agentType,
		RejectionReason: reason,
		Logs:            sanitizeLogs(logs),
	}
	if err := sendPlanReport(c, req); err != nil {
		return fmt.Errorf("failed to report plan rejection: %w", err)
	}
	return nil
}
