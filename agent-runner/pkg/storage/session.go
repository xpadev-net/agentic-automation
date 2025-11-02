package storage

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SaveSession saves agent session data to S3.
// It compresses session files (conversation history, context) into a tar.gz archive
// and uploads it to S3 for persistence across retry attempts.
func SaveSession(agentRunID int, agentType string) error {
	// 1. Load S3 configuration
	s3Cfg, err := LoadS3Config()
	if err != nil {
		return fmt.Errorf("failed to load S3 config: %w", err)
	}

	// Convert S3Config to Config for S3 client
	cfg := &Config{
		Endpoint:        s3Cfg.Endpoint,
		Region:          s3Cfg.Region,
		Bucket:          s3Cfg.Bucket,
		AccessKeyID:     s3Cfg.AccessKeyID,
		SecretAccessKey: s3Cfg.SecretAccessKey,
		UsePathStyle:    s3Cfg.UsePathStyle,
		MaxRetries:      s3Cfg.MaxRetries,
	}

	// 2. Initialize S3 client
	client, err := NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	// 3. Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// 4. Create temporary archive directory
	tmpDir := fmt.Sprintf("/tmp/session-archive-%d", agentRunID)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir) // Cleanup temp directory

	// 5. Copy session files based on agent type
	if err := copySessionFiles(agentType, homeDir, tmpDir); err != nil {
		return fmt.Errorf("failed to copy session files: %w", err)
	}

	// 6. Create tar.gz archive
	tarPath := fmt.Sprintf("/tmp/session-%d.tar.gz", agentRunID)
	defer os.Remove(tarPath) // Cleanup tar file
	if err := createTarGz(tmpDir, tarPath); err != nil {
		return fmt.Errorf("failed to create tar.gz archive: %w", err)
	}

	// 7. Generate S3 key
	s3Key := getS3SessionKey(agentRunID)

	// 8. Upload to S3 (with retry logic built into Client.Upload)
	ctx := context.Background()
	if err := client.Upload(ctx, s3Key, tarPath); err != nil {
		return fmt.Errorf("failed to upload session to S3: %w", err)
	}

	return nil
}

// RestoreSession restores agent session data from S3.
// It downloads the session archive from S3 and extracts it to restore
// conversation history and context for retry attempts.
func RestoreSession(agentRunID int, retryCount int) error {
	// 1. Early return if initial execution (no previous session)
	if retryCount == 0 {
		return nil
	}

	// 2. Load S3 configuration
	s3Cfg, err := LoadS3Config()
	if err != nil {
		return fmt.Errorf("failed to load S3 config: %w", err)
	}

	// Convert S3Config to Config for S3 client
	cfg := &Config{
		Endpoint:        s3Cfg.Endpoint,
		Region:          s3Cfg.Region,
		Bucket:          s3Cfg.Bucket,
		AccessKeyID:     s3Cfg.AccessKeyID,
		SecretAccessKey: s3Cfg.SecretAccessKey,
		UsePathStyle:    s3Cfg.UsePathStyle,
		MaxRetries:      s3Cfg.MaxRetries,
	}

	// 3. Initialize S3 client
	client, err := NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	// 4. Generate S3 key
	s3Key := getS3SessionKey(agentRunID)

	// 5. Download session archive to temporary file
	tarPath := fmt.Sprintf("/tmp/session-%d.tar.gz", agentRunID)
	defer os.Remove(tarPath) // Cleanup tar file

	ctx := context.Background()
	if err := client.Download(ctx, s3Key, tarPath); err != nil {
		return fmt.Errorf("failed to download session from S3: %w", err)
	}

	// 6. Extract tar.gz archive to root filesystem
	if err := extractTarGz(tarPath, "/"); err != nil {
		return fmt.Errorf("failed to extract session archive: %w", err)
	}

	// 7. Verify restored files exist
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory for verification: %w", err)
	}

	claudeDir := filepath.Join(homeDir, ".claude")
	cursorDir := filepath.Join(homeDir, ".cursor")

	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		if _, err := os.Stat(cursorDir); os.IsNotExist(err) {
			return fmt.Errorf("session restore verification failed: neither ~/.claude/ nor ~/.cursor/ found")
		}
	}

	return nil
}

