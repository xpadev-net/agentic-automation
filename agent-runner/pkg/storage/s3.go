package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/cenkalti/backoff/v4"
)

// Config represents S3 configuration.
// This is a temporary implementation until T053_S3 is completed.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
	MaxRetries      int
}

// S3Error wraps AWS SDK errors with additional context.
type S3Error struct {
	Message       string
	StatusCode    int
	OriginalError error
}

func (e *S3Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("S3 error (HTTP %d): %s", e.StatusCode, e.Message)
	}
	if e.OriginalError != nil {
		return fmt.Sprintf("S3 error: %s: %v", e.Message, e.OriginalError)
	}
	return fmt.Sprintf("S3 error: %s", e.Message)
}

func (e *S3Error) Unwrap() error {
	return e.OriginalError
}

// MaxRetriesExceededError indicates that the maximum number of retry attempts was exceeded.
type MaxRetriesExceededError struct {
	MaxAttempts int
	LastError   error
}

func (e *MaxRetriesExceededError) Error() string {
	if e.LastError != nil {
		return fmt.Sprintf("max retries (%d) exceeded: %v", e.MaxAttempts, e.LastError)
	}
	return fmt.Sprintf("max retries (%d) exceeded", e.MaxAttempts)
}

func (e *MaxRetriesExceededError) Unwrap() error {
	return e.LastError
}

// Client is the S3 client wrapper.
type Client struct {
	s3Client *s3.Client
	bucket   string
	config   *Config
}

// NewClient creates a new S3 client with the given configuration.
func NewClient(cfg *Config) (*Client, error) {
	// Validate configuration
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("access key ID and secret access key are required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1" // Default region
	}
	if cfg.MaxRetries < 0 {
		return nil, fmt.Errorf("MaxRetries must be non-negative, got: %d", cfg.MaxRetries)
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 5 // Default max retries
	}

	ctx := context.Background()

	// Build AWS SDK configuration
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Configure custom endpoint for MinIO
	if cfg.Endpoint != "" {
		awsCfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:           cfg.Endpoint,
					SigningRegion: cfg.Region,
				}, nil
			},
		)
	}

	// Create S3 client with UsePathStyle option for MinIO compatibility
	s3ClientOptions := func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
	}

	s3Client := s3.NewFromConfig(awsCfg, s3ClientOptions)

	return &Client{
		s3Client: s3Client,
		bucket:   cfg.Bucket,
		config:   cfg,
	}, nil
}

// isRetryableError determines if an error should be retried.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for AWS SDK/Smithy API errors
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		// Get HTTP status code if available
		errCode := apiErr.ErrorCode()
		// Check for specific error codes
		switch errCode {
		case "TooManyRequests", "Throttling", "ThrottlingException":
			return true
		case "ServiceUnavailable", "InternalServerError", "BadGateway", "GatewayTimeout":
			return true
		case "AccessDenied", "Forbidden", "NotFound", "NoSuchBucket", "NoSuchKey":
			return false // 4xx errors should not be retried
		}

		// Try to extract HTTP status code from error
		// AWS SDK Go v2 errors may contain status codes
		errMsg := apiErr.ErrorMessage()
		_ = errMsg // May be used for additional checks in the future
	}

	// Check for HTTP status codes in error context
	// AWS SDK Go v2 uses HTTPStatusCode() method (via smithyhttp.HTTPStatusError interface)
	var httpErr interface {
		HTTPStatusCode() int
	}
	if errors.As(err, &httpErr) {
		statusCode := httpErr.HTTPStatusCode()
		if statusCode == 429 || (statusCode >= 500 && statusCode < 600) {
			return true
		}
		if statusCode >= 400 && statusCode < 500 {
			return false // 4xx errors should not be retried
		}
	}

	// Check for network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Retry on timeouts and temporary network errors
		if netErr.Timeout() {
			return true
		}
		// Check Temporary() on the extracted net.Error
		// Note: netErr is already net.Error interface, so Temporary() method is available
		if netErr.Temporary() {
			return true
		}
	}

	// Check for net.OpError (connection reset, connection refused, etc.)
	// These errors (ECONNRESET, ECONNREFUSED) often have Timeout() and Temporary() both false,
	// but are transient network failures that should be retried
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// All network operations are considered retryable for transient failures
		return true
	}

	// Check for DNS errors (network-related)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Default: don't retry unknown errors
	return false
}

// ensureDir ensures that the directory containing the file exists.
func ensureDir(filePath string) error {
	dir := filepath.Dir(filePath)
	if dir == "." || dir == "" {
		return nil // Current directory, no need to create
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	return nil
}

// getFileSize returns the size of the file in bytes.
func getFileSize(filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat file: %w", err)
	}
	return info.Size(), nil
}

