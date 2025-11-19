package services_test

import (
	"context"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestCreateJobForPlanExecution_UsesPromptFromInput(t *testing.T) {
	// Setup
	logger := config.NewNopLogger()
	k8sClient := clients.NewKubernetesClientWithClientset(logger)
	jobService := services.NewKubernetesJobService(k8sClient, logger)

	// Define test cases
	tests := []struct {
		name           string
		inputJSON      string
		expectedPrompt string
	}{
		{
			name:           "Input has prompt",
			inputJSON:      `{"prompt": "Original task description", "schema_version": "1"}`,
			expectedPrompt: "Original task description",
		},
		{
			name:           "Input has empty prompt",
			inputJSON:      `{"prompt": "", "schema_version": "1"}`,
			expectedPrompt: "Plan execution for issue #123", // Default fallback
		},
		{
			name:           "Input missing prompt field",
			inputJSON:      `{"schema_version": "1"}`,
			expectedPrompt: "Plan execution for issue #123", // Default fallback
		},
		{
			name:           "Input is empty",
			inputJSON:      "",
			expectedPrompt: "Plan execution for issue #123", // Default fallback
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create AgentRun with Input
			agentRun := &models.AgentRun{
				ID:            i + 1,
				IssueID:       123,
				AgentType:     "claude-code",
				ExecutionMode: "plan_execution",
				RetryCount:    0,
			}

			if tc.inputJSON != "" {
				agentRun.Input = datatypes.JSON([]byte(tc.inputJSON))
			}

			issue := &models.Issue{
				Number: 123,
				Repo:   "owner/repo",
				Title:  "Test Issue",
			}

			// Execute
			// We need to set required env vars for the service to work
			t.Setenv("AGENT_RUNNER_IMAGE", "test-image")

			job, err := jobService.CreateJobForPlanExecution(context.Background(), agentRun, issue, "Plan content", "feature/branch")
			require.NoError(t, err)
			require.NotNil(t, job)

			// Verify
			// The prompt is passed as an argument to the container: --prompt=...
			foundPrompt := false
			for _, container := range job.Spec.Template.Spec.Containers {
				for _, arg := range container.Args {
					if len(arg) > 9 && arg[:9] == "--prompt=" {
						actualPrompt := arg[9:]
						assert.Equal(t, tc.expectedPrompt, actualPrompt)
						foundPrompt = true
						break
					}
				}
			}
			assert.True(t, foundPrompt, "Prompt argument not found in container args")
		})
	}
}
