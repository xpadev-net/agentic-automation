package clients

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"agentic-automation/internal/config"
	appconfig "agentic-automation/internal/config"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// JobConfig holds configuration for creating a Kubernetes Job
type JobConfig struct {
	AgentRunID            int
	RetryCount            int
	IssueID               int
	Repo                  string
	Prompt                string
	PreviousAttempts      string
	CILogs                string
	AgentType             string
	AgentRunnerImage      string
	TimeoutMinutes        int
	BranchName            string // Optional: existing branch name to checkout (empty means create new branch)
	ExecutionMode         string
	PlanContent           string
	ReviewFeedbackContent string
	CursorAllowWrite      bool
	JobName               string // Kubernetes Job name (injected as JOB_NAME environment variable)
}

// KubernetesClient wraps Kubernetes API client functionality
type KubernetesClient struct {
	clientset kubernetes.Interface
	namespace string
	logger    *config.AppLogger
}

const (
	// ServiceAccountNamespaceFile is the path to the namespace file in a Pod
	ServiceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// getNamespaceFromKubeconfig reads the namespace from kubeconfig's current context
// It handles both single file paths and multi-file KUBECONFIG (colon-separated paths)
func getNamespaceFromKubeconfig(kubeConfigPath string, logger *config.AppLogger) string {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()

	// Only set ExplicitPath if:
	// 1. kubeConfigPath is provided (not empty)
	// 2. kubeConfigPath does not contain a colon (single file path, not multi-file list)
	// 3. KUBECONFIG environment variable is not set (to avoid conflicts)
	// When KUBECONFIG is set, let the default loading rules handle it (supports colon-separated paths)
	if kubeConfigPath != "" && !strings.Contains(kubeConfigPath, ":") && os.Getenv("KUBECONFIG") == "" {
		loadingRules.ExplicitPath = kubeConfigPath
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	)

	ns, _, err := clientConfig.Namespace()
	if err != nil {
		logger.Debug("Failed to read namespace from kubeconfig",
			config.String("path", kubeConfigPath),
			config.Error(err),
		)
		return ""
	}

	if ns != "" {
		// Log the actual kubeconfig path that was used
		actualPath := kubeConfigPath
		if actualPath == "" {
			actualPath = os.Getenv("KUBECONFIG")
			if actualPath == "" {
				actualPath = loadingRules.GetDefaultFilename()
			}
		}
		logger.Info("Using namespace from kubeconfig",
			config.String("namespace", ns),
			config.String("path", actualPath),
		)
		return ns
	}

	return ""
}

// getNamespace determines the Kubernetes namespace to use
// Priority:
// 1. KUBERNETES_NAMESPACE environment variable (if explicitly set)
// 2. kubeconfig's current context namespace (when using kubeconfig)
// 3. Service account namespace file (when running in-cluster)
// 4. "default" (fallback)
func getNamespace(kubeConfigPath string, logger *config.AppLogger) string {
	// 1. Check environment variable first
	if ns := os.Getenv("KUBERNETES_NAMESPACE"); ns != "" {
		logger.Info("Using namespace from environment variable",
			config.String("namespace", ns),
		)
		return ns
	}

	// 2. Try to read from kubeconfig (when using kubeconfig)
	if kubeConfigPath != "" || os.Getenv("KUBECONFIG") != "" {
		ns := getNamespaceFromKubeconfig(kubeConfigPath, logger)
		if ns != "" {
			return ns
		}
	}

	// 3. Try to read from service account namespace file (in-cluster)
	nsBytes, err := os.ReadFile(ServiceAccountNamespaceFile)
	if err == nil {
		ns := strings.TrimSpace(string(nsBytes))
		if ns != "" {
			logger.Info("Using namespace from service account file",
				config.String("namespace", ns),
				config.String("file", ServiceAccountNamespaceFile),
			)
			return ns
		}
		logger.Warn("Service account namespace file is empty",
			config.String("file", ServiceAccountNamespaceFile),
		)
	} else if !os.IsNotExist(err) {
		// Log error only if it's not "file not found" (which is expected when using kubeconfig)
		logger.Warn("Failed to read service account namespace file",
			config.String("file", ServiceAccountNamespaceFile),
			config.Error(err),
		)
	}

	// 4. Fallback to default
	logger.Info("Using default namespace (no explicit namespace configured)")
	return "default"
}

// NewKubernetesClient creates a new Kubernetes client
// It supports both in-cluster config (when running in a Pod) and kubeconfig file
// Priority order:
// 1. KUBE_CONFIG_PATH environment variable (explicit custom path)
// 2. KUBECONFIG environment variable or ~/.kube/config (standard kubeconfig)
// 3. rest.InClusterConfig() (when running inside a Pod)
func NewKubernetesClient(logger *config.AppLogger) (*KubernetesClient, error) {
	var err error
	var k8sConfig *rest.Config
	var kubeConfigPath string

	// 1. Check if KUBE_CONFIG_PATH is set (explicit custom path)
	kubeConfigPath = appconfig.GetEnv("KUBE_CONFIG_PATH", "")
	if kubeConfigPath != "" {
		logger.Info("Using kubeconfig from KUBE_CONFIG_PATH", config.String("path", kubeConfigPath))
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig path: %w", err)
		}
	} else {
		// 2. Try to use default kubeconfig loading rules (KUBECONFIG env var or ~/.kube/config)
		// BuildConfigFromFlags with empty strings uses default loading rules
		logger.Info("Attempting to load default kubeconfig")
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", "")
		if err != nil {
			// 3. Fall back to in-cluster config if kubeconfig is not available
			logger.Info("Kubeconfig not available, attempting in-cluster config")
			k8sConfig, err = rest.InClusterConfig()
			if err != nil {
				return nil, fmt.Errorf("failed to get kubeconfig or in-cluster config: %w", err)
			}
			logger.Info("Using in-cluster config")
		} else {
			// Determine which kubeconfig was actually used
			loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
			actualPath := loadingRules.GetDefaultFilename()
			if envKubeconfig := os.Getenv("KUBECONFIG"); envKubeconfig != "" {
				actualPath = envKubeconfig
			}
			logger.Info("Using kubeconfig", config.String("path", actualPath))
			kubeConfigPath = actualPath
		}
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	namespace := getNamespace(kubeConfigPath, logger)

	return &KubernetesClient{
		clientset: clientset,
		namespace: namespace,
		logger:    logger,
	}, nil
}

// generateRandomSuffix generates a 6-character random suffix using crypto/rand
// for secure random generation. The suffix consists of lowercase alphanumeric
// characters (a-z, 0-9) to comply with Kubernetes DNS subdomain name constraints.
func generateRandomSuffix() (string, error) {
	const (
		charset = "abcdefghijklmnopqrstuvwxyz0123456789"
		length  = 6
	)

	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random suffix: %w", err)
	}

	for i := range bytes {
		bytes[i] = charset[bytes[i]%byte(len(charset))]
	}

	return string(bytes), nil
}

