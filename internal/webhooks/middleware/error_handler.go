package middleware

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const webhookPath = "/webhooks/github"

// ErrorHandler returns a Gin middleware that handles panics and errors globally
func ErrorHandler() gin.HandlerFunc {
	logger := config.GetLogger()

	return func(c *gin.Context) {
		// Panic recovery
		defer handlePanic(c, logger)

		// Process the request
		c.Next()

		// Handle errors set by handlers using c.Error()
		if len(c.Errors) > 0 {
			handleError(c, logger)
		}
	}
}

// handlePanic recovers from panics and logs the error with stack trace
func handlePanic(c *gin.Context, logger *zap.Logger) {
	if r := recover(); r != nil {
		// Get stack trace
		stack := debug.Stack()

		// Log panic details
		logger.Error("Panic recovered",
			zap.Any("panic", r),
			zap.String("path", c.Request.URL.Path),
			zap.String("method", c.Request.Method),
			zap.String("stack", string(stack)),
		)

		// Determine if this is a webhook request
		isWebhook := isWebhookRequest(c)

		// For webhook requests, always return 200 OK to prevent GitHub retries
		if isWebhook {
			c.JSON(http.StatusOK, gin.H{
				"error": "internal server error",
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal server error",
			})
		}

		c.Abort()
	}
}

// handleError processes errors set by handlers using c.Error()
func handleError(c *gin.Context, logger *zap.Logger) {
	// Get the last error (most recent)
	err := c.Errors.Last()

	if err == nil {
		return
	}

	// Log error details
	logger.Error("Request error",
		zap.Error(err.Err),
		zap.String("path", c.Request.URL.Path),
		zap.String("method", c.Request.Method),
		zap.String("type", err.Type.String()),
	)

	// Determine if this is a webhook request
	isWebhook := isWebhookRequest(c)

	// Get HTTP status code based on error type
	statusCode := getHTTPStatusCode(err.Err)

	// For webhook requests, always return 200 OK to prevent GitHub retries
	if isWebhook {
		c.JSON(http.StatusOK, gin.H{
			"error": err.Error(),
		})
	} else {
		c.JSON(statusCode, gin.H{
			"error": err.Error(),
		})
	}

	c.Abort()
}

// isWebhookRequest determines if the request is for a webhook endpoint
func isWebhookRequest(c *gin.Context) bool {
	return c.Request.URL.Path == webhookPath
}

// getHTTPStatusCode returns the appropriate HTTP status code for an error
func getHTTPStatusCode(err error) int {
	// Check if it's a GitHubError
	if ghErr, ok := err.(*clients.GitHubError); ok {
		if ghErr.ErrorResponse != nil && ghErr.ErrorResponse.Response != nil {
			statusCode := ghErr.ErrorResponse.Response.StatusCode
			// For GitHub API errors, use the status code if it's a server error (5xx)
			// Otherwise use 502 Bad Gateway or 503 Service Unavailable
			if statusCode >= 500 {
				return statusCode
			}
			// For rate limits and other client errors, return 502 Bad Gateway
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusForbidden {
				return http.StatusBadGateway
			}
			// For other 4xx errors from GitHub API, return 502 Bad Gateway
			return http.StatusBadGateway
		}
		// GitHubError without ErrorResponse (e.g., rate limit message only)
		return http.StatusServiceUnavailable
	}

	// Default to 500 Internal Server Error for unknown errors
	return http.StatusInternalServerError
}
