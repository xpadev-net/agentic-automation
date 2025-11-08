package middleware

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/utils"
	"fmt"
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

		code := utils.ERR_INTERNAL_SERVER_ERROR
		userMsg := utils.GetUserMessage(utils.NewCodedError(code, "", nil), "ja")

		// For webhook requests, always return 200 OK to prevent GitHub retries
		if isWebhook {
			c.JSON(http.StatusOK, gin.H{
				"error":      string(code),
				"message":    userMsg,
				"error_code": string(code),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      string(code),
				"message":    userMsg,
				"error_code": string(code),
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

	code := utils.GetErrorCode(err.Err)
	userMsg := utils.GetUserMessage(err.Err, "ja")
	statusCode := utils.GetHTTPStatusCode(code)

	// Log error details
	logger.Error("Request error",
		zap.Error(err.Err),
		zap.String("error_code", string(code)),
		zap.String("path", c.Request.URL.Path),
		zap.String("method", c.Request.Method),
		zap.String("type", fmt.Sprintf("%v", err.Type)),
	)

	// Determine if this is a webhook request
	isWebhook := isWebhookRequest(c)

	response := gin.H{
		"error":      string(code),
		"message":    userMsg,
		"error_code": string(code),
	}

	// For webhook requests, always return 200 OK to prevent GitHub retries
	if isWebhook {
		c.JSON(http.StatusOK, response)
	} else {
		c.JSON(statusCode, response)
	}

	c.Abort()
}

// isWebhookRequest determines if the request is for a webhook endpoint
func isWebhookRequest(c *gin.Context) bool {
	return c.Request.URL.Path == webhookPath
}

// getHTTPStatusCode returns the appropriate HTTP status code for an error
// This function is kept for backward compatibility but is now deprecated.
// Use utils.GetHTTPStatusCode(utils.GetErrorCode(err)) instead.
func getHTTPStatusCode(err error) int {
	code := utils.GetErrorCode(err)
	return utils.GetHTTPStatusCode(code)
}
