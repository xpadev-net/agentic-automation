package config

import "time"

// Command represents a hook or validation command from .agent-config.yaml
type Command struct {
	Name        string        `yaml:"name"`
	Command     string        `yaml:"command"`
	Description string        `yaml:"description"`
	Timeout     time.Duration `yaml:"timeout"`
	Required    bool          `yaml:"required"`
}

// Hooks contains pre and post execution hooks
type Hooks struct {
	Pre  []Command `yaml:"pre"`
	Post []Command `yaml:"post"`
}

// Manifest represents the .agent-config.yaml configuration
type Manifest struct {
	Version    string    `yaml:"version"`
	Hooks      Hooks     `yaml:"hooks"`
	Validation []Command `yaml:"validation"`
}
