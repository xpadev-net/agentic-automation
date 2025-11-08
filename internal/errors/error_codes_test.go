package errors

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/services"
)

func TestErrorCodeConstants(t *testing.T) {
	// Test that all error codes are defined
	codes := []ErrorCode{
		ERR_WEBHOOK_MISSING_DELIVERY,
		ERR_WEBHOOK_INVALID_PAYLOAD,
		ERR_WEBHOOK_INVALID_REPO_FORMAT,
		ERR_WEBHOOK_PAYLOAD_NOT_FOUND,
		ERR_GITHUB_RATE_LIMIT,
		ERR_GITHUB_NOT_FOUND,
		ERR_GITHUB_UNAUTHORIZED,
		ERR_GITHUB_FORBIDDEN,
		ERR_GITHUB_SERVER_ERROR,
		ERR_AUTH_INSUFFICIENT_PERMISSION,
		ERR_AUTH_MISSING_TOKEN,
		ERR_DEPENDENCY_BLOCKED,
		ERR_DEPENDENCY_CIRCULAR,
		ERR_K8S_JOB_CREATION_FAILED,
		ERR_K8S_JOB_NOT_FOUND,
		ERR_K8S_CLIENT_ERROR,
		ERR_DB_RECORD_NOT_FOUND,
		ERR_DB_CONNECTION_FAILED,
		ERR_DB_QUERY_FAILED,
		ERR_VALIDATION_INVALID_INPUT,
		ERR_VALIDATION_MISSING_REQUIRED,
		ERR_VALIDATION_INVALID_STATUS,
		ERR_AGENT_RUN_NOT_FOUND,
		ERR_AGENT_RUN_MAX_RETRIES_EXCEEDED,
		ERR_AGENT_RUN_INVALID_STATE,
		ERR_MERGE_CONFLICT,
		ERR_MERGE_NOT_MERGEABLE,
		ERR_MERGE_PROTECTION_VIOLATION,
		ERR_INTERNAL_SERVER_ERROR,
		ERR_INTERNAL_UNEXPECTED,
	}

	if len(codes) != 30 {
		t.Errorf("Expected 30 error codes, got %d", len(codes))
	}

	// Verify all codes have messages by checking GetUserMessage
	for _, code := range codes {
		err := NewCodedError(code, "", nil)
		msg := GetUserMessage(err, "ja")
		if msg == "" || msg == string(code) {
			t.Errorf("Error code %s does not have a message mapping", code)
		}
	}
}

