package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
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
		Long: `agent-runner executes AI agents (claude-code or cursor-agents) in Kubernetes Pods.
It processes GitHub Issues, runs lint/typecheck, commits changes, and reports results to the Operator API.`,
		Version: "0.1.0",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate command-line arguments
			if err := validateArgs(issueID, repo, prompt); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			return run(issueID, repo, prompt, previousAttempts, ciLogs)
		},
	}

	rootCmd.Flags().IntVar(&issueID, "issue-id", 0, "GitHub Issue ID (must be positive integer)")
	rootCmd.Flags().StringVar(&repo, "repo", "", "Repository in format owner/name (e.g., octocat/Hello-World)")
	rootCmd.Flags().StringVar(&prompt, "prompt", "", "Issue context prompt describing the task")
	rootCmd.Flags().StringVar(&previousAttempts, "previous-attempts", "", "Previous retry attempts in JSON format (optional)")
	rootCmd.Flags().StringVar(&ciLogs, "ci-logs", "", "CI failure logs for retry context (optional)")

	rootCmd.MarkFlagRequired("issue-id")
	rootCmd.MarkFlagRequired("repo")
	rootCmd.MarkFlagRequired("prompt")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// validateArgs validates command-line arguments
func validateArgs(issueID int, repo, prompt string) error {
	// Validate issue-id: must be positive integer
	if issueID <= 0 {
		return fmt.Errorf("--issue-id must be a positive integer, got: %d", issueID)
	}

	// Validate repo: must be in format owner/repo
	repoPattern := regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
	if !repoPattern.MatchString(repo) {
		return fmt.Errorf("--repo must be in format owner/repo, got: %q", repo)
	}

	// Validate prompt: must not be empty
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("--prompt must not be empty")
	}

	return nil
}

// validateEnv validates required environment variables
func validateEnv() (*envConfig, error) {
	cfg := &envConfig{}

	// Required environment variables
	var missing []string

	if cfg.OperatorAPIURL = os.Getenv("OPERATOR_API_URL"); cfg.OperatorAPIURL == "" {
		missing = append(missing, "OPERATOR_API_URL")
	}

	if cfg.OperatorAPIToken = os.Getenv("OPERATOR_API_TOKEN"); cfg.OperatorAPIToken == "" {
		missing = append(missing, "OPERATOR_API_TOKEN")
	}

	agentRunIDStr := os.Getenv("AGENT_RUN_ID")
	if agentRunIDStr == "" {
		missing = append(missing, "AGENT_RUN_ID")
	} else {
		var err error
		cfg.AgentRunID, err = strconv.Atoi(agentRunIDStr)
		if err != nil {
			return nil, fmt.Errorf("AGENT_RUN_ID must be an integer, got: %q", agentRunIDStr)
		}
		if cfg.AgentRunID <= 0 {
			return nil, fmt.Errorf("AGENT_RUN_ID must be a positive integer, got: %d", cfg.AgentRunID)
		}
	}

	if cfg.AgentType = os.Getenv("AGENT_TYPE"); cfg.AgentType == "" {
		missing = append(missing, "AGENT_TYPE")
	} else if cfg.AgentType != "claude-code" && cfg.AgentType != "cursor-agents" {
		return nil, fmt.Errorf("AGENT_TYPE must be 'claude-code' or 'cursor-agents', got: %q", cfg.AgentType)
	}

	if cfg.GitHubToken = os.Getenv("GITHUB_TOKEN"); cfg.GitHubToken == "" {
		missing = append(missing, "GITHUB_TOKEN")
	}

	// Optional environment variables with defaults
	cfg.WorkDir = os.Getenv("WORKSPACE_DIR")
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/workspace"
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// envConfig holds environment variable configuration
type envConfig struct {
	OperatorAPIURL   string
	OperatorAPIToken string
	AgentRunID       int
	AgentType        string
	GitHubToken      string
	WorkDir          string
}

func run(issueID int, repo, prompt, previousAttempts, ciLogs string) error {
	// 1. Validate and load environment variables
	envCfg, err := validateEnv()
	if err != nil {
		return fmt.Errorf("environment validation failed: %w", err)
	}

	// 2. Log arguments for debugging (basic info only, no sensitive data)
	fmt.Fprintf(os.Stderr, "Starting agent-runner:\n")
	fmt.Fprintf(os.Stderr, "  Issue ID: %d\n", issueID)
	fmt.Fprintf(os.Stderr, "  Repository: %s\n", repo)
	fmt.Fprintf(os.Stderr, "  Agent Type: %s\n", envCfg.AgentType)
	fmt.Fprintf(os.Stderr, "  Agent Run ID: %d\n", envCfg.AgentRunID)
	fmt.Fprintf(os.Stderr, "  Workspace: %s\n", envCfg.WorkDir)

	// 3. Prepare error handling
	// Note: Reporter will be implemented in T038
	// For now, we'll return errors directly

	// 4. Execution flow skeleton (to be implemented in T033-T048)
	// TODO: Clone repository (T035)
	// TODO: Restore session from S3 (T054_S3) - if retry_count > 0

	// TODO: Create feature branch (T035)
	// branchName := fmt.Sprintf("feature/issue-%d", issueID)

	// TODO: Build full prompt with previous attempts and CI logs (T037)
	// fullPrompt := context.BuildPrompt(prompt, previousAttempts, ciLogs)

	// TODO: Execute selected agent (T033)
	// executor := agent.NewExecutor(envCfg.AgentType)
	// output, err := executor.Execute(envCfg.WorkDir, fullPrompt)

	// TODO: Check for file changes (T036)
	// hasChanges, err := git.HasChanges(envCfg.WorkDir)

	// TODO: Run lint validation (T034)
	// if err := lint.RunLint(envCfg.WorkDir); err != nil {
	//     return reporter.ReportFailure(...)
	// }

	// TODO: Run type check validation (T034)
	// if err := lint.RunTypeCheck(envCfg.WorkDir); err != nil {
	//     return reporter.ReportFailure(...)
	// }

	// TODO: Commit changes (T035)
	// commitSHA, err := git.CommitChanges(envCfg.WorkDir, fmt.Sprintf("feat: implement issue #%d", issueID))

	// TODO: Push branch to remote (T035)
	// if err := git.PushBranch(envCfg.WorkDir, branchName, envCfg.GitHubToken); err != nil {
	//     return reporter.ReportFailure(...)
	// }

	// TODO: Save session to S3 (T055_S3) - always before reporting

	// TODO: Create Pull Request via GitHub API (T035)
	// prNumber, err := git.CreatePR(envCfg.GitHubToken, repo, branchName, issueID)

	// TODO: Report success to Operator API (T038)
	// return reporter.ReportSuccess(...)

	// Temporary: return error indicating implementation is incomplete
	return fmt.Errorf("execution flow not yet implemented (T033-T048)")
}
