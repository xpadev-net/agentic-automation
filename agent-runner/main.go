package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"agent-runner/pkg/agent"
	"agent-runner/pkg/config"
	"agent-runner/pkg/context"
	"agent-runner/pkg/git"
	"agent-runner/pkg/hooks"
	"agent-runner/pkg/reporter"
	"agent-runner/pkg/storage"
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
			return Run(issueID, repo, prompt, previousAttempts, ciLogs)
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

// constructOperatorURL constructs the Operator API URL from Kubernetes service components
func constructOperatorURL() (string, error) {
	namespace := os.Getenv("KUBERNETES_NAMESPACE")
	serviceName := os.Getenv("OPERATOR_SERVICE_NAME")
	port := os.Getenv("OPERATOR_SERVICE_PORT")

	var missing []string
	if namespace == "" {
		missing = append(missing, "KUBERNETES_NAMESPACE")
	}
	if serviceName == "" {
		missing = append(missing, "OPERATOR_SERVICE_NAME")
	}
	if port == "" {
		missing = append(missing, "OPERATOR_SERVICE_PORT")
	}

	if len(missing) > 0 {
		return "", fmt.Errorf("missing required environment variables for Operator URL construction: %s", strings.Join(missing, ", "))
	}

	// Construct Kubernetes internal service URL
	// Format: http://{service}.{namespace}.svc.cluster.local:{port}
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%s", serviceName, namespace, port)
	return url, nil
}

