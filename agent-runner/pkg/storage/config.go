package storage

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// S3Config holds S3 session persistence configuration.
// All fields are populated from environment variables via LoadS3Config().
type S3Config struct {
	// Endpoint is the S3 API endpoint URL (e.g., "http://minio:9000" or "https://s3.amazonaws.com").
	Endpoint string

	// Region is the S3 region name (e.g., "us-east-1").
	Region string

	// Bucket is the S3 bucket name for storing session data (e.g., "agent-sessions").
	Bucket string

	// AccessKeyID is the S3 access key ID for authentication.
	AccessKeyID string

	// SecretAccessKey is the S3 secret access key for authentication.
	SecretAccessKey string

	// UsePathStyle controls whether to use path-style URLs (required for MinIO).
	// If true, URLs are formatted as: http://endpoint/bucket/key
	// If false, URLs are formatted as: http://bucket.endpoint/key
	UsePathStyle bool

	// MaxRetries is the maximum number of retry attempts for S3 operations (default: 5).
	MaxRetries int
}

// LoadS3Config loads S3 configuration from environment variables.
// Returns an error if required variables are missing.
func LoadS3Config() (*S3Config, error) {
	cfg := &S3Config{}
	var missing []string

	// Required: S3_ENDPOINT
	if cfg.Endpoint = os.Getenv("S3_ENDPOINT"); cfg.Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}

	// Required: S3_REGION
	if cfg.Region = os.Getenv("S3_REGION"); cfg.Region == "" {
		missing = append(missing, "S3_REGION")
	}

	// Required: S3_BUCKET
	if cfg.Bucket = os.Getenv("S3_BUCKET"); cfg.Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}

	// Required: S3_ACCESS_KEY_ID
	if cfg.AccessKeyID = os.Getenv("S3_ACCESS_KEY_ID"); cfg.AccessKeyID == "" {
		missing = append(missing, "S3_ACCESS_KEY_ID")
	}

	// Required: S3_SECRET_ACCESS_KEY
	if cfg.SecretAccessKey = os.Getenv("S3_SECRET_ACCESS_KEY"); cfg.SecretAccessKey == "" {
		missing = append(missing, "S3_SECRET_ACCESS_KEY")
	}

	// Optional: S3_USE_PATH_STYLE (default: true for MinIO compatibility)
	usePathStyleStr := os.Getenv("S3_USE_PATH_STYLE")
	if usePathStyleStr == "" {
		cfg.UsePathStyle = true // デフォルト値
	} else {
		cfg.UsePathStyle = parseBool(usePathStyleStr, true)
	}

	// Optional: S3_MAX_RETRIES (default: 5)
	maxRetriesStr := os.Getenv("S3_MAX_RETRIES")
	if maxRetriesStr == "" {
		cfg.MaxRetries = 5 // デフォルト値
	} else {
		maxRetries, err := strconv.Atoi(maxRetriesStr)
		if err != nil {
			return nil, fmt.Errorf("S3_MAX_RETRIES must be an integer, got: %q", maxRetriesStr)
		}
		if maxRetries < 0 {
			return nil, fmt.Errorf("S3_MAX_RETRIES must be non-negative, got: %d", maxRetries)
		}
		cfg.MaxRetries = maxRetries
	}

	// Check for missing required variables
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required S3 environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// parseBool parses a boolean value from a string.
// Recognizes "true", "1", "yes", "on", "enabled" as true (case-insensitive).
// Recognizes "false", "0", "no", "off", "disabled" as false (case-insensitive).
// Returns defaultValue if the value is empty or does not match any pattern.
func parseBool(value string, defaultValue bool) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}