// GenerateJobName generates a job name from agentRunID with a random suffix
// to prevent naming conflicts when multiple jobs are created for the same agentRunID.
// Format: agent-runner-{agentRunID}-{randomSuffix}
func (c *KubernetesClient) GenerateJobName(agentRunID int) (string, error) {
	suffix, err := generateRandomSuffix()
	if err != nil {
		return "", fmt.Errorf("failed to generate job name: %w", err)
	}
	return fmt.Sprintf("agent-runner-%d-%s", agentRunID, suffix), nil
}

// GeneratePlanCreationJobName generates a unique job name for plan creation
// by including review_feedback_id and a random suffix to ensure uniqueness.
// Format: agent-runner-{agentRunID}-plan-{reviewFeedbackID}-{randomSuffix}
func (c *KubernetesClient) GeneratePlanCreationJobName(agentRunID int, reviewFeedbackID int) (string, error) {
	suffix, err := generateRandomSuffix()
	if err != nil {
		return "", fmt.Errorf("failed to generate plan creation job name: %w", err)
	}
	return fmt.Sprintf("agent-runner-%d-plan-%d-%s", agentRunID, reviewFeedbackID, suffix), nil
}

// buildEnvVars builds environment variables for the Job container
func (c *KubernetesClient) buildEnvVars(jobCfg *JobConfig) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		// Kubernetes namespace - injected via Downward API
		{
			Name: "KUBERNETES_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		// Operator service name - configured via environment
		{
			Name:  "OPERATOR_SERVICE_NAME",
			Value: appconfig.GetEnv("OPERATOR_SERVICE_NAME", "agent-operator"),
		},
		// Operator service port - configured via environment
		{
			Name:  "OPERATOR_SERVICE_PORT",
			Value: appconfig.GetEnv("OPERATOR_SERVICE_PORT", "3000"),
		},
		{
			Name:  "AGENT_RUN_ID",
			Value: strconv.Itoa(jobCfg.AgentRunID),
		},
		{
			Name:  "AGENT_TYPE",
			Value: jobCfg.AgentType,
		},
		{
			Name:  "WORKSPACE_DIR",
			Value: "/workspace",
		},
		{
			Name:  "S3_ENDPOINT",
			Value: appconfig.GetEnv("S3_ENDPOINT", ""),
		},
		{
			Name:  "S3_REGION",
			Value: appconfig.GetEnv("S3_REGION", ""),
		},
		{
			Name:  "S3_BUCKET",
			Value: appconfig.GetEnv("S3_BUCKET", ""),
		},
		{
			Name:  "S3_USE_PATH_STYLE",
			Value: appconfig.GetEnv("S3_USE_PATH_STYLE", "true"),
		},
		{
			Name:  "S3_MAX_RETRIES",
			Value: appconfig.GetEnv("S3_MAX_RETRIES", "5"),
		},
		{
			Name:  "RETRY_COUNT",
			Value: strconv.Itoa(jobCfg.RetryCount),
		},
		{
			Name:  "CURSOR_ALLOW_WRITE",
			Value: strconv.FormatBool(jobCfg.CursorAllowWrite),
		},
		{
			Name:  "CODEX_MODEL",
			Value: appconfig.GetEnv("CODEX_MODEL", "gpt-5.6-luna"),
		},
	}

	// Add Job name if specified (for agent-runner to report back the correct job name)
	if jobCfg.JobName != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "JOB_NAME",
			Value: jobCfg.JobName,
		})
	}

	// Add existing branch name if specified (for continuing work on existing PR)
	if jobCfg.BranchName != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "EXISTING_BRANCH_NAME",
			Value: jobCfg.BranchName,
		})
	}

	// Get Secret names from environment variables with defaults
	// GitHub App credentials are sourced from operator-secrets by default
	githubAppSecret := appconfig.GetEnv("OPERATOR_SECRETS_NAME", "operator-secrets")
	anthropicAPIKeySecret := appconfig.GetEnv("ANTHROPIC_API_KEY_SECRET", "anthropic-api-key")
	cursorAPIKeySecret := appconfig.GetEnv("CURSOR_API_KEY_SECRET", "cursor-api-key")
	codexAPIKeySecret := appconfig.GetEnv("CODEX_API_KEY_SECRET", "codex-api-key")
	operatorAPITokenSecret := appconfig.GetEnv("OPERATOR_API_TOKEN_SECRET", "agent-runner-secret")
	s3CredentialsSecret := appconfig.GetEnv("S3_CREDENTIALS_SECRET", "s3-credentials")

	// Add Secret references for sensitive values
	envVars = append(envVars, []corev1.EnvVar{
		// GitHub App ID
		{
			Name: "GITHUB_APP_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: githubAppSecret,
					},
					Key: "github-app-id",
				},
			},
		},
		// GitHub App Private Key (PEM contents)
		{
			Name: "GITHUB_PRIVATE_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: githubAppSecret,
					},
					Key: "github-private-key",
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
		{
			Name: "S3_ACCESS_KEY_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: s3CredentialsSecret,
					},
					Key: "access-key-id",
				},
			},
		},
		{
			Name: "S3_SECRET_ACCESS_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: s3CredentialsSecret,
					},
					Key: "secret-access-key",
				},
			},
		},
	}...)

	// Add agent-specific API key based on agent type
	if jobCfg.AgentType == "claude-code" {
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
	} else if jobCfg.AgentType == "cursor-agent" {
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
	} else if jobCfg.AgentType == "codex" {
		optional := true
		envVars = append(envVars, corev1.EnvVar{
			Name: "CODEX_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: codexAPIKeySecret,
					},
					Key:      "api-key",
					Optional: &optional,
				},
			},
		})
	}

	// Inject repository owner/name for token acquisition inside the pod
	if jobCfg.Repo != "" {
		parts := strings.SplitN(jobCfg.Repo, "/", 2)
		if len(parts) == 2 {
			envVars = append(envVars,
				corev1.EnvVar{Name: "REPO_OWNER", Value: parts[0]},
				corev1.EnvVar{Name: "REPO_NAME", Value: parts[1]},
			)
		}
	}

	return envVars
}

