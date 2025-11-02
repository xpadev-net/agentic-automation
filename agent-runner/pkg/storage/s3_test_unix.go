//go:build !windows

package storage

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClient_Download_DirectoryCreationFailure tests directory creation failure
// This test is Unix-only because it uses os.Getuid() which is not available on Windows
func TestClient_Download_DirectoryCreationFailure(t *testing.T) {
	cfg := &Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		MaxRetries:      5,
	}
	client, err := newClientForTesting(cfg, createMockS3Client())
	require.NoError(t, err)

	// Try to create a file in a path that would require creating a directory
	// with invalid characters (would fail on Windows) or in a read-only location
	// For simplicity, we'll test with a path that would fail
	// Note: This is platform-dependent, so we'll skip on certain platforms
	if os.Getuid() == 0 {
		t.Skip("Skipping test when running as root (may have write access to read-only paths)")
	}

	// Try a path that might fail (e.g., in a location we can't write)
	// This is a simplified test - actual directory creation failures are hard to simulate
	invalidPath := "/root/restricted/path/file.txt"
	err = client.Download(context.Background(), "test-key", invalidPath)
	// This will likely fail, but the exact error depends on the system
	// We just verify it doesn't panic
	assert.Error(t, err)
}
