package githubutil

import (
	"context"
	"os"
	"testing"
)

// TestGetGitHubToken_MissingEnv verifies that missing env variables cause an error.
func TestGetGitHubToken_MissingEnv(t *testing.T) {
	// Backup and clear envs
	oldAppID := os.Getenv("GITHUB_APP_ID")
	oldKey := os.Getenv("GITHUB_PRIVATE_KEY")
	_ = os.Unsetenv("GITHUB_APP_ID")
	_ = os.Unsetenv("GITHUB_PRIVATE_KEY")
	defer func() {
		_ = os.Setenv("GITHUB_APP_ID", oldAppID)
		_ = os.Setenv("GITHUB_PRIVATE_KEY", oldKey)
	}()

	if _, err := GetGitHubToken(context.Background(), "owner", "repo"); err == nil {
		t.Fatalf("expected error when envs are missing, got nil")
	}
}

// TestGetGitHubToken_InvalidAppID verifies invalid app id is detected.
func TestGetGitHubToken_InvalidAppID(t *testing.T) {
	oldAppID := os.Getenv("GITHUB_APP_ID")
	oldKey := os.Getenv("GITHUB_PRIVATE_KEY")
	_ = os.Setenv("GITHUB_APP_ID", "not-a-number")
	_ = os.Setenv("GITHUB_PRIVATE_KEY", "dummy")
	defer func() {
		_ = os.Setenv("GITHUB_APP_ID", oldAppID)
		_ = os.Setenv("GITHUB_PRIVATE_KEY", oldKey)
	}()

	if _, err := GetGitHubToken(context.Background(), "owner", "repo"); err == nil {
		t.Fatalf("expected error for invalid app id, got nil")
	}
}
