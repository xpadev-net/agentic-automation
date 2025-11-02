package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadManifest loads .agent-config.yaml from the working directory.
// Returns nil if file does not exist (skip hooks/validations).
// Returns an error if the file exists but cannot be read or parsed.
func LoadManifest(workDir string) (*Manifest, error) {
	// 1. Build file path
	manifestPath := filepath.Join(workDir, ".agent-config.yaml")

	// 2. Check if file exists
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			// File does not exist - return nil, nil (not an error)
			return nil, nil
		}
		// Other stat errors (permission, etc.) are returned as errors
		return nil, fmt.Errorf("failed to check manifest file %q: %w", manifestPath, err)
	}

	// 3. Read file contents
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file %q: %w", manifestPath, err)
	}

	// 4. Parse YAML
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest YAML %q: %w", manifestPath, err)
	}

	// 5. Validate version
	if manifest.Version == "" {
		return nil, fmt.Errorf("manifest version is required in %q", manifestPath)
	}
	if manifest.Version != "1.0" {
		return nil, fmt.Errorf("unsupported manifest version %q (expected 1.0) in %q", manifest.Version, manifestPath)
	}

	// 6. Validate commands
	if err := validateCommands(manifest.Hooks.Pre, "pre-hook"); err != nil {
		return nil, fmt.Errorf("invalid pre-hook in %q: %w", manifestPath, err)
	}
	if err := validateCommands(manifest.Hooks.Post, "post-hook"); err != nil {
		return nil, fmt.Errorf("invalid post-hook in %q: %w", manifestPath, err)
	}
	if err := validateCommands(manifest.Validation, "validation"); err != nil {
		return nil, fmt.Errorf("invalid validation in %q: %w", manifestPath, err)
	}

	// 7. Return manifest
	return &manifest, nil
}

// validateCommands validates a slice of commands.
func validateCommands(commands []Command, commandType string) error {
	for i, cmd := range commands {
		if cmd.Name == "" {
			return fmt.Errorf("%s at index %d: name is required", commandType, i)
		}
		if cmd.Command == "" {
			return fmt.Errorf("%s %q at index %d: command is required", commandType, cmd.Name, i)
		}
		if cmd.Timeout <= 0 {
			return fmt.Errorf("%s %q at index %d: timeout must be positive, got: %v", commandType, cmd.Name, i, cmd.Timeout)
		}
	}
	return nil
}
