package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     bool
	}{
		// Excluded cases - exact matches
		{
			name:     "credentials file in home",
			filePath: "/home/user/.claude/.credentials.json",
			want:     true,
		},
		{
			name:     "credentials file in workspace",
			filePath: "/workspace/.claude/.credentials.json",
			want:     true,
		},
		{
			name:     "credentials file relative",
			filePath: ".credentials.json",
			want:     true,
		},
		{
			name:     "env file in workspace",
			filePath: "/workspace/.env",
			want:     true,
		},
		{
			name:     "env file relative",
			filePath: ".env",
			want:     true,
		},

		// Excluded cases - extension patterns
		{
			name:     "pem file simple",
			filePath: "key.pem",
			want:     true,
		},
		{
			name:     "pem file with path",
			filePath: "/path/to/certificate.pem",
			want:     true,
		},
		{
			name:     "pem file with subdirectory",
			filePath: "certs/server.pem",
			want:     true,
		},
		{
			name:     "key file simple",
			filePath: "private.key",
			want:     true,
		},
		{
			name:     "key file with path",
			filePath: "/path/to/secret.key",
			want:     true,
		},

		// Excluded cases - filename pattern (*_key)
		{
			name:     "key suffix file",
			filePath: "private_key",
			want:     true,
		},
		{
			name:     "key suffix with extension",
			filePath: "api_key.txt",
			want:     true,
		},
		{
			name:     "key suffix with path",
			filePath: "/path/to/server_key",
			want:     true,
		},

		// Excluded cases - directory patterns
		{
			name:     "node_modules directory",
			filePath: "/workspace/node_modules/package.json",
			want:     true,
		},
		{
			name:     "node_modules root",
			filePath: "node_modules/",
			want:     true,
		},
		{
			name:     "node_modules nested",
			filePath: "/path/to/node_modules/file.js",
			want:     true,
		},
		{
			name:     "git directory config",
			filePath: "/workspace/.git/config",
			want:     true,
		},
		{
			name:     "git directory HEAD",
			filePath: ".git/HEAD",
			want:     true,
		},
		{
			name:     "git directory hooks",
			filePath: "/path/to/.git/hooks/pre-commit",
			want:     true,
		},
		{
			name:     "cache directory in claude",
			filePath: "/home/user/.claude/cache/temp.json",
			want:     true,
		},
		{
			name:     "cache directory root",
			filePath: "/workspace/.claude/cache/",
			want:     true,
		},
		{
			name:     "cache directory simple",
			filePath: "cache/file.txt",
			want:     true,
		},
		{
			name:     "cache directory nested",
			filePath: "/path/to/cache/data.json",
			want:     true,
		},

		// Not excluded cases - normal session files
		{
			name:     "session file jsonl",
			filePath: "/home/user/.claude/projects/abc123.jsonl",
			want:     false,
		},
		{
			name:     "claude settings file",
			filePath: "/home/user/.claude/settings.json",
			want:     false,
		},
		{
			name:     "workspace claude settings",
			filePath: "/workspace/.claude/settings.json",
			want:     false,
		},
		{
			name:     "CLAUDE markdown file",
			filePath: "/workspace/CLAUDE.md",
			want:     false,
		},
		{
			name:     "cursor session file",
			filePath: "/home/user/.cursor/session.json",
			want:     false,
		},

		// Not excluded cases - similar names but should not be excluded
		{
			name:     "env example file",
			filePath: ".env.example",
			want:     false,
		},
		{
			name:     "env local file",
			filePath: ".env.local",
			want:     false,
		},
		{
			name:     "credentials without dot",
			filePath: "credentials.json",
			want:     false,
		},
		{
			name:     "key file with different extension",
			filePath: "key.txt",
			want:     false,
		},
		{
			name:     "key without extension",
			filePath: "key",
			want:     false,
		},
		{
			name:     "cache in filename",
			filePath: "cache_file.json",
			want:     false,
		},
		{
			name:     "cache as prefix",
			filePath: "cacheable.txt",
			want:     false,
		},

		// Edge cases
		{
			name:     "empty string",
			filePath: "",
			want:     false,
		},
		{
			name:     "root directory",
			filePath: "/",
			want:     false,
		},
		{
			name:     "current directory",
			filePath: ".",
			want:     false,
		},
		{
			name:     "parent directory",
			filePath: "..",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldExclude(tt.filePath)
			assert.Equal(t, tt.want, got, "ShouldExclude(%q) = %v, want %v", tt.filePath, got, tt.want)
		})
	}
}

// TestShouldExclude_DirectoryPatterns tests directory pattern matching more thoroughly
func TestShouldExclude_DirectoryPatterns(t *testing.T) {
	// Test that directory patterns match correctly even with different path separators
	tests := []struct {
		name     string
		filePath string
		want     bool
	}{
		{
			name:     "node_modules with forward slash",
			filePath: "node_modules/package.json",
			want:     true,
		},
		{
			name:     "node_modules as directory name",
			filePath: filepath.Join("project", "node_modules", "package.json"),
			want:     true,
		},
		{
			name:     "git directory with forward slash",
			filePath: ".git/config",
			want:     true,
		},
		{
			name:     "git directory nested",
			filePath: filepath.Join("workspace", ".git", "config"),
			want:     true,
		},
		{
			name:     "cache directory standalone",
			filePath: "cache/data.json",
			want:     true,
		},
		{
			name:     "cache directory nested",
			filePath: filepath.Join(".claude", "cache", "temp.json"),
			want:     true,
		},
		{
			name:     "cache as filename should not match",
			filePath: "cache_file.json",
			want:     false,
		},
		{
			name:     "cached directory should not match",
			filePath: "cached/data.json",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldExclude(tt.filePath)
			assert.Equal(t, tt.want, got, "ShouldExclude(%q) = %v, want %v", tt.filePath, got, tt.want)
		})
	}
}
