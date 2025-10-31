# Agent Runner Detailed Specification

**Version**: 1.0.0
**Last Updated**: 2025-11-01

## Overview

This document provides detailed implementation guidance for the agent-runner Go binary, which executes AI agents (claude-code or cursor-agents) inside Kubernetes Pods.

## Agent CLI References

- **Claude Code CLI**: https://docs.claude.com/ja/docs/claude-code/cli-reference
- **Cursor Headless CLI**: https://cursor.com/ja/docs/cli/headless

---

## Supported Agents

### 1. Claude Code (claude-code)

**Installation** (in Dockerfile):
```dockerfile
RUN npm install -g @anthropic/claude-code
```

**Invocation**:
```bash
claude-code \
  --workspace /workspace \
  --task "Fix issue: ${ISSUE_TITLE}

${ISSUE_BODY}

Previous attempts:
${PREVIOUS_ATTEMPTS}

CI failures:
${CI_LOGS}" \
  --non-interactive \
  --max-tokens 8000
```

**Environment Variables Required**:
- `ANTHROPIC_API_KEY`: Claude API key

**Output Parsing**:
- Stdout: Agent's change summary and explanation
- Stderr: Error messages and warnings
- Exit Code:
  - 0: Success (changes made)
  - 1: Failure (error occurred)
  - 2: No changes needed

**File Change Detection**:
- Claude Code automatically commits changes to git
- Check via `git diff --cached --exit-code` (exit 1 = changes exist)

---

### 2. Cursor Headless (cursor-agents)

**Installation** (in Dockerfile):
```dockerfile
# Cursor CLI installation
RUN curl -fsSL https://download.cursor.com/install.sh | sh
```

**Invocation**:
```bash
cursor agent \
  --cwd /workspace \
  --prompt "Fix issue #${ISSUE_NUMBER}: ${ISSUE_TITLE}

Description:
${ISSUE_BODY}

Previous Attempts:
${PREVIOUS_ATTEMPTS}

Please fix the issue and ensure all tests pass." \
  --headless \
  --no-confirm
```

**Environment Variables Required**:
- `CURSOR_API_KEY`: Cursor API key

**Output Parsing**:
- Stdout: Agent actions log (JSON format)
- Stderr: Error messages
- Exit Code:
  - 0: Success
  - 1: Failure

**File Change Detection**:
- Cursor does not auto-commit
- Check via `git status --porcelain` (non-empty = changes exist)

---

## agent-runner Implementation (Go)

### Main Entry Point

**File**: `agent-runner/main.go`

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"agent-runner/pkg/agent"
	"agent-runner/pkg/context"
	"agent-runner/pkg/git"
	"agent-runner/pkg/lint"
	"agent-runner/pkg/reporter"
)

func main() {
	var (
		issueID          int
		repo             string
		prompt           string
		previousAttempts string
		ciLogs           string
	)

	rootCmd := &cobra.Command{
		Use:   "agent-runner",
		Short: "Execute AI agent for GitHub Issue automation",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(issueID, repo, prompt, previousAttempts, ciLogs)
		},
	}

	rootCmd.Flags().IntVar(&issueID, "issue-id", 0, "GitHub Issue ID")
	rootCmd.Flags().StringVar(&repo, "repo", "", "Repository (owner/name)")
	rootCmd.Flags().StringVar(&prompt, "prompt", "", "Issue context prompt")
	rootCmd.Flags().StringVar(&previousAttempts, "previous-attempts", "", "Previous retry attempts JSON")
	rootCmd.Flags().StringVar(&ciLogs, "ci-logs", "", "CI failure logs")

	rootCmd.MarkFlagRequired("issue-id")
	rootCmd.MarkFlagRequired("repo")
	rootCmd.MarkFlagRequired("prompt")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(issueID int, repo, prompt, previousAttempts, ciLogs string) error {
	// 1. Parse environment variables
	cfg := context.LoadConfig()

	// 2. Clone repository
	workDir := "/workspace"
	if err := git.CloneRepo(cfg.GitHubToken, repo, workDir); err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Git clone failed: %v", err))
	}

	// 3. Create feature branch
	branchName := fmt.Sprintf("feature/issue-%d", issueID)
	if err := git.CreateBranch(workDir, branchName); err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Branch creation failed: %v", err))
	}

	// 4. Build agent prompt
	fullPrompt := context.BuildPrompt(prompt, previousAttempts, ciLogs)

	// 5. Execute selected agent
	executor := agent.NewExecutor(cfg.AgentType)
	output, err := executor.Execute(workDir, fullPrompt)
	if err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Agent execution failed: %v\nOutput: %s", err, output))
	}

	// 6. Check for file changes
	hasChanges, err := git.HasChanges(workDir)
	if err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Git diff check failed: %v", err))
	}
	if !hasChanges {
		return reporter.ReportFailure(cfg, "No file changes detected after agent execution")
	}

	// 7. Run lint validation
	if err := lint.RunLint(workDir); err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Lint failed: %v", err))
	}

	// 8. Run type check validation
	if err := lint.RunTypeCheck(workDir); err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Type check failed: %v", err))
	}

	// 9. Commit changes
	commitSHA, err := git.CommitChanges(workDir, fmt.Sprintf("feat: implement issue #%d", issueID))
	if err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Git commit failed: %v", err))
	}

	// 10. Push to remote
	if err := git.PushBranch(workDir, branchName, cfg.GitHubToken); err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("Git push failed: %v", err))
	}

	// 11. Create Pull Request via GitHub API
	prNumber, err := git.CreatePR(cfg.GitHubToken, repo, branchName, issueID)
	if err != nil {
		return reporter.ReportFailure(cfg, fmt.Sprintf("PR creation failed: %v", err))
	}

	// 12. Report success to Operator API
	return reporter.ReportSuccess(cfg, prNumber, branchName, commitSHA)
}
```

---

### Agent Executor (pkg/agent/executor.go)

```go
package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Executor struct {
	agentType string
}

