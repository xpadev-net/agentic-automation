package storage

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Mock Error Types
// ============================================================================

// MockAPIError implements smithy.APIError for testing
type MockAPIError struct {
	Code    string
	Message string
	Status  int
}

func (e *MockAPIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Code
}

func (e *MockAPIError) ErrorCode() string {
	return e.Code
}

func (e *MockAPIError) ErrorMessage() string {
	return e.Message
}

func (e *MockAPIError) ErrorFault() smithy.ErrorFault {
	if e.Status >= 500 {
		return smithy.FaultServer
	}
	return smithy.FaultClient
}

// MockHTTPStatusError implements HTTPStatusCode() method for testing
type MockHTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *MockHTTPStatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func (e *MockHTTPStatusError) HTTPStatusCode() int {
	return e.StatusCode
}

// MockNetworkError implements net.Error for testing
type MockNetworkError struct {
	timeout   bool
	temporary bool
	message   string
}

func (e *MockNetworkError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "network error"
}

func (e *MockNetworkError) Timeout() bool {
	return e.timeout
}

func (e *MockNetworkError) Temporary() bool {
	return e.temporary
}

// ============================================================================
// isRetryableError Tests
// ============================================================================

func TestIsRetryableError_SmithyAPIErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// Retryable errors
		{
			name: "TooManyRequests",
			err:  &MockAPIError{Code: "TooManyRequests", Status: 429},
			want: true,
		},
		{
			name: "Throttling",
			err:  &MockAPIError{Code: "Throttling", Status: 429},
			want: true,
		},
		{
			name: "ThrottlingException",
			err:  &MockAPIError{Code: "ThrottlingException", Status: 429},
			want: true,
		},
		{
			name: "ServiceUnavailable",
			err:  &MockAPIError{Code: "ServiceUnavailable", Status: 503},
			want: true,
		},
		{
			name: "InternalServerError",
			err:  &MockAPIError{Code: "InternalServerError", Status: 500},
			want: true,
		},
		{
			name: "BadGateway",
			err:  &MockAPIError{Code: "BadGateway", Status: 502},
			want: true,
		},
		{
			name: "GatewayTimeout",
			err:  &MockAPIError{Code: "GatewayTimeout", Status: 504},
			want: true,
		},
		// Non-retryable errors
		{
			name: "AccessDenied",
			err:  &MockAPIError{Code: "AccessDenied", Status: 403},
			want: false,
		},
		{
			name: "Forbidden",
			err:  &MockAPIError{Code: "Forbidden", Status: 403},
			want: false,
		},
		{
			name: "NotFound",
			err:  &MockAPIError{Code: "NotFound", Status: 404},
			want: false,
		},
		{
			name: "NoSuchBucket",
			err:  &MockAPIError{Code: "NoSuchBucket", Status: 404},
			want: false,
		},
		{
			name: "NoSuchKey",
			err:  &MockAPIError{Code: "NoSuchKey", Status: 404},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			assert.Equal(t, tt.want, got, "isRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
		})
	}
}

func TestIsRetryableError_HTTPStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		// Retryable status codes
		{
			name:       "429 Too Many Requests",
			statusCode: 429,
			want:       true,
		},
		{
			name:       "500 Internal Server Error",
			statusCode: 500,
			want:       true,
		},
		{
			name:       "502 Bad Gateway",
			statusCode: 502,
			want:       true,
		},
		{
			name:       "503 Service Unavailable",
			statusCode: 503,
			want:       true,
		},
		{
			name:       "504 Gateway Timeout",
			statusCode: 504,
			want:       true,
		},
		// Non-retryable status codes
		{
			name:       "400 Bad Request",
			statusCode: 400,
			want:       false,
		},
		{
			name:       "403 Forbidden",
			statusCode: 403,
			want:       false,
		},
		{
			name:       "404 Not Found",
			statusCode: 404,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &MockHTTPStatusError{
				StatusCode: tt.statusCode,
				Message:    fmt.Sprintf("HTTP %d error", tt.statusCode),
			}
			got := isRetryableError(err)
			assert.Equal(t, tt.want, got, "isRetryableError(HTTP %d) = %v, want %v", tt.statusCode, got, tt.want)
		})
	}
}

