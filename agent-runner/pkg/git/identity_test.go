package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEnsureCommitIdentity_NoOpWhenConfigured verifies no error when identity already set.
func TestEnsureCommitIdentity_NoOpWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	// init repo
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	// set identity
	if err := exec.Command("git", "config", "user.name", "Pre Set").Run(); err != nil {
		t.Fatalf("set user.name failed: %v", err)
	}
	if err := exec.Command("git", "config", "user.email", "preset@example.com").Run(); err != nil {
		t.Fatalf("set user.email failed: %v", err)
	}

	if err := EnsureCommitIdentity(dir, "owner/repo", 1); err != nil {
		t.Fatalf("EnsureCommitIdentity returned error: %v", err)
	}
}

// TestEnsureCommitIdentity_FallbackWithoutCreds ensures fallback identity is set when none configured and no GitHub creds.
func TestEnsureCommitIdentity_FallbackWithoutCreds(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Unset possible envs to force token retrieval failure
	_ = os.Unsetenv("GITHUB_APP_ID")
	_ = os.Unsetenv("GITHUB_PRIVATE_KEY")

	if err := EnsureCommitIdentity(dir, "owner/repo", 123); err != nil {
		t.Fatalf("EnsureCommitIdentity returned error: %v", err)
	}

	// Verify values are set
	nameOut, err := exec.Command("git", "config", "--get", "user.name").Output()
	if err != nil {
		t.Fatalf("get user.name failed: %v", err)
	}
	emailOut, err := exec.Command("git", "config", "--get", "user.email").Output()
	if err != nil {
		t.Fatalf("get user.email failed: %v", err)
	}
	if string(nameOut) == "" || string(emailOut) == "" {
		t.Fatalf("expected identity to be configured, got name=%q email=%q", string(nameOut), string(emailOut))
	}

	_ = filepath.Base(dir) // silence unused import on filepath in case of future extensions
}