// BuildJobSpec builds a Kubernetes Job specification from JobConfig
func (c *KubernetesClient) BuildJobSpec(jobCfg *JobConfig) *batchv1.JobSpec {
	// Get service account name from environment variable
	serviceAccountName := appconfig.GetEnv("KUBERNETES_SERVICE_ACCOUNT", "agent-automation")

	// Build container args
	mode := strings.TrimSpace(jobCfg.ExecutionMode)
	if mode == "" {
		mode = "normal"
	}
	args := []string{
		fmt.Sprintf("--issue-id=%d", jobCfg.IssueID),
		fmt.Sprintf("--repo=%s", jobCfg.Repo),
		fmt.Sprintf("--prompt=%s", jobCfg.Prompt),
		fmt.Sprintf("--execution-mode=%s", mode),
	}
	if jobCfg.PreviousAttempts != "" {
		args = append(args, fmt.Sprintf("--previous-attempts=%s", jobCfg.PreviousAttempts))
	}
	if jobCfg.CILogs != "" {
		args = append(args, fmt.Sprintf("--ci-logs=%s", jobCfg.CILogs))
	}

	// Default resource limits
	// Note: Memory limit increased from 2Gi to 4Gi to handle memory-intensive tasks
	// like vitest which can consume significant memory during test execution and report generation
	memoryRequest := appconfig.GetEnv("AGENT_RUNNER_MEMORY_REQUEST", "512Mi")
	memoryLimit := appconfig.GetEnv("AGENT_RUNNER_MEMORY_LIMIT", "4Gi")
	cpuRequest := appconfig.GetEnv("AGENT_RUNNER_CPU_REQUEST", "500m")
	cpuLimit := appconfig.GetEnv("AGENT_RUNNER_CPU_LIMIT", "2000m")

	jobSpec := &batchv1.JobSpec{
		BackoffLimit: int32Ptr(0), // No retries at Job level (handled by Operator)
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(jobCfg.AgentRunID),
					"retry-count":  strconv.Itoa(jobCfg.RetryCount),
					"issue-id":     strconv.Itoa(jobCfg.IssueID),
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy:      corev1.RestartPolicyNever,
				ServiceAccountName: serviceAccountName,
				Containers: []corev1.Container{
					{
						Name:    "agent-runner",
						Image:   jobCfg.AgentRunnerImage,
						Command: []string{"agent-runner"},
						Args:    args,
						Env:     c.buildEnvVars(jobCfg),
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

	if jobCfg.AgentType == "codex" {
		const (
			authInputVolume = "codex-auth-input"
			authHomeVolume  = "codex-auth-home"
		)
		optional := true
		secretName := appconfig.GetEnv("CODEX_AUTH_SECRET", "codex-auth")
		jobSpec.Template.Spec.Volumes = append(jobSpec.Template.Spec.Volumes,
			corev1.Volume{
				Name: authInputVolume,
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Optional:   &optional,
					Items: []corev1.KeyToPath{{
						Key:  "auth.json",
						Path: "auth.json",
					}},
				}},
			},
			corev1.Volume{
				Name:         authHomeVolume,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		)
		jobSpec.Template.Spec.InitContainers = []corev1.Container{{
			Name:    "initialize-codex-auth",
			Image:   jobCfg.AgentRunnerImage,
			Command: []string{"/bin/sh", "-c"},
			Args: []string{
				`if [ -s /codex-auth-input/auth.json ]; then cp /codex-auth-input/auth.json /codex-auth-home/auth.json && chmod 0600 /codex-auth-home/auth.json; fi`,
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: authInputVolume, MountPath: "/codex-auth-input", ReadOnly: true},
				{Name: authHomeVolume, MountPath: "/codex-auth-home"},
			},
		}}
		jobSpec.Template.Spec.Containers[0].VolumeMounts = append(
			jobSpec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: authHomeVolume, MountPath: "/home/agent/.codex"},
		)
	}

	// Set ImagePullSecrets if configured via environment variable
	imagePullSecretName := appconfig.GetEnv("KUBERNETES_IMAGE_PULL_SECRET", "")
	if imagePullSecretName != "" {
		// Support comma-separated list of secret names
		secrets := strings.Split(imagePullSecretName, ",")
		imagePullSecrets := make([]corev1.LocalObjectReference, 0, len(secrets))
		for _, secret := range secrets {
			secret = strings.TrimSpace(secret)
			if secret != "" {
				imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{
					Name: secret,
				})
			}
		}
		if len(imagePullSecrets) > 0 {
			jobSpec.Template.Spec.ImagePullSecrets = imagePullSecrets
		}
	}

	// Only set ActiveDeadlineSeconds if TimeoutMinutes is positive
	// Kubernetes API validates this field as strictly positive integer
	if jobCfg.TimeoutMinutes > 0 {
		jobSpec.ActiveDeadlineSeconds = int64Ptr(int64(jobCfg.TimeoutMinutes * 60))
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

// int32Ptr returns a pointer to an int32
func int32Ptr(i int32) *int32 {
	return &i
}

// GetOperatorPod retrieves the Operator Pod information
// It uses HOSTNAME environment variable (set by Kubernetes) or attempts to get Pod name from Downward API
func (c *KubernetesClient) GetOperatorPod(ctx context.Context) (*corev1.Pod, error) {
	// Try to get Pod name from HOSTNAME environment variable (Kubernetes sets this automatically)
	podName := os.Getenv("HOSTNAME")
	if podName == "" {
		// Fallback: try to read from Downward API file
		// This is a fallback mechanism, but HOSTNAME should always be set in Kubernetes Pods
		c.logger.Warn("HOSTNAME environment variable is not set, cannot determine Operator Pod name")
		return nil, fmt.Errorf("HOSTNAME environment variable is not set")
	}

	// Get Pod information
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		c.logger.Error("Failed to get Operator Pod",
			config.String("pod_name", podName),
			config.String("namespace", c.namespace),
			config.Error(err),
		)
		return nil, fmt.Errorf("failed to get operator pod %s: %w", podName, err)
	}

	c.logger.Info("Retrieved Operator Pod information",
		config.String("pod_name", podName),
		config.String("namespace", c.namespace),
		config.String("uid", string(pod.UID)),
	)

	return pod, nil
}

