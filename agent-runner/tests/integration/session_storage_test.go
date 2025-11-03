package integration

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-runner/pkg/storage"
)

// minioServerInfo holds information about a running MinIO server instance
type minioServerInfo struct {
	endpoint    string
	bucket      string
	dataDir     string
	port        int
	cmd         *exec.Cmd
	originalEnv map[string]string
}

// setupMinIOServer downloads (if needed) and starts a MinIO server for testing.
// Returns the endpoint URL, bucket name, and a cleanup function.
func setupMinIOServer(t *testing.T) (endpoint string, bucket string, cleanup func()) {
	t.Helper()

	info := &minioServerInfo{
		bucket:      "agent-sessions",
		originalEnv: make(map[string]string),
	}

	// Step 1: Get MinIO binary (skip test if not available)
	minioBinary, err := getMinIOBinary(t)
	if err != nil {
		t.Skipf("Skipping MinIO integration test: %v", err)
	}

	// Step 2: Create temporary data directory
	info.dataDir = t.TempDir()

	// Step 3: Find an available port
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err, "Failed to find available port")
	info.port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Step 4: Start MinIO server
	info.cmd = exec.Command(minioBinary, "server", info.dataDir, "--address", fmt.Sprintf(":%d", info.port))
	info.cmd.Env = append(os.Environ(),
		"MINIO_ROOT_USER=minioadmin",
		"MINIO_ROOT_PASSWORD=minioadmin",
	)
	info.cmd.Stdout = os.Stdout // Allow debugging output
	info.cmd.Stderr = os.Stderr

	err = info.cmd.Start()
	require.NoError(t, err, "Failed to start MinIO server")

	// Step 5: Wait for server to be ready (health check)
	info.endpoint = fmt.Sprintf("http://localhost:%d", info.port)
	err = waitForMinIOServer(t, info.endpoint, 30*time.Second)
	require.NoError(t, err, "MinIO server failed to become ready")

	// Step 6: Create bucket
	err = createMinIOBucket(t, info.endpoint, info.bucket)
	require.NoError(t, err, "Failed to create bucket")

	// Step 7: Set environment variables
	s3EnvVars := map[string]string{
		"S3_ENDPOINT":          info.endpoint,
		"S3_REGION":            "us-east-1",
		"S3_BUCKET":            info.bucket,
		"S3_ACCESS_KEY_ID":     "minioadmin",
		"S3_SECRET_ACCESS_KEY": "minioadmin",
		"S3_USE_PATH_STYLE":    "true",
		"S3_MAX_RETRIES":       "3",
	}

	// Save original values and set new ones
	for key, value := range s3EnvVars {
		if original, exists := os.LookupEnv(key); exists {
			info.originalEnv[key] = original
		}
		os.Setenv(key, value)
	}

	// Cleanup function
	cleanup = func() {
		// Kill MinIO process
		if info.cmd != nil && info.cmd.Process != nil {
			info.cmd.Process.Kill()
			info.cmd.Wait() // Wait for process to exit
		}

		// Restore environment variables
		for key, originalValue := range info.originalEnv {
			os.Setenv(key, originalValue)
		}
		for key := range s3EnvVars {
			if _, exists := info.originalEnv[key]; !exists {
				os.Unsetenv(key)
			}
		}

		// Remove data directory (t.TempDir() handles this, but explicit cleanup is safe)
		if info.dataDir != "" {
			os.RemoveAll(info.dataDir)
		}
	}

	t.Cleanup(cleanup)

	return info.endpoint, info.bucket, cleanup
}

