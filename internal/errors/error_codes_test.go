package errors_test

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/errors"
	"agentic-automation/internal/services"
)

func TestErrorCodeConstants(t *testing.T) {
	// Test that all error codes are defined
	codes := []errors.ErrorCode{
		errors.ERR_WEBHOOK_MISSING_DELIVERY,
		errors.ERR_WEBHOOK_INVALID_PAYLOAD,
		errors.ERR_WEBHOOK_INVALID_REPO_FORMAT,
		errors.ERR_WEBHOOK_PAYLOAD_NOT_FOUND,
		errors.ERR_GITHUB_RATE_LIMIT,
		errors.ERR_GITHUB_NOT_FOUND,
		errors.ERR_GITHUB_UNAUTHORIZED,
		errors.ERR_GITHUB_FORBIDDEN,
		errors.ERR_GITHUB_SERVER_ERROR,
		errors.ERR_AUTH_INSUFFICIENT_PERMISSION,
		errors.ERR_AUTH_MISSING_TOKEN,
		errors.ERR_DEPENDENCY_BLOCKED,
		errors.ERR_DEPENDENCY_CIRCULAR,
		errors.ERR_K8S_JOB_CREATION_FAILED,
		errors.ERR_K8S_JOB_NOT_FOUND,
		errors.ERR_K8S_CLIENT_ERROR,
		errors.ERR_DB_RECORD_NOT_FOUND,
		errors.ERR_DB_CONNECTION_FAILED,
		errors.ERR_DB_QUERY_FAILED,
		errors.ERR_VALIDATION_INVALID_INPUT,
		errors.ERR_VALIDATION_MISSING_REQUIRED,
		errors.ERR_VALIDATION_INVALID_STATUS,
		errors.ERR_AGENT_RUN_NOT_FOUND,
		errors.ERR_AGENT_RUN_MAX_RETRIES_EXCEEDED,
		errors.ERR_AGENT_RUN_INVALID_STATE,
		errors.ERR_MERGE_CONFLICT,
		errors.ERR_MERGE_NOT_MERGEABLE,
		errors.ERR_MERGE_PROTECTION_VIOLATION,
		errors.ERR_INTERNAL_SERVER_ERROR,
		errors.ERR_INTERNAL_UNEXPECTED,
	}

	if len(codes) != 30 {
		t.Errorf("Expected 30 error codes, got %d", len(codes))
	}

	// Verify all codes have messages by checking GetUserMessage
	for _, code := range codes {
		err := errors.NewCodedError(code, "", nil)
		msg := errors.GetUserMessage(err, "ja")
		if msg == "" || msg == string(code) {
			t.Errorf("Error code %s does not have a message mapping", code)
		}
	}
}