// GetOwnerReferenceFromPod retrieves the appropriate OwnerReference from a Pod
// Priority: Deployment > StatefulSet > ReplicaSet > Pod itself
func (c *KubernetesClient) GetOwnerReferenceFromPod(ctx context.Context, pod *corev1.Pod) (*metav1.OwnerReference, error) {
	if pod == nil {
		return nil, fmt.Errorf("pod is nil")
	}

	// Check Pod's OwnerReferences
	for _, ownerRef := range pod.OwnerReferences {
		// Priority order: Deployment > StatefulSet > ReplicaSet
		if ownerRef.Kind == "Deployment" && ownerRef.APIVersion == "apps/v1" {
			// Verify the Deployment exists
			_, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, ownerRef.Name, metav1.GetOptions{})
			if err != nil {
				c.logger.Warn("Deployment owner reference found but deployment does not exist",
					config.String("deployment", ownerRef.Name),
					config.Error(err),
				)
				continue
			}

			c.logger.Info("Using Deployment as OwnerReference",
				config.String("deployment", ownerRef.Name),
				config.String("uid", string(ownerRef.UID)),
			)

			return &ownerRef, nil
		}

		if ownerRef.Kind == "StatefulSet" && ownerRef.APIVersion == "apps/v1" {
			// Verify the StatefulSet exists
			_, err := c.clientset.AppsV1().StatefulSets(c.namespace).Get(ctx, ownerRef.Name, metav1.GetOptions{})
			if err != nil {
				c.logger.Warn("StatefulSet owner reference found but statefulset does not exist",
					config.String("statefulset", ownerRef.Name),
					config.Error(err),
				)
				continue
			}

			c.logger.Info("Using StatefulSet as OwnerReference",
				config.String("statefulset", ownerRef.Name),
				config.String("uid", string(ownerRef.UID)),
			)

			return &ownerRef, nil
		}

		if ownerRef.Kind == "ReplicaSet" && ownerRef.APIVersion == "apps/v1" {
			// Verify the ReplicaSet exists
			_, err := c.clientset.AppsV1().ReplicaSets(c.namespace).Get(ctx, ownerRef.Name, metav1.GetOptions{})
			if err != nil {
				c.logger.Warn("ReplicaSet owner reference found but replicaset does not exist",
					config.String("replicaset", ownerRef.Name),
					config.Error(err),
				)
				continue
			}

			c.logger.Info("Using ReplicaSet as OwnerReference",
				config.String("replicaset", ownerRef.Name),
				config.String("uid", string(ownerRef.UID)),
			)

			return &ownerRef, nil
		}
	}

	// No suitable OwnerReference found, use Pod itself
	c.logger.Info("No suitable OwnerReference found, using Pod itself",
		config.String("pod_name", pod.Name),
		config.String("pod_uid", string(pod.UID)),
	)

	return &metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       pod.Name,
		UID:        pod.UID,
		Controller: boolPtr(false), // Pod is not a controller, so we don't set it as controller
	}, nil
}

