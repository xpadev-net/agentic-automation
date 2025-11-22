package clients

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