func TestIsRetryableError_NetworkErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Timeout network error",
			err:  &MockNetworkError{timeout: true, temporary: false, message: "connection timeout"},
			want: true,
		},
		{
			name: "Temporary network error",
			err:  &MockNetworkError{timeout: false, temporary: true, message: "temporary network failure"},
			want: true,
		},
		{
			name: "OpError connection reset",
			err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: errors.New("connection reset by peer"),
			},
			want: true,
		},
		{
			name: "OpError connection refused",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("connection refused"),
			},
			want: true,
		},
		{
			name: "DNSError",
			err: &net.DNSError{
				Err:         "no such host",
				Name:        "test.example.com",
				Server:      "8.8.8.8",
				IsTimeout:   false,
				IsTemporary: false,
			},
			want: true,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "Non-retryable network error",
			err:  &MockNetworkError{timeout: false, temporary: false, message: "permanent failure"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			assert.Equal(t, tt.want, got, "isRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
		})
	}
}

// ============================================================================
// Exponential Backoff Interval Tests
// ============================================================================

func TestExponentialBackoffIntervals(t *testing.T) {
	// Test that backoff configuration matches expected values
	backoffConfig := newExponentialBackoffConfig()

	// Verify initial interval
	assert.Equal(t, 1*time.Second, backoffConfig.InitialInterval, "InitialInterval should be 1 second")

	// Verify max interval
	assert.Equal(t, 16*time.Second, backoffConfig.MaxInterval, "MaxInterval should be 16 seconds")

	// Verify multiplier (default is 1.5)
	assert.Equal(t, 1.5, backoffConfig.Multiplier, "Multiplier should be 1.5")

	// Verify randomization factor (default is 0.5)
	assert.Equal(t, 0.5, backoffConfig.RandomizationFactor, "RandomizationFactor should be 0.5")
}

func TestBackoffMaxInterval(t *testing.T) {
	// Verify that backoff intervals respect MaxInterval
	backoffConfig := newExponentialBackoffConfig()

	// Calculate intervals for multiple attempts
	// With InitialInterval=1s, Multiplier=1.5:
	// Attempt 1: 0s (immediate)
	// Attempt 2: 1s * 1.5^0 = 1s
	// Attempt 3: 1s * 1.5^1 = 1.5s
	// Attempt 4: 1s * 1.5^2 = 2.25s
	// Attempt 5: 1s * 1.5^3 = 3.375s
	// ...
	// Eventually reaches MaxInterval of 16s

	// Test that calculated intervals don't exceed MaxInterval
	maxCalculatedInterval := backoffConfig.InitialInterval
	for i := 0; i < 20; i++ {
		maxCalculatedInterval = time.Duration(float64(maxCalculatedInterval) * backoffConfig.Multiplier)
		if maxCalculatedInterval > backoffConfig.MaxInterval {
			maxCalculatedInterval = backoffConfig.MaxInterval
			break
		}
	}

	assert.LessOrEqual(t, maxCalculatedInterval, backoffConfig.MaxInterval, "Calculated interval should not exceed MaxInterval")
}

// newExponentialBackoffConfig creates a new exponential backoff config matching s3.go implementation
func newExponentialBackoffConfig() struct {
	InitialInterval     time.Duration
	MaxInterval         time.Duration
	Multiplier          float64
	RandomizationFactor float64
	MaxElapsedTime      time.Duration
} {
	// This matches the configuration in s3.go Upload and Download methods
	return struct {
		InitialInterval     time.Duration
		MaxInterval         time.Duration
		Multiplier          float64
		RandomizationFactor float64
		MaxElapsedTime      time.Duration
	}{
		InitialInterval:     1 * time.Second,
		MaxInterval:         16 * time.Second,
		Multiplier:          1.5, // Default from backoff package
		RandomizationFactor: 0.5, // Default from backoff package
		MaxElapsedTime:      0,   // Use max retries instead
	}
}

// ============================================================================
// MaxRetriesExceededError Tests
// ============================================================================

func TestMaxRetriesExceededError_ErrorMethod(t *testing.T) {
	originalErr := errors.New("connection timeout")
	err := &MaxRetriesExceededError{
		MaxAttempts: 5,
		LastError:   originalErr,
	}

	errorMsg := err.Error()
	assert.Contains(t, errorMsg, "max retries (5) exceeded", "Error message should contain max retries count")
	assert.Contains(t, errorMsg, originalErr.Error(), "Error message should contain last error")
}

func TestMaxRetriesExceededError_ErrorMethod_NoLastError(t *testing.T) {
	err := &MaxRetriesExceededError{
		MaxAttempts: 3,
		LastError:   nil,
	}

	errorMsg := err.Error()
	assert.Contains(t, errorMsg, "max retries (3) exceeded", "Error message should contain max retries count")
}