// boolPtr returns a pointer to a bool
func boolPtr(b bool) *bool {
	return &b
}

// CreateJob creates a Kubernetes Job
func (c *KubernetesClient) CreateJob(ctx context.Context, jobName string, jobCfg *JobConfig) (*batchv1.Job, error) {
	jobSpec := c.BuildJobSpec(jobCfg)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: c.namespace,
			Labels: map[string]string{
				"app":          "agent-runner",
				"agent-run-id": strconv.Itoa(jobCfg.AgentRunID),
				"retry-count":  strconv.Itoa(jobCfg.RetryCount),
				"issue-id":     strconv.Itoa(jobCfg.IssueID),
			},
		},
		Spec: *jobSpec,
	}

	if len(job.Spec.Template.Spec.Containers) == 0 {
		return nil, fmt.Errorf("job template has no containers configured")
	}

	container := &job.Spec.Template.Spec.Containers[0]
	env := container.Env
	volumes := job.Spec.Template.Spec.Volumes
	volumeMounts := container.VolumeMounts

	const maxInlineContentSize = 900 * 1024

	if jobCfg.ExecutionMode == "plan_creation" {
		reviewContent := strings.TrimSpace(jobCfg.ReviewFeedbackContent)
		if reviewContent != "" {
			if len(reviewContent) > maxInlineContentSize {
				configMapName, err := c.createConfigMapForReviewContent(ctx, jobName, reviewContent)
				if err != nil {
					return nil, fmt.Errorf("failed to create review feedback configmap: %w", err)
				}
				reviewVolumeName := fmt.Sprintf("%s-review", jobName)
				volumes = append(volumes, corev1.Volume{
					Name: reviewVolumeName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
							Items:                []corev1.KeyToPath{{Key: "review_feedback_content.txt", Path: "review_feedback_content.txt"}},
						},
					},
				})
				volumeMounts = append(volumeMounts, corev1.VolumeMount{
					Name:      reviewVolumeName,
					MountPath: "/config/review",
					ReadOnly:  true,
				})
				env = append(env, corev1.EnvVar{
					Name:  "REVIEW_FEEDBACK_CONTENT_FILE",
					Value: "/config/review/review_feedback_content.txt",
				})
			} else {
				env = append(env, corev1.EnvVar{
					Name:  "REVIEW_FEEDBACK_CONTENT",
					Value: reviewContent,
				})
			}
		}
	}

	if jobCfg.ExecutionMode == "plan_execution" {
		planContent := strings.TrimSpace(jobCfg.PlanContent)
		c.logger.Info("Processing plan content for plan_execution mode",
			config.String("job_name", jobName),
			config.Int("plan_content_length", len(jobCfg.PlanContent)),
			config.Int("plan_content_length_trimmed", len(planContent)),
			config.Bool("plan_content_empty", planContent == ""),
		)
		if planContent != "" {
			if len(planContent) > maxInlineContentSize {
				c.logger.Info("Plan content exceeds max inline size, using ConfigMap",
					config.String("job_name", jobName),
					config.Int("plan_content_length", len(planContent)),
					config.Int("max_inline_size", maxInlineContentSize),
				)
				configMapName, err := c.createConfigMapForPlanContent(ctx, jobName, planContent)
				if err != nil {
					return nil, fmt.Errorf("failed to create plan content configmap: %w", err)
				}
				planVolumeName := fmt.Sprintf("%s-plan", jobName)
				volumes = append(volumes, corev1.Volume{
					Name: planVolumeName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
							Items:                []corev1.KeyToPath{{Key: "plan_content.txt", Path: "plan_content.txt"}},
						},
					},
				})
				volumeMounts = append(volumeMounts, corev1.VolumeMount{
					Name:      planVolumeName,
					MountPath: "/config/plan",
					ReadOnly:  true,
				})
				env = append(env, corev1.EnvVar{
					Name:  "PLAN_CONTENT_FILE",
					Value: "/config/plan/plan_content.txt",
				})
				c.logger.Info("Plan content environment variable set (file)",
					config.String("job_name", jobName),
					config.String("env_var", "PLAN_CONTENT_FILE"),
					config.String("value", "/config/plan/plan_content.txt"),
				)
			} else {
				env = append(env, corev1.EnvVar{
					Name:  "PLAN_CONTENT",
					Value: planContent,
				})
				c.logger.Info("Plan content environment variable set (inline)",
					config.String("job_name", jobName),
					config.String("env_var", "PLAN_CONTENT"),
					config.Int("value_length", len(planContent)),
				)
			}
		} else {
			c.logger.Warn("Plan content is empty for plan_execution mode",
				config.String("job_name", jobName),
				config.Int("original_length", len(jobCfg.PlanContent)),
			)
		}
	}

	container.Env = env
	container.VolumeMounts = volumeMounts
	job.Spec.Template.Spec.Volumes = volumes

	// Try to set OwnerReference from Operator Pod
	operatorPod, err := c.GetOperatorPod(ctx)
	if err != nil {
		// Log warning but continue without OwnerReference
		// This is not a fatal error - the Job can still be created
		c.logger.Warn("Failed to get Operator Pod for OwnerReference, creating Job without OwnerReference",
			config.String("job_name", jobName),
			config.Error(err),
		)
	} else {
		// Get OwnerReference from Operator Pod
		ownerRef, err := c.GetOwnerReferenceFromPod(ctx, operatorPod)
		if err != nil {
			// Log warning but continue without OwnerReference
			c.logger.Warn("Failed to get OwnerReference from Operator Pod, creating Job without OwnerReference",
				config.String("job_name", jobName),
				config.Error(err),
			)
		} else {
			// Set OwnerReference on Job
			job.OwnerReferences = []metav1.OwnerReference{*ownerRef}
			c.logger.Info("Set OwnerReference on Job",
				config.String("job_name", jobName),
				config.String("owner_kind", ownerRef.Kind),
				config.String("owner_name", ownerRef.Name),
				config.String("owner_uid", string(ownerRef.UID)),
			)
		}
	}

	c.logger.Info("Creating Kubernetes Job",
		config.String("name", jobName),
		config.String("namespace", c.namespace),
		config.Int("agentRunID", jobCfg.AgentRunID),
	)

	createdJob, err := c.clientset.BatchV1().Jobs(c.namespace).Create(ctx, job, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		existing, getErr := c.GetJob(ctx, jobName)
		if getErr != nil {
			return nil, getErr
		}
		if existing.Labels["agent-run-id"] == strconv.Itoa(jobCfg.AgentRunID) && existing.Labels["retry-count"] == strconv.Itoa(jobCfg.RetryCount) {
			return existing, nil
		}
		return nil, fmt.Errorf("existing job does not match execution attempt: %s", jobName)
	}
	if err != nil {
		c.logger.Error("Failed to create Job",
			config.String("name", jobName),
			config.Error(err),
		)
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	c.logger.Info("Job created successfully",
		config.String("name", jobName),
		config.String("uid", string(createdJob.UID)),
	)

	return createdJob, nil
}

