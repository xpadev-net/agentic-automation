package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// setupTestDir creates a temporary directory for testing.
// Returns the directory path and a cleanup function.
func setupTestDir(t *testing.T) (string, func()) {
	tmpDir, err := os.MkdirTemp("", "loader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

// writeManifestFile writes a test manifest file to the specified directory.
func writeManifestFile(t *testing.T, dir string, content string) {
	manifestPath := filepath.Join(dir, ".agent-config.yaml")
	if err := os.WriteFile(manifestPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write manifest file: %v", err)
	}
}

// LoadManifest関数のテストケース

func TestLoadManifest_FileNotExists(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest when file does not exist, got: %+v", manifest)
	}
	if err != nil {
		t.Errorf("Expected nil error when file does not exist, got: %v", err)
	}
}

func TestLoadManifest_InvalidYAML(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, "invalid: yaml: content: [unclosed")

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid YAML, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse manifest YAML") {
		t.Errorf("Expected error message to contain 'failed to parse manifest YAML', got: %v", err)
	}
}

func TestLoadManifest_MissingVersion(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `hooks:
  pre: []
validation: []`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest when version is missing, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error when version is missing, got nil")
	}
	if !strings.Contains(err.Error(), "manifest version is required") {
		t.Errorf("Expected error message to contain 'manifest version is required', got: %v", err)
	}
}

func TestLoadManifest_EmptyVersion(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: ""
hooks:
  pre: []`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest when version is empty, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error when version is empty, got nil")
	}
	if !strings.Contains(err.Error(), "manifest version is required") {
		t.Errorf("Expected error message to contain 'manifest version is required', got: %v", err)
	}
}

func TestLoadManifest_UnsupportedVersion(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "2.0"
hooks:
  pre: []`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for unsupported version, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for unsupported version, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported manifest version") {
		t.Errorf("Expected error message to contain 'unsupported manifest version', got: %v", err)
	}
	if !strings.Contains(err.Error(), "2.0") {
		t.Errorf("Expected error message to contain '2.0', got: %v", err)
	}
}

func TestLoadManifest_ValidManifest_Minimal(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  pre: []
  post: []
validation: []`)

	manifest, err := LoadManifest(tmpDir)

	if err != nil {
		t.Fatalf("Expected no error for valid minimal manifest, got: %v", err)
	}
	if manifest == nil {
		t.Fatal("Expected non-nil manifest for valid minimal manifest")
	}
	if manifest.Version != "1.0" {
		t.Errorf("Expected version '1.0', got: %q", manifest.Version)
	}
	if len(manifest.Hooks.Pre) != 0 {
		t.Errorf("Expected empty pre-hooks, got: %d", len(manifest.Hooks.Pre))
	}
	if len(manifest.Hooks.Post) != 0 {
		t.Errorf("Expected empty post-hooks, got: %d", len(manifest.Hooks.Post))
	}
	if len(manifest.Validation) != 0 {
		t.Errorf("Expected empty validation, got: %d", len(manifest.Validation))
	}
}

func TestLoadManifest_ValidManifest_Full(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  pre:
    - name: "install-dependencies"
      command: "npm ci"
      description: "Install npm dependencies"
      timeout: "5m"
      required: true
  post:
    - name: "notify-discord"
      command: "curl -X POST $DISCORD_WEBHOOK_URL"
      timeout: "30s"
      required: false
validation:
  - name: "lint"
    command: "npm run lint"
    description: "ESLint validation"
    timeout: "3m"
    required: true
  - name: "type-check"
    command: "npm run type-check"
    timeout: "2m"
    required: true`)

	manifest, err := LoadManifest(tmpDir)

	if err != nil {
		t.Fatalf("Expected no error for valid full manifest, got: %v", err)
	}
	if manifest == nil {
		t.Fatal("Expected non-nil manifest for valid full manifest")
	}
	if manifest.Version != "1.0" {
		t.Errorf("Expected version '1.0', got: %q", manifest.Version)
	}

	// Check pre-hooks
	if len(manifest.Hooks.Pre) != 1 {
		t.Fatalf("Expected 1 pre-hook, got: %d", len(manifest.Hooks.Pre))
	}
	preHook := manifest.Hooks.Pre[0]
	if preHook.Name != "install-dependencies" {
		t.Errorf("Expected pre-hook name 'install-dependencies', got: %q", preHook.Name)
	}
	if preHook.Command != "npm ci" {
		t.Errorf("Expected pre-hook command 'npm ci', got: %q", preHook.Command)
	}
	if preHook.Description != "Install npm dependencies" {
		t.Errorf("Expected pre-hook description 'Install npm dependencies', got: %q", preHook.Description)
	}
	if preHook.Timeout != 5*time.Minute {
		t.Errorf("Expected pre-hook timeout 5m, got: %v", preHook.Timeout)
	}
	if !preHook.Required {
		t.Errorf("Expected pre-hook required true, got: %v", preHook.Required)
	}

	// Check post-hooks
	if len(manifest.Hooks.Post) != 1 {
		t.Fatalf("Expected 1 post-hook, got: %d", len(manifest.Hooks.Post))
	}
	postHook := manifest.Hooks.Post[0]
	if postHook.Name != "notify-discord" {
		t.Errorf("Expected post-hook name 'notify-discord', got: %q", postHook.Name)
	}
	if postHook.Timeout != 30*time.Second {
		t.Errorf("Expected post-hook timeout 30s, got: %v", postHook.Timeout)
	}
	if postHook.Required {
		t.Errorf("Expected post-hook required false, got: %v", postHook.Required)
	}

	// Check validation
	if len(manifest.Validation) != 2 {
		t.Fatalf("Expected 2 validation commands, got: %d", len(manifest.Validation))
	}
	validation1 := manifest.Validation[0]
	if validation1.Name != "lint" {
		t.Errorf("Expected validation name 'lint', got: %q", validation1.Name)
	}
	if validation1.Timeout != 3*time.Minute {
		t.Errorf("Expected validation timeout 3m, got: %v", validation1.Timeout)
	}
	validation2 := manifest.Validation[1]
	if validation2.Name != "type-check" {
		t.Errorf("Expected validation name 'type-check', got: %q", validation2.Name)
	}
	if validation2.Timeout != 2*time.Minute {
		t.Errorf("Expected validation timeout 2m, got: %v", validation2.Timeout)
	}
}