// getMinIOBinary returns the path to the MinIO binary.
// It uses MINIO_SERVER_PATH if set, otherwise downloads it to a cache directory.
func getMinIOBinary(t *testing.T) (string, error) {
	t.Helper()

	// Check if path is provided via environment variable
	if path := os.Getenv("MINIO_SERVER_PATH"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// Determine OS and architecture
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map Go OS/ARCH to MinIO release naming
	var osName string
	switch goos {
	case "linux":
		osName = "linux"
	case "darwin":
		osName = "darwin"
	case "windows":
		osName = "windows"
	default:
		return "", fmt.Errorf("unsupported OS: %s", goos)
	}

	var archName string
	switch goarch {
	case "amd64":
		archName = "amd64"
	case "arm64":
		archName = "arm64"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}

	// Cache directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".cache", "minio-test")
	err = os.MkdirAll(cacheDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	binaryName := "minio"
	if goos == "windows" {
		binaryName = "minio.exe"
	}
	binaryPath := filepath.Join(cacheDir, fmt.Sprintf("%s-%s-%s", binaryName, osName, archName))

	// Check if binary already exists
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	// Binary not found - return error instead of downloading
	return "", fmt.Errorf("MinIO binary not found at %s. Set MINIO_SERVER_PATH environment variable or ensure MinIO binary is cached at ~/.cache/minio-test/", binaryPath)
}

// waitForMinIOServer waits for the MinIO server to become ready by polling the health endpoint.
func waitForMinIOServer(t *testing.T, endpoint string, timeout time.Duration) error {
	t.Helper()

	healthURL := fmt.Sprintf("%s/minio/health/live", endpoint)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("MinIO server did not become ready within %v", timeout)
}

// createMinIOBucket creates a bucket in MinIO using AWS SDK Go v2.
func createMinIOBucket(t *testing.T, endpoint, bucketName string) error {
	t.Helper()

	ctx := context.Background()

	// Create AWS config with custom endpoint
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"minioadmin",
			"minioadmin",
			"",
		)),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Configure custom endpoint for MinIO
	awsCfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: "us-east-1",
			}, nil
		},
	)

	// Create S3 client with path-style addressing
	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	// Create bucket
	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		// Bucket might already exist, check if it's a BucketAlreadyOwnedByYou error
		// In MinIO, this is usually fine for testing
		if !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") &&
			!strings.Contains(err.Error(), "BucketAlreadyExists") {
			return fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return nil
}

// testFileInfo represents file information for test setup
type testFileInfo struct {
	Content     string
	Permissions os.FileMode
	IsDir       bool
}

// createTestSessionFiles creates a directory structure for testing based on the
// provided file map. Each key is a file path (relative to baseDir), and the
// value contains the file content, permissions, and whether it's a directory.
func createTestSessionFiles(t *testing.T, baseDir string, files map[string]testFileInfo) {
	t.Helper()

	for filePath, info := range files {
		fullPath := filepath.Join(baseDir, filePath)

		if info.IsDir {
			// Create directory
			err := os.MkdirAll(fullPath, info.Permissions)
			require.NoError(t, err, "Failed to create directory: %s", fullPath)
		} else {
			// Create parent directories if needed
			err := os.MkdirAll(filepath.Dir(fullPath), 0755)
			require.NoError(t, err, "Failed to create parent directory for: %s", fullPath)

			// Create file
			err = os.WriteFile(fullPath, []byte(info.Content), info.Permissions)
			require.NoError(t, err, "Failed to create file: %s", fullPath)
		}
	}
}

// setupTestHomeDir creates a temporary directory structure that simulates
// a home directory with .claude/ or .cursor/ directories for testing.
func setupTestHomeDir(t *testing.T, agentType string) (homeDir string, cleanup func()) {
	t.Helper()

	homeDir = t.TempDir()

	var sessionDir string
	switch agentType {
	case "claude-code":
		sessionDir = filepath.Join(homeDir, ".claude")
	case "cursor-agents":
		sessionDir = filepath.Join(homeDir, ".cursor")
	default:
		sessionDir = filepath.Join(homeDir, ".claude")
	}

	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err, "Failed to create session directory")

	// Override HOME environment variable for SaveSession/RestoreSession
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)

	cleanup = func() {
		if originalHome != "" {
			os.Setenv("HOME", originalHome)
		} else {
			os.Unsetenv("HOME")
		}
	}

	t.Cleanup(cleanup)
	return homeDir, cleanup
}

// verifyS3ObjectExists verifies that an object exists in S3 with the given key.
func verifyS3ObjectExists(t *testing.T, endpoint, bucket, key string) bool {
	t.Helper()

	ctx := context.Background()

	// Create AWS config
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"minioadmin",
			"minioadmin",
			"",
		)),
	)
	require.NoError(t, err)

	// Configure custom endpoint
	awsCfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: "us-east-1",
			}, nil
		},
	)

	// Create S3 client
	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	// Check if object exists
	_, err = s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	return err == nil
}