func (c *KubernetesClient) createConfigMapForReviewContent(ctx context.Context, jobName, content string) (string, error) {
	configMapName := fmt.Sprintf("%s-review-content", jobName)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: c.namespace,
		},
		Data: map[string]string{
			"review_feedback_content.txt": content,
		},
	}

	if _, err := c.clientset.CoreV1().ConfigMaps(c.namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		c.logger.Error("Failed to create review content ConfigMap",
			config.String("configmap", configMapName),
			config.Error(err),
		)
		return "", err
	}

	return configMapName, nil
}

func (c *KubernetesClient) createConfigMapForPlanContent(ctx context.Context, jobName, content string) (string, error) {
	configMapName := fmt.Sprintf("%s-plan-content", jobName)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: c.namespace,
		},
		Data: map[string]string{
			"plan_content.txt": content,
		},
	}

	if _, err := c.clientset.CoreV1().ConfigMaps(c.namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		c.logger.Error("Failed to create plan content ConfigMap",
			config.String("configmap", configMapName),
			config.Error(err),
		)
		return "", err
	}

	return configMapName, nil
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

// FindJobByAgentRunID finds a Kubernetes Job by agent-run-id label
// This is used as a fallback when JobName is not stored in the database.
// Returns the first matching Job if multiple are found (should not happen in normal operation).
func (c *KubernetesClient) FindJobByAgentRunID(ctx context.Context, agentRunID int) (*batchv1.Job, error) {
	labelSelector := fmt.Sprintf("agent-run-id=%d", agentRunID)
	jobs, err := c.ListJobs(ctx, labelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to find job by agent-run-id %d: %w", agentRunID, err)
	}

	if len(jobs.Items) == 0 {
		return nil, fmt.Errorf("no job found with agent-run-id=%d", agentRunID)
	}

	// Return the first matching job (normally there should be only one)
	job := &jobs.Items[0]
	if len(jobs.Items) > 1 {
		c.logger.Warn("Multiple jobs found with same agent-run-id, using first one",
			config.Int("agent_run_id", agentRunID),
			config.Int("job_count", len(jobs.Items)),
			config.String("selected_job", job.Name),
		)
	}

	return job, nil
}

