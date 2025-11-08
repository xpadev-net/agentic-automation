package errors

import (
	"errors"
	"net/http"
)

// ErrorCode represents a standardized error code for the system
type ErrorCode string

// Error code constants
const (
	// Webhook related errors (4)
	ERR_WEBHOOK_MISSING_DELIVERY    ErrorCode = "ERR_WEBHOOK_MISSING_DELIVERY"
	ERR_WEBHOOK_INVALID_PAYLOAD     ErrorCode = "ERR_WEBHOOK_INVALID_PAYLOAD"
	ERR_WEBHOOK_INVALID_REPO_FORMAT ErrorCode = "ERR_WEBHOOK_INVALID_REPO_FORMAT"
	ERR_WEBHOOK_PAYLOAD_NOT_FOUND   ErrorCode = "ERR_WEBHOOK_PAYLOAD_NOT_FOUND"

	// GitHub API related errors (5)
	ERR_GITHUB_RATE_LIMIT   ErrorCode = "ERR_GITHUB_RATE_LIMIT"
	ERR_GITHUB_NOT_FOUND    ErrorCode = "ERR_GITHUB_NOT_FOUND"
	ERR_GITHUB_UNAUTHORIZED ErrorCode = "ERR_GITHUB_UNAUTHORIZED"
	ERR_GITHUB_FORBIDDEN    ErrorCode = "ERR_GITHUB_FORBIDDEN"
	ERR_GITHUB_SERVER_ERROR ErrorCode = "ERR_GITHUB_SERVER_ERROR"

	// Authentication/Authorization related errors (2)
	ERR_AUTH_INSUFFICIENT_PERMISSION ErrorCode = "ERR_AUTH_INSUFFICIENT_PERMISSION"
	ERR_AUTH_MISSING_TOKEN           ErrorCode = "ERR_AUTH_MISSING_TOKEN"

	// Dependency related errors (2)
	ERR_DEPENDENCY_BLOCKED  ErrorCode = "ERR_DEPENDENCY_BLOCKED"
	ERR_DEPENDENCY_CIRCULAR ErrorCode = "ERR_DEPENDENCY_CIRCULAR"

	// Kubernetes related errors (3)
	ERR_K8S_JOB_CREATION_FAILED ErrorCode = "ERR_K8S_JOB_CREATION_FAILED"
	ERR_K8S_JOB_NOT_FOUND       ErrorCode = "ERR_K8S_JOB_NOT_FOUND"
	ERR_K8S_CLIENT_ERROR        ErrorCode = "ERR_K8S_CLIENT_ERROR"

	// Database related errors (3)
	ERR_DB_RECORD_NOT_FOUND  ErrorCode = "ERR_DB_RECORD_NOT_FOUND"
	ERR_DB_CONNECTION_FAILED ErrorCode = "ERR_DB_CONNECTION_FAILED"
	ERR_DB_QUERY_FAILED      ErrorCode = "ERR_DB_QUERY_FAILED"

	// Validation related errors (3)
	ERR_VALIDATION_INVALID_INPUT    ErrorCode = "ERR_VALIDATION_INVALID_INPUT"
	ERR_VALIDATION_MISSING_REQUIRED ErrorCode = "ERR_VALIDATION_MISSING_REQUIRED"
	ERR_VALIDATION_INVALID_STATUS   ErrorCode = "ERR_VALIDATION_INVALID_STATUS"

	// Agent execution related errors (3)
	ERR_AGENT_RUN_NOT_FOUND            ErrorCode = "ERR_AGENT_RUN_NOT_FOUND"
	ERR_AGENT_RUN_MAX_RETRIES_EXCEEDED ErrorCode = "ERR_AGENT_RUN_MAX_RETRIES_EXCEEDED"
	ERR_AGENT_RUN_INVALID_STATE        ErrorCode = "ERR_AGENT_RUN_INVALID_STATE"

	// Merge related errors (3)
	ERR_MERGE_CONFLICT             ErrorCode = "ERR_MERGE_CONFLICT"
	ERR_MERGE_NOT_MERGEABLE        ErrorCode = "ERR_MERGE_NOT_MERGEABLE"
	ERR_MERGE_PROTECTION_VIOLATION ErrorCode = "ERR_MERGE_PROTECTION_VIOLATION"

	// Internal errors (2)
	ERR_INTERNAL_SERVER_ERROR ErrorCode = "ERR_INTERNAL_SERVER_ERROR"
	ERR_INTERNAL_UNEXPECTED   ErrorCode = "ERR_INTERNAL_UNEXPECTED"
)

