package main

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/webhooks"
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
)

func main() {
	// Initialize configuration (loads .env, sets up logger and database)
	if err := config.InitConfig(); err != nil {
		// If logger is not yet initialized, use standard log
		logger := config.GetLogger()
		logger.Fatal("Failed to initialize configuration", zap.Error(err))
	}

	logger := config.GetLogger()

	// Create webhook server
	server, err := webhooks.NewServer()
	if err != nil {
		logger.Fatal("Failed to create webhook server", zap.Error(err))
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := server.Start(); err != nil {
			logger.Error("Server error", zap.Error(err))
		}
	}()

	logger.Info("Webhook server started. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	<-sigChan
	logger.Info("Shutdown signal received")

	// Create context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown server
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Error during server shutdown", zap.Error(err))
		os.Exit(1)
	}

	logger.Info("Server stopped gracefully")
}
