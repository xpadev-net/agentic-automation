# Agent Manifest Specification

## Overview

The Agent Manifest (`.agent-config.yaml`) is a repository-level configuration file that allows customization of command execution during the AI agent automation workflow. It defines pre-execution hooks, validation commands, and post-execution hooks.

## File Location

**Path**: `.agent-config.yaml` (repository root)

The manifest file should be committed to the target repository's git history. If the file does not exist, all hooks and validations will be skipped.

## Schema Version

Current version: `1.0`

## Full Schema

```yaml
version: "1.0"

# Pre-execution hooks (before AI agent runs)
hooks:
  pre:
    - name: string              # Hook identifier (e.g., "install-dependencies")
      command: string           # Shell command to execute
      description: string       # Human-readable description (optional)
      timeout: duration         # Maximum execution time (e.g., "5m", "30s")
      required: boolean         # If true, failure aborts the workflow

# Validation commands (after AI agent, before git commit)
validation:
  - name: string              # Validation identifier (e.g., "lint", "type-check")
    command: string           # Shell command to execute
    description: string       # Human-readable description (optional)
    timeout: duration         # Maximum execution time
    required: boolean         # If true, failure triggers agent retry with error feedback

# Post-execution hooks (after successful PR creation)
hooks:
  post:
    - name: string              # Hook identifier (e.g., "notify-slack")
      command: string           # Shell command to execute
      description: string       # Human-readable description (optional)
      timeout: duration         # Maximum execution time
      required: boolean         # If true, failure logs warning but does not abort
```

## Field Definitions

### `version`

**Type**: `string`

**Required**: Yes

**Description**: Schema version for forward compatibility. Current version is `"1.0"`.

### `hooks.pre`

**Type**: `array` of hook objects

**Required**: No

**Description**: Commands executed **before** the AI agent starts working. Useful for environment setup tasks like installing dependencies or building artifacts.

**Execution order**: Sequential, in the order defined in the manifest.

**Failure behavior**:
- If `required: true` and hook fails → Abort workflow, report error to Operator API
- If `required: false` and hook fails → Log warning, continue to next hook

**Example use cases**:
- `npm install` or `yarn install`
- `docker-compose up -d` (start local services)
- `go mod download`
- `mkdir -p build/`

### `validation`

**Type**: `array` of validation objects

**Required**: No

**Description**: Commands executed **after** the AI agent completes its work but **before** committing changes to git. These are typically linters, type checkers, or test suites.

**Execution order**: Sequential, in the order defined in the manifest.

**Failure behavior**:
- If `required: true` and validation fails → Retry the AI agent with validation error output as feedback (up to max retry limit)
- If `required: false` and validation fails → Log warning, continue to next validation

**Example use cases**:
- `npm run lint`
- `npm run type-check`
- `go vet ./...`
- `cargo check`
- `pytest tests/`

### `hooks.post`

**Type**: `array` of hook objects

**Required**: No

**Description**: Commands executed **after** a Pull Request has been successfully created. Useful for notifications or triggering downstream workflows.

**Execution order**: Sequential, in the order defined in the manifest.

**Failure behavior**:
- If `required: true` and hook fails → Log error, report to Operator API (PR already created, cannot abort)
- If `required: false` and hook fails → Log warning

**Example use cases**:
- Send Slack/Discord notification
- Trigger CI/CD pipeline
- Update external tracking systems

### Hook/Validation Object Fields

#### `name`

**Type**: `string`

**Required**: Yes

**Description**: Unique identifier for the hook or validation step. Used in logs and error reports.

#### `command`

**Type**: `string`

**Required**: Yes

**Description**: Shell command to execute. Commands are executed in the working directory of the cloned repository.

**Environment**: Commands run with the same environment variables as the agent-runner process, including `PATH`, `HOME`, and any injected secrets.

#### `description`

**Type**: `string`

**Required**: No