func NewExecutor(agentType string) *Executor {
	return &Executor{agentType: agentType}
}

func (e *Executor) Execute(workDir, prompt string) (string, error) {
	switch e.agentType {
	case "claude-code":
		return e.executeClaudeCode(workDir, prompt)
	case "cursor-agents":
		return e.executeCursor(workDir, prompt)
	default:
		return "", fmt.Errorf("unknown agent type: %s", e.agentType)
	}
}

func (e *Executor) executeClaudeCode(workDir, prompt string) (string, error) {
	cmd := exec.Command("claude-code",
		"--workspace", workDir,
		"--task", prompt,
		"--non-interactive",
		"--max-tokens", "8000",
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("ANTHROPIC_API_KEY=%s", os.Getenv("ANTHROPIC_API_KEY")))

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (e *Executor) executeCursor(workDir, prompt string) (string, error) {
	cmd := exec.Command("cursor", "agent",
		"--cwd", workDir,
		"--prompt", prompt,
		"--headless",
		"--no-confirm",
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("CURSOR_API_KEY=%s", os.Getenv("CURSOR_API_KEY")))

	output, err := cmd.CombinedOutput()
	return string(output), err
}
```

---

### Git Operations (pkg/git/operations.go)

```go
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

func CloneRepo(token, repo, dest string) error {
	url := fmt.Sprintf("https://%s@github.com/%s.git", token, repo)
	cmd := exec.Command("git", "clone", url, dest)
	return cmd.Run()
}

func CreateBranch(workDir, branchName string) error {
	cmd := exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = workDir
	return cmd.Run()
}

func HasChanges(workDir string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

func CommitChanges(workDir, message string) (string, error) {
	// Add all changes
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = workDir
	if err := addCmd.Run(); err != nil {
		return "", err
	}

	// Commit
	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = workDir
	if err := commitCmd.Run(); err != nil {
		return "", err
	}

	// Get commit SHA
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = workDir
	output, err := shaCmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func PushBranch(workDir, branchName, token string) error {
	cmd := exec.Command("git", "push", "-u", "origin", branchName)
	cmd.Dir = workDir
	return cmd.Run()
}

func CreatePR(token, repo, branchName string, issueNumber int) (int, error) {
	// Use go-github library to create PR via API
	// Implementation details in actual code
	// Returns PR number
	return 0, fmt.Errorf("not implemented")
}
```

---

### Lint Runner (pkg/lint/runner.go)

```go
package lint

import (
	"os/exec"
)

func RunLint(workDir string) error {
	cmd := exec.Command("npm", "run", "lint")
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lint failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

func RunTypeCheck(workDir string) error {
	cmd := exec.Command("npm", "run", "type-check")
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("type check failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}
```

---

### Reporter Client (pkg/reporter/client.go)

```go
package reporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Config struct {
	OperatorAPIURL   string
	OperatorAPIToken string
	AgentRunID       int
	AgentType        string
}

type ReportRequest struct {
	Status       string `json:"status"`
	AgentType    string `json:"agent_type"`
	PRNumber     int    `json:"pr_number,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Logs         string `json:"logs,omitempty"`
}

func ReportSuccess(cfg Config, prNumber int, branch, commitSHA string) error {
	req := ReportRequest{
		Status:    "succeeded",
		AgentType: cfg.AgentType,
		PRNumber:  prNumber,
		Branch:    branch,
		CommitSHA: commitSHA,
	}
	return sendReport(cfg, req)
}

func ReportFailure(cfg Config, errorMsg string) error {
	req := ReportRequest{
		Status:       "failed",
		AgentType:    cfg.AgentType,
		ErrorMessage: errorMsg,
	}
	return sendReport(cfg, req)
}

func sendReport(cfg Config, req ReportRequest) error {
	url := fmt.Sprintf("%s/api/agent-runs/%d/report", cfg.OperatorAPIURL, cfg.AgentRunID)

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	// Exponential backoff retry (max 5 attempts)
	for attempt := 1; attempt <= 5; attempt++ {
		httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return err
		}

		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.OperatorAPIToken))
		httpReq.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(httpReq)
		if err == nil && resp.StatusCode == http.StatusOK {
			return nil
		}

		// Exponential backoff: 1s, 2s, 4s, 8s
		if attempt < 5 {
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
	}

	return fmt.Errorf("failed to report after 5 attempts")
}
```

---

## Dockerfile

```dockerfile
# Multi-stage build
FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY . .
RUN go build -o agent-runner main.go

# Runtime image
FROM node:22-alpine

# Install git
RUN apk add --no-cache git

# Install Claude Code CLI
RUN npm install -g @anthropic/claude-code

# Install Cursor CLI
RUN curl -fsSL https://download.cursor.com/install.sh | sh

# Copy agent-runner binary
COPY --from=builder /build/agent-runner /usr/local/bin/agent-runner

# Set working directory
WORKDIR /workspace

# Entry point
ENTRYPOINT ["agent-runner"]
```

---

## Environment Variables

| Variable | Description | Required | Example |
|----------|-------------|----------|---------|
| `OPERATOR_API_URL` | Operator REST API base URL | Yes | `http://agent-operator.default.svc.cluster.local:3000` |
| `OPERATOR_API_TOKEN` | Bearer token for API auth | Yes | `sk-secret-token-abc123` |
| `AGENT_RUN_ID` | AgentRun database record ID | Yes | `456` |
| `AGENT_TYPE` | Agent to execute | Yes | `claude-code` or `cursor-agents` |
| `GITHUB_TOKEN` | GitHub Personal Access Token | Yes | `ghp_xxxxx` |
| `ANTHROPIC_API_KEY` | Claude API key | Conditional | Required if `AGENT_TYPE=claude-code` |
| `CURSOR_API_KEY` | Cursor API key | Conditional | Required if `AGENT_TYPE=cursor-agents` |
| `WORKSPACE_DIR` | Working directory | No | `/workspace` (default) |

---

## Error Handling

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success (report sent to Operator) |
| 1 | Failure (any step failed, report sent) |

### Error Scenarios

1. **Git Clone Failed**: Report failure with error message
2. **Agent Execution Failed**: Report with agent stderr/stdout
3. **No File Changes**: Report failure "No changes detected"
4. **Lint Failed**: Report with lint output
5. **Type Check Failed**: Report with typecheck output
6. **Git Push Failed**: Report with git error
7. **PR Creation Failed**: Report with GitHub API error
8. **Operator API Unreachable**: Retry 5 times, then exit 1

---

## Testing

### Unit Tests

- `pkg/agent/executor_test.go` - Mock agent CLI execution
- `pkg/lint/runner_test.go` - Mock npm commands
- `pkg/git/operations_test.go` - Mock git commands
- `pkg/reporter/client_test.go` - Mock HTTP requests

### Integration Tests

- End-to-end test with mock repository
- Test full workflow: clone → agent → lint → commit → push → PR → report

---

## References

- [Claude Code CLI Reference](https://docs.claude.com/ja/docs/claude-code/cli-reference)
- [Cursor Headless CLI](https://cursor.com/ja/docs/cli/headless)
- [ai-agent-execution.md](./ai-agent-execution.md) - Operator contract
- [internal-api.yaml](./internal-api.yaml) - Report API specification

---

**Version**: 1.0.0
**Last Updated**: 2025-11-01