// ErrorMessage contains user-friendly messages in multiple languages
type ErrorMessage struct {
	JA string // Japanese message
	EN string // English message
}

// errorMessages maps error codes to user-friendly messages
var errorMessages = map[ErrorCode]ErrorMessage{
	ERR_WEBHOOK_MISSING_DELIVERY: {
		JA: "Webhookリクエストに必須ヘッダーが含まれていません",
		EN: "Required header is missing in webhook request",
	},
	ERR_WEBHOOK_INVALID_PAYLOAD: {
		JA: "Webhookペイロードの形式が不正です",
		EN: "Invalid webhook payload format",
	},
	ERR_WEBHOOK_INVALID_REPO_FORMAT: {
		JA: "リポジトリ名の形式が不正です",
		EN: "Invalid repository name format",
	},
	ERR_WEBHOOK_PAYLOAD_NOT_FOUND: {
		JA: "Webhookペイロードが見つかりません",
		EN: "Webhook payload not found",
	},
	ERR_GITHUB_RATE_LIMIT: {
		JA: "GitHub APIのレート制限に達しました。しばらく待ってから再試行してください",
		EN: "GitHub API rate limit exceeded. Please wait and try again",
	},
	ERR_GITHUB_NOT_FOUND: {
		JA: "リソースが見つかりません",
		EN: "Resource not found",
	},
	ERR_GITHUB_UNAUTHORIZED: {
		JA: "認証に失敗しました",
		EN: "Authentication failed",
	},
	ERR_GITHUB_FORBIDDEN: {
		JA: "権限が不足しています",
		EN: "Insufficient permissions",
	},
	ERR_GITHUB_SERVER_ERROR: {
		JA: "GitHub APIサーバーでエラーが発生しました",
		EN: "GitHub API server error occurred",
	},
	ERR_AUTH_INSUFFICIENT_PERMISSION: {
		JA: "実行権限が不足しています（Collaborator+が必要です）",
		EN: "Insufficient execution permission (Collaborator+ required)",
	},
	ERR_AUTH_MISSING_TOKEN: {
		JA: "認証トークンが設定されていません",
		EN: "Authentication token is missing",
	},
	ERR_DEPENDENCY_BLOCKED: {
		JA: "依存Issueが未クローズのためブロックされています",
		EN: "Blocked by unclosed dependency issues",
	},
	ERR_DEPENDENCY_CIRCULAR: {
		JA: "循環依存が検出されました",
		EN: "Circular dependency detected",
	},
	ERR_K8S_JOB_CREATION_FAILED: {
		JA: "Kubernetes Jobの作成に失敗しました",
		EN: "Failed to create Kubernetes Job",
	},
	ERR_K8S_JOB_NOT_FOUND: {
		JA: "Kubernetes Jobが見つかりません",
		EN: "Kubernetes Job not found",
	},
	ERR_K8S_CLIENT_ERROR: {
		JA: "Kubernetes APIクライアントエラーが発生しました",
		EN: "Kubernetes API client error occurred",
	},
	ERR_DB_RECORD_NOT_FOUND: {
		JA: "レコードが見つかりません",
		EN: "Record not found",
	},
	ERR_DB_CONNECTION_FAILED: {
		JA: "データベースへの接続に失敗しました",
		EN: "Failed to connect to database",
	},
	ERR_DB_QUERY_FAILED: {
		JA: "データベースクエリの実行に失敗しました",
		EN: "Failed to execute database query",
	},
	ERR_VALIDATION_INVALID_INPUT: {
		JA: "入力値が不正です",
		EN: "Invalid input value",
	},
	ERR_VALIDATION_MISSING_REQUIRED: {
		JA: "必須フィールドが欠如しています",
		EN: "Required field is missing",
	},
	ERR_VALIDATION_INVALID_STATUS: {
		JA: "ステータス値が不正です",
		EN: "Invalid status value",
	},
	ERR_AGENT_RUN_NOT_FOUND: {
		JA: "AgentRunが見つかりません",
		EN: "AgentRun not found",
	},
	ERR_AGENT_RUN_MAX_RETRIES_EXCEEDED: {
		JA: "最大リトライ回数に達しました",
		EN: "Maximum retry count exceeded",
	},
	ERR_AGENT_RUN_INVALID_STATE: {
		JA: "AgentRunの状態が不正です",
		EN: "Invalid AgentRun state",
	},
	ERR_MERGE_CONFLICT: {
		JA: "マージコンフリクトが発生しました",
		EN: "Merge conflict occurred",
	},
	ERR_MERGE_NOT_MERGEABLE: {
		JA: "マージ不可能な状態です",
		EN: "Pull request is not mergeable",
	},
	ERR_MERGE_PROTECTION_VIOLATION: {
		JA: "マージ保護ルールに違反しています",
		EN: "Merge protection rule violation",
	},
	ERR_INTERNAL_SERVER_ERROR: {
		JA: "内部サーバーエラーが発生しました",
		EN: "Internal server error occurred",
	},
	ERR_INTERNAL_UNEXPECTED: {
		JA: "予期しないエラーが発生しました",
		EN: "Unexpected error occurred",
	},
}

