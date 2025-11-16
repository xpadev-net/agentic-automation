package middleware

import (
	"agentic-automation/internal/config"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
)

const signatureHeader = "X-Hub-Signature-256"

// verifySignature verifies the GitHub webhook signature using HMAC-SHA256.
// It compares the provided signature with the computed HMAC of the payload.
// This function uses hmac.Equal for constant-time comparison to prevent timing attacks.
func verifySignature(payload []byte, signature, secret string) bool {
	// Extract the hex string from "sha256=<hex>" format
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}

	expectedSignature := strings.TrimPrefix(signature, prefix)

	// Validate hex string format
	if len(expectedSignature) != 64 { // SHA256 produces 64 hex characters
		return false
	}

	// Compute HMAC-SHA256
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	computed := hex.EncodeToString(mac.Sum(nil))

	// Use hmac.Equal for constant-time comparison to prevent timing attacks
	return hmac.Equal([]byte(computed), []byte(expectedSignature))
}

// VerifyWebhookSignature is a Gin middleware that verifies GitHub webhook signatures.
// It reads the X-Hub-Signature-256 header and validates it against the request body
// using the GITHUB_WEBHOOK_SECRET environment variable.
//
// On verification failure, it aborts the request with 401 Unauthorized.
// On success, it calls c.Next() to continue to the next handler.
func VerifyWebhookSignature() gin.HandlerFunc {
	logger := config.GetLogger()

	// Get webhook secret from environment (required)
	secret, err := config.GetEnvRequired("GITHUB_WEBHOOK_SECRET")
	if err != nil {
		// This should be caught at server startup, but handle it here as well
		logger.Fatal("GITHUB_WEBHOOK_SECRET environment variable is required", config.Error(err))
	}

	return func(c *gin.Context) {
		// Get signature from header
		signature := c.GetHeader(signatureHeader)
		if signature == "" {
			logger.Warn("Missing X-Hub-Signature-256 header")
			c.AbortWithStatusJSON(401, gin.H{"error": "missing signature header"})
			return
		}

		// Read request body
		payload, err := c.GetRawData()
		if err != nil {
			logger.Warn("Failed to read request body for signature verification", config.Error(err))
			c.AbortWithStatusJSON(401, gin.H{"error": "failed to read request body"})
			return
		}

		// Verify signature
		if !verifySignature(payload, signature, secret) {
			logger.Warn("Invalid webhook signature")
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid signature"})
			return
		}

		// Restore request body so downstream handlers can read it
		// GetRawData() drains c.Request.Body, so we need to restore it
		c.Request.Body = io.NopCloser(bytes.NewBuffer(payload))

		// Store the payload in context as well for backwards compatibility
		c.Set("webhook_payload", payload)

		// Continue to next handler
		c.Next()
	}
}
