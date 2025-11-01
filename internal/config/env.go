package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// LoadEnv loads environment variables from .env file
// It will not override existing environment variables
func LoadEnv() error {
	// Try to load .env file, but don't fail if it doesn't exist
	if err := godotenv.Load(); err != nil {
		// .env file is optional in production
		if _, ok := err.(*os.PathError); !ok {
			return fmt.Errorf("failed to load .env file: %w", err)
		}
	}

	return nil
}

// GetEnv returns an environment variable value, or default if not set
func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// GetEnvRequired returns an environment variable value, or error if not set
func GetEnvRequired(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return value, nil
}

// GetEnvInt returns an integer environment variable value, or default if not set or invalid
// It parses the environment variable value using strconv.Atoi()
// If the value is empty or cannot be parsed as an integer, defaultValue is returned
func GetEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}

// GetEnvBool returns a boolean environment variable value, or default if not set or invalid
// It recognizes the following as true (case-insensitive): "true", "1", "yes", "on", "enabled"
// It recognizes the following as false (case-insensitive): "false", "0", "no", "off", "disabled"
// If the value is empty or does not match any of these patterns, defaultValue is returned
func GetEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	valueLower := strings.ToLower(strings.TrimSpace(value))
	switch valueLower {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}

// GetEnvDuration returns a time.Duration environment variable value, or default if not set or invalid
// It parses the environment variable value using time.ParseDuration()
// Supported formats include: "30s", "5m", "1h", "24h", etc.
// If the value is empty or cannot be parsed as a duration, defaultValue is returned
func GetEnvDuration(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	durationValue, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}
	return durationValue
}

// GetEnvIntRequired returns an integer environment variable value, or error if not set or invalid
// It parses the environment variable value using strconv.Atoi()
// Returns an error if the environment variable is not set, is empty, or cannot be parsed as an integer
// The error message includes the variable name and invalid value for debugging
func GetEnvIntRequired(key string) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return 0, fmt.Errorf("required environment variable %s is not set", key)
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("environment variable %s contains invalid integer value: %s", key, value)
	}
	return intValue, nil
}

// InitConfig initializes the configuration by loading environment variables
// This should be called at application startup
func InitConfig() error {
	// Load .env file first (so env vars are available for logger config)
	if err := LoadEnv(); err != nil {
		// LoadEnv returns nil for missing files (os.PathError) - that's normal
		// But if it returns a non-nil error, the file exists but couldn't be parsed
		// This is a configuration error and we should fail fast
		return fmt.Errorf("configuration initialization failed: %w", err)
	}

	// Initialize logger after loading env vars (so LOG_LEVEL is available)
	if err := InitLogger(); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}

	// Initialize database connection after logger (so we can log connection status)
	if err := InitDatabase(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Now we can use the logger
	logger := GetLogger()
	logger.Info("Configuration initialized")
	return nil
}
