package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"agentic-automation/internal/webhooks/handlers"
	"agentic-automation/internal/webhooks/middleware"
	tu "agentic-automation/tests/integration/testutils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIgnoredCommentDoesNotBlockAssignment(t *testing.T) {
	for _, initial := range []struct {
		name, body, status string
		allow              bool
	}{
		{"ordinary comment", "thanks for the update", "no_trigger", true},
		{"denied command", "/run-agent", "permission_denied", false},
	} {
		t.Run(initial.name, func(t *testing.T) {
			t.Setenv("AGENT_ASSIGNMENT_BOT_USERNAME", "agent-bot")
			t.Setenv("AGENT_ASSIGNMENT_REPOSITORY", "")
			t.Setenv("AGENT_RUNNER_IMAGE", "example/agent-runner:test")
			t.Setenv("OPERATOR_SERVICE_NAME", "agent-operator")
			t.Setenv("OPERATOR_SERVICE_PORT", "3000")
			db := setupDB(t)
			config.SetDBForTesting(db)
			logger := config.NewNopLogger()
			config.SetLoggerForTesting(logger)
			repo := "test/" + uuid.NewString()
			auth := &tu.StubAuthorization{Allow: initial.allow}
			k8s := clients.NewKubernetesClientWithClientset(logger)
			deps := handlers.IssueCommentDeps{
				Logger: logger, KubernetesClient: k8s, AuthorizationService: auth,
				IssueContextService:       &tu.StubIssueContext{Context: &services.IssueContext{Number: 7, Title: "Example"}},
				GitHubNotificationService: &tu.StubGitHubNotification{},
			}
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.POST("/", func(c *gin.Context) { body, _ := c.GetRawData(); c.Set("webhook_payload", body); c.Next() }, middleware.IdempotencyMiddleware(), func(c *gin.Context) {
				if c.GetHeader("X-GitHub-Event") == "issues" {
					handlers.HandleIssuesWithDeps(c, handlers.IssuesDeps{Logger: logger, AssignmentRunner: func(c *gin.Context) { handlers.HandleIssueCommentWithDeps(c, deps) }})
				} else {
					handlers.HandleIssueCommentWithDeps(c, deps)
				}
			})
			send := func(event, delivery, action, body string) map[string]interface{} {
				t.Helper()
				payload := map[string]interface{}{
					"action": action, "issue": map[string]interface{}{"id": 77, "number": 7, "title": "Example", "state": "open"},
					"repository": map[string]interface{}{"full_name": repo},
					"comment":    map[string]interface{}{"body": body, "user": map[string]interface{}{"login": "alice"}},
					"assignee":   map[string]interface{}{"login": "agent-bot"}, "sender": map[string]interface{}{"login": "alice"},
				}
				raw, err := json.Marshal(payload)
				require.NoError(t, err)
				req := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
				req.Header.Set("X-GitHub-Event", event)
				req.Header.Set("X-GitHub-Delivery", delivery)
				out := httptest.NewRecorder()
				router.ServeHTTP(out, req)
				require.Equal(t, 200, out.Code, out.Body.String())
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &result), out.Body.String())
				return result
			}
			firstID := uuid.NewString()
			require.Equal(t, initial.status, send("issue_comment", firstID, "created", initial.body)["status"])
			require.Equal(t, "already_processed", send("issue_comment", firstID, "created", initial.body)["status"])
			issue, err := repositories.NewIssueRepository().FindByRepoAndNumber(repo, 7)
			require.NoError(t, err)
			var count int64
			require.NoError(t, db.Model(&models.AgentRun{}).Where("issue_id = ?", issue.ID).Count(&count).Error)
			require.Zero(t, count, "ignored/denied receipts must not create queued executions")
			auth.Allow = true
			assignedID := uuid.NewString()
			require.Equal(t, "plan_creation_started", send("issues", assignedID, "assigned", "")["status"])
			require.Equal(t, "already_processed", send("issues", assignedID, "assigned", "")["status"])
			busyDelivery := uuid.NewString()
			require.Equal(t, "already_running", send("issues", busyDelivery, "assigned", "")["status"])
			require.NoError(t, db.Model(&models.AgentRun{}).Where("issue_id = ?", issue.ID).Update("state", "failed").Error)
			require.Equal(t, "already_processed", send("issues", busyDelivery, "assigned", "")["status"])
			jobs, err := k8s.ListJobs(t.Context(), fmt.Sprintf("issue-id=%d", 7))
			require.NoError(t, err)
			require.Len(t, jobs.Items, 1)
		})
	}
}

func TestIssueAdmissionSerializesConcurrentDeliveries(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate=%v", duplicate), func(t *testing.T) {
			db := setupDB(t)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			// Use separate connections and real concurrent transactions.
			sqlDB.SetMaxOpenConns(16)
			issue := models.Issue{Repo: "test/" + uuid.NewString(), Number: 1, Title: "Example", State: "open", Labels: "[]"}
			require.NoError(t, db.Create(&issue).Error)
			key := uuid.NewString()
			var admitted atomic.Int32
			errs := make(chan error, 16)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					delivery := key
					if !duplicate {
						delivery = fmt.Sprintf("%s-%d", key, i)
					}
					_, isNew, err := repositories.ReserveIssueRun(db, delivery, issue.ID)
					if err != nil {
						errs <- err
						return
					}
					if isNew {
						admitted.Add(1)
					}
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, admitted.Load())
			var count int64
			require.NoError(t, db.Model(&models.AgentRun{}).Where("issue_id = ?", issue.ID).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

// Receipt dispatch is independent of AgentRun creation, covering approval
// comments and other handlers that only emit notifications or update metadata.
func TestWebhookReceiptDispatchAndProjection(t *testing.T) {
	db := setupDB(t)
	config.SetDBForTesting(db)
	config.SetLoggerForTesting(config.NewNopLogger())
	var dispatched atomic.Int32
	router := gin.New()
	router.POST("/", func(c *gin.Context) { raw, _ := c.GetRawData(); c.Set("webhook_payload", raw); c.Next() }, middleware.IdempotencyMiddleware(), func(c *gin.Context) { dispatched.Add(1); c.JSON(200, gin.H{"status": "handled"}) })
	send := func(delivery, state string) int {
		raw := fmt.Sprintf(`{"issue":{"number":1,"title":"Example","state":%q},"repository":{"full_name":"example/receipts"}}`, state)
		req := httptest.NewRequest("POST", "/", bytes.NewBufferString(raw))
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-GitHub-Event", "issue_comment")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out.Code
	}
	require.Equal(t, 200, send("closed-delivery", "closed"))
	require.Equal(t, 200, send("reopened-delivery", "open"))
	require.Equal(t, 200, send("closed-delivery", "closed"))
	issue, err := repositories.NewIssueRepository().FindByRepoAndNumber("example/receipts", 1)
	require.NoError(t, err)
	require.Equal(t, "open", issue.State, "old duplicates must not mutate the projection")
	require.EqualValues(t, 2, dispatched.Load())
	var wg sync.WaitGroup
	codes := make(chan int, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- send("approval-delivery", "open") }()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		require.Equal(t, 200, code)
	}
	require.EqualValues(t, 3, dispatched.Load(), "concurrent approval receipt must dispatch once without an AgentRun")
	var runs int64
	require.NoError(t, db.Model(&models.AgentRun{}).Count(&runs).Error)
	require.Zero(t, runs)
}
