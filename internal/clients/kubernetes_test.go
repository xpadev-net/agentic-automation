package clients

import (
	"context"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agentic-automation/internal/config"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateRandomSuffix(t *testing.T) {
	t.Run("generates 6-character suffix", func(t *testing.T) {
		suffix, err := generateRandomSuffix()
		require.NoError(t, err)
		assert.Len(t, suffix, 6, "suffix should be 6 characters long")
	})

	t.Run("contains only lowercase alphanumeric characters", func(t *testing.T) {
		suffix, err := generateRandomSuffix()
		require.NoError(t, err)

		// Check that all characters are lowercase alphanumeric (a-z, 0-9)
		validPattern := regexp.MustCompile(`^[a-z0-9]{6}$`)
		assert.True(t, validPattern.MatchString(suffix),
			"suffix should contain only lowercase alphanumeric characters, got: %s", suffix)
	})

	t.Run("generates unique suffixes", func(t *testing.T) {
		// Generate multiple suffixes and check they're unique
		suffixes := make(map[string]bool)
		for i := 0; i < 100; i++ {
			suffix, err := generateRandomSuffix()
			require.NoError(t, err)
			assert.False(t, suffixes[suffix], "suffix should be unique, got duplicate: %s", suffix)
			suffixes[suffix] = true
		}
	})
}

func TestGenerateJobName(t *testing.T) {
	client := &KubernetesClient{}

	t.Run("generates job name with correct format", func(t *testing.T) {
		agentRunID := 123
		jobName, err := client.GenerateJobName(agentRunID)
		require.NoError(t, err)

		// Check format: agent-runner-{agentRunID}-{randomSuffix}
		expectedPrefix := "agent-runner-123-"
		assert.Contains(t, jobName, expectedPrefix, "job name should contain prefix")
		assert.Len(t, jobName, len(expectedPrefix)+6, "job name should be prefix + 6-character suffix")
	})

	t.Run("generates unique job names", func(t *testing.T) {
		agentRunID := 123
		jobNames := make(map[string]bool)
		for i := 0; i < 100; i++ {
			jobName, err := client.GenerateJobName(agentRunID)
			require.NoError(t, err)
			assert.False(t, jobNames[jobName], "job name should be unique, got duplicate: %s", jobName)
			jobNames[jobName] = true
		}
	})

	t.Run("handles different agent run IDs", func(t *testing.T) {
		jobName1, err1 := client.GenerateJobName(123)
		require.NoError(t, err1)

		jobName2, err2 := client.GenerateJobName(456)
		require.NoError(t, err2)

		assert.NotEqual(t, jobName1, jobName2, "job names for different agent run IDs should be different")
		assert.Contains(t, jobName1, "agent-runner-123-")
		assert.Contains(t, jobName2, "agent-runner-456-")
	})
}

func TestBuildJobSpecCodexAuthFile(t *testing.T) {
	t.Setenv("CODEX_AUTH_SECRET", "test-codex-auth")
	client := &KubernetesClient{}
	spec := client.BuildJobSpec(&JobConfig{
		AgentRunID:       1,
		IssueID:          2,
		Repo:             "owner/repo",
		AgentType:        "codex",
		AgentRunnerImage: "agent-runner:test",
	})

	require.Len(t, spec.Template.Spec.InitContainers, 1)
	initContainer := spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "initialize-codex-auth", initContainer.Name)
	assert.Contains(t, initContainer.Args[0], "chmod 0600")
	require.Len(t, spec.Template.Spec.Volumes, 2)

	input := spec.Template.Spec.Volumes[0]
	require.NotNil(t, input.Secret)
	assert.Equal(t, "test-codex-auth", input.Secret.SecretName)
	require.NotNil(t, input.Secret.Optional)
	assert.True(t, *input.Secret.Optional)
	require.Len(t, input.Secret.Items, 1)
	assert.Equal(t, "auth.json", input.Secret.Items[0].Key)
	assert.Equal(t, "auth.json", input.Secret.Items[0].Path)

	require.Len(t, spec.Template.Spec.Containers, 1)
	mainContainer := spec.Template.Spec.Containers[0]
	require.Len(t, mainContainer.VolumeMounts, 1)
	assert.Equal(t, "/home/agent/.codex", mainContainer.VolumeMounts[0].MountPath)
	assert.False(t, mainContainer.VolumeMounts[0].ReadOnly)

	var codexKey *corev1.EnvVar
	for i := range mainContainer.Env {
		if mainContainer.Env[i].Name == "CODEX_API_KEY" {
			codexKey = &mainContainer.Env[i]
			break
		}
	}
	require.NotNil(t, codexKey)
	require.NotNil(t, codexKey.ValueFrom.SecretKeyRef.Optional)
	assert.True(t, *codexKey.ValueFrom.SecretKeyRef.Optional)
}