// verifyArchiveContents reads a tar.gz archive and verifies that it contains
// the expected files and excludes the specified files.
func verifyArchiveContents(t *testing.T, tarPath string, expectedFiles []string, excludedFiles []string) {
	t.Helper()

	// Open the tar.gz file
	file, err := os.Open(tarPath)
	require.NoError(t, err, "Failed to open archive file")
	defer file.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(file)
	require.NoError(t, err, "Failed to create gzip reader")
	defer gzipReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzipReader)

	// Collect all files in the archive
	archiveFiles := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "Failed to read tar header")

		if !header.FileInfo().IsDir() {
			archiveFiles[header.Name] = true
		}
	}

	// Verify expected files are present
	for _, expectedFile := range expectedFiles {
		assert.True(t, archiveFiles[expectedFile], "Expected file %s not found in archive", expectedFile)
	}

	// Verify excluded files are not present
	for _, excludedFile := range excludedFiles {
		// Check exact match and also check if any file path contains the excluded pattern
		found := false
		for archivedFile := range archiveFiles {
			if archivedFile == excludedFile || strings.Contains(archivedFile, excludedFile) {
				found = true
				break
			}
		}
		assert.False(t, found, "Excluded file %s found in archive", excludedFile)
	}
}

// downloadS3Object downloads an object from S3 and returns its local path.
func downloadS3Object(t *testing.T, endpoint, bucket, key string) string {
	t.Helper()

	ctx := context.Background()

	// Create AWS config
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"minioadmin",
			"minioadmin",
			"",
		)),
	)
	require.NoError(t, err)

	// Configure custom endpoint
	awsCfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: "us-east-1",
			}, nil
		},
	)

	// Create S3 client
	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	// Download object
	output, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer output.Body.Close()

	// Create temporary file for downloaded object
	tmpFile := filepath.Join(t.TempDir(), filepath.Base(key))
	outFile, err := os.Create(tmpFile)
	require.NoError(t, err)
	defer outFile.Close()

	_, err = io.Copy(outFile, output.Body)
	require.NoError(t, err)

	return tmpFile
}

// verifyRestoredFiles verifies that restored files match the original files.
func verifyRestoredFiles(t *testing.T, homeDir string, agentType string, originalFiles map[string]testFileInfo) {
	t.Helper()

	var sessionDir string
	switch agentType {
	case "claude-code":
		sessionDir = filepath.Join(homeDir, ".claude")
	case "cursor-agents":
		sessionDir = filepath.Join(homeDir, ".cursor")
	default:
		t.Fatalf("unsupported agent type: %s", agentType)
	}

	// Verify directory exists
	assert.DirExists(t, sessionDir, "Session directory should exist")

	// Verify each file
	for filePath, expectedInfo := range originalFiles {
		if expectedInfo.IsDir {
			continue // Skip directory entries
		}

		fullPath := filepath.Join(sessionDir, filePath)
		assert.FileExists(t, fullPath, "File %s should exist", filePath)

		// Verify content
		content, err := os.ReadFile(fullPath)
		require.NoError(t, err, "Failed to read file %s", filePath)
		assert.Equal(t, expectedInfo.Content, string(content), "File %s content should match", filePath)

		// Verify permissions
		info, err := os.Stat(fullPath)
		require.NoError(t, err, "Failed to stat file %s", filePath)
		assert.Equal(t, expectedInfo.Permissions.Perm(), info.Mode().Perm(), "File %s permissions should match", filePath)
	}
}

