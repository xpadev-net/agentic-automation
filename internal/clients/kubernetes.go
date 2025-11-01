package clients

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	appconfig "agentic-automation/internal/config"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// JobConfig holds configuration for creating a Kubernetes Job
type JobConfig struct {
	AgentRunID       int
	IssueID          int
	Repo             string
	Prompt           string
	PreviousAttempts string
	CILogs           string
	AgentType        string
	AgentRunnerImage string
	TimeoutMinutes   int
	OperatorAPIURL   string
	OperatorAPIToken string
}

// KubernetesClient wraps Kubernetes API client functionality
type KubernetesClient struct {
	clientset kubernetes.Interface
	namespace string
	logger    *zap.Logger
}

const (
	// ServiceAccountNamespaceFile is the path to the namespace file in a Pod
	ServiceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// getNamespace determines the Kubernetes namespace to use
// Priority:
// 1. KUBERNETES_NAMESPACE environment variable (if explicitly set)
// 2. Service account namespace file (when running in-cluster)
// 3. "default" (fallback)
func getNamespace(logger *zap.Logger) string {
	// 1. Check environment variable first
	if ns := os.Getenv("KUBERNETES_NAMESPACE"); ns != "" {
		logger.Info("Using namespace from environment variable",
			zap.String("namespace", ns),
		)
		return ns
	}

	// 2. Try to read from service account namespace file (in-cluster)
	nsBytes, err := os.ReadFile(ServiceAccountNamespaceFile)
	if err == nil {
		ns := strings.TrimSpace(string(nsBytes))
		if ns != "" {
			logger.Info("Using namespace from service account file",
				zap.String("namespace", ns),
				zap.String("file", ServiceAccountNamespaceFile),
			)
			return ns
		}
		logger.Warn("Service account namespace file is empty",
			zap.String("file", ServiceAccountNamespaceFile),
		)
	} else if !os.IsNotExist(err) {
		// Log error only if it's not "file not found" (which is expected when using kubeconfig)
		logger.Warn("Failed to read service account namespace file",
			zap.String("file", ServiceAccountNamespaceFile),
			zap.Error(err),
		)
	}

	// 3. Fallback to default
	logger.Info("Using default namespace (no explicit namespace configured)")
	return "default"
}

// NewKubernetesClient creates a new Kubernetes client
// It supports both in-cluster config (when running in a Pod) and kubeconfig file
func NewKubernetesClient(logger *zap.Logger) (*KubernetesClient, error) {
	var err error

	// Check if KUBE_CONFIG_PATH is set (for local development)
	kubeConfigPath := appconfig.GetEnv("KUBE_CONFIG_PATH", "")
	var k8sConfig *rest.Config
	if kubeConfigPath != "" {
		logger.Info("Using kubeconfig file", zap.String("path", kubeConfigPath))
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
	} else {
		// Use in-cluster config (when running inside a Pod)
		logger.Info("Using in-cluster config")
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	namespace := getNamespace(logger)

	return &KubernetesClient{
		clientset: clientset,
		namespace: namespace,
		logger:    logger,
	}, nil
}

// GenerateJobName generates a job name from agentRunID
func (c *KubernetesClient) GenerateJobName(agentRunID int) string {
	return fmt.Sprintf("agent-runner-%d", agentRunID)
}

// buildEnvVars builds environment variables for the Job container
func (c *KubernetesClient) buildEnvVars(config *JobConfig) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{
			Name:  "OPERATOR_API_URL",
			Value: config.OperatorAPIURL,
		},
		{
			Name:  "AGENT_RUN_ID",
			Value: strconv.Itoa(config.AgentRunID),
		},
		{
			Name:  "AGENT_TYPE",
			Value: config.AgentType,
		},
		{
			Name:  "WORKSPACE_DIR",
			Value: "/workspace",
		},
	}

	// Get Secret names from environment variables with defaults
	githubTokenSecret := appconfig.GetEnv("GITHUB_TOKEN_SECRET", "github-token")
	anthropicAPIKeySecret := appconfig.GetEnv("ANTHROPIC_API_KEY_SECRET", "anthropic-api-key")
	cursorAPIKeySecret := appconfig.GetEnv("CURSOR_API_KEY_SECRET", "cursor-api-key")
	operatorAPITokenSecret := appconfig.GetEnv("OPERATOR_API_TOKEN_SECRET", "agent-runner-secret")

	// Add Secret references for sensitive values
	envVars = append(envVars, []corev1.EnvVar{
		{
			Name: "GITHUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: githubTokenSecret,
					},
					Key: "token",
				},
			},
		},
		{
			Name: "OPERATOR_API_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: operatorAPITokenSecret,
					},
					Key: "token",
				},
			},
		},
	}...)

	// Add agent-specific API key based on agent type
	if config.AgentType == "claude-code" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "ANTHROPIC_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: anthropicAPIKeySecret,
					},
					Key: "api-key",
				},
			},
		})
	} else if config.AgentType == "cursor-agents" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "CURSOR_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cursorAPIKeySecret,
					},
					Key: "api-key",
				},
			},
		})
	}

	return envVars
}

