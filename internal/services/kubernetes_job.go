package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"agentic-automation/internal/clients"
	appconfig "agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
)

// KubernetesJobService provides methods to create Kubernetes Jobs for AgentRun execution
type KubernetesJobService interface {
	// CreateJobForAgentRun creates a Kubernetes Job for the given AgentRun and Issue
	// Parameters:
	//   - ctx: Context for cancellation and timeout control
	//   - agentRun: AgentRun record (must not be nil)
	//   - issue: Issue record (must not be nil)
	//   - prompt: Formatted prompt string for the agent (must not be empty)
	// Returns:
	//   - *batchv1.Job: Created Kubernetes Job, or nil on error
	//   - error: Error if Job creation fails
	CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string) (*batchv1.Job, error)
}

// kubernetesJobService implements KubernetesJobService interface
type kubernetesJobService struct {
	kubernetesClient *clients.KubernetesClient
	logger           *zap.Logger
}

// NewKubernetesJobService creates a new KubernetesJobService instance.
// It requires a KubernetesClient and logger as dependencies.
//
// Parameters:
//   - kubernetesClient: KubernetesClient instance (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - KubernetesJobService: Initialized service instance
func NewKubernetesJobService(kubernetesClient *clients.KubernetesClient, logger *zap.Logger) KubernetesJobService {
	if kubernetesClient == nil {
		panic("kubernetesClient is required for KubernetesJobService")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	logger.Info("KubernetesJobService initialized",
		zap.String("service", "kubernetes_job"),
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
func getRequiredEnv(key string, logger *zap.Logger) (string, error) {
	value := appconfig.GetEnv(key, "")
	if value == "" {
		logger.Error("Required environment variable is not set",
			zap.String("env_key", key),
			zap.String("service", "kubernetes_job"),
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
func getOptionalEnvInt(key string, defaultValue int, logger *zap.Logger) int {
	valueStr := appconfig.GetEnv(key, "")
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		logger.Warn("Failed to parse environment variable as integer, using default",
			zap.String("env_key", key),
			zap.String("value", valueStr),
			zap.Int("default_value", defaultValue),
			zap.Error(err),
			zap.String("service", "kubernetes_job"),
		)
		return defaultValue
	}
	return value
}

// extractPreviousAttemptsJSON extracts previous attempts JSON from AgentRun Input field
// Parameters:
//   - agentRun: AgentRun record
//   - logger: Logger for logging (currently unused but kept for future extensibility)
//
// Returns:
//   - string: Previous attempts JSON string, or empty string if not available
func extractPreviousAttemptsJSON(agentRun *models.AgentRun, logger *zap.Logger) string {
	if agentRun == nil || agentRun.Input == "" {
		return ""
	}

	// At this point, we return the Input field as-is
	// The agent-runner will parse it according to its own format
	// In the future, we might parse and restructure this data here

	// Validate that Input is valid JSON (basic check)
	var rawValue interface{}
	if err := json.Unmarshal([]byte(agentRun.Input), &rawValue); err != nil {
		// If Input is not valid JSON, return empty string
		// This is a defensive check - Input should normally contain valid JSON
		logger.Warn("AgentRun.Input contains invalid JSON, returning empty string for PreviousAttempts",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Error(err),
			zap.String("service", "kubernetes_job"),
		)
		return ""
	}

	return agentRun.Input
}

// CreateJobForAgentRun creates a Kubernetes Job for the given AgentRun and Issue
func (s *kubernetesJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string) (*batchv1.Job, error) {
	// Input validation
	if agentRun == nil {
		s.logger.Error("agentRun must not be nil",
			zap.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("agentRun must not be nil")
	}

	if issue == nil {
		s.logger.Error("issue must not be nil",
			zap.Int("agent_run_id", agentRun.ID),
			zap.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("issue must not be nil")
	}

	if prompt == "" {
		s.logger.Error("prompt must not be empty",
			zap.Int("agent_run_id", agentRun.ID),
			zap.Int("issue_id", issue.Number),
			zap.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("prompt must not be empty")
	}

	// Get required environment variables
	agentRunnerImage, err := getRequiredEnv("AGENT_RUNNER_IMAGE", s.logger)
	if err != nil {
		return nil, err
	}

	operatorAPIURL, err := getRequiredEnv("OPERATOR_API_URL", s.logger)
	if err != nil {
		return nil, err
	}

	// Get optional environment variables with defaults
	timeoutMinutes := getOptionalEnvInt("AGENT_RUNNER_TIMEOUT_MINUTES", 60, s.logger)

	// Log job configuration before building
	s.logger.Info("Building JobConfig for AgentRun",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("issue_id", issue.Number),
		zap.String("repo", issue.Repo),
		zap.String("agent_type", agentRun.AgentType),
		zap.Int("retry_count", agentRun.RetryCount),
		zap.Int("timeout_minutes", timeoutMinutes),
		zap.String("service", "kubernetes_job"),
	)

	// Build JobConfig
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
		OperatorAPIURL:   operatorAPIURL,
	}

	// Generate job name
	jobName := s.kubernetesClient.GenerateJobName(agentRun.ID)

	// Create Kubernetes Job
	job, err := s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
	if err != nil {
		s.logger.Error("Failed to create Kubernetes Job",
			zap.Int("agent_run_id", agentRun.ID),
			zap.String("job_name", jobName),
			zap.Error(err),
			zap.String("service", "kubernetes_job"),
		)
		return nil, fmt.Errorf("failed to create kubernetes job: %w", err)
	}

	// Log successful job creation
	s.logger.Info("Kubernetes Job created successfully",
		zap.Int("agent_run_id", agentRun.ID),
		zap.String("job_name", jobName),
		zap.String("job_uid", string(job.UID)),
		zap.String("namespace", job.Namespace),
		zap.String("service", "kubernetes_job"),
	)

	return job, nil
}
