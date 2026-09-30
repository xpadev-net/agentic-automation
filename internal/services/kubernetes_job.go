package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	appconfig "agentic-automation/internal/config"
	"agentic-automation/internal/models"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// KubernetesJobService provides methods to create Kubernetes Jobs for AgentRun execution
type KubernetesJobService interface {
	// CreateJobForAgentRun creates a Kubernetes Job for the given AgentRun and Issue
	// Parameters:
	//   - ctx: Context for cancellation and timeout control
	//   - agentRun: AgentRun record (must not be nil)
	//   - issue: Issue record (must not be nil)
	//   - prompt: Formatted prompt string for the agent (must not be empty)
	//   - branchName: Optional existing branch name to checkout (empty means create new branch)
	// Returns:
	//   - *batchv1.Job: Created Kubernetes Job, or nil on error
	//   - error: Error if Job creation fails
	CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error)
	// CreateJobForAgentRunWithFeedback creates a Kubernetes Job with aggregated feedback for retry
	// Parameters:
	//   - ctx: Context for cancellation and timeout control
	//   - agentRun: AgentRun record (must not be nil)
	//   - issue: Issue record (must not be nil)
	//   - prompt: Formatted prompt string for the agent (must not be empty)
	//   - feedback: AggregatedFeedback containing review feedback and CI logs (may be nil)
	//   - branchName: Optional existing branch name to checkout (empty means create new branch)
	// Returns:
	//   - *batchv1.Job: Created Kubernetes Job, or nil on error
	//   - error: Error if Job creation fails
	CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *AggregatedFeedback, branchName string) (*batchv1.Job, error)
	// CreateJobForPlanCreation creates a Kubernetes Job for generating a remediation plan from review feedback
	CreateJobForPlanCreation(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, reviewFeedback *models.ReviewFeedback, branchName string) (*batchv1.Job, error)
	// CreateJobForPlanExecution creates a Kubernetes Job for executing a previously generated plan
	CreateJobForPlanExecution(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, planContent string, branchName string) (*batchv1.Job, error)
}

// kubernetesJobService implements KubernetesJobService interface
type kubernetesJobService struct {
	kubernetesClient *clients.KubernetesClient
	logger           *config.AppLogger
}

// NewKubernetesJobService creates a new KubernetesJobService instance.
// It requires a KubernetesClient and logger as dependencies.
//
// Parameters:
//   - kubernetesClient: KubernetesClient instance (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses config.NewNopLogger())
//
// Returns:
//   - KubernetesJobService: Initialized service instance
func NewKubernetesJobService(kubernetesClient *clients.KubernetesClient, logger *config.AppLogger) KubernetesJobService {
	if kubernetesClient == nil {
		panic("kubernetesClient is required for KubernetesJobService")
	}

	// Use config.NewNopLogger() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = config.NewNopLogger()
	}

	logger.Info("KubernetesJobService initialized",
		config.String("service", "kubernetes_job"),
	)

	return &kubernetesJobService{
		kubernetesClient: kubernetesClient,
		logger:           logger,
	}
}

// getRequiredEnv retrieves a required environment variable or returns an error
// Parameters:
//   - key: Environment variable name
//   - logger: Logger for error logging
//
// Returns:
//   - string: Environment variable value
//   - error: Error if environment variable is empty
func getRequiredEnv(key string, logger *config.AppLogger) (string, error) {
	value := appconfig.GetEnv(key, "")
	if value == "" {
		logger.Error("Required environment variable is not set",
			config.String("env_key", key),
			config.String("service", "kubernetes_job"),
		)
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return value, nil
}

// getOptionalEnvInt retrieves an optional integer environment variable with default value
// Parameters:
//   - key: Environment variable name
//   - defaultValue: Default value if not set
//   - logger: Logger for logging
//
// Returns:
//   - int: Environment variable value or default value
func getOptionalEnvInt(key string, defaultValue int, logger *config.AppLogger) int {
	valueStr := appconfig.GetEnv(key, "")
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		logger.Warn("Failed to parse environment variable as integer, using default",
			config.String("env_key", key),
			config.String("value", valueStr),
			config.Int("default_value", defaultValue),
			config.Error(err),
			config.String("service", "kubernetes_job"),
		)
		return defaultValue
	}
	return value
}