// FindActiveJobByAgentRunID finds an active (Running or Pending) Kubernetes Job by agent-run-id label.
// This is used to prevent duplicate job creation when multiple workers try to create a job for the same agent-run-id.
// Returns the first active job found if multiple exist, or an error if no active job is found.
func (c *KubernetesClient) FindActiveJobByAgentRunID(ctx context.Context, agentRunID int) (*batchv1.Job, error) {
	labelSelector := fmt.Sprintf("agent-run-id=%d", agentRunID)
	jobs, err := c.ListJobs(ctx, labelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs by agent-run-id %d: %w", agentRunID, err)
	}

	if len(jobs.Items) == 0 {
		return nil, errors.NewNotFound(schema.GroupResource{Resource: "jobs"}, fmt.Sprintf("agent-run-id=%d", agentRunID))
	}

	// Failed Pod counts are not terminal Job evidence.
	var activeJobs []*batchv1.Job
	for i := range jobs.Items {
		job := &jobs.Items[i]
		terminal := false
		for _, condition := range job.Status.Conditions {
			if condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) {
				terminal = true
			}
		}
		if !terminal {
			activeJobs = append(activeJobs, job)
		}
	}

	if len(activeJobs) == 0 {
		return nil, errors.NewNotFound(schema.GroupResource{Resource: "jobs"}, fmt.Sprintf("agent-run-id=%d", agentRunID))
	}

	// Return the first active job
	job := activeJobs[0]
	if len(activeJobs) > 1 {
		c.logger.Warn("Multiple active jobs found with same agent-run-id, using first one",
			config.Int("agent_run_id", agentRunID),
			config.Int("active_job_count", len(activeJobs)),
			config.String("selected_job", job.Name),
		)
	}

	return job, nil
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
		config.String("name", jobName),
		config.String("namespace", c.namespace),
	)

	propagationPolicy := metav1.DeletePropagationForeground
	err := c.clientset.BatchV1().Jobs(c.namespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagationPolicy,
	})
	if err != nil {
		if errors.IsNotFound(err) {
			c.logger.Warn("Job not found during deletion",
				config.String("name", jobName),
			)
			return nil // Not an error if already deleted
		}
		c.logger.Error("Failed to delete Job",
			config.String("name", jobName),
			config.Error(err),
		)
		return fmt.Errorf("failed to delete job: %w", err)
	}

	c.logger.Info("Job deleted successfully",
		config.String("name", jobName),
	)

	return nil
}

// WaitForJobDeletion waits for a Kubernetes Job to be fully deleted
// It polls the Kubernetes API to check if the Job still exists
// Returns nil when the Job is confirmed deleted, or an error on timeout
func (c *KubernetesClient) WaitForJobDeletion(ctx context.Context, jobName string) error {
	timeout := 30 * time.Second
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				c.logger.Warn("Job deletion wait timeout",
					config.String("job_name", jobName),
					config.Duration("timeout", timeout),
				)
				return fmt.Errorf("job deletion wait timeout: %s", jobName)
			}

			_, err := c.clientset.BatchV1().Jobs(c.namespace).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					// Job削除完了
					c.logger.Info("Job deletion confirmed",
						config.String("job_name", jobName),
					)
					return nil
				}
				// その他のエラーは無視して継続（一時的なAPIエラーの可能性）
				c.logger.Debug("Error checking job existence during deletion wait",
					config.String("job_name", jobName),
					config.Error(err),
				)
			}
		}
	}
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
				config.String("job", job.Name),
				config.Error(err),
			)
			continue
		}
		if status == "Running" || status == "Pending" {
			count++
		}
	}

	return count, nil
}