func TestLoadManifest_InvalidPreHook_EmptyName(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  pre:
    - name: ""
      command: "npm install"
      timeout: "5m"
      required: true`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid pre-hook, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid pre-hook, got nil")
	}
	// Error occurs during YAML unmarshaling, not in validateCommands
	if !strings.Contains(err.Error(), "failed to parse manifest YAML") {
		t.Errorf("Expected error message to contain 'failed to parse manifest YAML', got: %v", err)
	}
	if !strings.Contains(err.Error(), "command name is required") {
		t.Errorf("Expected error message to contain 'command name is required', got: %v", err)
	}
}

func TestLoadManifest_InvalidPreHook_EmptyCommand(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  pre:
    - name: "install"
      command: ""
      timeout: "5m"
      required: true`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid pre-hook, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid pre-hook, got nil")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("Expected error message to contain 'command is required', got: %v", err)
	}
}

func TestLoadManifest_InvalidPreHook_ZeroTimeout(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  pre:
    - name: "install"
      command: "npm install"
      timeout: "0s"
      required: true`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid pre-hook, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid pre-hook, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be positive") {
		t.Errorf("Expected error message to contain 'timeout must be positive', got: %v", err)
	}
}

func TestLoadManifest_InvalidPostHook(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  post:
    - name: ""
      command: "echo done"
      timeout: "10s"
      required: false`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid post-hook, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid post-hook, got nil")
	}
	// Error occurs during YAML unmarshaling, not in validateCommands
	if !strings.Contains(err.Error(), "failed to parse manifest YAML") {
		t.Errorf("Expected error message to contain 'failed to parse manifest YAML', got: %v", err)
	}
	if !strings.Contains(err.Error(), "command name is required") {
		t.Errorf("Expected error message to contain 'command name is required', got: %v", err)
	}
}