func resolveExecutionMode(agentRun *models.AgentRun) string {
	if agentRun == nil {
		return "normal"
	}

	mode := strings.TrimSpace(agentRun.ExecutionMode)
	if mode == "" {
		return "normal"
	}

	return mode
}

// extractPreviousAttemptsJSON extracts previous attempts JSON from AgentRun Input field
// Parameters:
//   - agentRun: AgentRun record
//   - logger: Logger for logging
//
// Returns:
//   - string: Previous attempts JSON string (array format), or empty string if not available
//
// Note: The new Input schema (v1) stores a structured object with prompt, agent_type, and issue metadata.
// This is not the same format as previous-attempts, which expects an array of {retry_count, error} entries.
// Until we implement proper previous attempts tracking, this function returns an empty string to avoid
// passing the Input object to agent-runner, which would cause validation errors.
func extractPreviousAttemptsJSON(agentRun *models.AgentRun, logger *config.AppLogger) string {
	if agentRun == nil || len(agentRun.Input) == 0 {
		return ""
	}

	// Parse Input to detect schema version
	var inputMap map[string]interface{}
	if err := json.Unmarshal(agentRun.Input, &inputMap); err != nil {
		// If Input is not valid JSON, return empty string
		logger.Warn("AgentRun.Input contains invalid JSON, returning empty string for PreviousAttempts",
			config.Int("agent_run_id", agentRun.ID),
			config.Error(err),
			config.String("service", "kubernetes_job"),
		)
		return ""
	}

	// Check if this is the new structured schema (v1)
	if schemaVersion, ok := inputMap["schema_version"].(string); ok && schemaVersion == "1" {
		// New schema detected - Input contains structured data, not previous attempts
		// Return empty string to avoid passing the Input object to agent-runner
		// TODO: In the future, extract actual previous attempts from Output or a dedicated field
		logger.Debug("AgentRun.Input uses new schema v1, returning empty string for PreviousAttempts",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return ""
	}

	// Old format or unrecognized format - return empty string for safety
	// Previous attempts should be in a separate field or extracted differently
	logger.Debug("AgentRun.Input does not contain previous attempts data, returning empty string",
		config.Int("agent_run_id", agentRun.ID),
		config.String("service", "kubernetes_job"),
	)
	return ""
}

// CreateJobForAgentRun creates a Kubernetes Job for the given AgentRun and Issue
func (s *kubernetesJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Input validation
	if agentRun == nil {
		s.logger.Error("agentRun must not be nil",
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("agentRun must not be nil")
	}

	if issue == nil {
		s.logger.Error("issue must not be nil",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("issue must not be nil")
	}

	if prompt == "" {
		s.logger.Error("prompt must not be empty",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("issue_id", issue.Number),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("prompt must not be empty")
	}

	if s.kubernetesClient == nil {
		s.logger.Error("kubernetesClient must not be nil",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("kubernetesClient is not initialized")
	}

	// Get required environment variables
	agentRunnerImage, err := getRequiredEnv("AGENT_RUNNER_IMAGE", s.logger)
	if err != nil {
		return nil, err
	}

	// Get optional environment variables with defaults
	timeoutMinutes := getOptionalEnvInt("AGENT_RUNNER_TIMEOUT_MINUTES", 60, s.logger)

	// Log job configuration before building
	s.logger.Info("Building JobConfig for AgentRun",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("issue_id", issue.Number),
		config.String("repo", issue.Repo),
		config.String("agent_type", agentRun.AgentType),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("timeout_minutes", timeoutMinutes),
		config.String("service", "kubernetes_job"),
	)

	// Build JobConfig
	executionMode := resolveExecutionMode(agentRun)

	// Check for existing active job to prevent duplicate creation
	existingJob, err := s.kubernetesClient.FindActiveJobByAgentRunID(ctx, agentRun.ID)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil && existingJob != nil && !isPriorReportedAttempt(existingJob, agentRun) {
		if existingJob.Labels["retry-count"] == strconv.Itoa(agentRun.RetryCount) {
			agentRun.JobName = &existingJob.Name
			return existingJob, nil
		}
		// Active job already exists, return AlreadyExists error
		s.logger.Info("Active job already exists for agent run, skipping creation",
			config.Int("agent_run_id", agentRun.ID),
			config.String("existing_job_name", existingJob.Name),
			config.String("service", "kubernetes_job"),
		)
		return nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Resource: "jobs"},
			existingJob.Name,
		)
	}
	// NotFound is expected for a new attempt; other lookup errors stop dispatch.

	// Generate job name
	jobName := agentRun.AttemptJobName()

	jobConfig := &clients.JobConfig{
		AgentRunID:       agentRun.ID,
		RetryCount:       agentRun.RetryCount,
		IssueID:          issue.Number,
		Repo:             issue.Repo,
		Prompt:           prompt,
		PreviousAttempts: extractPreviousAttemptsJSON(agentRun, s.logger),
		CILogs:           "", // Future extension - currently empty string
		AgentType:        agentRun.AgentType,
		AgentRunnerImage: agentRunnerImage,
		TimeoutMinutes:   timeoutMinutes,
		BranchName:       branchName,
		ExecutionMode:    executionMode,
		CursorAllowWrite: true,
		JobName:          jobName,
	}

	// Create Kubernetes Job
	job, err := s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
	if err != nil {
		s.logger.Error("Failed to create Kubernetes Job",
			config.Int("agent_run_id", agentRun.ID),
			config.String("job_name", jobName),
			config.Error(err),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("failed to create kubernetes job: %w", err)
	}

	// Store job name in AgentRun for cleanup
	agentRun.JobName = &jobName

	// Log successful job creation
	s.logger.Info("Kubernetes Job created successfully",
		config.Int("agent_run_id", agentRun.ID),
		config.String("job_name", jobName),
		config.String("job_uid", string(job.UID)),
		config.String("namespace", job.Namespace),
		config.String("service", "kubernetes_job"),
	)

	return job, nil
}

// CreateJobForAgentRunWithFeedback creates a Kubernetes Job with aggregated feedback for retry
func (s *kubernetesJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Input validation
	if agentRun == nil {
		s.logger.Error("agentRun must not be nil",
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("agentRun must not be nil")
	}

	if issue == nil {
		s.logger.Error("issue must not be nil",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("issue must not be nil")
	}

	if prompt == "" {
		s.logger.Error("prompt must not be empty",
			config.Int("agent_run_id", agentRun.ID),
			config.Int("issue_id", issue.Number),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("prompt must not be empty")
	}

	if s.kubernetesClient == nil {
		s.logger.Error("kubernetesClient must not be nil",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("kubernetesClient is not initialized")
	}

	// Get required environment variables
	agentRunnerImage, err := getRequiredEnv("AGENT_RUNNER_IMAGE", s.logger)
	if err != nil {
		return nil, err
	}

	// Get optional environment variables with defaults
	timeoutMinutes := getOptionalEnvInt("AGENT_RUNNER_TIMEOUT_MINUTES", 60, s.logger)

	// Extract feedback values (handle nil case)
	hasFeedback := feedback != nil
	var previousAttempts string
	var ciLogs string
	var hasReviewFeedback bool
	var hasCIFailure bool
	var previousAttemptsLength int
	var ciLogsLength int

	if hasFeedback {
		previousAttempts = feedback.PreviousAttemptsJSON
		ciLogs = feedback.CILogs
		hasReviewFeedback = feedback.HasReviewFeedback
		hasCIFailure = feedback.HasCIFailure
		previousAttemptsLength = len(previousAttempts)
		ciLogsLength = len(ciLogs)
	} else {
		// Preserve existing behavior: extract previous attempts from AgentRun when feedback is nil
		// This ensures agent-runner maintains context for retries even when aggregation fails
		previousAttempts = extractPreviousAttemptsJSON(agentRun, s.logger)
		ciLogs = "" // CILogs remains empty as per existing CreateJobForAgentRun behavior
		previousAttemptsLength = len(previousAttempts)
		ciLogsLength = 0
	}

	// Log job configuration before building
	s.logger.Info("Building JobConfig for AgentRun with feedback",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("issue_id", issue.Number),
		config.String("repo", issue.Repo),
		config.String("agent_type", agentRun.AgentType),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("timeout_minutes", timeoutMinutes),
		config.Bool("has_feedback", hasFeedback),
		config.Bool("has_review_feedback", hasReviewFeedback),
		config.Bool("has_ci_failure", hasCIFailure),
		config.Int("previous_attempts_length", previousAttemptsLength),
		config.Int("ci_logs_length", ciLogsLength),
		config.String("service", "kubernetes_job"),
	)

	// Build JobConfig
	executionMode := resolveExecutionMode(agentRun)

	// Check for existing active job to prevent duplicate creation
	existingJob, err := s.kubernetesClient.FindActiveJobByAgentRunID(ctx, agentRun.ID)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil && existingJob != nil && !isPriorReportedAttempt(existingJob, agentRun) {
		if existingJob.Labels["retry-count"] == strconv.Itoa(agentRun.RetryCount) {
			agentRun.JobName = &existingJob.Name
			return existingJob, nil
		}
		// Active job already exists, return AlreadyExists error
		s.logger.Info("Active job already exists for agent run, skipping creation",
			config.Int("agent_run_id", agentRun.ID),
			config.String("existing_job_name", existingJob.Name),
			config.String("service", "kubernetes_job"),
		)
		return nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Resource: "jobs"},
			existingJob.Name,
		)
	}
	// NotFound is expected for a new attempt; other lookup errors stop dispatch.

	// Generate job name
	jobName := agentRun.AttemptJobName()

	jobConfig := &clients.JobConfig{
		AgentRunID:       agentRun.ID,
		RetryCount:       agentRun.RetryCount,
		IssueID:          issue.Number,
		Repo:             issue.Repo,
		Prompt:           prompt,
		PreviousAttempts: previousAttempts,
		CILogs:           ciLogs,
		AgentType:        agentRun.AgentType,
		AgentRunnerImage: agentRunnerImage,
		TimeoutMinutes:   timeoutMinutes,
		BranchName:       branchName,
		ExecutionMode:    executionMode,
		CursorAllowWrite: true,
		JobName:          jobName,
	}

	// Create Kubernetes Job
	job, err := s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
	if err != nil {
		s.logger.Error("Failed to create Kubernetes Job with feedback",
			config.Int("agent_run_id", agentRun.ID),
			config.String("job_name", jobName),
			config.Error(err),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("failed to create kubernetes job: %w", err)
	}

	// Store job name in AgentRun for cleanup
	agentRun.JobName = &jobName

	// Log successful job creation
	s.logger.Info("Kubernetes Job created successfully with feedback",
		config.Int("agent_run_id", agentRun.ID),
		config.String("job_name", jobName),
		config.String("job_uid", string(job.UID)),
		config.String("namespace", job.Namespace),
		config.Bool("has_feedback", hasFeedback),
		config.String("service", "kubernetes_job"),
	)

	return job, nil
}

// CreateJobForPlanCreation creates a Kubernetes Job that generates a plan from review feedback content or issue content.
// reviewFeedback can be nil for issue-triggered plan creation (e.g., /run-agent from issue).
func (s *kubernetesJobService) CreateJobForPlanCreation(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, reviewFeedback *models.ReviewFeedback, branchName string) (*batchv1.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if agentRun == nil {
		s.logger.Error("agentRun must not be nil for plan creation",
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("agentRun must not be nil")
	}

	if issue == nil {
		s.logger.Error("issue must not be nil for plan creation",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("issue must not be nil")
	}

	if s.kubernetesClient == nil {
		s.logger.Error("kubernetesClient must not be nil",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("kubernetesClient is not initialized")
	}

	// Extract content for plan creation
	var planContent string
	var contentSource string
	var reviewFeedbackID int

	if reviewFeedback != nil {
		// Review feedback-based plan creation
		if reviewFeedback.Content != nil {
			planContent = strings.TrimSpace(*reviewFeedback.Content)
		}
		if planContent == "" {
			s.logger.Error("review feedback content must not be empty for plan creation",
				config.Int("agent_run_id", agentRun.ID),
				config.Int("review_feedback_id", reviewFeedback.ID),
				config.String("service", "kubernetes_job"),
			)
			return nil, fmt.Errorf("review feedback content must not be empty")
		}
		contentSource = "review_feedback"
		reviewFeedbackID = reviewFeedback.ID
	} else {
		// Issue-based plan creation (for /run-agent from issue)
		// Extract prompt from AgentRun.Input if available
		if len(agentRun.Input) > 0 {
			var inputMap map[string]interface{}
			if err := json.Unmarshal(agentRun.Input, &inputMap); err == nil {
				if prompt, ok := inputMap["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
					planContent = strings.TrimSpace(prompt)
				}
			}
		}
		// Fallback to issue title/body if prompt not found in Input
		if planContent == "" {
			planContent = fmt.Sprintf("Issue #%d: %s", issue.Number, issue.Title)
			if issue.Body != nil && strings.TrimSpace(*issue.Body) != "" {
				planContent += "\n\n" + strings.TrimSpace(*issue.Body)
			}
		}
		contentSource = "issue"
		reviewFeedbackID = 0
	}

	if planContent == "" {
		s.logger.Error("plan content must not be empty for plan creation",
			config.Int("agent_run_id", agentRun.ID),
			config.String("content_source", contentSource),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("plan content must not be empty")
	}

	agentRunnerImage, err := getRequiredEnv("AGENT_RUNNER_IMAGE", s.logger)
	if err != nil {
		return nil, err
	}

	timeoutMinutes := getOptionalEnvInt("AGENT_RUNNER_TIMEOUT_MINUTES", 60, s.logger)

	s.logger.Info("Building JobConfig for plan creation",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("issue_id", issue.Number),
		config.String("repo", issue.Repo),
		config.String("agent_type", agentRun.AgentType),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("timeout_minutes", timeoutMinutes),
		config.String("content_source", contentSource),
		config.Int("review_feedback_id", reviewFeedbackID),
		config.Int("plan_content_length", len(planContent)),
		config.String("service", "kubernetes_job"),
	)

	// Check for existing active job to prevent duplicate creation
	existingJob, err := s.kubernetesClient.FindActiveJobByAgentRunID(ctx, agentRun.ID)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil && existingJob != nil && !isPriorReportedAttempt(existingJob, agentRun) {
		if existingJob.Labels["retry-count"] == strconv.Itoa(agentRun.RetryCount) {
			agentRun.JobName = &existingJob.Name
			return existingJob, nil
		}
		// Active job already exists, return AlreadyExists error
		s.logger.Info("Active job already exists for agent run, skipping plan creation",
			config.Int("agent_run_id", agentRun.ID),
			config.String("existing_job_name", existingJob.Name),
			config.String("content_source", contentSource),
			config.Int("review_feedback_id", reviewFeedbackID),
			config.String("service", "kubernetes_job"),
		)
		return nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Resource: "jobs"},
			existingJob.Name,
		)
	}
	// NotFound is expected for a new attempt; other lookup errors stop dispatch.

	jobName := fmt.Sprintf("agent-runner-%d-plan-%d-attempt-%d", agentRun.ID, reviewFeedbackID, agentRun.RetryCount)

	jobConfig := &clients.JobConfig{
		AgentRunID:            agentRun.ID,
		RetryCount:            agentRun.RetryCount,
		IssueID:               issue.Number,
		Repo:                  issue.Repo,
		Prompt:                fmt.Sprintf("Plan creation for issue #%d", issue.Number),
		PreviousAttempts:      "",
		CILogs:                "",
		AgentType:             agentRun.AgentType,
		AgentRunnerImage:      agentRunnerImage,
		TimeoutMinutes:        timeoutMinutes,
		BranchName:            branchName,
		ExecutionMode:         "plan_creation",
		ReviewFeedbackContent: planContent,
		CursorAllowWrite:      false,
		JobName:               jobName,
	}

	job, err := s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
	if err != nil {
		s.logger.Error("Failed to create plan creation job",
			config.Int("agent_run_id", agentRun.ID),
			config.String("content_source", contentSource),
			config.Int("review_feedback_id", reviewFeedbackID),
			config.String("job_name", jobName),
			config.Error(err),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("failed to create plan creation kubernetes job: %w", err)
	}

	// Store job name in AgentRun for cleanup
	agentRun.JobName = &jobName

	s.logger.Info("Plan creation job created successfully",
		config.Int("agent_run_id", agentRun.ID),
		config.String("content_source", contentSource),
		config.Int("review_feedback_id", reviewFeedbackID),
		config.String("job_name", jobName),
		config.String("job_uid", string(job.UID)),
		config.String("namespace", job.Namespace),
		config.String("service", "kubernetes_job"),
	)

	return job, nil
}

// CreateJobForPlanExecution creates a Kubernetes Job for executing a previously generated plan.
func (s *kubernetesJobService) CreateJobForPlanExecution(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, planContent string, branchName string) (*batchv1.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if agentRun == nil {
		s.logger.Error("agentRun must not be nil for plan execution",
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("agentRun must not be nil")
	}

	if issue == nil {
		s.logger.Error("issue must not be nil for plan execution",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("issue must not be nil")
	}

	planContent = strings.TrimSpace(planContent)
	if planContent == "" {
		s.logger.Error("planContent must not be empty for plan execution",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("planContent must not be empty")
	}

	if s.kubernetesClient == nil {
		s.logger.Error("kubernetesClient must not be nil",
			config.Int("agent_run_id", agentRun.ID),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("kubernetesClient is not initialized")
	}

	agentRunnerImage, err := getRequiredEnv("AGENT_RUNNER_IMAGE", s.logger)
	if err != nil {
		return nil, err
	}

	timeoutMinutes := getOptionalEnvInt("AGENT_RUNNER_TIMEOUT_MINUTES", 60, s.logger)

	s.logger.Info("Building JobConfig for plan execution",
		config.Int("agent_run_id", agentRun.ID),
		config.Int("issue_id", issue.Number),
		config.String("repo", issue.Repo),
		config.String("agent_type", agentRun.AgentType),
		config.Int("retry_count", agentRun.RetryCount),
		config.Int("timeout_minutes", timeoutMinutes),
		config.Int("plan_content_length", len(planContent)),
		config.String("service", "kubernetes_job"),
	)

	// Extract prompt from AgentRun.Input if available
	prompt := ""
	if len(agentRun.Input) > 0 {
		var inputMap map[string]interface{}
		if err := json.Unmarshal(agentRun.Input, &inputMap); err == nil {
			if p, ok := inputMap["prompt"].(string); ok && strings.TrimSpace(p) != "" {
				prompt = strings.TrimSpace(p)
			}
		}
	}
	if prompt == "" {
		prompt = buildPlanExecutionPrompt(issue)
	}

	if existing, err := s.kubernetesClient.FindActiveJobByAgentRunID(ctx, agentRun.ID); err == nil && existing != nil && !isPriorReportedAttempt(existing, agentRun) {
		if existing.Labels["retry-count"] == strconv.Itoa(agentRun.RetryCount) {
			agentRun.JobName = &existing.Name
			return existing, nil
		}
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "jobs"}, existing.Name)
	} else if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	// Generate job name
	jobName := agentRun.AttemptJobName()

	jobConfig := &clients.JobConfig{
		AgentRunID:       agentRun.ID,
		RetryCount:       agentRun.RetryCount,
		IssueID:          issue.Number,
		Repo:             issue.Repo,
		Prompt:           prompt,
		PreviousAttempts: extractPreviousAttemptsJSON(agentRun, s.logger),
		CILogs:           "",
		AgentType:        agentRun.AgentType,
		AgentRunnerImage: agentRunnerImage,
		TimeoutMinutes:   timeoutMinutes,
		BranchName:       branchName,
		ExecutionMode:    "plan_execution",
		PlanContent:      planContent,
		CursorAllowWrite: true,
		JobName:          jobName,
	}

	// Create Kubernetes Job
	job, err := s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
	if err != nil {
		s.logger.Error("Failed to create plan execution job",
			config.Int("agent_run_id", agentRun.ID),
			config.String("job_name", jobName),
			config.Error(err),
			config.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("failed to create plan execution kubernetes job: %w", err)
	}

	// Store job name in AgentRun for cleanup
	agentRun.JobName = &jobName

	s.logger.Info("Plan execution job created successfully",
		config.Int("agent_run_id", agentRun.ID),
		config.String("job_name", jobName),
		config.String("job_uid", string(job.UID)),
		config.String("namespace", job.Namespace),
		config.String("service", "kubernetes_job"),
	)

	return job, nil
}

// buildPlanExecutionPrompt constructs the prompt for plan execution jobs using Issue context.
// Body is optional; when provided it is separated from the title by an empty line to preserve readability.
func buildPlanExecutionPrompt(issue *models.Issue) string {
	if issue == nil {
		return ""
	}

	title := strings.TrimSpace(issue.Title)

	var body string
	if issue.Body != nil {
		body = strings.TrimSpace(*issue.Body)
	}

	if title == "" {
		return body
	}

	if body == "" {
		return title
	}

	return fmt.Sprintf("%s\n\n%s", title, body)
}

// A new retry is admitted only by CAS from a terminal reported attempt. That
// older Pod can still be finishing its report HTTP call/cleanup; it must not
// strand the admitted next attempt. Same/future attempts remain protected.
func isPriorReportedAttempt(job *batchv1.Job, run *models.AgentRun) bool {
	attempt := 0
	if value, exists := job.Labels["retry-count"]; exists {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		attempt = parsed
	}
	return attempt < run.RetryCount
}