func TestCodedError_Error(t *testing.T) {
	tests := []struct {
		name    string
		err     *CodedError
		want    string
		wantMsg bool
	}{
		{
			name: "with message",
			err: &CodedError{
				Code:    ERR_WEBHOOK_MISSING_DELIVERY,
				Message: "test message",
			},
			want:    "test message",
			wantMsg: true,
		},
		{
			name: "with cause",
			err: &CodedError{
				Code:  ERR_WEBHOOK_MISSING_DELIVERY,
				Cause: errors.New("underlying error"),
			},
			want:    "underlying error",
			wantMsg: true,
		},
		{
			name: "only code",
			err: &CodedError{
				Code: ERR_WEBHOOK_MISSING_DELIVERY,
			},
			want:    string(ERR_WEBHOOK_MISSING_DELIVERY),
			wantMsg: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("CodedError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodedError_Unwrap(t *testing.T) {
	underlying := errors.New("underlying error")
	err := &CodedError{
		Code:  ERR_WEBHOOK_MISSING_DELIVERY,
		Cause: underlying,
	}

	got := err.Unwrap()
	if got != underlying {
		t.Errorf("CodedError.Unwrap() = %v, want %v", got, underlying)
	}

	// Test with nil cause
	errNoCause := &CodedError{
		Code: ERR_WEBHOOK_MISSING_DELIVERY,
	}
	if errNoCause.Unwrap() != nil {
		t.Errorf("CodedError.Unwrap() with nil cause should return nil")
	}
}

func TestNewCodedError(t *testing.T) {
	code := ERR_WEBHOOK_MISSING_DELIVERY
	message := "test message"
	cause := errors.New("cause")

	err := NewCodedError(code, message, cause)

	if err.Code != code {
		t.Errorf("NewCodedError().Code = %v, want %v", err.Code, code)
	}
	if err.Message != message {
		t.Errorf("NewCodedError().Message = %v, want %v", err.Message, message)
	}
	if err.Cause != cause {
		t.Errorf("NewCodedError().Cause = %v, want %v", err.Cause, cause)
	}
	if err.Details == nil {
		t.Errorf("NewCodedError().Details should not be nil")
	}
}

func TestWrapCodedError(t *testing.T) {
	code := ERR_WEBHOOK_MISSING_DELIVERY
	cause := errors.New("underlying error")

	err := WrapCodedError(code, cause)

	if err == nil {
		t.Fatal("WrapCodedError() should not return nil")
	}
	if err.Code != code {
		t.Errorf("WrapCodedError().Code = %v, want %v", err.Code, code)
	}
	if err.Cause != cause {
		t.Errorf("WrapCodedError().Cause = %v, want %v", err.Cause, cause)
	}
	if err.Message != cause.Error() {
		t.Errorf("WrapCodedError().Message = %v, want %v", err.Message, cause.Error())
	}

	// Test with nil cause
	nilErr := WrapCodedError(code, nil)
	if nilErr != nil {
		t.Errorf("WrapCodedError() with nil cause should return nil, got %v", nilErr)
	}
}

func TestGetUserMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		lang string
		want string
	}{
		{
			name: "CodedError with Japanese",
			err:  NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			lang: "ja",
			want: "Webhookリクエストに必須ヘッダーが含まれていません",
		},
		{
			name: "CodedError with English",
			err:  NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			lang: "en",
			want: "Required header is missing in webhook request",
		},
		{
			name: "CodedError default to Japanese",
			err:  NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			lang: "",
			want: "Webhookリクエストに必須ヘッダーが含まれていません",
		},
		{
			name: "non-coded error",
			err:  errors.New("some error"),
			lang: "ja",
			want: "some error",
		},
		{
			name: "nil error",
			err:  nil,
			lang: "ja",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetUserMessage(tt.err, tt.lang)
			if got != tt.want {
				t.Errorf("GetUserMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorCode
	}{
		{
			name: "CodedError",
			err:  NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			want: ERR_WEBHOOK_MISSING_DELIVERY,
		},
		{
			name: "wrapped CodedError",
			err:  WrapCodedError(ERR_DEPENDENCY_BLOCKED, errors.New("test")),
			want: ERR_DEPENDENCY_BLOCKED,
		},
		{
			name: "non-coded error",
			err:  errors.New("some error"),
			want: ERR_INTERNAL_UNEXPECTED,
		},
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetErrorCode(tt.err)
			if got != tt.want {
				t.Errorf("GetErrorCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetErrorCode_WithExtractors tests that GetErrorCode works with error code extractors
// This test uses mock implementations to verify the interface-based approach
// Note: Test files (_test.go) are treated as separate packages, so importing clients/services
// here does not cause circular dependency issues in the main package
func TestGetErrorCode_WithExtractors(t *testing.T) {
	// Test with a mock GitHubErrorCodeExtractor
	mockGitHubErr := &mockGitHubError{code: ERR_GITHUB_NOT_FOUND}
	got := GetErrorCode(mockGitHubErr)
	if got != ERR_GITHUB_NOT_FOUND {
		t.Errorf("GetErrorCode() with GitHubErrorCodeExtractor = %v, want %v", got, ERR_GITHUB_NOT_FOUND)
	}

	// Test with a mock CircularDependencyErrorCodeExtractor
	mockCircularErr := &mockCircularDependencyError{code: ERR_DEPENDENCY_CIRCULAR}
	got = GetErrorCode(mockCircularErr)
	if got != ERR_DEPENDENCY_CIRCULAR {
		t.Errorf("GetErrorCode() with CircularDependencyErrorCodeExtractor = %v, want %v", got, ERR_DEPENDENCY_CIRCULAR)
	}
}

// TestGetErrorCode_WithWrappedErrors tests that GetErrorCode works with wrapped errors
// This test uses actual types from clients and services packages to verify
// that the interface-based approach works correctly with real implementations.
// Note: Test files (_test.go) are treated as separate packages, so importing clients/services
// here does not cause circular dependency issues in the main package.
func TestGetErrorCode_WithWrappedErrors(t *testing.T) {
	// Test with wrapped GitHubError
	githubErr := &clients.GitHubError{Code: ERR_GITHUB_NOT_FOUND}
	wrappedErr := fmt.Errorf("failed to get issue: %w", githubErr)
	got := GetErrorCode(wrappedErr)
	if got != ERR_GITHUB_NOT_FOUND {
		t.Errorf("GetErrorCode() with wrapped GitHubError = %v, want %v", got, ERR_GITHUB_NOT_FOUND)
	}

	// Test with wrapped CircularDependencyError
	circularErr := &services.CircularDependencyError{Code: ERR_DEPENDENCY_CIRCULAR}
	wrappedErr = fmt.Errorf("validation failed: %w", circularErr)
	got = GetErrorCode(wrappedErr)
	if got != ERR_DEPENDENCY_CIRCULAR {
		t.Errorf("GetErrorCode() with wrapped CircularDependencyError = %v, want %v", got, ERR_DEPENDENCY_CIRCULAR)
	}

	// Test with double-wrapped CircularDependencyError
	doubleWrappedErr := fmt.Errorf("outer error: %w", wrappedErr)
	got = GetErrorCode(doubleWrappedErr)
	if got != ERR_DEPENDENCY_CIRCULAR {
		t.Errorf("GetErrorCode() with double-wrapped CircularDependencyError = %v, want %v", got, ERR_DEPENDENCY_CIRCULAR)
	}
}

// Mock implementations for testing
type mockGitHubError struct {
	code ErrorCode
}

func (m *mockGitHubError) Error() string {
	return "mock GitHub error"
}

func (m *mockGitHubError) GetErrorCode() ErrorCode {
	return m.code
}

type mockCircularDependencyError struct {
	code ErrorCode
}

func (m *mockCircularDependencyError) Error() string {
	return "mock circular dependency error"
}

func (m *mockCircularDependencyError) GetErrorCode() ErrorCode {
	return m.code
}

func TestIsErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
		want bool
	}{
		{
			name: "matching code",
			err:  NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			code: ERR_WEBHOOK_MISSING_DELIVERY,
			want: true,
		},
		{
			name: "non-matching code",
			err:  NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			code: ERR_WEBHOOK_INVALID_PAYLOAD,
			want: false,
		},
		{
			name: "non-coded error",
			err:  errors.New("some error"),
			code: ERR_WEBHOOK_MISSING_DELIVERY,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsErrorCode(tt.err, tt.code)
			if got != tt.want {
				t.Errorf("IsErrorCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetHTTPStatusCode(t *testing.T) {
	tests := []struct {
		name string
		code ErrorCode
		want int
	}{
		// Webhook errors
		{
			name: "ERR_WEBHOOK_MISSING_DELIVERY",
			code: ERR_WEBHOOK_MISSING_DELIVERY,
			want: http.StatusBadRequest,
		},
		{
			name: "ERR_WEBHOOK_INVALID_PAYLOAD",
			code: ERR_WEBHOOK_INVALID_PAYLOAD,
			want: http.StatusBadRequest,
		},
		// GitHub API errors
		{
			name: "ERR_GITHUB_RATE_LIMIT",
			code: ERR_GITHUB_RATE_LIMIT,
			want: http.StatusTooManyRequests,
		},
		{
			name: "ERR_GITHUB_NOT_FOUND",
			code: ERR_GITHUB_NOT_FOUND,
			want: http.StatusNotFound,
		},
		{
			name: "ERR_GITHUB_UNAUTHORIZED",
			code: ERR_GITHUB_UNAUTHORIZED,
			want: http.StatusUnauthorized,
		},
		{
			name: "ERR_GITHUB_FORBIDDEN",
			code: ERR_GITHUB_FORBIDDEN,
			want: http.StatusForbidden,
		},
		{
			name: "ERR_GITHUB_SERVER_ERROR",
			code: ERR_GITHUB_SERVER_ERROR,
			want: http.StatusBadGateway,
		},
		// Authentication errors
		{
			name: "ERR_AUTH_INSUFFICIENT_PERMISSION",
			code: ERR_AUTH_INSUFFICIENT_PERMISSION,
			want: http.StatusForbidden,
		},
		// Dependency errors
		{
			name: "ERR_DEPENDENCY_BLOCKED",
			code: ERR_DEPENDENCY_BLOCKED,
			want: http.StatusConflict,
		},
		{
			name: "ERR_DEPENDENCY_CIRCULAR",
			code: ERR_DEPENDENCY_CIRCULAR,
			want: http.StatusConflict,
		},
		// Database errors
		{
			name: "ERR_DB_RECORD_NOT_FOUND",
			code: ERR_DB_RECORD_NOT_FOUND,
			want: http.StatusNotFound,
		},
		{
			name: "ERR_DB_CONNECTION_FAILED",
			code: ERR_DB_CONNECTION_FAILED,
			want: http.StatusInternalServerError,
		},
		// Validation errors
		{
			name: "ERR_VALIDATION_INVALID_INPUT",
			code: ERR_VALIDATION_INVALID_INPUT,
			want: http.StatusBadRequest,
		},
		// Agent execution errors
		{
			name: "ERR_AGENT_RUN_NOT_FOUND",
			code: ERR_AGENT_RUN_NOT_FOUND,
			want: http.StatusNotFound,
		},
		// Merge errors
		{
			name: "ERR_MERGE_CONFLICT",
			code: ERR_MERGE_CONFLICT,
			want: http.StatusConflict,
		},
		// Internal errors
		{
			name: "ERR_INTERNAL_SERVER_ERROR",
			code: ERR_INTERNAL_SERVER_ERROR,
			want: http.StatusInternalServerError,
		},
		{
			name: "ERR_INTERNAL_UNEXPECTED",
			code: ERR_INTERNAL_UNEXPECTED,
			want: http.StatusInternalServerError,
		},
		// Unknown code
		{
			name: "unknown code",
			code: ErrorCode("UNKNOWN_CODE"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetHTTPStatusCode(tt.code)
			if got != tt.want {
				t.Errorf("GetHTTPStatusCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrorsAs(t *testing.T) {
	// Test that errors.As works with CodedError
	err := NewCodedError(ERR_WEBHOOK_MISSING_DELIVERY, "test", nil)

	var codedErr *CodedError
	if !errors.As(err, &codedErr) {
		t.Fatal("errors.As() should work with CodedError")
	}

	if codedErr.Code != ERR_WEBHOOK_MISSING_DELIVERY {
		t.Errorf("errors.As() extracted code = %v, want %v", codedErr.Code, ERR_WEBHOOK_MISSING_DELIVERY)
	}
}

func TestErrorsIs(t *testing.T) {
	// Test that errors.Is works with wrapped errors
	underlying := errors.New("underlying")
	err := WrapCodedError(ERR_WEBHOOK_MISSING_DELIVERY, underlying)

	if !errors.Is(err, underlying) {
		t.Error("errors.Is() should work with wrapped errors")
	}
}