// BuildJobSpec builds a Kubernetes Job specification from JobConfig
func (c *KubernetesClient) BuildJobSpec(config *JobConfig) *batchv1.JobSpec {
	// Get service account name from environment variable
	serviceAccountName := appconfig.GetEnv("KUBERNETES_SERVICE_ACCOUNT", "agent-automation")

	// Build container args
	args := []string{
		fmt.Sprintf("--issue-id=%d", config.IssueID),
		fmt.Sprintf("--repo=%s", config.Repo),
		fmt.Sprintf("--prompt=%s", config.Prompt),
	}
	if config.PreviousAttempts != "" {
		args = append(args, fmt.Sprintf("--previous-attempts=%s", config.PreviousAttempts))
	}
	if config.CILogs != "" {
		args = append(args, fmt.Sprintf("--ci-logs=%s", config.CILogs))
	}

	// Default resource limits
	memoryRequest := appconfig.GetEnv("AGENT_RUNNER_MEMORY_REQUEST", "512Mi")
	memoryLimit := appconfig.GetEnv("AGENT_RUNNER_MEMORY_LIMIT", "2Gi")
	cpuRequest := appconfig.GetEnv("AGENT_RUNNER_CPU_REQUEST", "500m")
	cpuLimit := appconfig.GetEnv("AGENT_RUNNER_CPU_LIMIT", "2000m")

	jobSpec := &batchv1.JobSpec{
		ActiveDeadlineSeconds: int64Ptr(int64(config.TimeoutMinutes * 60)),
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(config.AgentRunID),
					"issue-id":     strconv.Itoa(config.IssueID),
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy:      corev1.RestartPolicyNever,
				ServiceAccountName: serviceAccountName,
				Containers: []corev1.Container{
					{
						Name:    "agent-runner",
						Image:   config.AgentRunnerImage,
						Command: []string{"agent-runner"},
						Args:    args,
						Env:     c.buildEnvVars(config),
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: parseQuantity(memoryRequest),
								corev1.ResourceCPU:    parseQuantity(cpuRequest),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: parseQuantity(memoryLimit),
								corev1.ResourceCPU:    parseQuantity(cpuLimit),
							},
						},
					},
				},
			},
		},
	}

	return jobSpec
}

// parseQuantity is a helper to parse resource quantities
func parseQuantity(qty string) resource.Quantity {
	parsed, err := resource.ParseQuantity(qty)
	if err != nil {
		// Fallback to a default if parsing fails
		return resource.MustParse("0")
	}
	return parsed
}

// int64Ptr returns a pointer to an int64
func int64Ptr(i int64) *int64 {
	return &i
}

// CreateJob creates a Kubernetes Job
func (c *KubernetesClient) CreateJob(ctx context.Context, jobName string, config *JobConfig) (*batchv1.Job, error) {
	jobSpec := c.BuildJobSpec(config)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: c.namespace,
			Labels: map[string]string{
				"app":          "agent-runner",
				"agent-run-id": strconv.Itoa(config.AgentRunID),
				"issue-id":     strconv.Itoa(config.IssueID),
			},
		},
		Spec: *jobSpec,
	}

	c.logger.Info("Creating Kubernetes Job",
		zap.String("name", jobName),
		zap.String("namespace", c.namespace),
		zap.Int("agentRunID", config.AgentRunID),
	)

	createdJob, err := c.clientset.BatchV1().Jobs(c.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		c.logger.Error("Failed to create Job",
			zap.String("name", jobName),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	c.logger.Info("Job created successfully",
		zap.String("name", jobName),
		zap.String("uid", string(createdJob.UID)),
	)

	return createdJob, nil
}

// GetJob retrieves a Kubernetes Job by name
func (c *KubernetesClient) GetJob(ctx context.Context, jobName string) (*batchv1.Job, error) {
	job, err := c.clientset.BatchV1().Jobs(c.namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("job %s not found: %w", jobName, err)
		}
		return nil, fmt.Errorf("failed to get job %s: %w", jobName, err)
	}
	return job, nil
}