func TestCodedError_Error(t *testing.T) {
	tests := []struct {
		name    string
		err     *errors.CodedError
		want    string
		wantMsg bool
	}{
		{
			name: "with message",
			err: &errors.CodedError{
				Code:    errors.ERR_WEBHOOK_MISSING_DELIVERY,
				Message: "test message",
			},
			want:    "test message",
			wantMsg: true,
		},
		{
			name: "with cause",
			err: &errors.CodedError{
				Code:  errors.ERR_WEBHOOK_MISSING_DELIVERY,
				Cause: stderrors.New("underlying error"),
			},
			want:    "underlying error",
			wantMsg: true,
		},
		{
			name: "only code",
			err: &errors.CodedError{
				Code: errors.ERR_WEBHOOK_MISSING_DELIVERY,
			},
			want:    string(errors.ERR_WEBHOOK_MISSING_DELIVERY),
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
	underlying := stderrors.New("underlying error")
	err := &errors.CodedError{
		Code:  errors.ERR_WEBHOOK_MISSING_DELIVERY,
		Cause: underlying,
	}

	got := err.Unwrap()
	if got != underlying {
		t.Errorf("CodedError.Unwrap() = %v, want %v", got, underlying)
	}

	// Test with nil cause
	errNoCause := &errors.CodedError{
		Code: errors.ERR_WEBHOOK_MISSING_DELIVERY,
	}
	if errNoCause.Unwrap() != nil {
		t.Errorf("CodedError.Unwrap() with nil cause should return nil")
	}
}

func TestNewCodedError(t *testing.T) {
	code := errors.ERR_WEBHOOK_MISSING_DELIVERY
	message := "test message"
	cause := stderrors.New("cause")

	err := errors.NewCodedError(code, message, cause)

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
	code := errors.ERR_WEBHOOK_MISSING_DELIVERY
	cause := stderrors.New("underlying error")

	err := errors.WrapCodedError(code, cause)

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
	nilErr := errors.WrapCodedError(code, nil)
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
			err:  errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			lang: "ja",
			want: "Webhookリクエストに必須ヘッダーが含まれていません",
		},
		{
			name: "CodedError with English",
			err:  errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			lang: "en",
			want: "Required header is missing in webhook request",
		},
		{
			name: "CodedError default to Japanese",
			err:  errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			lang: "",
			want: "Webhookリクエストに必須ヘッダーが含まれていません",
		},
		{
			name: "non-coded error",
			err:  stderrors.New("some error"),
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
			got := errors.GetUserMessage(tt.err, tt.lang)
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
		want errors.ErrorCode
	}{
		{
			name: "CodedError",
			err:  errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			want: errors.ERR_WEBHOOK_MISSING_DELIVERY,
		},
		{
			name: "wrapped CodedError",
			err:  errors.WrapCodedError(errors.ERR_DEPENDENCY_BLOCKED, stderrors.New("test")),
			want: errors.ERR_DEPENDENCY_BLOCKED,
		},
		{
			name: "non-coded error",
			err:  stderrors.New("some error"),
			want: errors.ERR_INTERNAL_UNEXPECTED,
		},
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.GetErrorCode(tt.err)
			if got != tt.want {
				t.Errorf("GetErrorCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetErrorCode_WithExtractors tests that GetErrorCode works with error code extractors
// This test uses mock implementations to verify the interface-based approach
func TestGetErrorCode_WithExtractors(t *testing.T) {
	// Test with a mock GitHubErrorCodeExtractor
	mockGitHubErr := &mockGitHubError{code: errors.ERR_GITHUB_NOT_FOUND}
	got := errors.GetErrorCode(mockGitHubErr)
	if got != errors.ERR_GITHUB_NOT_FOUND {
		t.Errorf("GetErrorCode() with GitHubErrorCodeExtractor = %v, want %v", got, errors.ERR_GITHUB_NOT_FOUND)
	}

	// Test with a mock CircularDependencyErrorCodeExtractor
	mockCircularErr := &mockCircularDependencyError{code: errors.ERR_DEPENDENCY_CIRCULAR}
	got = errors.GetErrorCode(mockCircularErr)
	if got != errors.ERR_DEPENDENCY_CIRCULAR {
		t.Errorf("GetErrorCode() with CircularDependencyErrorCodeExtractor = %v, want %v", got, errors.ERR_DEPENDENCY_CIRCULAR)
	}
}

// TestGetErrorCode_WithWrappedErrors tests that GetErrorCode works with wrapped errors
// This test uses actual types from clients and services packages to verify
// that the interface-based approach works correctly with real implementations.
func TestGetErrorCode_WithWrappedErrors(t *testing.T) {
	// Test with wrapped GitHubError
	githubErr := &clients.GitHubError{Code: errors.ERR_GITHUB_NOT_FOUND}
	wrappedErr := fmt.Errorf("failed to get issue: %w", githubErr)
	got := errors.GetErrorCode(wrappedErr)
	if got != errors.ERR_GITHUB_NOT_FOUND {
		t.Errorf("GetErrorCode() with wrapped GitHubError = %v, want %v", got, errors.ERR_GITHUB_NOT_FOUND)
	}

	// Test with wrapped CircularDependencyError
	circularErr := &services.CircularDependencyError{Code: errors.ERR_DEPENDENCY_CIRCULAR}
	wrappedErr = fmt.Errorf("validation failed: %w", circularErr)
	got = errors.GetErrorCode(wrappedErr)
	if got != errors.ERR_DEPENDENCY_CIRCULAR {
		t.Errorf("GetErrorCode() with wrapped CircularDependencyError = %v, want %v", got, errors.ERR_DEPENDENCY_CIRCULAR)
	}

	// Test with double-wrapped CircularDependencyError
	doubleWrappedErr := fmt.Errorf("outer error: %w", wrappedErr)
	got = errors.GetErrorCode(doubleWrappedErr)
	if got != errors.ERR_DEPENDENCY_CIRCULAR {
		t.Errorf("GetErrorCode() with double-wrapped CircularDependencyError = %v, want %v", got, errors.ERR_DEPENDENCY_CIRCULAR)
	}
}

// Mock implementations for testing
type mockGitHubError struct {
	code errors.ErrorCode
}

func (m *mockGitHubError) Error() string {
	return "mock GitHub error"
}

func (m *mockGitHubError) GetErrorCode() errors.ErrorCode {
	return m.code
}

type mockCircularDependencyError struct {
	code errors.ErrorCode
}

func (m *mockCircularDependencyError) Error() string {
	return "mock circular dependency error"
}

func (m *mockCircularDependencyError) GetErrorCode() errors.ErrorCode {
	return m.code
}

func TestIsErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code errors.ErrorCode
		want bool
	}{
		{
			name: "matching code",
			err:  errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			code: errors.ERR_WEBHOOK_MISSING_DELIVERY,
			want: true,
		},
		{
			name: "non-matching code",
			err:  errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "", nil),
			code: errors.ERR_WEBHOOK_INVALID_PAYLOAD,
			want: false,
		},
		{
			name: "non-coded error",
			err:  stderrors.New("some error"),
			code: errors.ERR_WEBHOOK_MISSING_DELIVERY,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.IsErrorCode(tt.err, tt.code)
			if got != tt.want {
				t.Errorf("IsErrorCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetHTTPStatusCode(t *testing.T) {
	tests := []struct {
		name string
		code errors.ErrorCode
		want int
	}{
		// Webhook errors
		{
			name: "ERR_WEBHOOK_MISSING_DELIVERY",
			code: errors.ERR_WEBHOOK_MISSING_DELIVERY,
			want: http.StatusBadRequest,
		},
		{
			name: "ERR_WEBHOOK_INVALID_PAYLOAD",
			code: errors.ERR_WEBHOOK_INVALID_PAYLOAD,
			want: http.StatusBadRequest,
		},
		// GitHub API errors
		{
			name: "ERR_GITHUB_RATE_LIMIT",
			code: errors.ERR_GITHUB_RATE_LIMIT,
			want: http.StatusTooManyRequests,
		},
		{
			name: "ERR_GITHUB_NOT_FOUND",
			code: errors.ERR_GITHUB_NOT_FOUND,
			want: http.StatusNotFound,
		},
		{
			name: "ERR_GITHUB_UNAUTHORIZED",
			code: errors.ERR_GITHUB_UNAUTHORIZED,
			want: http.StatusUnauthorized,
		},
		{
			name: "ERR_GITHUB_FORBIDDEN",
			code: errors.ERR_GITHUB_FORBIDDEN,
			want: http.StatusForbidden,
		},
		{
			name: "ERR_GITHUB_SERVER_ERROR",
			code: errors.ERR_GITHUB_SERVER_ERROR,
			want: http.StatusBadGateway,
		},
		// Authentication errors
		{
			name: "ERR_AUTH_INSUFFICIENT_PERMISSION",
			code: errors.ERR_AUTH_INSUFFICIENT_PERMISSION,
			want: http.StatusForbidden,
		},
		// Dependency errors
		{
			name: "ERR_DEPENDENCY_BLOCKED",
			code: errors.ERR_DEPENDENCY_BLOCKED,
			want: http.StatusConflict,
		},
		{
			name: "ERR_DEPENDENCY_CIRCULAR",
			code: errors.ERR_DEPENDENCY_CIRCULAR,
			want: http.StatusConflict,
		},
		// Database errors
		{
			name: "ERR_DB_RECORD_NOT_FOUND",
			code: errors.ERR_DB_RECORD_NOT_FOUND,
			want: http.StatusNotFound,
		},
		{
			name: "ERR_DB_CONNECTION_FAILED",
			code: errors.ERR_DB_CONNECTION_FAILED,
			want: http.StatusInternalServerError,
		},
		// Validation errors
		{
			name: "ERR_VALIDATION_INVALID_INPUT",
			code: errors.ERR_VALIDATION_INVALID_INPUT,
			want: http.StatusBadRequest,
		},
		// Agent execution errors
		{
			name: "ERR_AGENT_RUN_NOT_FOUND",
			code: errors.ERR_AGENT_RUN_NOT_FOUND,
			want: http.StatusNotFound,
		},
		// Merge errors
		{
			name: "ERR_MERGE_CONFLICT",
			code: errors.ERR_MERGE_CONFLICT,
			want: http.StatusConflict,
		},
		// Internal errors
		{
			name: "ERR_INTERNAL_SERVER_ERROR",
			code: errors.ERR_INTERNAL_SERVER_ERROR,
			want: http.StatusInternalServerError,
		},
		{
			name: "ERR_INTERNAL_UNEXPECTED",
			code: errors.ERR_INTERNAL_UNEXPECTED,
			want: http.StatusInternalServerError,
		},
		// Unknown code
		{
			name: "unknown code",
			code: errors.ErrorCode("UNKNOWN_CODE"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.GetHTTPStatusCode(tt.code)
			if got != tt.want {
				t.Errorf("GetHTTPStatusCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrorsAs(t *testing.T) {
	// Test that errors.As works with CodedError
	err := errors.NewCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, "test", nil)

	var codedErr *errors.CodedError
	if !stderrors.As(err, &codedErr) {
		t.Fatal("errors.As() should work with CodedError")
	}

	if codedErr.Code != errors.ERR_WEBHOOK_MISSING_DELIVERY {
		t.Errorf("errors.As() extracted code = %v, want %v", codedErr.Code, errors.ERR_WEBHOOK_MISSING_DELIVERY)
	}
}

func TestErrorsIs(t *testing.T) {
	// Test that errors.Is works with wrapped errors
	underlying := stderrors.New("underlying")
	err := errors.WrapCodedError(errors.ERR_WEBHOOK_MISSING_DELIVERY, underlying)

	if !stderrors.Is(err, underlying) {
		t.Error("errors.Is() should work with wrapped errors")
	}
}