**Description**: Human-readable description of what this command does. Used in logs and error reports.

#### `timeout`

**Type**: `duration` (string)

**Required**: Yes

**Description**: Maximum time allowed for command execution. Format follows Go's `time.Duration` syntax:
- `"30s"` = 30 seconds
- `"5m"` = 5 minutes
- `"1h30m"` = 1 hour 30 minutes

If the command exceeds this timeout, it will be terminated and treated as a failure.

#### `required`

**Type**: `boolean`

**Required**: Yes

**Description**: Determines failure handling behavior:
- `true`: Failure has consequences (abort workflow, retry agent, or log error)
- `false`: Failure is logged as a warning but workflow continues

## Example Manifests

### Example 1: Node.js/TypeScript Project

```yaml
version: "1.0"

hooks:
  pre:
    - name: "install-dependencies"
      command: "npm ci"
      description: "Install npm dependencies using package-lock.json"
      timeout: "5m"
      required: true

validation:
  - name: "lint"
    command: "npm run lint"
    description: "ESLint code style validation"
    timeout: "3m"
    required: true

  - name: "type-check"
    command: "npm run type-check"
    description: "TypeScript type checking"
    timeout: "2m"
    required: true

  - name: "tests"
    command: "npm run test"
    description: "Run test suite"
    timeout: "10m"
    required: false

hooks:
  post:
    - name: "notify-discord"
      command: "curl -X POST $DISCORD_WEBHOOK_URL -H 'Content-Type: application/json' -d '{\"content\": \"PR created for issue #$ISSUE_NUMBER\"}'"
      description: "Send Discord notification"
      timeout: "30s"
      required: false
```

### Example 2: Go Project

```yaml
version: "1.0"

hooks:
  pre:
    - name: "download-dependencies"
      command: "go mod download"
      description: "Download Go module dependencies"
      timeout: "3m"
      required: true

validation:
  - name: "fmt-check"
    command: "test -z $(gofmt -l .)"
    description: "Check Go code formatting"
    timeout: "1m"
    required: true

  - name: "vet"
    command: "go vet ./..."
    description: "Go static analysis"
    timeout: "2m"
    required: true

  - name: "build"
    command: "go build ./..."
    description: "Compile all packages"
    timeout: "5m"
    required: true

hooks:
  post: []
```

### Example 3: Python Project

```yaml
version: "1.0"

hooks:
  pre:
    - name: "install-dependencies"
      command: "pip install -r requirements.txt"
      description: "Install Python dependencies"
      timeout: "5m"
      required: true

validation:
  - name: "black"
    command: "black --check ."
    description: "Check Python code formatting"
    timeout: "2m"
    required: true

  - name: "mypy"
    command: "mypy ."
    description: "Type checking with mypy"
    timeout: "3m"
    required: true

  - name: "pytest"
    command: "pytest tests/"
    description: "Run pytest test suite"
    timeout: "10m"
    required: false

hooks:
  post: []
```

## Execution Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Clone Repository                                         │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Load .agent-config.yaml                                  │
│    (If file not found, skip all hooks/validations)         │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Execute Pre-Hooks (Sequential)                           │
│    - Run each hooks.pre command in order                    │
│    - If required=true hook fails → Abort workflow           │
│    - If required=false hook fails → Log warning, continue   │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Restore Session from S3 (if retry)                       │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Execute AI Agent                                          │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Execute Validations (Sequential)                         │
│    - Run each validation command in order                   │
│    - If required=true validation fails → Retry agent        │
│    - If required=false validation fails → Log warning       │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 7. Git Commit & Push                                         │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 8. Create Pull Request                                       │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ 9. Execute Post-Hooks (Sequential)                          │
│    - Run each hooks.post command in order                   │
│    - Failures are logged but do not affect PR               │
└─────────────────────────┴───────────────────────────────────┘
```

## Agent-Runner Implementation

### Package Structure

```
agent-runner/
├── pkg/
│   ├── config/
│   │   ├── loader.go       # Load and parse .agent-config.yaml
│   │   └── types.go        # Manifest struct definitions
│   └── hooks/
│       ├── runner.go       # Execute hooks/validations
│       └── errors.go       # Hook-specific error types
└── cmd/
    └── main.go             # Updated execution flow
