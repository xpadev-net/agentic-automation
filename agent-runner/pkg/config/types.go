package config

import (
	"errors"
	"fmt"
	"time"
)

// Manifest represents the agent configuration manifest structure.
// It is loaded from .agent-config.yaml in the repository root.
type Manifest struct {
	Version    string    `yaml:"version"`
	Hooks      Hooks     `yaml:"hooks"`
	Validation []Command `yaml:"validation"`
}

// Hooks contains pre-execution and post-execution hook commands.
type Hooks struct {
	Pre  []Command `yaml:"pre"`
	Post []Command `yaml:"post"`
}

// Command represents a single hook or validation command.
type Command struct {
	Name        string        `yaml:"name"`
	Command     string        `yaml:"command"`
	Description string        `yaml:"description"`
	Timeout     time.Duration `yaml:"-"`
	Required    bool          `yaml:"required"`
}

// commandYAML is an intermediate structure for parsing YAML with timeout as string.
type commandYAML struct {
	Name        string `yaml:"name"`
	Command     string `yaml:"command"`
	Description string `yaml:"description"`
	Timeout     string `yaml:"timeout"`
	Required    bool   `yaml:"required"`
}

// UnmarshalYAML implements custom YAML unmarshaling for Command.
// It handles the conversion of timeout string (e.g., "5m", "30s") to time.Duration.
func (c *Command) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var cmdYAML commandYAML
	if err := unmarshal(&cmdYAML); err != nil {
		return err
	}

	// Validate required fields
	if cmdYAML.Name == "" {
		return errors.New("command name is required")
	}
	if cmdYAML.Command == "" {
		return errors.New("command is required")
	}
	if cmdYAML.Timeout == "" {
		return fmt.Errorf("timeout is required for command %q", cmdYAML.Name)
	}

	// Parse timeout duration
	timeout, err := time.ParseDuration(cmdYAML.Timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q for command %q: %w", cmdYAML.Timeout, cmdYAML.Name, err)
	}

	// Validate timeout is positive
	if timeout <= 0 {
		return fmt.Errorf("timeout must be positive for command %q, got: %v", cmdYAML.Name, timeout)
	}

	// Assign values
	c.Name = cmdYAML.Name
	c.Command = cmdYAML.Command
	c.Description = cmdYAML.Description
	c.Timeout = timeout
	c.Required = cmdYAML.Required

	return nil
}

// Error types for manifest loading and validation
var (
	ErrInvalidVersion = errors.New("unsupported manifest version")
	ErrInvalidTimeout = errors.New("invalid timeout value")
	ErrInvalidCommand = errors.New("invalid command configuration")
)
