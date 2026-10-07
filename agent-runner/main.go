package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"agent-runner/pkg/agent"
	"agent-runner/pkg/config"
	"agent-runner/pkg/git"
	"agent-runner/pkg/hooks"
	"agent-runner/pkg/logship"
	"agent-runner/pkg/parser"
	"agent-runner/pkg/prompts"
	"agent-runner/pkg/reporter"
	"agent-runner/pkg/storage"
	"agent-runner/pkg/version"
)

// ErrGenerationFailure indicates that plan generation failed (reason matches example text)
var ErrGenerationFailure = errors.New("generation failure detected: reason matches example text")

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
		Long: `agent-runner executes AI agents (claude-code, cursor-agent, or codex) in Kubernetes Pods.
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
		// 生成失敗判定: reasonが"[却下理由]"と完全一致する場合
		if reason == "[却下理由]" {
			return fmt.Errorf("%w: %s", ErrGenerationFailure, reason)
		}
		// 通常の却下処理
		if err := client.ReportPlanRejection(reason, agentType, output); err != nil {
			return fmt.Errorf("failed to report plan rejection: %w", err)
		}
		return fmt.Errorf("plan was rejected: %s", reason)
	}
	// プラン作成成功
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
	} else if cfg.AgentType != "claude-code" && cfg.AgentType != "cursor-agent" && cfg.AgentType != "codex" {
		return nil, fmt.Errorf("AGENT_TYPE must be 'claude-code', 'cursor-agent', or 'codex', got: %q", cfg.AgentType)
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

	// Codex specific environment variables
	cfg.CodexModel = os.Getenv("CODEX_MODEL")
	if cfg.CodexModel == "" {
		cfg.CodexModel = "gpt-5.6-luna"
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
	CodexModel       string
}

// isOptionAgentType reports whether the agent supports model/allow-write options
// (cursor-agent and codex).
func isOptionAgentType(agentType string) bool {
	return agentType == "cursor-agent" || agentType == "codex"
}

// agentModel returns the configured model for option-capable agents.
func (c *envConfig) agentModel() string {
	if c.AgentType == "codex" {
		return c.CodexModel
	}
	return c.CursorModel
}

// commitChangesIfNeeded commits changes if there are any file changes.
// Returns the commit SHA, commit message if a commit was made, empty strings if no changes, and error if commit failed.
// If skipHooks is true, the --no-verify flag is added to skip pre-commit hooks.
// If commitMsgPrefix is empty and agentType supports options (cursor-agent or codex), AI-generated commit message will be used.
func commitChangesIfNeeded(workDir, repo string, issueID int, commitMsgPrefix string, skipHooks bool, agentType, model, issuePrompt string) (string, string, error) {
	// Check for file changes
	hasChanges, err := git.HasChanges(workDir)
	if err != nil {
		return "", "", fmt.Errorf("file change check failed: %w", err)
	}
	if !hasChanges {
		fmt.Fprintf(os.Stderr, "No file changes to commit\n")
		return "", "", nil
	}

	// Ensure git commit identity
	if err := git.EnsureCommitIdentity(workDir, repo, issueID); err != nil {
		return "", "", fmt.Errorf("git identity setup failed: %w", err)
	}

	// Build commit message
	commitMsg := commitMsgPrefix
	if commitMsg == "" {
		// Try AI generation if an option-capable agent (cursor-agent or codex) is used
		if isOptionAgentType(agentType) {
			// Stage changes before generating commit message (required for GetStagedDiff)
			addCmd := exec.Command("git", "add", ".")
			addCmd.Dir = workDir
			if err := addCmd.Run(); err != nil {
				return "", "", fmt.Errorf("git add failed: %w", err)
			}

			// Generate commit message using AI
			generatedMsg, err := git.GenerateCommitMessage(workDir, repo, issueID, issuePrompt, agentType, model)
			if err != nil {
				// Log warning and fallback to default message
				fmt.Fprintf(os.Stderr, "WARNING: Failed to generate commit message with AI: %v (using default message)\n", err)
				commitMsg = fmt.Sprintf("feat: implement issue #%d", issueID)
			} else {
				// Issue番号が含まれているか検証
				issueRef := fmt.Sprintf("#%d", issueID)
				if !strings.Contains(generatedMsg, issueRef) {
					// Issue番号が含まれていない場合は付加
					// メッセージの先頭または末尾に付加（既存の形式に合わせる）
					if strings.HasPrefix(generatedMsg, "feat:") || strings.HasPrefix(generatedMsg, "fix:") {
						// 既存の形式: "feat: implement issue #%d" に合わせる
						commitMsg = fmt.Sprintf("%s (issue #%d)", generatedMsg, issueID)
					} else {
						// その他の場合は末尾に付加
						commitMsg = fmt.Sprintf("%s (#%d)", generatedMsg, issueID)
					}
					fmt.Fprintf(os.Stderr, "WARNING: Generated commit message did not include issue reference, appended: %s\n", commitMsg)
				} else {
					commitMsg = generatedMsg
				}
				fmt.Fprintf(os.Stderr, "Generated commit message with AI: %s\n", commitMsg)
			}
		} else {
			// Use default message for agents without AI generation
			commitMsg = fmt.Sprintf("feat: implement issue #%d", issueID)
		}
	}

	// Commit changes
	fmt.Fprintf(os.Stderr, "Committing changes: %s\n", commitMsg)
	commitSHA, err := git.CommitChanges(workDir, commitMsg, skipHooks)
	if err != nil {
		return "", "", fmt.Errorf("git commit failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Committed changes (SHA: %s)\n", commitSHA)
	return commitSHA, commitMsg, nil
}

// syncBranchWithBase synchronizes the working branch with the base branch.
// It checks if the branch is behind, merges if needed, and resolves conflicts with AI if any.
// Note: This function does NOT push changes. The caller is responsible for pushing after validations succeed.
// Returns (changed, error) where changed indicates if any merge or conflict resolution was performed.
func syncBranchWithBase(workDir, repo, baseBranch, branchName string, issueID int, agentType string) (bool, error) {
	fmt.Fprintf(os.Stderr, "Syncing branch %s with base branch %s\n", branchName, baseBranch)

	// Check if branch is behind base branch
	baseRef := fmt.Sprintf("origin/%s", baseBranch)
	headRef := branchName
	behind, err := git.IsBehind(workDir, baseRef, headRef)
	if err != nil {
		return false, fmt.Errorf("failed to check if branch is behind: %w", err)
	}

	if !behind {
		fmt.Fprintf(os.Stderr, "Branch %s is up-to-date with %s\n", branchName, baseBranch)
		return false, nil
	}

	fmt.Fprintf(os.Stderr, "Branch %s is behind %s, merging...\n", branchName, baseBranch)

	// Merge base branch into working branch
	if err := git.MergeBranch(workDir, baseBranch); err != nil {
		return false, fmt.Errorf("failed to merge %s into %s: %w", baseBranch, branchName, err)
	}

	// Check for conflicts
	conflicts, err := git.ListConflicts(workDir)
	if err != nil {
		return false, fmt.Errorf("failed to list conflicts: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "Merge conflicts detected in %d files, resolving with AI...\n", len(conflicts))
		// Resolve conflicts with AI (cursor-agent or codex)
		if !isOptionAgentType(agentType) {
			// Abort merge before returning error to clean up workspace
			fmt.Fprintf(os.Stderr, "Conflict resolution requires cursor-agent or codex, but agent type is %s. Aborting merge...\n", agentType)
			if abortErr := git.AbortMerge(workDir); abortErr != nil {
				return false, fmt.Errorf("conflict resolution requires cursor-agent or codex, but agent type is %s; failed to abort merge: %w", agentType, abortErr)
			}
			return false, fmt.Errorf("conflict resolution requires cursor-agent or codex, but agent type is %s", agentType)
		}
		if err := git.ResolveConflictsWithAI(workDir, repo, issueID, agentType); err != nil {
			// If conflict resolution fails, abort merge to clean up workspace
			fmt.Fprintf(os.Stderr, "Failed to resolve conflicts with AI. Aborting merge...\n")
			if abortErr := git.AbortMerge(workDir); abortErr != nil {
				return false, fmt.Errorf("failed to resolve conflicts with AI: %w; failed to abort merge: %w", err, abortErr)
			}
			return false, fmt.Errorf("failed to resolve conflicts with AI: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Conflicts resolved successfully\n")
	} else {
		fmt.Fprintf(os.Stderr, "Merge completed without conflicts\n")
	}

	// Return true to indicate that merge or conflict resolution was performed
	// Note: Push is handled by the caller after validations succeed
	return true, nil
}

// Run executes the agent-runner workflow.
// This function is exported for testing purposes.

func Run(issueID int, repo, prompt, previousAttempts, ciLogs, executionMode string) (retErr error) {
	executionMode = normalizeExecutionMode(executionMode)
	if executionMode == "" {
		return fmt.Errorf("invalid execution mode")
	}
	// 1. Validate and load environment variables
	envCfg, err := validateEnv()
	if err != nil {
		return fmt.Errorf("environment validation failed: %w", err)
	}

	// Tee stderr to the Operator log ingestion endpoint (best-effort).
	// From here on, everything written to os.Stderr is also shipped.
	shipper, err := logship.Attach(envCfg.OperatorAPIURL, envCfg.OperatorAPIToken, envCfg.AgentRunID, envCfg.RetryCount)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: log shipping unavailable: %v\n", err)
	}
	defer shipper.Close()
	// Print the returned error while the shipper is still attached so the
	// failure reason reaches the shipped log — cobra only prints it after
	// Run returns, which is after the shipper has closed. (Registered after
	// the Close defer so it runs first.)
	defer func() {
		if retErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", retErr)
		}
	}()

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
	if err := git.CreateBranch(envCfg.WorkDir, branchName, envCfg.RetryCount, existingBranchName, baseBranch); err != nil {
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
		fullPrompt = prompts.BuildPlanCreationPrompt(reviewContent)
	case "plan_execution":
		fmt.Fprintf(os.Stderr, "  Plan content length for prompt: %d characters\n", len(planContent))
		fullPrompt = prompts.BuildPlanExecutionPrompt(prompt, planContent)
	default:
		fullPrompt = prompts.BuildTaskPrompt(prompt, previousAttempts, ciLogs)
	}
	fmt.Fprintf(os.Stderr, "Built prompt (length: %d characters)\n", len(fullPrompt))

	// 9. Run pre-hooks
	if executionMode == "plan_creation" {
		fmt.Fprintf(os.Stderr, "Skipping pre-hooks in plan_creation mode\n")
	} else if manifest != nil && len(manifest.Hooks.Pre) > 0 {
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

	// 10. Execute agent with validation retry loop
	const maxValidationRetries = 10
	const maxGenerationRetries = 3 // プラン作成モード用のリトライ上限
	executor := agent.NewExecutor(envCfg.AgentType)
	var agentOutput string
	var commitSHA string
	var lastCommitMsg string
	var validationErr error

	// プラン作成モード用のリトライループ
	if executionMode == "plan_creation" {
		for generationRetry := 0; generationRetry <= maxGenerationRetries; generationRetry++ {
			if generationRetry > 0 {
				fmt.Fprintf(os.Stderr, "Generation retry attempt #%d/%d\n", generationRetry, maxGenerationRetries)
			} else {
				fmt.Fprintf(os.Stderr, "Agent execution started (initial attempt)\n")
			}

			// Record HEAD before agent execution for rollback on failure
			preExecutionHEAD, headErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
			if headErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: Failed to get HEAD before agent execution: %v (rollback may not work)\n", headErr)
			}

			// Execute agent
			if isOptionAgentType(envCfg.AgentType) {
				agentOutput, err = executor.ExecuteWithOptions(envCfg.WorkDir, fullPrompt, envCfg.agentModel(), envCfg.CursorAllowWrite)
			} else {
				agentOutput, err = executor.Execute(envCfg.WorkDir, fullPrompt)
			}
			if err != nil {
				// Rollback any commits created by the agent before returning error
				if preExecutionHEAD != "" {
					currentHEAD, getErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
					if getErr == nil && currentHEAD != preExecutionHEAD {
						// HEAD has advanced, meaning commits were created
						if resetErr := git.ResetToCommit(envCfg.WorkDir, preExecutionHEAD); resetErr != nil {
							fmt.Fprintf(os.Stderr, "WARNING: Failed to rollback commits after agent execution failure: %v\n", resetErr)
						} else {
							fmt.Fprintf(os.Stderr, "Rolled back commits after agent execution failure (from %s to %s)\n", currentHEAD, preExecutionHEAD)
						}
					}
				}

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

			// Check if agent created a commit and rollback if needed
			if preExecutionHEAD != "" {
				currentHEAD, getErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
				if getErr == nil && currentHEAD != preExecutionHEAD {
					fmt.Fprintf(os.Stderr, "Detected commit created by agent, rolling back to %s\n", preExecutionHEAD)
					if err := git.ResetToCommit(envCfg.WorkDir, preExecutionHEAD); err != nil {
						return fmt.Errorf("failed to reset to initial HEAD: %w", err)
					}
					fmt.Fprintf(os.Stderr, "Rolled back to initial HEAD (changes preserved in staging area)\n")
				}
			}

			// Handle plan creation result
			err = handlePlanCreationResult(reporterClient, envCfg.AgentType, agentOutput)
			if err != nil {
				// 生成失敗エラーの場合、リトライ可能かチェック
				if errors.Is(err, ErrGenerationFailure) {
					if generationRetry >= maxGenerationRetries {
						// リトライ上限に達した場合、通常の却下として扱う
						fmt.Fprintf(os.Stderr, "Generation retry limit reached, reporting as plan rejection\n")
						if reportErr := reporterClient.ReportPlanRejection(
							"プラン生成に失敗しました（リトライ上限に達しました）",
							envCfg.AgentType,
							agentOutput,
						); reportErr != nil {
							return fmt.Errorf("failed to report plan rejection after retry limit: %w", reportErr)
						}
						return fmt.Errorf("plan generation failed after %d retries", maxGenerationRetries)
					}
					// リトライ可能な場合、ループを継続
					fmt.Fprintf(os.Stderr, "Generation failure detected, retrying...\n")
					continue
				}
				// 生成失敗以外のエラー（通常の却下など）は即座に返す
				return err
			}
			// プラン作成成功
			return nil
		}
		// このコードには到達しないはずだが、念のため
		return fmt.Errorf("unexpected end of generation retry loop")
	}

	// 既存のvalidation retry loop（plan_creation以外のモード用）
	for retryCount := 0; retryCount <= maxValidationRetries; retryCount++ {
		if retryCount > 0 {
			fmt.Fprintf(os.Stderr, "Validation retry attempt #%d/%d\n", retryCount, maxValidationRetries)
			// Build prompt with validation error for retry
			validationErrMsg := validationErr.Error()
			switch executionMode {
			case "plan_execution":
				fullPrompt = prompts.BuildPlanExecutionPrompt(prompt, planContent, validationErrMsg)
			default:
				fullPrompt = prompts.BuildTaskPrompt(prompt, previousAttempts, ciLogs, validationErrMsg)
			}
			fmt.Fprintf(os.Stderr, "Built retry prompt with validation error (length: %d characters)\n", len(fullPrompt))
		} else {
			fmt.Fprintf(os.Stderr, "Agent execution started (initial attempt)\n")
		}

		// Record HEAD before agent execution for rollback on failure
		preExecutionHEAD, headErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
		if headErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to get HEAD before agent execution: %v (rollback may not work)\n", headErr)
		}

		// Execute agent
		if isOptionAgentType(envCfg.AgentType) {
			agentOutput, err = executor.ExecuteWithOptions(envCfg.WorkDir, fullPrompt, envCfg.agentModel(), envCfg.CursorAllowWrite)
		} else {
			agentOutput, err = executor.Execute(envCfg.WorkDir, fullPrompt)
		}
		if err != nil {
			// Rollback any commits created by the agent before returning error
			if preExecutionHEAD != "" {
				currentHEAD, getErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
				if getErr == nil && currentHEAD != preExecutionHEAD {
					// HEAD has advanced, meaning commits were created
					if resetErr := git.ResetToCommit(envCfg.WorkDir, preExecutionHEAD); resetErr != nil {
						fmt.Fprintf(os.Stderr, "WARNING: Failed to rollback commits after agent execution failure: %v\n", resetErr)
					} else {
						fmt.Fprintf(os.Stderr, "Rolled back commits after agent execution failure (from %s to %s)\n", currentHEAD, preExecutionHEAD)
					}
				}
			}

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

		// Check if agent created a commit and rollback if needed
		if preExecutionHEAD != "" {
			currentHEAD, getErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
			if getErr == nil && currentHEAD != preExecutionHEAD {
				fmt.Fprintf(os.Stderr, "Detected commit created by agent, rolling back to %s\n", preExecutionHEAD)
				if err := git.ResetToCommit(envCfg.WorkDir, preExecutionHEAD); err != nil {
					return fmt.Errorf("failed to reset to initial HEAD: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Rolled back to initial HEAD (changes preserved in staging area)\n")
			}
		}

		// Check for file changes
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
			if retryCount == 0 {
				// Only report failure on initial attempt
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
			// On retry, if no changes, continue to validation check
			fmt.Fprintf(os.Stderr, "No file changes detected after retry, checking validation\n")
		} else {
			fmt.Fprintf(os.Stderr, "File changes detected\n")
		}

		// Run validations
		if executionMode == "plan_creation" {
			fmt.Fprintf(os.Stderr, "Skipping validations in plan_creation mode\n")
		} else if manifest != nil && len(manifest.Validation) > 0 {
			fmt.Fprintf(os.Stderr, "Executing %d validations\n", len(manifest.Validation))
			validationErr = hooks.RunValidations(manifest.Validation, envCfg.WorkDir)
			if validationErr != nil {
				fmt.Fprintf(os.Stderr, "Validation failed: %v\n", validationErr)
				// Commit changes as checkpoint before retry
				checkpointCommitMsg := fmt.Sprintf("feat: implement issue #%d (validation retry checkpoint #%d)", issueID, retryCount)
				checkpointSHA, _, commitErr := commitChangesIfNeeded(envCfg.WorkDir, repo, issueID, checkpointCommitMsg, true, envCfg.AgentType, envCfg.agentModel(), prompt)
				if commitErr != nil {
					reportErr := reporterClient.ReportFailure(
						fmt.Sprintf("Failed to commit checkpoint: %v", commitErr),
						"",
						envCfg.AgentType,
					)
					if reportErr != nil {
						fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
					}
					return fmt.Errorf("failed to commit checkpoint: %w", commitErr)
				}
				if checkpointSHA != "" {
					fmt.Fprintf(os.Stderr, "Committed checkpoint (SHA: %s) before validation retry\n", checkpointSHA)
				}

				// Check if we've reached max retries
				if retryCount >= maxValidationRetries {
					return fmt.Errorf("validation failed after %d retries: %w", maxValidationRetries, validationErr)
				}
				// Continue to next retry
				continue
			}
			fmt.Fprintf(os.Stderr, "Executed %d validations successfully\n", len(manifest.Validation))
		} else {
			fmt.Fprintf(os.Stderr, "No validations to execute\n")
		}

		// Validation passed, commit changes
		commitMsgPrefix := ""
		if retryCount > 0 {
			commitMsgPrefix = fmt.Sprintf("feat: implement issue #%d (validation retry #%d)", issueID, retryCount)
		}
		commitSHA, lastCommitMsg, err = commitChangesIfNeeded(envCfg.WorkDir, repo, issueID, commitMsgPrefix, false, envCfg.AgentType, envCfg.agentModel(), prompt)
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
		if commitSHA != "" {
			fmt.Fprintf(os.Stderr, "Committed changes after validation success (SHA: %s)\n", commitSHA)
		}

		// Validation passed, break out of retry loop
		break
	}

	// 11.5. Post-Commit Sync: コミット後にデフォルトブランチとの同期を確認
	var changed bool
	if executionMode == "plan_creation" {
		fmt.Fprintf(os.Stderr, "Skipping post-commit sync in plan_creation mode (no write operations)\n")
		changed = false
	} else {
		fmt.Fprintf(os.Stderr, "Post-Commit Sync: Checking if branch is behind base branch\n")
		var err error
		changed, err = syncBranchWithBase(envCfg.WorkDir, repo, baseBranch, branchName, issueID, envCfg.AgentType)
		if err != nil {
			reportErr := reporterClient.ReportFailure(
				fmt.Sprintf("Post-Commit Sync failed: %v", err),
				"",
				envCfg.AgentType,
			)
			if reportErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
			}
			return fmt.Errorf("post-commit sync failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Post-Commit Sync completed successfully\n")
	}

	// 11.6. Re-run validations if merge or conflict resolution was performed
	// This ensures that any changes introduced by the merge or AI conflict resolution
	// are validated before pushing to prevent breaking changes from being pushed.
	if changed {
		if executionMode == "plan_creation" {
			fmt.Fprintf(os.Stderr, "Skipping post-commit sync validations in plan_creation mode\n")
		} else {
			fmt.Fprintf(os.Stderr, "Merge or conflict resolution was performed, re-running validations\n")
			if manifest != nil && len(manifest.Validation) > 0 {
				// Retry loop for post-commit sync validation
				var postSyncValidationErr error
				for postSyncRetryCount := 0; postSyncRetryCount <= maxValidationRetries; postSyncRetryCount++ {
					if postSyncRetryCount > 0 {
						fmt.Fprintf(os.Stderr, "Post-commit sync validation retry attempt #%d/%d\n", postSyncRetryCount, maxValidationRetries)
						// Build prompt with validation error for retry
						validationErrMsg := postSyncValidationErr.Error()
						switch executionMode {
						case "plan_creation":
							// Plan creation mode doesn't support validation retry
							return fmt.Errorf("validation failed after post-commit sync in plan_creation mode: %w", postSyncValidationErr)
						case "plan_execution":
							fullPrompt = prompts.BuildPlanExecutionPrompt(prompt, planContent, validationErrMsg)
						default:
							fullPrompt = prompts.BuildTaskPrompt(prompt, previousAttempts, ciLogs, validationErrMsg)
						}
						fmt.Fprintf(os.Stderr, "Built retry prompt with validation error (length: %d characters)\n", len(fullPrompt))

						// Record HEAD before agent execution for rollback on failure
						preExecutionHEAD, headErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
						if headErr != nil {
							fmt.Fprintf(os.Stderr, "WARNING: Failed to get HEAD before agent execution: %v (rollback may not work)\n", headErr)
						}

						// Execute agent again
						if isOptionAgentType(envCfg.AgentType) {
							agentOutput, err = executor.ExecuteWithOptions(envCfg.WorkDir, fullPrompt, envCfg.agentModel(), envCfg.CursorAllowWrite)
						} else {
							agentOutput, err = executor.Execute(envCfg.WorkDir, fullPrompt)
						}
						if err != nil {
							// Rollback any commits created by the agent before returning error
							if preExecutionHEAD != "" {
								currentHEAD, getErr := git.GetCurrentCommitSHA(envCfg.WorkDir)
								if getErr == nil && currentHEAD != preExecutionHEAD {
									// HEAD has advanced, meaning commits were created
									if resetErr := git.ResetToCommit(envCfg.WorkDir, preExecutionHEAD); resetErr != nil {
										fmt.Fprintf(os.Stderr, "WARNING: Failed to rollback commits after agent execution failure: %v\n", resetErr)
									} else {
										fmt.Fprintf(os.Stderr, "Rolled back commits after agent execution failure (from %s to %s)\n", currentHEAD, preExecutionHEAD)
									}
								}
							}

							reportErr := reporterClient.ReportFailure(
								fmt.Sprintf("Agent execution failed during post-commit sync retry: %v", err),
								agentOutput,
								envCfg.AgentType,
							)
							if reportErr != nil {
								fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
							}
							return fmt.Errorf("agent execution failed during post-commit sync retry: %w", err)
						}
						fmt.Fprintf(os.Stderr, "Agent execution completed during post-commit sync retry (output length: %d)\n", len(agentOutput))

						// Commit changes after retry
						retryCommitMsg := fmt.Sprintf("feat: implement issue #%d (post-commit sync validation retry #%d)", issueID, postSyncRetryCount)
						retryCommitSHA, retryCommitMsgValue, commitErr := commitChangesIfNeeded(envCfg.WorkDir, repo, issueID, retryCommitMsg, false, envCfg.AgentType, envCfg.agentModel(), prompt)
						if commitErr != nil {
							reportErr := reporterClient.ReportFailure(
								fmt.Sprintf("Failed to commit after post-commit sync retry: %v", commitErr),
								"",
								envCfg.AgentType,
							)
							if reportErr != nil {
								fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
							}
							return fmt.Errorf("failed to commit after post-commit sync retry: %w", commitErr)
						}
						if retryCommitSHA != "" {
							fmt.Fprintf(os.Stderr, "Committed changes after post-commit sync retry (SHA: %s)\n", retryCommitSHA)
							commitSHA = retryCommitSHA
							lastCommitMsg = retryCommitMsgValue
						}
					}

					fmt.Fprintf(os.Stderr, "Executing %d validations after post-commit sync\n", len(manifest.Validation))
					postSyncValidationErr = hooks.RunValidations(manifest.Validation, envCfg.WorkDir)
					if postSyncValidationErr != nil {
						fmt.Fprintf(os.Stderr, "Validation failed after post-commit sync: %v\n", postSyncValidationErr)
						// Commit changes as checkpoint before retry
						checkpointCommitMsg := fmt.Sprintf("feat: implement issue #%d (post-commit sync validation retry checkpoint #%d)", issueID, postSyncRetryCount)
						checkpointSHA, _, commitErr := commitChangesIfNeeded(envCfg.WorkDir, repo, issueID, checkpointCommitMsg, true, envCfg.AgentType, envCfg.agentModel(), prompt)
						if commitErr != nil {
							reportErr := reporterClient.ReportFailure(
								fmt.Sprintf("Failed to commit checkpoint after post-commit sync: %v", commitErr),
								"",
								envCfg.AgentType,
							)
							if reportErr != nil {
								fmt.Fprintf(os.Stderr, "WARNING: Failed to report failure to Operator API: %v\n", reportErr)
							}
							return fmt.Errorf("failed to commit checkpoint after post-commit sync: %w", commitErr)
						}
						if checkpointSHA != "" {
							fmt.Fprintf(os.Stderr, "Committed checkpoint after post-commit sync (SHA: %s) before validation retry\n", checkpointSHA)
						}

						// Check if we've reached max retries
						if postSyncRetryCount >= maxValidationRetries {
							return fmt.Errorf("validation failed after post-commit sync after %d retries: %w", maxValidationRetries, postSyncValidationErr)
						}
						// Continue to next retry
						continue
					}
					fmt.Fprintf(os.Stderr, "Executed %d validations after post-commit sync successfully\n", len(manifest.Validation))
					// Validation passed, break out of retry loop
					break
				}
			} else {
				fmt.Fprintf(os.Stderr, "No validations to execute after post-commit sync\n")
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "No merge or conflict resolution was performed, skipping re-validation\n")
	}

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

	// 14. Generate PR title and body (if an option-capable agent is used)
	var prTitle, prBody string
	if isOptionAgentType(envCfg.AgentType) {
		fmt.Fprintf(os.Stderr, "Generating PR title and body\n")
		// Use lastCommitMsg if available, otherwise use default commit message
		commitMsgForPR := lastCommitMsg
		if commitMsgForPR == "" {
			commitMsgForPR = fmt.Sprintf("feat: implement issue #%d", issueID)
		}
		title, body, err := git.GeneratePRTitleAndBody(envCfg.WorkDir, repo, issueID, prompt, commitMsgForPR, envCfg.AgentType, envCfg.agentModel(), baseBranch)
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
		// For agents without AI generation, use default format
		prTitle = ""
		prBody = ""
	}

	// Add collapsible sections for Issue body and Plan content
	prBody = buildPRBodyWithCollapsibleSections(prBody, prompt, planContent, issueID)

	// Ensure issue-closing keyword is present in PR body
	closingKeyword := fmt.Sprintf("close #%d", issueID)
	if prBody == "" {
		// prBodyが空の場合は、closeキーワードのみを含むPR本文を生成
		prBody = closingKeyword
		fmt.Fprintf(os.Stderr, "Created PR body with issue-closing keyword\n")
	} else {
		// prBodyが空でない場合は、closeキーワードが含まれているかチェック
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

	// 16. Run post-hooks
	if executionMode == "plan_creation" {
		fmt.Fprintf(os.Stderr, "Skipping post-hooks in plan_creation mode\n")
	} else if manifest != nil && len(manifest.Hooks.Post) > 0 {
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
