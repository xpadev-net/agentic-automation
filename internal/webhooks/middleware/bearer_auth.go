package middleware

import (
	"agentic-automation/internal/config"
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const authorizationHeader = "Authorization"
const bearerPrefix = "Bearer "

// VerifyBearerToken is a Gin middleware that verifies Bearer token authentication.
// It reads the Authorization header and validates it against the OPERATOR_API_TOKEN
// environment variable.
//
// On verification failure, it aborts the request with 401 Unauthorized.
// On success, it calls c.Next() to continue to the next handler.
func VerifyBearerToken() gin.HandlerFunc {
	logger := config.GetLogger()

	// Get API token from environment (required)
	token, err := config.GetEnvRequired("OPERATOR_API_TOKEN")
	if err != nil {
		// This should be caught at server startup, but handle it here as well
		logger.Fatal("OPERATOR_API_TOKEN environment variable is required", zap.Error(err))
	}

	return func(c *gin.Context) {
		// Get Authorization header
		authHeader := c.GetHeader(authorizationHeader)
		if authHeader == "" {
			logger.Warn("Missing Authorization header",
				zap.String("path", c.Request.URL.Path),
			)
			c.AbortWithStatusJSON(401, gin.H{
				"error":   "INVALID_TOKEN",
				"message": "Invalid or missing Bearer token",
			})
			return
		}

		// Check if it starts with "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			logger.Warn("Authorization header does not start with 'Bearer '",
				zap.String("path", c.Request.URL.Path),
			)
			c.AbortWithStatusJSON(401, gin.H{
				"error":   "INVALID_TOKEN",
				"message": "Invalid or missing Bearer token",
			})
			return
		}

		// Extract token (remove "Bearer " prefix)
		providedToken := strings.TrimPrefix(authHeader, bearerPrefix)
		if providedToken == "" {
			logger.Warn("Empty Bearer token",
				zap.String("path", c.Request.URL.Path),
			)
			c.AbortWithStatusJSON(401, gin.H{
				"error":   "INVALID_TOKEN",
				"message": "Invalid or missing Bearer token",
			})
			return
		}

		// Compare tokens using constant-time comparison to prevent timing attacks
		// Use subtle.ConstantTimeCompare for secure comparison
		if len(providedToken) != len(token) || subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
			logger.Warn("Invalid Bearer token",
				zap.String("path", c.Request.URL.Path),
			)
			c.AbortWithStatusJSON(401, gin.H{
				"error":   "INVALID_TOKEN",
				"message": "Invalid or missing Bearer token",
			})
			return
		}

		// Token is valid, continue to next handler
		c.Next()
	}
}