// createTarGz creates a tar.gz archive from the source directory.
func createTarGz(srcDir, destPath string) error {
	// Create the output file
	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Create gzip writer
	gzipWriter := gzip.NewWriter(outFile)
	defer gzipWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// Walk through source directory and add files to archive
	return filepath.Walk(srcDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from source directory
		relPath, err := filepath.Rel(srcDir, filePath)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Skip root directory entry
		if relPath == "." {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header: %w", err)
		}

		// Set header name to relative path (preserves directory structure)
		header.Name = relPath

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}

		// Skip directories (header is enough)
		if info.IsDir() {
			return nil
		}

		// Open file for reading
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()

		// Copy file contents to tar archive
		if _, err := io.Copy(tarWriter, file); err != nil {
			return fmt.Errorf("failed to write file to archive: %w", err)
		}

		return nil
	})
}

// extractTarGz extracts a tar.gz archive to the destination directory.
func extractTarGz(srcPath, destDir string) error {
	// Open the tar.gz file
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open archive file: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzipReader)

	// Extract each file in the archive
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Construct destination path
		destPath := filepath.Join(destDir, header.Name)

		// Get file info from header
		info := header.FileInfo()

		// Create parent directories if needed
		if info.IsDir() {
			if err := os.MkdirAll(destPath, info.Mode()); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create parent directories for file
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}

		// Create destination file
		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		// Copy file contents
		if _, err := io.Copy(outFile, tarReader); err != nil {
			outFile.Close()
			return fmt.Errorf("failed to extract file contents: %w", err)
		}

		// Set file modification time
		if err := os.Chtimes(destPath, info.ModTime(), info.ModTime()); err != nil {
			outFile.Close()
			return fmt.Errorf("failed to set file time: %w", err)
		}

		outFile.Close()
	}

	return nil
}

// copySessionFiles copies session files to the temporary archive directory
// based on the agent type.
func copySessionFiles(agentType, homeDir, tmpDir string) error {
	switch agentType {
	case "claude-code":
		// Copy ~/.claude/ to tmpDir/.claude/
		srcClaudeDir := filepath.Join(homeDir, ".claude")
		destClaudeDir := filepath.Join(tmpDir, ".claude")
		if err := copyDir(srcClaudeDir, destClaudeDir); err != nil {
			// If directory doesn't exist, skip (not an error for initial runs)
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to copy ~/.claude/: %w", err)
			}
		}
		return nil

	case "cursor-agents":
		// Copy ~/.cursor/ to tmpDir/.cursor/
		srcCursorDir := filepath.Join(homeDir, ".cursor")
		destCursorDir := filepath.Join(tmpDir, ".cursor")
		if err := copyDir(srcCursorDir, destCursorDir); err != nil {
			// If directory doesn't exist, skip (not an error for initial runs)
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to copy ~/.cursor/: %w", err)
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported agent type: %s", agentType)
	}
}

// copyDir recursively copies a directory tree.
func copyDir(src, dest string) error {
	// Check if source exists
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Create destination directory with same permissions
	if err := os.MkdirAll(dest, srcInfo.Mode()); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Walk source directory
	return filepath.Walk(src, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from source
		relPath, err := filepath.Rel(src, srcPath)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Construct destination path
		destPath := filepath.Join(dest, relPath)

		// Handle directories
		if info.IsDir() {
			if err := os.MkdirAll(destPath, info.Mode()); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			return nil
		}

		// Handle files
		return copyFile(srcPath, destPath, info.Mode())
	})
}

// copyFile copies a single file from src to dest with the given permissions.
func copyFile(src, dest string, mode os.FileMode) error {
	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	// Create destination file
	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy contents
	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	return nil
}

// getS3SessionKey generates the S3 key for a session archive.
func getS3SessionKey(agentRunID int) string {
	return fmt.Sprintf("sessions/%d/session.tar.gz", agentRunID)
}
