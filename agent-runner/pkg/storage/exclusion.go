package storage

import (
	"path/filepath"
	"strings"
)

// ShouldExclude determines if a file or directory should be excluded from
// session archiving based on security patterns (credentials, keys, etc.).
// Returns true if the file should be excluded, false otherwise.
func ShouldExclude(filePath string) bool {
	// Empty path should not be excluded
	if filePath == "" {
		return false
	}

	// Normalize the path
	cleanPath := filepath.Clean(filePath)
	baseName := filepath.Base(cleanPath)

	// 1. Exact match patterns (using filepath.Base)
	exactMatches := []string{
		".credentials.json",
		".env",
	}
	for _, pattern := range exactMatches {
		if baseName == pattern {
			return true
		}
	}

	// 2. Extension patterns (using filepath.Ext and filepath.Match)
	ext := filepath.Ext(cleanPath)
	extensionPatterns := []string{".pem", ".key"}
	for _, pattern := range extensionPatterns {
		if ext == pattern {
			return true
		}
	}

	// 3. Filename pattern (*_key suffix)
	// Check the base name without extension (e.g., "api_key" from "api_key.txt")
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	if strings.HasSuffix(nameWithoutExt, "_key") {
		return true
	}

	// 4. Directory patterns (check if pattern appears as directory name in path)
	directoryPatterns := []string{
		"node_modules",
		".git",
		"cache",
	}
	sep := string(filepath.Separator)
	for _, pattern := range directoryPatterns {
		// Check if pattern appears as a directory in the path
		// Matches: "pattern/", "/pattern/", "/pattern", or ends with "pattern"
		if strings.Contains(cleanPath, pattern+sep) ||
			strings.Contains(cleanPath, sep+pattern+sep) ||
			strings.HasPrefix(cleanPath, pattern+sep) ||
			strings.HasSuffix(cleanPath, sep+pattern) ||
			cleanPath == pattern {
			return true
		}
	}

	return false
}