func TestBuildJobSpecCodexModelDefaultsToLuna(t *testing.T) {
	t.Setenv("CODEX_MODEL", "")
	client := &KubernetesClient{}
	spec := client.BuildJobSpec(&JobConfig{
		AgentRunID:       1,
		IssueID:          2,
		Repo:             "owner/repo",
		AgentType:        "codex",
		AgentRunnerImage: "agent-runner:test",
	})

	var codexModel string
	for _, env := range spec.Template.Spec.Containers[0].Env {
		if env.Name == "CODEX_MODEL" {
			codexModel = env.Value
			break
		}
	}
	assert.Equal(t, "gpt-5.6-luna", codexModel)
}

func TestBuildJobSpecNonCodexHasNoCodexAuthVolumes(t *testing.T) {
	client := &KubernetesClient{}
	spec := client.BuildJobSpec(&JobConfig{
		AgentRunID:       1,
		IssueID:          2,
		Repo:             "owner/repo",
		AgentType:        "cursor-agent",
		AgentRunnerImage: "agent-runner:test",
	})

	assert.Empty(t, spec.Template.Spec.InitContainers)
	assert.Empty(t, spec.Template.Spec.Volumes)
	assert.Empty(t, spec.Template.Spec.Containers[0].VolumeMounts)
}

func TestGeneratePlanCreationJobName(t *testing.T) {
	client := &KubernetesClient{}

	t.Run("generates plan creation job name with correct format", func(t *testing.T) {
		agentRunID := 123
		reviewFeedbackID := 456
		jobName, err := client.GeneratePlanCreationJobName(agentRunID, reviewFeedbackID)
		require.NoError(t, err)

		// Check format: agent-runner-{agentRunID}-plan-{reviewFeedbackID}-{randomSuffix}
		expectedPrefix := "agent-runner-123-plan-456-"
		assert.Contains(t, jobName, expectedPrefix, "job name should contain prefix")
		assert.Len(t, jobName, len(expectedPrefix)+6, "job name should be prefix + 6-character suffix")
	})

	t.Run("generates unique job names", func(t *testing.T) {
		agentRunID := 123
		reviewFeedbackID := 456
		jobNames := make(map[string]bool)
		for i := 0; i < 100; i++ {
			jobName, err := client.GeneratePlanCreationJobName(agentRunID, reviewFeedbackID)
			require.NoError(t, err)
			assert.False(t, jobNames[jobName], "job name should be unique, got duplicate: %s", jobName)
			jobNames[jobName] = true
		}
	})

	t.Run("handles different IDs", func(t *testing.T) {
		jobName1, err1 := client.GeneratePlanCreationJobName(123, 456)
		require.NoError(t, err1)

		jobName2, err2 := client.GeneratePlanCreationJobName(789, 101112)
		require.NoError(t, err2)

		assert.NotEqual(t, jobName1, jobName2, "job names for different IDs should be different")
		assert.Contains(t, jobName1, "agent-runner-123-plan-456-")
		assert.Contains(t, jobName2, "agent-runner-789-plan-101112-")
	})
}