// ListJobs lists Kubernetes Jobs matching the label selector
func (c *KubernetesClient) ListJobs(ctx context.Context, labelSelector string) (*batchv1.JobList, error) {
	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}

	jobs, err := c.clientset.BatchV1().Jobs(c.namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	return jobs, nil
}

// GetJobStatus returns the status of a Job as a string
func (c *KubernetesClient) GetJobStatus(ctx context.Context, jobName string) (string, error) {
	job, err := c.GetJob(ctx, jobName)
	if err != nil {
		return "", err
	}

	// Check job conditions to determine status
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return "Complete", nil
		}
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return "Failed", nil
		}
	}

	// Check active/completions
	if job.Status.Active > 0 {
		return "Running", nil
	}

	if job.Status.Succeeded > 0 {
		return "Complete", nil
	}

	if job.Status.Failed > 0 {
		return "Failed", nil
	}

	return "Pending", nil
}

// IsJobComplete checks if a Job is complete
func (c *KubernetesClient) IsJobComplete(ctx context.Context, jobName string) (bool, error) {
	status, err := c.GetJobStatus(ctx, jobName)
	if err != nil {
		return false, err
	}
	return status == "Complete", nil
}

// IsJobFailed checks if a Job has failed
func (c *KubernetesClient) IsJobFailed(ctx context.Context, jobName string) (bool, error) {
	status, err := c.GetJobStatus(ctx, jobName)
	if err != nil {
		return false, err
	}
	return status == "Failed", nil
}

// DeleteJob deletes a Kubernetes Job
func (c *KubernetesClient) DeleteJob(ctx context.Context, jobName string) error {
	c.logger.Info("Deleting Kubernetes Job",
		zap.String("name", jobName),
		zap.String("namespace", c.namespace),
	)

	propagationPolicy := metav1.DeletePropagationForeground
	err := c.clientset.BatchV1().Jobs(c.namespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagationPolicy,
	})
	if err != nil {
		if errors.IsNotFound(err) {
			c.logger.Warn("Job not found during deletion",
				zap.String("name", jobName),
			)
			return nil // Not an error if already deleted
		}
		c.logger.Error("Failed to delete Job",
			zap.String("name", jobName),
			zap.Error(err),
		)
		return fmt.Errorf("failed to delete job: %w", err)
	}

	c.logger.Info("Job deleted successfully",
		zap.String("name", jobName),
	)

	return nil
}

// GetPod retrieves a Kubernetes Pod by name
func (c *KubernetesClient) GetPod(ctx context.Context, podName string) (*corev1.Pod, error) {
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("pod %s not found: %w", podName, err)
		}
		return nil, fmt.Errorf("failed to get pod %s: %w", podName, err)
	}
	return pod, nil
}

// ListPods lists Kubernetes Pods matching the label selector
func (c *KubernetesClient) ListPods(ctx context.Context, labelSelector string) (*corev1.PodList, error) {
	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}

	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}
	return pods, nil
}

// GetPodStatus returns the status of a Pod as a string
func (c *KubernetesClient) GetPodStatus(ctx context.Context, podName string) (string, error) {
	pod, err := c.GetPod(ctx, podName)
	if err != nil {
		return "", err
	}

	phase := pod.Status.Phase
	if phase == "" {
		return "Unknown", nil
	}
	return string(phase), nil
}

// GetPodLogs retrieves logs from a Pod
func (c *KubernetesClient) GetPodLogs(ctx context.Context, podName string, tailLines *int64) (string, error) {
	if tailLines == nil {
		defaultLines := int64(100)
		tailLines = &defaultLines
	}

	opts := corev1.PodLogOptions{
		TailLines: tailLines,
	}

	req := c.clientset.CoreV1().Pods(c.namespace).GetLogs(podName, &opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get log stream for pod %s: %w", podName, err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, stream)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read log stream for pod %s: %w", podName, err)
	}

	return buf.String(), nil
}

// GetActiveJobsCount returns the count of active (running) Jobs matching the label selector
func (c *KubernetesClient) GetActiveJobsCount(ctx context.Context, labelSelector string) (int, error) {
	jobs, err := c.ListJobs(ctx, labelSelector)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, job := range jobs.Items {
		status, err := c.GetJobStatus(ctx, job.Name)
		if err != nil {
			c.logger.Warn("Failed to get job status for count",
				zap.String("job", job.Name),
				zap.Error(err),
			)
			continue
		}
		if status == "Running" || status == "Pending" {
			count++
		}
	}

	return count, nil
}