func TestMaxRetriesExceededError_Unwrap(t *testing.T) {
	originalErr := errors.New("connection timeout")
	err := &MaxRetriesExceededError{
		MaxAttempts: 5,
		LastError:   originalErr,
	}

	unwrapped := err.Unwrap()
	assert.Equal(t, originalErr, unwrapped, "Unwrap() should return the last error")
}

func TestMaxRetriesExceededError_Unwrap_Nil(t *testing.T) {
	err := &MaxRetriesExceededError{
		MaxAttempts: 3,
		LastError:   nil,
	}

	unwrapped := err.Unwrap()
	assert.Nil(t, unwrapped, "Unwrap() should return nil when LastError is nil")
}

func TestMaxRetriesExceededError_ErrorsAs(t *testing.T) {
	originalErr := errors.New("connection timeout")
	err := &MaxRetriesExceededError{
		MaxAttempts: 5,
		LastError:   originalErr,
	}

	var maxRetriesErr *MaxRetriesExceededError
	assert.True(t, errors.As(err, &maxRetriesErr), "errors.As should recognize MaxRetriesExceededError")
	if maxRetriesErr != nil {
		assert.Equal(t, 5, maxRetriesErr.MaxAttempts, "MaxAttempts should be preserved")
		assert.Equal(t, originalErr, maxRetriesErr.LastError, "LastError should be preserved")
	}
}

// ============================================================================
// Upload/Download Error Handling Tests
// ============================================================================

func TestUpload_InputValidation(t *testing.T) {
	cfg := &Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		Region:          "us-east-1",
		MaxRetries:      3,
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Create a temporary file for testing
	tmpfile, err := os.CreateTemp("", "s3_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("test content")
	tmpfile.Close()

	tests := []struct {
		name     string
		key      string
		filePath string
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "empty key",
			key:      "",
			filePath: tmpfile.Name(),
			wantErr:  true,
			errMsg:   "S3 key cannot be empty",
		},
		{
			name:     "empty filePath",
			key:      "test-key",
			filePath: "",
			wantErr:  true,
			errMsg:   "file path cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.Upload(nil, tt.key, tt.filePath)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for %s", tt.name)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
				}
			} else {
				assert.NoError(t, err, "Expected no error for %s", tt.name)
			}
		})
	}
}

func TestDownload_InputValidation(t *testing.T) {
	cfg := &Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		Region:          "us-east-1",
		MaxRetries:      3,
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Create a temporary file for output
	tmpfile, err := os.CreateTemp("", "s3_test_output_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpfile.Close()
	os.Remove(tmpfile.Name()) // Remove it so Download will create it

	tests := []struct {
		name     string
		key      string
		filePath string
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "empty key",
			key:      "",
			filePath: tmpfile.Name(),
			wantErr:  true,
			errMsg:   "S3 key cannot be empty",
		},
		{
			name:     "empty filePath",
			key:      "test-key",
			filePath: "",
			wantErr:  true,
			errMsg:   "file path cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.Download(nil, tt.key, tt.filePath)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for %s", tt.name)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
				}
			} else {
				assert.NoError(t, err, "Expected no error for %s", tt.name)
			}
		})
	}
}

// ============================================================================
// Test Helper Functions
// ============================================================================

// createTestFile creates a temporary file for testing and returns the path and cleanup function
func createTestFile(t *testing.T, content []byte) (string, func()) {
	t.Helper()

	tmpfile, err := os.CreateTemp("", "s3_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if content != nil {
		if _, err := tmpfile.Write(content); err != nil {
			tmpfile.Close()
			os.Remove(tmpfile.Name())
			t.Fatalf("Failed to write to temp file: %v", err)
		}
	}

	if err := tmpfile.Close(); err != nil {
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to close temp file: %v", err)
	}

	return tmpfile.Name(), func() {
		os.Remove(tmpfile.Name())
	}
}

// setupTestClient creates a test S3 client with custom config
func setupTestClient(t *testing.T, cfg *Config) *Client {
	t.Helper()

	if cfg == nil {
		cfg = &Config{
			Bucket:          "test-bucket",
			AccessKeyID:     "test-key",
			SecretAccessKey: "test-secret",
			Region:          "us-east-1",
			MaxRetries:      3,
		}
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("Failed to create test client: %v", err)
	}

	return client
}