// validateEnv validates required environment variables
func validateEnv() (*envConfig, error) {
	cfg := &envConfig{}

	// Required environment variables
	var missing []string

	// Construct Operator API URL from Kubernetes service components
	var err error
	cfg.OperatorAPIURL, err = constructOperatorURL()
	if err != nil {
		return nil, err
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
	retryCountStr := os.Getenv("RETRY_COUNT")
	if retryCountStr == "" {
		cfg.RetryCount = 0 // デフォルト値
	} else {
		retryCount, err := strconv.Atoi(retryCountStr)
		if err != nil {
			return nil, fmt.Errorf("RETRY_COUNT must be an integer, got: %q", retryCountStr)
		}
		if retryCount < 0 {
			return nil, fmt.Errorf("RETRY_COUNT must be non-negative, got: %d", retryCount)
		}
		cfg.RetryCount = retryCount
	}

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
	RetryCount       int
	WorkDir          string
}

// Run executes the agent-runner workflow.
// This function is exported for testing purposes.
func Run(issueID int, repo, prompt, previousAttempts, ciLogs string) error {
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
	fmt.Fprintf(os.Stderr, "  Retry Count: %d\n", envCfg.RetryCount)
	fmt.Fprintf(os.Stderr, "  Workspace: %s\n", envCfg.WorkDir)
	fmt.Fprintf(os.Stderr, "  Operator API URL: %s\n", envCfg.OperatorAPIURL)

	// 3. Initialize reporter client
	reporterClient, err := reporter.NewClient(envCfg.OperatorAPIURL, envCfg.OperatorAPIToken, envCfg.AgentRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize reporter client: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Initialized reporter client for AgentRun ID: %d\n", envCfg.AgentRunID)

	// 4. Clone repository
	fmt.Fprintf(os.Stderr, "Cloning repository %s to %s\n", repo, envCfg.WorkDir)
	if err := git.CloneRepo(envCfg.GitHubToken, repo, envCfg.WorkDir); err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Repository clone failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("repository clone failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Cloned repository %s to %s\n", repo, envCfg.WorkDir)

	// 5. Restore session from S3 (if retry_count > 0)
	if envCfg.RetryCount > 0 {
		fmt.Fprintf(os.Stderr, "Restoring session for AgentRun ID: %d (retry_count: %d)\n", envCfg.AgentRunID, envCfg.RetryCount)
		if err := storage.RestoreSession(envCfg.AgentRunID, envCfg.RetryCount); err != nil {
			reportErr := reporterClient.ReportFailure(
				fmt.Sprintf("Session restore failed: %v", err),
				"",
				envCfg.AgentType,
			)
			if reportErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
			}
			return fmt.Errorf("session restore failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Restored session for AgentRun ID: %d (retry_count: %d)\n", envCfg.AgentRunID, envCfg.RetryCount)
	} else {
		fmt.Fprintf(os.Stderr, "Initial execution, skipping session restore\n")
	}

	// 6. Load manifest
	manifest, err := config.LoadManifest(envCfg.WorkDir)
	if err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Manifest load failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("manifest load failed: %w", err)
	}
	if manifest == nil {
		fmt.Fprintf(os.Stderr, "No manifest file found, skipping hooks/validations\n")
	} else {
		fmt.Fprintf(os.Stderr, "Loaded manifest: version %s\n", manifest.Version)
	}

	// 7. Create feature branch
	branchName := fmt.Sprintf("feature/issue-%d", issueID)
	fmt.Fprintf(os.Stderr, "Creating/checking out branch: %s\n", branchName)
	if err := git.CreateBranch(envCfg.WorkDir, branchName, envCfg.RetryCount); err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Branch creation failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("branch creation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Created/checked out branch: %s\n", branchName)

	// 8. Build prompt
	fmt.Fprintf(os.Stderr, "Building prompt\n")
	fullPrompt := context.BuildPrompt(prompt, previousAttempts, ciLogs)
	fmt.Fprintf(os.Stderr, "Built prompt (length: %d characters)\n", len(fullPrompt))

	// 9. Run pre-hooks
	if manifest != nil && len(manifest.Hooks.Pre) > 0 {
		fmt.Fprintf(os.Stderr, "Executing %d pre-hooks\n", len(manifest.Hooks.Pre))
		if err := hooks.RunPreHooks(manifest.Hooks.Pre, envCfg.WorkDir); err != nil {
			// Extract hook output if it's a HookError
			var hookErr *hooks.HookError
			logs := ""
			if errors.As(err, &hookErr) {
				logs = hookErr.Output
			}
			reportErr := reporterClient.ReportFailure(
				fmt.Sprintf("Pre-hook failed: %v", err),
				logs,
				envCfg.AgentType,
			)
			if reportErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
			}
			return fmt.Errorf("pre-hook failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Executed %d pre-hooks\n", len(manifest.Hooks.Pre))
	} else {
		fmt.Fprintf(os.Stderr, "No pre-hooks to execute\n")
	}

	// 10. Execute agent
	fmt.Fprintf(os.Stderr, "Agent execution started\n")
	executor := agent.NewExecutor(envCfg.AgentType)
	agentOutput, err := executor.Execute(envCfg.WorkDir, fullPrompt)
	if err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Agent execution failed: %v", err),
			agentOutput,
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("agent execution failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Agent execution completed (output length: %d)\n", len(agentOutput))

	// 11. Check for file changes
	fmt.Fprintf(os.Stderr, "Checking for file changes\n")
	hasChanges, err := git.HasChanges(envCfg.WorkDir)
	if err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Git diff check failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("file change check failed: %w", err)
	}
	if !hasChanges {
		reportErr := reporterClient.ReportFailure(
			"No file changes detected after agent execution",
			agentOutput,
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("no file changes detected after agent execution")
	}
	fmt.Fprintf(os.Stderr, "File changes detected\n")

	// 12. Run validations
	if manifest != nil && len(manifest.Validation) > 0 {
		fmt.Fprintf(os.Stderr, "Executing %d validations\n", len(manifest.Validation))
		if err := hooks.RunValidations(manifest.Validation, envCfg.WorkDir); err != nil {
			// Validation failures should NOT report to Operator API
			// The retry orchestrator will handle retries based on Pod exit code
			return fmt.Errorf("validation failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Executed %d validations\n", len(manifest.Validation))
	} else {
		fmt.Fprintf(os.Stderr, "No validations to execute\n")
	}

	// 11. Commit changes
	commitMsg := fmt.Sprintf("feat: implement issue #%d", issueID)
	fmt.Fprintf(os.Stderr, "Committing changes\n")
	commitSHA, err := git.CommitChanges(envCfg.WorkDir, commitMsg)
	if err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Git commit failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("git commit failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Committed changes (SHA: %s)\n", commitSHA)

	// 12. Push branch
	fmt.Fprintf(os.Stderr, "Pushing branch %s to remote\n", branchName)
	if err := git.PushBranch(envCfg.WorkDir, branchName, envCfg.GitHubToken); err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Git push failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("git push failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Pushed branch %s to remote\n", branchName)

	// 13. Save session to S3
	fmt.Fprintf(os.Stderr, "Saving session to S3 for AgentRun ID: %d\n", envCfg.AgentRunID)
	if err := storage.SaveSession(envCfg.AgentRunID, envCfg.AgentType); err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Session save failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("session save failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Saved session to S3 for AgentRun ID: %d\n", envCfg.AgentRunID)

	// 14. Create Pull Request
	fmt.Fprintf(os.Stderr, "Creating Pull Request\n")
	prNumber, err := git.CreatePR(envCfg.GitHubToken, repo, branchName, issueID)
	if err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("PR creation failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("PR creation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Created Pull Request #%d\n", prNumber)

	// 15. Run post-hooks
	if manifest != nil && len(manifest.Hooks.Post) > 0 {
		fmt.Fprintf(os.Stderr, "Executing %d post-hooks\n", len(manifest.Hooks.Post))
		if err := hooks.RunPostHooks(manifest.Hooks.Post, envCfg.WorkDir); err != nil {
			// Post-hook failures are logged as warnings and do not abort execution (PR already created)
			fmt.Fprintf(os.Stderr, "WARNING: Post-hook execution failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Executed %d post-hooks\n", len(manifest.Hooks.Post))
		}
	} else {
		fmt.Fprintf(os.Stderr, "No post-hooks to execute\n")
	}

	// 16. Report success to Operator API
	fmt.Fprintf(os.Stderr, "Reporting success to Operator API\n")
	if err := reporterClient.ReportSuccess(prNumber, branchName, commitSHA, envCfg.AgentType); err != nil {
		return fmt.Errorf("failed to report success: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Successfully reported to Operator API\n")

	return nil
}
