package main

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/services"
	"agentic-automation/internal/version"
	"agentic-automation/internal/webhooks"
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Initialize configuration (loads .env, sets up logger and database)
	if err := config.InitConfig(); err != nil {
		// If logger is not yet initialized, use standard log
		logger := config.GetLogger()
		logger.Fatal("Failed to initialize configuration", config.Error(err))
	}

	logger := config.GetLogger()

	// Build information
	logger.Info("Build info", config.String("commit", version.Commit), config.String("builtAt", version.BuiltAt))

	// Create webhook server
	server, err := webhooks.NewServer()
	if err != nil {
		logger.Fatal("Failed to create webhook server", config.Error(err))
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Create error channel for startup failures
	errChan := make(chan error, 1)

	// Start server in a goroutine
	go func() {
		if err := server.Start(); err != nil {
			errChan <- err
		}
	}()

	lifecycleCtx, stopLifecycle := context.WithCancel(context.Background())
	reconciler, err := services.NewAgentRunReconciler(config.GetDB(), func() (services.JobObserver, error) { return clients.NewKubernetesClient(logger) }, logger, services.DefaultReconcileOptions())
	if err != nil {
		logger.Fatal("Failed to configure lifecycle reconciliation", config.Error(err))
	}
	lifecycleDone := make(chan struct{})
	go func() { defer close(lifecycleDone); reconciler.Run(lifecycleCtx) }()
	defer stopLifecycle()

	logger.Info("Webhook server starting. Press Ctrl+C to stop.")

	// Wait for interrupt signal or startup error
	select {
	case sig := <-sigChan:
		logger.Info("Shutdown signal received", config.String("signal", sig.String()))
	case err := <-errChan:
		logger.Fatal("Server failed to start", config.Error(err))
	}

	// Create context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stopLifecycle()
	select {
	case <-lifecycleDone:
	case <-ctx.Done():
		logger.Warn("Lifecycle shutdown timed out")
	}

	// Shutdown server
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Error during server shutdown", config.Error(err))
		os.Exit(1)
	}

	logger.Info("Server stopped gracefully")
}
