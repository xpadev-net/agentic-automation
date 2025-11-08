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
	"agent-runner/pkg/parser"
	"agent-runner/pkg/reporter"
	"agent-runner/pkg/storage"
	"agent-runner/pkg/version"
)

func main() {
	var (
		issueID          int
		repo             string
		prompt           string
		previousAttempts string
		ciLogs           string
		executionMode    string
	)

	rootCmd := &cobra.Command{
		Use:   "agent-runner",
		Short: "Execute AI agent for GitHub Issue automation",
		Long: `agent-runner executes AI agents (claude-code or cursor-agent) in Kubernetes Pods.
It processes GitHub Issues, runs lint/typecheck, commits changes, and reports results to the Operator API.`,
		Version: "0.1.0",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Build information to stderr (keeps stdout clean for any machine parsing)
			fmt.Fprintf(os.Stderr, "Build: commit=%s builtAt=%s\n", version.Commit, version.BuiltAt)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate command-line arguments
			if err := validateArgs(issueID, repo, prompt); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			return Run(issueID, repo, prompt, previousAttempts, ciLogs, executionMode)
		},
	}

	rootCmd.Flags().IntVar(&issueID, "issue-id", 0, "GitHub Issue ID (must be positive integer)")
	rootCmd.Flags().StringVar(&repo, "repo", "", "Repository in format owner/name (e.g., octocat/Hello-World)")
	rootCmd.Flags().StringVar(&prompt, "prompt", "", "Issue context prompt describing the task")
	rootCmd.Flags().StringVar(&previousAttempts, "previous-attempts", "", "Previous retry attempts in JSON format (optional)")
	rootCmd.Flags().StringVar(&ciLogs, "ci-logs", "", "CI failure logs for retry context (optional)")
	rootCmd.Flags().StringVar(&executionMode, "execution-mode", "normal", "Execution mode: normal, plan_creation, plan_execution")

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

func normalizeExecutionMode(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	switch mode {
	case "", "normal":
		return "normal"
	case "plan_creation":
		return "plan_creation"
	case "plan_execution":
		return "plan_execution"
	default:
		return ""
	}
}

// buildPRBodyWithCollapsibleSections builds PR body with collapsible sections for Issue body and Plan content.
// It adds <details> sections for Issue body and Plan content (if not empty) before the base PR body.
func buildPRBodyWithCollapsibleSections(baseBody string, issueBody string, planContent string, issueID int) string {
	var sections []string

	// Add Issue body section if not empty
	if strings.TrimSpace(issueBody) != "" {
		issueSection := fmt.Sprintf("<details>\n<summary>Issue #%d</summary>\n\n```xml\n%s\n```\n\n</details>", issueID, issueBody)
		sections = append(sections, issueSection)
	}

	// Add Plan content section if not empty
	if strings.TrimSpace(planContent) != "" {
		planSection := fmt.Sprintf("<details>\n<summary>プラン</summary>\n\n%s\n\n</details>", planContent)
		sections = append(sections, planSection)
	}

	// If no sections to add, return base body as is
	if len(sections) == 0 {
		return baseBody
	}

	// Combine sections with base body
	result := strings.Join(sections, "\n\n")
	if strings.TrimSpace(baseBody) != "" {
		result = result + "\n\n" + baseBody
	}

	return result
}

func readContentFromEnvOrFile(envKey, fileKey string) (string, error) {
	if value := os.Getenv(envKey); strings.TrimSpace(value) != "" {
		return value, nil
	}
	if filePath := os.Getenv(fileKey); strings.TrimSpace(filePath) != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
		}
		return string(data), nil
	}
	return "", nil
}