func TestLoadManifest_InvalidValidation(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
validation:
  - name: "lint"
    command: ""
    timeout: "3m"
    required: true`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid validation, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid validation, got nil")
	}
	// Error occurs during YAML unmarshaling, not in validateCommands
	if !strings.Contains(err.Error(), "failed to parse manifest YAML") {
		t.Errorf("Expected error message to contain 'failed to parse manifest YAML', got: %v", err)
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("Expected error message to contain 'command is required', got: %v", err)
	}
}

func TestLoadManifest_MultipleInvalidCommands(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, `version: "1.0"
hooks:
  pre:
    - name: "valid"
      command: "echo ok"
      timeout: "10s"
      required: true
    - name: ""
      command: "echo invalid"
      timeout: "10s"
      required: true`)

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for invalid commands, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for invalid commands, got nil")
	}
	// Error occurs during YAML unmarshaling, not in validateCommands
	if !strings.Contains(err.Error(), "failed to parse manifest YAML") {
		t.Errorf("Expected error message to contain 'failed to parse manifest YAML', got: %v", err)
	}
	if !strings.Contains(err.Error(), "command name is required") {
		t.Errorf("Expected error message to contain 'command name is required', got: %v", err)
	}
}

// validateCommands関数のテストケース

func TestValidateCommands_EmptySlice(t *testing.T) {
	err := validateCommands([]Command{}, "test-type")

	if err != nil {
		t.Errorf("Expected nil error for empty slice, got: %v", err)
	}
}

func TestValidateCommands_ValidCommands(t *testing.T) {
	commands := []Command{
		{Name: "test1", Command: "echo 1", Timeout: 5 * time.Minute},
		{Name: "test2", Command: "echo 2", Timeout: 10 * time.Second},
	}

	err := validateCommands(commands, "test-type")

	if err != nil {
		t.Errorf("Expected nil error for valid commands, got: %v", err)
	}
}

func TestValidateCommands_EmptyName(t *testing.T) {
	commands := []Command{
		{Name: "", Command: "echo test", Timeout: 5 * time.Minute},
	}

	err := validateCommands(commands, "test-type")

	if err == nil {
		t.Fatal("Expected error for empty name, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("Expected error message to contain 'name is required', got: %v", err)
	}
	if !strings.Contains(err.Error(), "at index 0") {
		t.Errorf("Expected error message to contain 'at index 0', got: %v", err)
	}
}

func TestValidateCommands_EmptyCommand(t *testing.T) {
	commands := []Command{
		{Name: "test", Command: "", Timeout: 5 * time.Minute},
	}

	err := validateCommands(commands, "test-type")

	if err == nil {
		t.Fatal("Expected error for empty command, got nil")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("Expected error message to contain 'command is required', got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"test\"") {
		t.Errorf("Expected error message to contain '\"test\"', got: %v", err)
	}
}

func TestValidateCommands_ZeroTimeout(t *testing.T) {
	commands := []Command{
		{Name: "test", Command: "echo test", Timeout: 0},
	}

	err := validateCommands(commands, "test-type")

	if err == nil {
		t.Fatal("Expected error for zero timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be positive") {
		t.Errorf("Expected error message to contain 'timeout must be positive', got: %v", err)
	}
}

func TestValidateCommands_NegativeTimeout(t *testing.T) {
	commands := []Command{
		{Name: "test", Command: "echo test", Timeout: -1 * time.Second},
	}

	err := validateCommands(commands, "test-type")

	if err == nil {
		t.Fatal("Expected error for negative timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be positive") {
		t.Errorf("Expected error message to contain 'timeout must be positive', got: %v", err)
	}
}

// Command.UnmarshalYAMLのテストケース

func TestCommand_UnmarshalYAML_Valid(t *testing.T) {
	yamlContent := `name: "test-command"
command: "echo hello"
description: "Test command"
timeout: "30s"
required: true`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err != nil {
		t.Fatalf("Expected no error for valid YAML, got: %v", err)
	}
	if cmd.Name != "test-command" {
		t.Errorf("Expected name 'test-command', got: %q", cmd.Name)
	}
	if cmd.Command != "echo hello" {
		t.Errorf("Expected command 'echo hello', got: %q", cmd.Command)
	}
	if cmd.Description != "Test command" {
		t.Errorf("Expected description 'Test command', got: %q", cmd.Description)
	}
	if cmd.Timeout != 30*time.Second {
		t.Errorf("Expected timeout 30s, got: %v", cmd.Timeout)
	}
	if !cmd.Required {
		t.Errorf("Expected required true, got: %v", cmd.Required)
	}
}

func TestCommand_UnmarshalYAML_MissingName(t *testing.T) {
	yamlContent := `command: "echo test"
timeout: "10s"
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for missing name, got nil")
	}
	if !strings.Contains(err.Error(), "command name is required") {
		t.Errorf("Expected error message to contain 'command name is required', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_EmptyName(t *testing.T) {
	yamlContent := `name: ""
command: "echo test"
timeout: "10s"`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for empty name, got nil")
	}
	if !strings.Contains(err.Error(), "command name is required") {
		t.Errorf("Expected error message to contain 'command name is required', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_MissingCommand(t *testing.T) {
	yamlContent := `name: "test"
timeout: "10s"
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for missing command, got nil")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("Expected error message to contain 'command is required', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_EmptyCommand(t *testing.T) {
	yamlContent := `name: "test"
command: ""
timeout: "10s"`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for empty command, got nil")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("Expected error message to contain 'command is required', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_MissingTimeout(t *testing.T) {
	yamlContent := `name: "test"
command: "echo test"
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for missing timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout is required for command") {
		t.Errorf("Expected error message to contain 'timeout is required for command', got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"test\"") {
		t.Errorf("Expected error message to contain '\"test\"', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_EmptyTimeout(t *testing.T) {
	yamlContent := `name: "test"
command: "echo test"
timeout: ""
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for empty timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout is required for command") {
		t.Errorf("Expected error message to contain 'timeout is required for command', got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"test\"") {
		t.Errorf("Expected error message to contain '\"test\"', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_InvalidTimeoutFormat(t *testing.T) {
	yamlContent := `name: "test"
command: "echo test"
timeout: "invalid-duration"
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for invalid timeout format, got nil")
	}
	if !strings.Contains(err.Error(), "invalid timeout") {
		t.Errorf("Expected error message to contain 'invalid timeout', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_ZeroTimeout(t *testing.T) {
	yamlContent := `name: "test"
command: "echo test"
timeout: "0s"
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err == nil {
		t.Fatal("Expected error for zero timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be positive") {
		t.Errorf("Expected error message to contain 'timeout must be positive', got: %v", err)
	}
}

func TestCommand_UnmarshalYAML_VariousTimeoutFormats(t *testing.T) {
	yamlContent := `name: "test"
command: "echo test"
timeout: "1h30m45s"
required: false`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err != nil {
		t.Fatalf("Expected no error for valid timeout format, got: %v", err)
	}
	expectedTimeout := 1*time.Hour + 30*time.Minute + 45*time.Second
	if cmd.Timeout != expectedTimeout {
		t.Errorf("Expected timeout 1h30m45s (%v), got: %v", expectedTimeout, cmd.Timeout)
	}
}

func TestCommand_UnmarshalYAML_OptionalFields(t *testing.T) {
	yamlContent := `name: "test"
command: "echo test"
timeout: "10s"`

	var cmd Command
	err := yaml.Unmarshal([]byte(yamlContent), &cmd)

	if err != nil {
		t.Fatalf("Expected no error when optional fields are missing, got: %v", err)
	}
	if cmd.Description != "" {
		t.Errorf("Expected empty description (default), got: %q", cmd.Description)
	}
	if cmd.Required {
		t.Errorf("Expected required false (default), got: %v", cmd.Required)
	}
}

// エッジケースと統合テスト

func TestLoadManifest_EmptyManifestFile(t *testing.T) {
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	writeManifestFile(t, tmpDir, "")

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for empty file, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for empty file, got nil")
	}
	// Empty YAML parses successfully but results in empty Version field
	// So we get "manifest version is required" error
	if !strings.Contains(err.Error(), "manifest version is required") {
		t.Errorf("Expected error message to contain 'manifest version is required', got: %v", err)
	}
}

func TestLoadManifest_FilePermissionError(t *testing.T) {
	// Note: This test may not work on all platforms or may require root
	// On Unix systems, we can create a file and then remove read permissions
	tmpDir, cleanup := setupTestDir(t)
	defer cleanup()

	manifestPath := filepath.Join(tmpDir, ".agent-config.yaml")
	if err := os.WriteFile(manifestPath, []byte(`version: "1.0"`), 0644); err != nil {
		t.Fatalf("Failed to create manifest file: %v", err)
	}

	// Remove read permissions (Unix-specific)
	if err := os.Chmod(manifestPath, 0200); err != nil {
		// If chmod fails (e.g., on Windows), skip this test
		t.Skipf("Skipping permission test on this platform: %v", err)
	}
	defer os.Chmod(manifestPath, 0644) // Restore permissions for cleanup

	manifest, err := LoadManifest(tmpDir)

	if manifest != nil {
		t.Errorf("Expected nil manifest for permission error, got: %+v", manifest)
	}
	if err == nil {
		t.Fatal("Expected error for permission error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read manifest file") {
		t.Errorf("Expected error message to contain 'failed to read manifest file', got: %v", err)
	}
}
