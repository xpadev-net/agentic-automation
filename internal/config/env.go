package config

import (
	"fmt"
	"os"

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