func handlePlanCreationResult(client *reporter.Client, agentType, output string) error {
	planContent, rejected, reason := parser.ParsePlanResult(output)
	if rejected {
		if err := client.ReportPlanRejection(reason, agentType, output); err != nil {
			return fmt.Errorf("failed to report plan rejection: %w", err)
		}
		return fmt.Errorf("plan was rejected: %s", reason)
	}
	if err := client.ReportPlanCreation(planContent, agentType, output); err != nil {
		return fmt.Errorf("failed to report plan creation: %w", err)
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
	} else if cfg.AgentType != "claude-code" && cfg.AgentType != "cursor-agent" {
		return nil, fmt.Errorf("AGENT_TYPE must be 'claude-code' or 'cursor-agent', got: %q", cfg.AgentType)
	}

	// GitHub authentication is handled via GitHub App installation token (on-demand). No PAT support.

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

	// Cursor agent specific environment variables
	cfg.CursorModel = os.Getenv("CURSOR_MODEL")
	if cfg.CursorModel == "" {
		cfg.CursorModel = "auto" // Default value
	}

	cursorAllowWriteStr := os.Getenv("CURSOR_ALLOW_WRITE")
	if cursorAllowWriteStr == "" {
		cfg.CursorAllowWrite = true // Default value
	} else {
		cfg.CursorAllowWrite = strings.ToLower(cursorAllowWriteStr) == "true"
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
	RetryCount       int
	WorkDir          string
	CursorModel      string
	CursorAllowWrite bool
}

// Run executes the agent-runner workflow.
// This function is exported for testing purposes.

func Run(issueID int, repo, prompt, previousAttempts, ciLogs, executionMode string) error {
	executionMode = normalizeExecutionMode(executionMode)
	if executionMode == "" {
		return fmt.Errorf("invalid execution mode")
	}
	// 1. Validate and load environment variables
	envCfg, err := validateEnv()
	if err != nil {
		return fmt.Errorf("environment validation failed: %w", err)
	}

	if executionMode == "plan_creation" {
		// プラン作成モードではwriteを不可に強制
		envCfg.CursorAllowWrite = false
	}

	reviewContent := ""
	planContent := ""
	if executionMode == "plan_creation" {
		reviewContent, err = readContentFromEnvOrFile("REVIEW_FEEDBACK_CONTENT", "REVIEW_FEEDBACK_CONTENT_FILE")
		if err != nil {
			return fmt.Errorf("failed to load review feedback content: %w", err)
		}
		if reviewContent == "" {
			return fmt.Errorf("REVIEW_FEEDBACK_CONTENT is required for plan_creation mode")
		}
	} else if executionMode == "plan_execution" {
		fmt.Fprintf(os.Stderr, "Loading plan content for plan_execution mode\n")
		planContentEnv := os.Getenv("PLAN_CONTENT")
		planContentFile := os.Getenv("PLAN_CONTENT_FILE")
		fmt.Fprintf(os.Stderr, "  PLAN_CONTENT env var: %s (length: %d)\n",
			func() string {
				if planContentEnv == "" {
					return "(not set)"
				}
				return "(set)"
			}(), len(planContentEnv))
		fmt.Fprintf(os.Stderr, "  PLAN_CONTENT_FILE env var: %s\n",
			func() string {
				if planContentFile == "" {
					return "(not set)"
				}
				return planContentFile
			}())
		planContent, err = readContentFromEnvOrFile("PLAN_CONTENT", "PLAN_CONTENT_FILE")
		if err != nil {
			return fmt.Errorf("failed to load plan content: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Loaded plan content length: %d characters\n", len(planContent))
		if planContent == "" {
			return fmt.Errorf("PLAN_CONTENT is required for plan_execution mode (env var length: %d, file path: %q)",
				len(planContentEnv), planContentFile)
		}
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
	if err := git.CloneRepo("", repo, envCfg.WorkDir); err != nil {
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

	// 4.5. Get default branch (base branch) for PR generation and creation
	fmt.Fprintf(os.Stderr, "Getting default branch for repository %s\n", repo)
	baseBranch, err := git.GetDefaultBranch(repo)
	if err != nil {
		// Log warning but continue with default (master)
		fmt.Fprintf(os.Stderr, "WARNING: Failed to get default branch: %v (using 'master' as fallback)\n", err)
		baseBranch = "master"
	} else {
		fmt.Fprintf(os.Stderr, "Default branch: %s\n", baseBranch)
	}

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

	// 7. Create feature branch or checkout existing branch
	branchName := fmt.Sprintf("feature/issue-%d", issueID)
	existingBranchName := os.Getenv("EXISTING_BRANCH_NAME")
	if existingBranchName != "" {
		fmt.Fprintf(os.Stderr, "Checking out existing branch: %s\n", existingBranchName)
		branchName = existingBranchName
	} else {
		fmt.Fprintf(os.Stderr, "Creating/checking out branch: %s\n", branchName)
	}
	if err := git.CreateBranch(envCfg.WorkDir, branchName, envCfg.RetryCount, existingBranchName); err != nil {
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
	fmt.Fprintf(os.Stderr, "Building prompt (mode: %s)\n", executionMode)
	var fullPrompt string
	switch executionMode {
	case "plan_creation":
		fullPrompt = context.BuildPlanCreationPrompt(reviewContent)
	case "plan_execution":
		fmt.Fprintf(os.Stderr, "  Plan content length for prompt: %d characters\n", len(planContent))
		fullPrompt = context.BuildPlanExecutionPrompt(prompt, planContent)
	default:
		fullPrompt = context.BuildPrompt(prompt, previousAttempts, ciLogs)
	}
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
	var agentOutput string
	if envCfg.AgentType == "cursor-agent" {
		agentOutput, err = executor.ExecuteWithOptions(envCfg.WorkDir, fullPrompt, envCfg.CursorModel, envCfg.CursorAllowWrite)
	} else {
		agentOutput, err = executor.Execute(envCfg.WorkDir, fullPrompt)
	}
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

	// 11. Plan creationモードではここで終了処理に移行する
	if executionMode == "plan_creation" {
		if err := handlePlanCreationResult(reporterClient, envCfg.AgentType, agentOutput); err != nil {
			return err
		}
		return nil
	}

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
	// 10.5 Ensure git commit identity (user.name/email)
	if err := git.EnsureCommitIdentity(envCfg.WorkDir, repo, issueID); err != nil {
		reportErr := reporterClient.ReportFailure(
			fmt.Sprintf("Git identity setup failed: %v", err),
			"",
			envCfg.AgentType,
		)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
		}
		return fmt.Errorf("git identity setup failed: %w", err)
	}
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
	if err := git.PushBranch(envCfg.WorkDir, branchName, ""); err != nil {
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

	// 12.5. Post @codex review comment if PR already exists (for retry cases)
	existingPRNumber, err := git.FindPRByBranch("", repo, branchName)
	if err != nil {
		// Log warning but continue (non-blocking)
		fmt.Fprintf(os.Stderr, "WARNING: Failed to find existing PR for branch %s: %v (continuing)\n", branchName, err)
	} else if existingPRNumber > 0 {
		// PR exists, post comment
		if err := git.PostPRComment("", repo, existingPRNumber, "@codex review"); err != nil {
			// Log warning but continue (non-blocking)
			fmt.Fprintf(os.Stderr, "WARNING: Failed to post @codex review comment to PR #%d: %v (continuing)\n", existingPRNumber, err)
		} else {
			fmt.Fprintf(os.Stderr, "Posted @codex review comment to PR #%d\n", existingPRNumber)
		}
	}

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

	// 14. Generate PR title and body (if cursor-agent is used)
	var prTitle, prBody string
	if envCfg.AgentType == "cursor-agent" {
		fmt.Fprintf(os.Stderr, "Generating PR title and body\n")
		title, body, err := git.GeneratePRTitleAndBody(envCfg.WorkDir, issueID, prompt, commitMsg, envCfg.AgentType, envCfg.CursorModel, baseBranch)
		if err != nil {
			// Log warning but continue with default title/body
			fmt.Fprintf(os.Stderr, "WARNING: Failed to generate PR title and body: %v (using default format)\n", err)
			prTitle = ""
			prBody = ""
		} else {
			prTitle = title
			prBody = body
			fmt.Fprintf(os.Stderr, "Generated PR title and body\n")
		}
	} else {
		// For non-cursor-agent, use default format
		prTitle = ""
		prBody = ""
	}

	// Add collapsible sections for Issue body and Plan content
	prBody = buildPRBodyWithCollapsibleSections(prBody, prompt, planContent, issueID)

	// Ensure issue-closing keyword is present in PR body
	if prBody != "" {
		closingKeyword := fmt.Sprintf("close #%d", issueID)
		// Check if any closing keyword pattern exists (case-insensitive)
		bodyLower := strings.ToLower(prBody)
		hasClosingKeyword := strings.Contains(bodyLower, strings.ToLower(closingKeyword)) ||
			strings.Contains(bodyLower, fmt.Sprintf("closes #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("closed #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("fix #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("fixes #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("fixed #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("resolve #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("resolves #%d", issueID)) ||
			strings.Contains(bodyLower, fmt.Sprintf("resolved #%d", issueID))
		if !hasClosingKeyword {
			prBody = prBody + "\n\n" + closingKeyword
			fmt.Fprintf(os.Stderr, "Added issue-closing keyword to PR body\n")
		}
	}

	// 15. Create Pull Request
	fmt.Fprintf(os.Stderr, "Creating Pull Request\n")
	prNumber, err := git.CreatePR("", repo, branchName, issueID, prTitle, prBody, baseBranch)
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

	// 15.5. Post @codex review comment to PR
	if err := git.PostPRComment("", repo, prNumber, "@codex review"); err != nil {
		// Log warning but continue (non-blocking)
		fmt.Fprintf(os.Stderr, "WARNING: Failed to post @codex review comment to PR #%d: %v (continuing)\n", prNumber, err)
	} else {
		fmt.Fprintf(os.Stderr, "Posted @codex review comment to PR #%d\n", prNumber)
	}

	// 16. Run post-hooks
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

	// 17. Report success to Operator API
	fmt.Fprintf(os.Stderr, "Reporting success to Operator API\n")
	if err := reporterClient.ReportSuccess(prNumber, branchName, commitSHA, envCfg.AgentType); err != nil {
		return fmt.Errorf("failed to report success: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Successfully reported to Operator API\n")

	return nil
}