// TestSessionStorage_SaveAndRestore_ClaudeCode tests SaveSession and RestoreSession for claude-code agent type.
func TestSessionStorage_SaveAndRestore_ClaudeCode(t *testing.T) {
	// Setup MinIO server
	endpoint, bucket, _ := setupMinIOServer(t)

	// Setup test home directory with claude-code session files
	homeDir, _ := setupTestHomeDir(t, "claude-code")

	agentRunID := 12345
	agentType := "claude-code"

	// Create test session files
	testFiles := map[string]testFileInfo{
		".claude/settings.json": {
			Content:     `{"model": "claude-3", "temperature": 0.7}`,
			Permissions: 0644,
		},
		".claude/projects/project1.jsonl": {
			Content:     `{"type": "project", "name": "test-project"}`,
			Permissions: 0644,
		},
		".claude/conversations/conv1.json": {
			Content:     `{"messages": [{"role": "user", "content": "test"}]}`,
			Permissions: 0644,
		},
	}

	createTestSessionFiles(t, homeDir, testFiles)

	// Test SaveSession
	err := storage.SaveSession(agentRunID, agentType)
	require.NoError(t, err, "SaveSession should succeed")

	// Verify file was uploaded to S3
	expectedKey := fmt.Sprintf("sessions/%d/session.tar.gz", agentRunID)
	exists := verifyS3ObjectExists(t, endpoint, bucket, expectedKey)
	assert.True(t, exists, "Session archive should exist in S3")

	// Download and verify archive contents
	tarPath := downloadS3Object(t, endpoint, bucket, expectedKey)
	defer os.Remove(tarPath)

	// Verify archive contains expected files (relative paths from archive root)
	verifyArchiveContents(t, tarPath,
		[]string{
			".claude/settings.json",
			".claude/projects/project1.jsonl",
			".claude/conversations/conv1.json",
		},
		nil,
	)

	// Test RestoreSession: Create a new home directory and restore
	newHomeDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", newHomeDir)
	defer func() {
		if originalHome != "" {
			os.Setenv("HOME", originalHome)
		} else {
			os.Unsetenv("HOME")
		}
	}()

	err = storage.RestoreSession(agentRunID, 1) // retryCount > 0
	require.NoError(t, err, "RestoreSession should succeed")

	// Verify restored files
	verifyRestoredFiles(t, newHomeDir, agentType, testFiles)
}

// TestSessionStorage_SaveAndRestore_CursorAgents tests SaveSession and RestoreSession for cursor-agents agent type.
func TestSessionStorage_SaveAndRestore_CursorAgents(t *testing.T) {
	// Setup MinIO server
	endpoint, bucket, _ := setupMinIOServer(t)

	// Setup test home directory with cursor-agents session files
	homeDir, _ := setupTestHomeDir(t, "cursor-agents")

	agentRunID := 23456
	agentType := "cursor-agents"

	// Create test session files
	testFiles := map[string]testFileInfo{
		".cursor/session.json": {
			Content:     `{"session_id": "test-session", "context": {}}`,
			Permissions: 0644,
		},
		".cursor/config.yaml": {
			Content:     `agent: cursor-agents\nversion: 1.0`,
			Permissions: 0644,
		},
		".cursor/cache/data.bin": {
			Content:     "binary cache data",
			Permissions: 0644,
		},
	}

	createTestSessionFiles(t, homeDir, testFiles)

	// Test SaveSession
	err := storage.SaveSession(agentRunID, agentType)
	require.NoError(t, err, "SaveSession should succeed")

	// Verify file was uploaded to S3
	expectedKey := fmt.Sprintf("sessions/%d/session.tar.gz", agentRunID)
	exists := verifyS3ObjectExists(t, endpoint, bucket, expectedKey)
	assert.True(t, exists, "Session archive should exist in S3")

	// Test RestoreSession: Create a new home directory and restore
	newHomeDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", newHomeDir)
	defer func() {
		if originalHome != "" {
			os.Setenv("HOME", originalHome)
		} else {
			os.Unsetenv("HOME")
		}
	}()

	err = storage.RestoreSession(agentRunID, 1) // retryCount > 0
	require.NoError(t, err, "RestoreSession should succeed")

	// Verify restored files
	verifyRestoredFiles(t, newHomeDir, agentType, testFiles)
}

// TestSessionStorage_FullLifecycle tests the complete SaveSession -> RestoreSession lifecycle.
func TestSessionStorage_FullLifecycle(t *testing.T) {
	// Setup MinIO server
	_, _, _ = setupMinIOServer(t)

	agentRunID := 34567

	// Test with both agent types
	agentTypes := []string{"claude-code", "cursor-agents"}

	for _, agentType := range agentTypes {
		t.Run(agentType, func(t *testing.T) {
			// Setup test home directory
			homeDir, _ := setupTestHomeDir(t, agentType)

			// Create test session files
			var testFiles map[string]testFileInfo
			if agentType == "claude-code" {
				testFiles = map[string]testFileInfo{
					".claude/settings.json": {
						Content:     `{"test": "data"}`,
						Permissions: 0644,
					},
				}
			} else {
				testFiles = map[string]testFileInfo{
					".cursor/session.json": {
						Content:     `{"test": "data"}`,
						Permissions: 0644,
					},
				}
			}

			createTestSessionFiles(t, homeDir, testFiles)

			// Step 1: SaveSession
			err := storage.SaveSession(agentRunID, agentType)
			require.NoError(t, err, "SaveSession should succeed")

			// Step 2: RestoreSession to a new directory
			newHomeDir := t.TempDir()
			originalHome := os.Getenv("HOME")
			os.Setenv("HOME", newHomeDir)
			defer func() {
				if originalHome != "" {
					os.Setenv("HOME", originalHome)
				} else {
					os.Unsetenv("HOME")
				}
			}()

			err = storage.RestoreSession(agentRunID, 1) // retryCount > 0
			require.NoError(t, err, "RestoreSession should succeed")

			// Step 3: Verify restored files
			verifyRestoredFiles(t, newHomeDir, agentType, testFiles)

			// Increment agentRunID for next iteration to avoid conflicts
			agentRunID++
		})
	}
}

