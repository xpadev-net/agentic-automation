package handlers

import (
	"agentic-automation/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestReportAttemptIdentity(t *testing.T) {
	modern := "agent-runner-7-attempt-1"
	legacy := "agent-runner-7-abcdef"
	old := "agent-runner-7-attempt-0"
	for _, tc := range []struct {
		name             string
		retry            int
		stored, reported *string
		want             bool
	}{
		{"current attempt", 1, &modern, &modern, true},
		{"old callback", 1, &modern, &old, false},
		{"missing modern identity", 1, &modern, nil, false},
		{"legacy first attempt", 0, &legacy, nil, true},
		{"unscoped legacy retry", 1, &legacy, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			got := validateReportAttempt(ctx, &models.AgentRun{ID: 7, RetryCount: tc.retry, JobName: tc.stored}, tc.reported)
			require.Equal(t, tc.want, got)
		})
	}
}