func TestFindJobByAgentRunID(t *testing.T) {
	ctx := context.Background()
	logger := config.NewNopLogger()
	client := NewKubernetesClientWithClientset(logger)

	t.Run("finds job by agent-run-id label", func(t *testing.T) {
		agentRunID := 123
		issueID := 456

		// Create a job with agent-run-id label
		jobName := "test-job-123"
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
		}

		createdJob, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)

		// Find the job by agent-run-id
		foundJob, err := client.FindJobByAgentRunID(ctx, agentRunID)
		require.NoError(t, err)
		assert.Equal(t, createdJob.Name, foundJob.Name)
		assert.Equal(t, createdJob.Labels["agent-run-id"], strconv.Itoa(agentRunID))
	})

	t.Run("returns error when no job found", func(t *testing.T) {
		agentRunID := 999

		_, err := client.FindJobByAgentRunID(ctx, agentRunID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no job found with agent-run-id=999")
	})

	t.Run("returns first job when multiple jobs found", func(t *testing.T) {
		agentRunID := 789
		issueID1 := 100
		issueID2 := 200

		// Create first job
		job1 := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-789-1",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID1),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
		}

		// Create second job with same agent-run-id
		job2 := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-789-2",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID2),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
		}

		_, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job1, metav1.CreateOptions{})
		require.NoError(t, err)
		_, err = client.clientset.BatchV1().Jobs("default").Create(ctx, job2, metav1.CreateOptions{})
		require.NoError(t, err)

		// Find job - should return first one and log warning
		foundJob, err := client.FindJobByAgentRunID(ctx, agentRunID)
		require.NoError(t, err)
		assert.NotNil(t, foundJob)
		assert.Equal(t, foundJob.Labels["agent-run-id"], strconv.Itoa(agentRunID))
		// Should return one of the jobs (order may vary)
		assert.True(t, foundJob.Name == "test-job-789-1" || foundJob.Name == "test-job-789-2")
	})

	t.Run("does not find job with different agent-run-id", func(t *testing.T) {
		agentRunID := 111
		issueID := 222

		// Create a job with different agent-run-id
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-111",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
		}

		_, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)

		// Try to find job with different agent-run-id
		_, err = client.FindJobByAgentRunID(ctx, 999)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no job found with agent-run-id=999")
	})
}

func TestFindActiveJobByAgentRunID(t *testing.T) {
	ctx := context.Background()
	logger := config.NewNopLogger()
	client := NewKubernetesClientWithClientset(logger)

	t.Run("finds active job by agent-run-id label", func(t *testing.T) {
		agentRunID := 123
		issueID := 456

		// Create a job with agent-run-id label and Running status
		jobName := "test-job-active-123"
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1, // Active job
			},
		}

		createdJob, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)

		// Find the active job by agent-run-id
		foundJob, err := client.FindActiveJobByAgentRunID(ctx, agentRunID)
		require.NoError(t, err)
		assert.Equal(t, createdJob.Name, foundJob.Name)
		assert.Equal(t, createdJob.Labels["agent-run-id"], strconv.Itoa(agentRunID))
	})

	t.Run("returns error when no active job found", func(t *testing.T) {
		agentRunID := 999

		_, err := client.FindActiveJobByAgentRunID(ctx, agentRunID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no active job found with agent-run-id=999")
	})

	t.Run("ignores completed jobs", func(t *testing.T) {
		agentRunID := 456
		issueID := 789

		// Create a completed job
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-completed-456",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Succeeded: 1, // Completed job
			},
		}

		_, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)

		// Should not find completed job
		_, err = client.FindActiveJobByAgentRunID(ctx, agentRunID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no active job found with agent-run-id=456")
	})

	t.Run("returns first active job when multiple active jobs found", func(t *testing.T) {
		agentRunID := 789
		issueID1 := 100
		issueID2 := 200

		// Create first active job
		job1 := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-active-789-1",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID1),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1, // Active job
			},
		}

		// Create second active job with same agent-run-id
		job2 := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-active-789-2",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID2),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1, // Active job
			},
		}

		_, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job1, metav1.CreateOptions{})
		require.NoError(t, err)
		_, err = client.clientset.BatchV1().Jobs("default").Create(ctx, job2, metav1.CreateOptions{})
		require.NoError(t, err)

		// Find active job - should return one of them and log warning
		foundJob, err := client.FindActiveJobByAgentRunID(ctx, agentRunID)
		require.NoError(t, err)
		assert.NotNil(t, foundJob)
		assert.Equal(t, foundJob.Labels["agent-run-id"], strconv.Itoa(agentRunID))
		// Should return one of the active jobs
		assert.True(t, foundJob.Name == "test-job-active-789-1" || foundJob.Name == "test-job-active-789-2")
	})

	t.Run("finds pending job", func(t *testing.T) {
		agentRunID := 321
		issueID := 654

		// Create a pending job (no status set)
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job-pending-321",
				Namespace: "default",
				Labels: map[string]string{
					"app":          "agent-runner",
					"agent-run-id": strconv.Itoa(agentRunID),
					"issue-id":     strconv.Itoa(issueID),
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "test:latest",
							},
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			// No status set - should be treated as Pending
		}

		createdJob, err := client.clientset.BatchV1().Jobs("default").Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)

		// Find the pending job
		foundJob, err := client.FindActiveJobByAgentRunID(ctx, agentRunID)
		require.NoError(t, err)
		assert.Equal(t, createdJob.Name, foundJob.Name)
	})
}