// TestSessionStorage_FileExclusion tests that excluded files are not included in the archive.
func TestSessionStorage_FileExclusion(t *testing.T) {
	// Setup MinIO server
	endpoint, bucket, _ := setupMinIOServer(t)

	// Setup test home directory
	homeDir, _ := setupTestHomeDir(t, "claude-code")

	agentRunID := 45678
	agentType := "claude-code"

	// Create test files including excluded files
	testFiles := map[string]testFileInfo{
		".claude/settings.json": {
			Content:     `{"valid": "data"}`,
			Permissions: 0644,
		},
		".claude/.env": { // Should be excluded
			Content:     "SECRET_KEY=should-not-be-included",
			Permissions: 0644,
		},
		".claude/key.pem": { // Should be excluded
			Content:     "-----BEGIN PRIVATE KEY-----",
			Permissions: 0600,
		},
		".claude/api_key.txt": { // Should be excluded (_key suffix)
			Content:     "secret-api-key",
			Permissions: 0644,
		},
		".claude/node_modules/pkg/file.js": { // Should be excluded (node_modules)
			Content:     "console.log('test');",
			Permissions: 0644,
		},
	}

	createTestSessionFiles(t, homeDir, testFiles)

	// Test SaveSession
	err := storage.SaveSession(agentRunID, agentType)
	require.NoError(t, err, "SaveSession should succeed")

	// Download and verify archive contents
	expectedKey := fmt.Sprintf("sessions/%d/session.tar.gz", agentRunID)
	tarPath := downloadS3Object(t, endpoint, bucket, expectedKey)
	defer os.Remove(tarPath)

	// Verify expected files are present and excluded files are not
	verifyArchiveContents(t, tarPath,
		[]string{
			".claude/settings.json",
		},
		[]string{
			".env",
			".pem",
			"api_key",
			"node_modules",
		},
	)
}

// TestSessionStorage_RestoreEarlyReturn tests that RestoreSession returns early when retryCount=0.
func TestSessionStorage_RestoreEarlyReturn(t *testing.T) {
	// Setup MinIO server (though it won't be used)
	_, _, _ = setupMinIOServer(t)

	agentRunID := 56789

	// Test with retryCount=0 (should return immediately without S3 access)
	err := storage.RestoreSession(agentRunID, 0)
	assert.NoError(t, err, "RestoreSession with retryCount=0 should return nil without errors")

	// Verify no S3 object was created (this is implicit - if it tried to access S3 with wrong credentials, it would fail)
	// Since retryCount=0 returns early, no S3 access should occur
}

// TestSessionStorage_RestoreNotFound tests RestoreSession when the session doesn't exist in S3.
func TestSessionStorage_RestoreNotFound(t *testing.T) {
	// Setup MinIO server
	_, _, _ = setupMinIOServer(t)

	// Setup test home directory
	homeDir, _ := setupTestHomeDir(t, "claude-code")

	agentRunID := 99999 // Non-existent session ID

	// Test RestoreSession with non-existent session
	err := storage.RestoreSession(agentRunID, 1) // retryCount > 0
	assert.Error(t, err, "RestoreSession should fail for non-existent session")
	assert.Contains(t, err.Error(), "failed to download session from S3", "Error should indicate S3 download failure")

	// Verify no files were restored
	sessionDir := filepath.Join(homeDir, ".claude")
	if _, err := os.Stat(sessionDir); err == nil {
		// Directory might exist but should be empty or not contain restored files
		entries, _ := os.ReadDir(sessionDir)
		assert.Len(t, entries, 0, "Session directory should not contain restored files")
	}
}
