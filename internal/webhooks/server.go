package webhooks

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	"context"
	"net/http"
	"os"
	"time"

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
	router    *gin.Engine
	logger    *zap.Logger
	server    *http.Server
	startTime time.Time
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

	// Initialize and inject GitHub App client (fail-fast on error)
	if ghApp, err := clients.NewGitHubAppClient(logger); err == nil {
		handlers.SetAppGitHubClient(ghApp)
	} else {
		// Log error and continue; individual handlers will surface initialization errors
		logger.Error("Failed to initialize GitHub App client at startup", zap.Error(err))
	}

	// Apply global error handling middleware (before signature verification)
	router.Use(middleware.ErrorHandler())

	// Health check endpoint (no authentication required)
	router.GET("/health", handleHealth)

	// Apply signature verification and idempotency middleware to webhook endpoint
	router.POST(webhookPath,
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		handleGitHubWebhook)

	// Status event dedicated endpoint (optional in addition to generic webhook)
	router.POST("/webhooks/status",
		middleware.VerifyWebhookSignature(),
		middleware.IdempotencyMiddleware(),
		handlers.HandleStatus)

	// APIルート (Bearer認証)
	router.POST("/api/agent-runs/:id/report",
		middleware.VerifyBearerToken(),
		handlers.HandleAgentReport)

	return router
}

// handleHealth handles GET /health requests
// Returns server health status, database connectivity, uptime, and version
func handleHealth(c *gin.Context) {
	logger := config.GetLogger()

	// Check database connectivity
	dbStatus := "connected"
	db := config.GetDB()
	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("Failed to get database connection for health check", zap.Error(err))
		dbStatus = "disconnected"
	} else if err := sqlDB.Ping(); err != nil {
		logger.Error("Database ping failed during health check", zap.Error(err))
		dbStatus = "disconnected"
	}

	// Calculate uptime from server start time
	var uptime int64
	if server, exists := c.Get("server"); exists {
		if srv, ok := server.(*Server); ok {
			uptime = int64(time.Since(srv.startTime).Seconds())
		}
	}

	// Get version from environment or use git hash
	version := config.GetEnv("VERSION", "dev")

	// Determine overall status
	status := "ok"
	if dbStatus == "disconnected" {
		status = "degraded"
	}

	// Return appropriate status code
	statusCode := http.StatusOK
	if status == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, gin.H{
		"status":   status,
		"database": dbStatus,
		"uptime":   uptime,
		"version":  version,
	})
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
	case models.EventTypePullRequestReviewComment:
		handlers.HandlePullRequestReviewComment(c)
		return
	case models.EventTypeCheckSuite:
		handlers.HandleCheckSuite(c)
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

	server := &Server{
		router:    router,
		logger:    logger,
		server:    httpServer,
		startTime: time.Now(),
	}

	// Store server instance in router context for health checks
	router.Use(func(c *gin.Context) {
		c.Set("server", server)
		c.Next()
	})

	return server, nil
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