// Upload uploads a local file to S3 with exponential backoff retry.
func (c *Client) Upload(ctx context.Context, key string, filePath string) error {
	// Input validation
	if key == "" {
		return fmt.Errorf("S3 key cannot be empty")
	}
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Check if file exists and get size
	fileSize, err := getFileSize(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Uploading to S3: key=%s, size=%d\n", key, fileSize)

	// Configure exponential backoff
	backoffConfig := backoff.NewExponentialBackOff()
	backoffConfig.InitialInterval = 1 * time.Second
	backoffConfig.MaxInterval = 16 * time.Second
	backoffConfig.MaxElapsedTime = 0 // Use max retries instead

	// Retry operation
	retryCount := 0
	operation := func() error {
		retryCount++
		if retryCount > 1 {
			fmt.Fprintf(os.Stderr, "S3 upload retry attempt %d/%d\n", retryCount-1, c.config.MaxRetries)
		}

		// Open file for reading
		file, err := os.Open(filePath)
		if err != nil {
			return backoff.Permanent(fmt.Errorf("failed to open file: %w", err))
		}
		defer file.Close()

		// Create PutObject input
		putObjectInput := &s3.PutObjectInput{
			Bucket: aws.String(c.bucket),
			Key:    aws.String(key),
			Body:   file,
		}

		// Upload to S3
		_, err = c.s3Client.PutObject(ctx, putObjectInput)
		if err != nil {
			if isRetryableError(err) {
				fmt.Fprintf(os.Stderr, "S3 upload failed (attempt %d/%d): %v, retrying...\n", retryCount, c.config.MaxRetries, err)
				return err // Trigger retry
			} else {
				// Non-retryable error
				fmt.Fprintf(os.Stderr, "S3 upload failed with non-retryable error: %v\n", err)
				return backoff.Permanent(err) // Stop retrying
			}
		}

		if retryCount > 1 {
			fmt.Fprintf(os.Stderr, "S3 upload succeeded after %d attempts: key=%s\n", retryCount, key)
		} else {
			fmt.Fprintf(os.Stderr, "S3 upload succeeded: key=%s\n", key)
		}
		return nil
	}

	// Execute with retry
	err = backoff.Retry(operation, backoff.WithMaxRetries(backoffConfig, uint64(c.config.MaxRetries)))
	if err != nil {
		var permanentErr *backoff.PermanentError
		if errors.As(err, &permanentErr) {
			return fmt.Errorf("S3 upload failed with non-retryable error: %w", permanentErr.Err)
		}
		return &MaxRetriesExceededError{
			MaxAttempts: c.config.MaxRetries,
			LastError:   err,
		}
	}

	return nil
}

// Download downloads an object from S3 to a local file with exponential backoff retry.
func (c *Client) Download(ctx context.Context, key string, filePath string) error {
	// Input validation
	if key == "" {
		return fmt.Errorf("S3 key cannot be empty")
	}
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Ensure destination directory exists
	if err := ensureDir(filePath); err != nil {
		return fmt.Errorf("failed to ensure directory: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Downloading from S3: key=%s\n", key)

	// Configure exponential backoff
	backoffConfig := backoff.NewExponentialBackOff()
	backoffConfig.InitialInterval = 1 * time.Second
	backoffConfig.MaxInterval = 16 * time.Second
	backoffConfig.MaxElapsedTime = 0 // Use max retries instead

	// Retry operation
	retryCount := 0
	operation := func() error {
		retryCount++
		if retryCount > 1 {
			fmt.Fprintf(os.Stderr, "S3 download retry attempt %d/%d\n", retryCount-1, c.config.MaxRetries)
		}

		// Create GetObject input
		getObjectInput := &s3.GetObjectInput{
			Bucket: aws.String(c.bucket),
			Key:    aws.String(key),
		}

		// Download from S3
		getObjectOutput, err := c.s3Client.GetObject(ctx, getObjectInput)
		if err != nil {
			if isRetryableError(err) {
				fmt.Fprintf(os.Stderr, "S3 download failed (attempt %d/%d): %v, retrying...\n", retryCount, c.config.MaxRetries, err)
				return err // Trigger retry
			} else {
				// Non-retryable error
				fmt.Fprintf(os.Stderr, "S3 download failed with non-retryable error: %v\n", err)
				return backoff.Permanent(err) // Stop retrying
			}
		}
		defer getObjectOutput.Body.Close()

		// Create local file
		localFile, err := os.Create(filePath)
		if err != nil {
			return backoff.Permanent(fmt.Errorf("failed to create local file: %w", err))
		}
		defer localFile.Close()

		// Copy response body to local file
		_, err = io.Copy(localFile, getObjectOutput.Body)
		if err != nil {
			return backoff.Permanent(fmt.Errorf("failed to write to local file: %w", err))
		}

		if retryCount > 1 {
			fmt.Fprintf(os.Stderr, "S3 download succeeded after %d attempts: key=%s -> %s\n", retryCount, key, filePath)
		} else {
			fmt.Fprintf(os.Stderr, "S3 download succeeded: key=%s -> %s\n", key, filePath)
		}
		return nil
	}

	// Execute with retry
	err := backoff.Retry(operation, backoff.WithMaxRetries(backoffConfig, uint64(c.config.MaxRetries)))
	if err != nil {
		var permanentErr *backoff.PermanentError
		if errors.As(err, &permanentErr) {
			return fmt.Errorf("S3 download failed with non-retryable error: %w", permanentErr.Err)
		}
		return &MaxRetriesExceededError{
			MaxAttempts: c.config.MaxRetries,
			LastError:   err,
		}
	}

	return nil
}
