package config

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the global logger instance
var Logger *zap.Logger

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