```

### Key Types (Go)

```go
package config

import "time"

type Manifest struct {
    Version    string      `yaml:"version"`
    Hooks      Hooks       `yaml:"hooks"`
    Validation []Command   `yaml:"validation"`
}

type Hooks struct {
    Pre  []Command `yaml:"pre"`
    Post []Command `yaml:"post"`
}

type Command struct {
    Name        string        `yaml:"name"`
    Command     string        `yaml:"command"`
    Description string        `yaml:"description"`
    Timeout     time.Duration `yaml:"timeout"`
    Required    bool          `yaml:"required"`
}

// LoadManifest loads .agent-config.yaml from the working directory.
// Returns nil if file does not exist (skip hooks/validations).
func LoadManifest(workDir string) (*Manifest, error)
```

### Pseudo-code Execution

```go
// In agent-runner cmd/main.go

func main() {
    // ... clone repository ...

    // Load manifest
    manifest, err := config.LoadManifest(workDir)
    if err != nil {
        log.Fatalf("Failed to load manifest: %v", err)
    }

    // Execute pre-hooks
    if manifest != nil {
        if err := hooks.RunPreHooks(manifest.Hooks.Pre, workDir); err != nil {
            reportError(operatorAPIURL, runID, err)
            os.Exit(1)
        }
    }

    // ... restore session, execute AI agent ...

    // Execute validations
    if manifest != nil {
        if err := hooks.RunValidations(manifest.Validation, workDir); err != nil {
            // Retry agent with validation error feedback
            agentInput = appendValidationError(agentInput, err)
            retryCount++
            goto retryAgent
        }
    }

    // ... commit, push, create PR ...

    // Execute post-hooks
    if manifest != nil {
        if err := hooks.RunPostHooks(manifest.Hooks.Post, workDir); err != nil {
            log.Warnf("Post-hook failed: %v", err)
        }
    }
}
```

## Backward Compatibility

**Previous behavior**: `npm run lint` and `npm run type-check` were hardcoded in `pkg/lint/runner.go`.

**New behavior**:
- If `.agent-config.yaml` exists → Use manifest commands
- If `.agent-config.yaml` does not exist → Skip all validations (no hardcoded fallback)

**Migration path**: Existing repositories must add `.agent-config.yaml` to maintain validation behavior.

## Security Considerations

1. **Command Injection**: Commands are executed in a shell. Ensure environment variables used in commands are sanitized.
2. **Secrets**: Avoid hardcoding secrets in `.agent-config.yaml`. Use environment variables (e.g., `$DISCORD_WEBHOOK_URL`).
3. **Timeout Enforcement**: Always enforce timeout limits to prevent runaway processes.
4. **Working Directory**: Commands execute in the cloned repository directory. Ensure they cannot escape this sandbox.

## Error Reporting

When a hook or validation fails, the agent-runner should:

1. **Capture output**: Store stdout and stderr from the failed command
2. **Report to Operator API**: Send error details to `/api/agent-runs/{id}/report`
3. **Retry logic**:
   - Pre-hook failure: Abort, no retry
   - Validation failure: Retry agent (up to max retry limit)
   - Post-hook failure: Log warning, no retry

## Future Extensions

Potential future enhancements (not in v1.0):

- **Conditional execution**: Run validations only if certain file paths changed
- **Parallel execution**: Run independent validations concurrently
- **Matrix testing**: Run validations with multiple tool versions
- **Custom environment**: Per-hook environment variable overrides
- **Validation caching**: Skip validations if code hasn't changed
