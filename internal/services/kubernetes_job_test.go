package services

import (
	"agentic-automation/internal/models"
	"testing"
)

func TestResolveExecutionMode(t *testing.T) {
	cases := []struct {
		name     string
		input    *models.AgentRun
		expected string
	}{
		{
			name:     "nil agent run",
			input:    nil,
			expected: "normal",
		},
		{
			name: "blank execution mode",
			input: &models.AgentRun{
				ExecutionMode: "",
			},
			expected: "normal",
		},
		{
			name: "whitespace execution mode",
			input: &models.AgentRun{
				ExecutionMode: "  plan_creation  ",
			},
			expected: "plan_creation",
		},
		{
			name: "preset execution mode",
			input: &models.AgentRun{
				ExecutionMode: "plan_execution",
			},
			expected: "plan_execution",
		},
	}

	for _, tc := range cases {
		result := resolveExecutionMode(tc.input)
		if result != tc.expected {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.expected, result)
		}
	}
}
