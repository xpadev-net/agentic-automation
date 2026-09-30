package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
)

type completingPlanService struct {
	*fakePlanJobService
	onCreate func(*models.AgentRun)
}

func (s *completingPlanService) CreateJobForPlanExecution(ctx context.Context, run *models.AgentRun, issue *models.Issue, plan, branch string) (*batchv1.Job, error) {
	job, err := s.fakePlanJobService.CreateJobForPlanExecution(ctx, run, issue, plan, branch)
	if err == nil && s.onCreate != nil {
		s.onCreate(run)
	}
	return job, err
}

func TestReviewPlanCallbacksPreserveExecutionClaim(t *testing.T) {
	for _, fastCompletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale duplicate", true: "fast execution report"}[fastCompletion], func(t *testing.T) {
			fixtures := setupPlanTestFixtures(t)
			staleFeedback := *fixtures.reviewFeedback
			fake := &completingPlanService{fakePlanJobService: &fakePlanJobService{}}
			if fastCompletion {
				fake.onCreate = func(run *models.AgentRun) {
					require.NoError(t, fixtures.db.Model(&models.ReviewFeedback{}).Where("id = ? AND execution_agent_run_id = ?", fixtures.reviewFeedback.ID, run.ID).Update("plan_creation_status", "executed").Error)
				}
			}
			oldClient, oldService := kubernetesClientFactory, kubernetesJobServiceFactory
			kubernetesClientFactory = func(*config.AppLogger) (*clients.KubernetesClient, error) { return nil, nil }
			kubernetesJobServiceFactory = func(*clients.KubernetesClient, *config.AppLogger) services.KubernetesJobService { return fake }
			t.Cleanup(func() { kubernetesClientFactory = oldClient; kubernetesJobServiceFactory = oldService })
			repo := repositories.NewAgentRunRepository(fixtures.db)
			feedbackRepo := repositories.NewReviewFeedbackRepositoryWithDB(fixtures.db)
			invoke := func(run *models.AgentRun, feedback *models.ReviewFeedback) {
				w := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(w)
				ctx.Request = httptest.NewRequest("POST", "/", nil)
				handlePlanCreated(ctx, context.Background(), run.ID, run, feedback, &PlanReportRequest{Status: "plan_created", AgentType: "claude-code", PlanContent: "fixture plan"}, "", repo, feedbackRepo, fixtures.db, run.State)
				require.Equal(t, 200, w.Code, w.Body.String())
			}
			invoke(fixtures.agentRun, fixtures.reviewFeedback)
			completed, err := repo.GetByID(fixtures.agentRun.ID)
			require.NoError(t, err)
			invoke(completed, &staleFeedback)
			var final models.ReviewFeedback
			require.NoError(t, fixtures.db.First(&final, fixtures.reviewFeedback.ID).Error)
			require.NotNil(t, final.ExecutionAgentRunID)
			if fastCompletion {
				require.Equal(t, "executed", final.PlanCreationStatus)
			} else {
				require.Equal(t, "created", final.PlanCreationStatus)
			}
			require.Equal(t, 1, fake.planCallCount, "duplicate callback must not dispatch twice")
			var count int64
			require.NoError(t, fixtures.db.Model(&models.AgentRun{}).Where("execution_mode = ?", "plan_execution").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}
