package config

import (
	"context"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the global logger instance
var Logger *zap.Logger

// contextKey is a type for context keys to avoid collisions
type contextKey string

const (
	// githubEventIDKey is the context key for GitHub event ID (X-GitHub-Delivery)
	githubEventIDKey contextKey = "github_event_id"
	// agentRunIDKey is the context key for agent run ID
	agentRunIDKey contextKey = "agent_run_id"
	// operationIDKey is the context key for operation ID
	operationIDKey contextKey = "operation_id"
)

// InitLogger initializes the global logger with configuration from environment variables
func InitLogger() error {
	var config zap.Config
	var err error

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	// Parse log level
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(logLevel)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	if env == "production" {
		// Production: JSON output
		config = zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(zapLevel)
	} else {
		// Development: Human-readable output
		config = zap.NewDevelopmentConfig()
		config.Level = zap.NewAtomicLevelAt(zapLevel)
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	Logger, err = config.Build()
	if err != nil {
		return err
	}

	return nil
}

// GetLogger returns the global logger instance
// Must call InitLogger() first
func GetLogger() *zap.Logger {
	if Logger == nil {
		// Fallback to a basic logger if InitLogger hasn't been called
		logger, _ := zap.NewDevelopment()
		return logger
	}
	return Logger
}

// SetLoggerForTesting sets the logger instance for testing purposes only
// This function should only be used in test files
func SetLoggerForTesting(testLogger *zap.Logger) {
	Logger = testLogger
}

// ResetLoggerForTesting resets the logger instance to nil for testing purposes only
// This function should only be used in test files
func ResetLoggerForTesting() {
	Logger = nil
}

// ContextWithTraceIDs creates a context with trace IDs attached.
// Empty strings are ignored, allowing partial trace ID assignment.
// This function can be called multiple times to add additional trace IDs.
func ContextWithTraceIDs(ctx context.Context, githubEventID, agentRunID, operationID string) context.Context {
	if githubEventID != "" {
		ctx = context.WithValue(ctx, githubEventIDKey, githubEventID)
	}
	if agentRunID != "" {
		ctx = context.WithValue(ctx, agentRunIDKey, agentRunID)
	}
	if operationID != "" {
		ctx = context.WithValue(ctx, operationIDKey, operationID)
	}
	return ctx
}

// LoggerWithTraceIDs returns a logger with trace IDs extracted from the context.
// Extracts github_event_id, agent_run_id, and operation_id from context if present.
// Returns a logger with trace ID fields attached, or the base logger if no trace IDs are found.
func LoggerWithTraceIDs(ctx context.Context) *zap.Logger {
	logger := GetLogger()
	var fields []zap.Field

	if githubEventID := ctx.Value(githubEventIDKey); githubEventID != nil {
		if id, ok := githubEventID.(string); ok && id != "" {
			fields = append(fields, zap.String("github_event_id", id))
		}
	}

	if agentRunID := ctx.Value(agentRunIDKey); agentRunID != nil {
		if id, ok := agentRunID.(string); ok && id != "" {
			fields = append(fields, zap.String("agent_run_id", id))
		}
	}

	if operationID := ctx.Value(operationIDKey); operationID != nil {
		if id, ok := operationID.(string); ok && id != "" {
			fields = append(fields, zap.String("operation_id", id))
		}
	}

	if len(fields) > 0 {
		return logger.With(fields...)
	}
	return logger
}

// WithTraceIDs adds trace IDs to a logger instance.
// Convenient wrapper for logger.With() with trace ID fields.
// Empty strings are ignored.
func WithTraceIDs(logger *zap.Logger, githubEventID, agentRunID, operationID string) *zap.Logger {
	var fields []zap.Field

	if githubEventID != "" {
		fields = append(fields, zap.String("github_event_id", githubEventID))
	}
	if agentRunID != "" {
		fields = append(fields, zap.String("agent_run_id", agentRunID))
	}
	if operationID != "" {
		fields = append(fields, zap.String("operation_id", operationID))
	}

	if len(fields) > 0 {
		return logger.With(fields...)
	}
	return logger
}
