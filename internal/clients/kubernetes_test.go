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