// CodedError represents an error with a standardized error code
type CodedError struct {
	Code    ErrorCode
	Message string
	Details map[string]interface{}
	Cause   error
}

// Error implements the error interface
func (e *CodedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

// Unwrap returns the underlying error for error unwrapping
func (e *CodedError) Unwrap() error {
	return e.Cause
}

// NewCodedError creates a new CodedError
func NewCodedError(code ErrorCode, message string, cause error) *CodedError {
	return &CodedError{
		Code:    code,
		Message: message,
		Cause:   cause,
		Details: make(map[string]interface{}),
	}
}

// WrapCodedError wraps an existing error with an error code
func WrapCodedError(code ErrorCode, cause error) *CodedError {
	if cause == nil {
		return nil
	}
	return &CodedError{
		Code:    code,
		Message: cause.Error(),
		Cause:   cause,
		Details: make(map[string]interface{}),
	}
}

// GetUserMessage returns a user-friendly message for the error
// lang should be "ja" or "en", defaults to "ja" if not specified
func GetUserMessage(err error, lang string) string {
	if err == nil {
		return ""
	}

	// Check if it's a coded error by checking the error chain
	isCodedError := false
	currentErr := err
	for currentErr != nil {
		if _, ok := currentErr.(*CodedError); ok {
			isCodedError = true
			break
		}
		if _, ok := currentErr.(GitHubErrorCodeExtractor); ok {
			isCodedError = true
			break
		}
		if _, ok := currentErr.(CircularDependencyErrorCodeExtractor); ok {
			isCodedError = true
			break
		}
		currentErr = errors.Unwrap(currentErr)
	}

	// For non-coded errors, return the error message itself
	if !isCodedError {
		return err.Error()
	}

	code := GetErrorCode(err)
	msg, ok := errorMessages[code]
	if !ok {
		return err.Error()
	}

	if lang == "en" {
		if msg.EN != "" {
			return msg.EN
		}
		// Fallback to Japanese if English is not available
		return msg.JA
	}

	// Default to Japanese
	if msg.JA != "" {
		return msg.JA
	}
	return msg.EN
}

// GetErrorCode extracts the error code from an error
// Returns ERR_INTERNAL_UNEXPECTED if the error is not a CodedError
func GetErrorCode(err error) ErrorCode {
	if err == nil {
		return ""
	}

	// Check error chain recursively
	for err != nil {
		// Check if it's a CodedError
		if codedErr, ok := err.(*CodedError); ok {
			return codedErr.Code
		}

		// Check if it implements GitHubErrorCodeExtractor
		if extractor, ok := err.(GitHubErrorCodeExtractor); ok {
			return extractor.GetErrorCode()
		}

		// Check if it implements CircularDependencyErrorCodeExtractor
		if extractor, ok := err.(CircularDependencyErrorCodeExtractor); ok {
			return extractor.GetErrorCode()
		}

		// Unwrap and continue
		err = errors.Unwrap(err)
	}

	// Default to unexpected error
	return ERR_INTERNAL_UNEXPECTED
}

// IsErrorCode checks if an error has a specific error code
func IsErrorCode(err error, code ErrorCode) bool {
	return GetErrorCode(err) == code
}

// GetHTTPStatusCode returns the HTTP status code for an error code
func GetHTTPStatusCode(code ErrorCode) int {
	switch code {
	// Webhook errors - 400 Bad Request
	case ERR_WEBHOOK_MISSING_DELIVERY,
		ERR_WEBHOOK_INVALID_PAYLOAD,
		ERR_WEBHOOK_INVALID_REPO_FORMAT,
		ERR_WEBHOOK_PAYLOAD_NOT_FOUND:
		return http.StatusBadRequest

	// GitHub API errors
	case ERR_GITHUB_RATE_LIMIT:
		return http.StatusTooManyRequests
	case ERR_GITHUB_NOT_FOUND:
		return http.StatusNotFound
	case ERR_GITHUB_UNAUTHORIZED:
		return http.StatusUnauthorized
	case ERR_GITHUB_FORBIDDEN:
		return http.StatusForbidden
	case ERR_GITHUB_SERVER_ERROR:
		return http.StatusBadGateway

	// Authentication/Authorization errors
	case ERR_AUTH_INSUFFICIENT_PERMISSION,
		ERR_AUTH_MISSING_TOKEN:
		return http.StatusForbidden

	// Dependency errors - 409 Conflict
	case ERR_DEPENDENCY_BLOCKED,
		ERR_DEPENDENCY_CIRCULAR:
		return http.StatusConflict

	// Kubernetes errors
	case ERR_K8S_JOB_CREATION_FAILED,
		ERR_K8S_CLIENT_ERROR:
		return http.StatusInternalServerError
	case ERR_K8S_JOB_NOT_FOUND:
		return http.StatusNotFound

	// Database errors
	case ERR_DB_RECORD_NOT_FOUND:
		return http.StatusNotFound
	case ERR_DB_CONNECTION_FAILED,
		ERR_DB_QUERY_FAILED:
		return http.StatusInternalServerError

	// Validation errors - 400 Bad Request
	case ERR_VALIDATION_INVALID_INPUT,
		ERR_VALIDATION_MISSING_REQUIRED,
		ERR_VALIDATION_INVALID_STATUS:
		return http.StatusBadRequest

	// Agent execution errors
	case ERR_AGENT_RUN_NOT_FOUND:
		return http.StatusNotFound
	case ERR_AGENT_RUN_MAX_RETRIES_EXCEEDED,
		ERR_AGENT_RUN_INVALID_STATE:
		return http.StatusBadRequest

	// Merge errors - 409 Conflict
	case ERR_MERGE_CONFLICT,
		ERR_MERGE_NOT_MERGEABLE,
		ERR_MERGE_PROTECTION_VIOLATION:
		return http.StatusConflict

	// Internal errors
	case ERR_INTERNAL_SERVER_ERROR,
		ERR_INTERNAL_UNEXPECTED:
		return http.StatusInternalServerError

	default:
		return http.StatusInternalServerError
	}
}

// GitHubErrorCodeExtractor is an interface for extracting error codes from GitHub errors
// This will be implemented by the GitHubError type in clients/github.go
type GitHubErrorCodeExtractor interface {
	GetErrorCode() ErrorCode
}

// CircularDependencyErrorCodeExtractor is an interface for extracting error codes from circular dependency errors
// This will be implemented by the CircularDependencyError type in services/circular_dependency_detector.go
type CircularDependencyErrorCodeExtractor interface {
	GetErrorCode() ErrorCode
}
