package handlers

import (
	"encoding/json"
	"testing"
)

func assignmentPayload(t *testing.T, raw string) IssuesPayload {
	t.Helper()
	var payload IssuesPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return payload
}

func TestAssignmentTarget(t *testing.T) {
	t.Setenv("AGENT_ASSIGNMENT_BOT_USERNAME", "agent-bot[bot]")
	t.Setenv("AGENT_ASSIGNMENT_BOT_USER_ID", "")

	tests := []struct {
		name  string
		json  string
		match bool
	}{
		{
			name:  "configured assignee",
			json:  `{"assignee":{"login":"agent-bot","id":10}}`,
			match: true,
		},
		{
			name: "ordinary user",
			json: `{"assignee":{"login":"developer","id":11}}`,
		},
		{
			name:  "configured bot in assignees",
			json:  `{"assignee":null,"assignees":[{"login":"developer","id":11},{"login":"AGENT-BOT[bot]","id":10}]}`,
			match: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, matched := assignmentTarget(assignmentPayload(t, tt.json))
			if matched != tt.match {
				t.Fatalf("matched = %v, want %v", matched, tt.match)
			}
		})
	}
}

func TestAssignmentRepositoryAllowed(t *testing.T) {
	t.Setenv("AGENT_ASSIGNMENT_REPOSITORY", "example/service")
	if !assignmentRepositoryAllowed("example/service") {
		t.Fatal("configured repository should be allowed")
	}
	if assignmentRepositoryAllowed("example/other") {
		t.Fatal("different repository should be rejected")
	}

	t.Setenv("AGENT_ASSIGNMENT_REPOSITORY", "")
	if !assignmentRepositoryAllowed("any/repository") {
		t.Fatal("empty repository restriction should allow all repositories")
	}
}
