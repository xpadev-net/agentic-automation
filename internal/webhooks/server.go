package webhooks

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"context"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	eventHeader    = "X-GitHub-Event"
	deliveryHeader = "X-GitHub-Delivery"
	webhookPath    = "/webhooks/github"
)

// Server represents the webhook server
type Server struct {
	router *gin.Engine
	logger *zap.Logger
	server *http.Server
}

// setupRouter creates and configures the Gin router
func setupRouter(logger *zap.Logger) *gin.Engine {
	// Set Gin mode based on environment
	env := config.GetEnv("ENV", "development")
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// Create router (use gin.New() for all environments)
	router := gin.New()

	// Apply global error handling middleware (before signature verification)
	router.Use(middleware.ErrorHandler())

	// Apply signature verification and idempotency middleware to webhook endpoint
	router.POST(webhookPath,
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		handleGitHubWebhook)

	// APIルート (Bearer認証)
	router.POST("/api/agent-runs/:id/report",
		middleware.VerifyBearerToken(),
		handlers.HandleAgentReport)

	return router
}

// handleGitHubWebhook is the handler for GitHub webhook events
// It routes events to appropriate handlers based on event type
func handleGitHubWebhook(c *gin.Context) {
	logger := config.GetLogger()

	// Get headers
	eventType := c.GetHeader(eventHeader)
	deliveryID := c.GetHeader(deliveryHeader)

	// Route to specific handler based on event type
	switch eventType {
	case models.EventTypeIssueComment:
		handlers.HandleIssueComment(c)
		return
	default:
		// For unhandled event types, log and return 200 OK
		// Get payload from context (set by signature middleware)
		payload, exists := c.Get("webhook_payload")
		if !exists {
			// If payload is not in context, try to read from request body
			body, err := c.GetRawData()
			if err != nil {
				c.Error(err)
				return
			}
			payload = body
		}

		// Log the webhook event
		logger.Info("Received unhandled GitHub webhook event",
			zap.String("event_type", eventType),
			zap.String("delivery_id", deliveryID),
			zap.Int("payload_size", len(payload.([]byte))),
		)

		// Return 200 OK to acknowledge receipt
		c.JSON(200, gin.H{
			"status":      "received",
			"event":       eventType,
			"delivery_id": deliveryID,
		})
	}
}

// NewServer creates a new webhook server instance
func NewServer() (*Server, error) {
	logger := config.GetLogger()

	// Get port from environment (default: 3000)
	port := config.GetEnv("PORT", "3000")

	// Setup router
	router := setupRouter(logger)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	return &Server{
		router: router,
		logger: logger,
		server: httpServer,
	}, nil
}

// Start starts the webhook server
func (s *Server) Start() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	s.logger.Info("Starting webhook server", zap.String("port", port))

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.logger.Error("Failed to start server", zap.Error(err))
		return err
	}

	return nil
}

// Shutdown gracefully shuts down the webhook server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down webhook server")

	if err := s.server.Shutdown(ctx); err != nil {
		s.logger.Error("Error during server shutdown", zap.Error(err))
		return err
	}

	s.logger.Info("Webhook server stopped")
	return nil
}
