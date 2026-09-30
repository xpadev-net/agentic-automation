package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type observedJobs struct {
	job   *batchv1.Job
	pods  []corev1.Pod
	err   error
	onGet func()
}

func (o *observedJobs) GetJob(_ context.Context, name string) (*batchv1.Job, error) {
	if o.onGet != nil {
		f := o.onGet
		o.onGet = nil
		f()
	}
	if o.err != nil {
		return nil, o.err
	}
	if o.job == nil || o.job.Name != name {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "jobs"}, name)
	}
	return o.job.DeepCopy(), nil
}
func (o *observedJobs) ListJobs(context.Context, string) (*batchv1.JobList, error) {
	if o.err != nil {
		return nil, o.err
	}
	list := &batchv1.JobList{}
	if o.job != nil {
		list.Items = []batchv1.Job{*o.job.DeepCopy()}
	}
	return list, nil
}
func (o *observedJobs) ListPods(context.Context, string) (*corev1.PodList, error) {
	return &corev1.PodList{Items: o.pods}, nil
}

func TestReconcileUsesTerminalJobEvidenceAndGrace(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		condition   batchv1.JobConditionType
		age         time.Duration
		failedCount int32
		want        string
	}{
		{"pending", "", 0, 0, "started"},
		{"failed pod alone is not terminal", "", time.Hour, 1, "started"},
		{"failure target is not terminal", batchv1.JobFailureTarget, time.Hour, 1, "started"},
		{"fresh failure", batchv1.JobFailed, time.Minute, 1, "started"},
		{"deadline failure", batchv1.JobFailed, 6 * time.Minute, 1, "failed"},
		{"complete without report", batchv1.JobComplete, 6 * time.Minute, 0, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupDB(t)
			repo := repositories.NewAgentRunRepository(db)
			started := now.Add(-time.Hour)
			run := &models.AgentRun{IssueID: 1, State: "started", ExecutionMode: "plan_creation", StartedAt: &started, Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")}
			run, _, err := repo.CreateOrGet(tc.name, run)
			require.NoError(t, err)
			name := run.AttemptJobName()
			run.JobName = &name
			require.NoError(t, repo.Update(run))
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("fixture-job"), Labels: map[string]string{"agent-run-id": fmt.Sprint(run.ID), "retry-count": "0"}}}
			job.Status.Failed = tc.failedCount
			if tc.condition != "" {
				job.Status.Conditions = []batchv1.JobCondition{{Type: tc.condition, Status: corev1.ConditionTrue, Reason: "DeadlineExceeded", LastTransitionTime: metav1.NewTime(now.Add(-tc.age))}}
			}
			observer := &observedJobs{job: job, pods: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "Job", UID: job.UID}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "runner", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}}}}}}}}
			opts := services.DefaultReconcileOptions()
			opts.Now = func() time.Time { return now }
			reconciler, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) { return observer, nil }, config.NewNopLogger(), opts)
			require.NoError(t, err)
			require.NoError(t, reconciler.ReconcileOnce(t.Context()))
			got, err := repo.GetByID(run.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.State)
			if tc.want == "failed" {
				require.NotNil(t, got.CompletedAt)
				var output map[string]any
				require.NoError(t, json.Unmarshal(got.Output, &output))
				require.Equal(t, false, output["result_known"])
				if tc.condition == batchv1.JobComplete {
					require.Contains(t, *got.ErrorMessage, "AgentReportMissing")
				} else {
					require.Contains(t, string(got.Output), "OOMKilled")
				}
			}
		})
	}
}

func TestReconcileMissingJobAndOutage(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, state string
		reserved    bool
		age         time.Duration
		apiError    bool
		want        string
	}{
		{"fresh dispatch", "started", true, time.Minute, false, "started"},
		{"absent old dispatch", "started", true, time.Hour, false, "failed"},
		{"reserved queued dispatch", "queued", true, time.Hour, false, "failed"},
		{"unreserved legacy queue", "queued", false, time.Hour, false, "queued"},
		{"API outage", "started", true, time.Hour, true, "started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupDB(t)
			repo := repositories.NewAgentRunRepository(db)
			started := now.Add(-tc.age)
			run := &models.AgentRun{State: tc.state, ExecutionMode: "plan_creation", StartedAt: &started, CreatedAt: started, Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")}
			run, _, err := repo.CreateOrGet(tc.name, run)
			require.NoError(t, err)
			if tc.reserved {
				name := run.AttemptJobName()
				run.JobName = &name
				require.NoError(t, repo.Update(run))
			}
			observer := &observedJobs{}
			if tc.apiError {
				observer.err = errors.New("fixture API unavailable")
			}
			opts := services.DefaultReconcileOptions()
			opts.Now = func() time.Time { return now }
			r, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) { return observer, nil }, nil, opts)
			require.NoError(t, err)
			require.NoError(t, r.ReconcileOnce(t.Context()))
			got, err := repo.GetByID(run.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.State)
		})
	}
}

func TestLifecycleCallbacksAndReconcilerCannotOverwriteEachOther(t *testing.T) {
	now := time.Now().UTC()
	db := setupDB(t)
	repo := repositories.NewAgentRunRepository(db)
	started := now.Add(-time.Hour)
	run, _, err := repo.CreateOrGet("race", &models.AgentRun{State: "started", ExecutionMode: "plan_creation", StartedAt: &started, Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")})
	require.NoError(t, err)
	name := run.AttemptJobName()
	run.JobName = &name
	require.NoError(t, repo.Update(run))
	staleCallback, err := repo.GetByID(run.ID)
	require.NoError(t, err)
	observer := &observedJobs{}
	opts := services.DefaultReconcileOptions()
	opts.Now = func() time.Time { return now }
	r, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) { return observer, nil }, nil, opts)
	require.NoError(t, err)
	require.NoError(t, r.ReconcileOnce(t.Context()))
	staleCallback.State = "succeeded"
	staleCallback.CompletedAt = &now
	require.ErrorIs(t, repo.Update(staleCallback), repositories.ErrConcurrentLifecycle)
	got, err := repo.GetByID(run.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", got.State)

	// A callback that wins during Kubernetes observation also survives the scan.
	second, _, err := repo.CreateOrGet("callback-wins", &models.AgentRun{State: "started", ExecutionMode: "plan_creation", StartedAt: &started, Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")})
	require.NoError(t, err)
	name = second.AttemptJobName()
	second.JobName = &name
	require.NoError(t, repo.Update(second))
	observer.onGet = func() {
		require.NoError(t, services.NewAgentRunStateMachine(repo, nil).TransitionToSucceeded(second.ID, nil, nil))
	}
	require.NoError(t, r.ReconcileOnce(t.Context()))
	got, err = repo.GetByID(second.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", got.State)
}

func TestReconcilerRunsAtStartupAndStops(t *testing.T) {
	db := setupDB(t)
	var calls atomic.Int32
	opts := services.DefaultReconcileOptions()
	opts.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) {
		if calls.Add(1) == 2 {
			cancel()
		}
		return &observedJobs{}, nil
	}, nil, opts)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciliation did not stop")
	}
	require.EqualValues(t, 2, calls.Load())
}

func TestReconcileWaitsWhenTerminalTimestampIsMissing(t *testing.T) {
	now := time.Now().UTC()
	db := setupDB(t)
	repo := repositories.NewAgentRunRepository(db)
	run, _, err := repo.CreateOrGet("no-time", &models.AgentRun{State: "started", ExecutionMode: "plan_creation", Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")})
	require.NoError(t, err)
	name := run.AttemptJobName()
	run.JobName = &name
	require.NoError(t, repo.Update(run))
	observer := &observedJobs{job: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"agent-run-id": fmt.Sprint(run.ID), "retry-count": "0"}}, Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}}
	opts := services.DefaultReconcileOptions()
	opts.Now = func() time.Time { return now }
	r, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) { return observer, nil }, nil, opts)
	require.NoError(t, err)
	require.NoError(t, r.ReconcileOnce(t.Context()))
	got, _ := repo.GetByID(run.ID)
	require.Equal(t, "started", got.State)
	now = now.Add(6 * time.Minute)
	require.NoError(t, r.ReconcileOnce(t.Context()))
	got, _ = repo.GetByID(run.ID)
	require.Equal(t, "failed", got.State)
	require.True(t, strings.Contains(*got.ErrorMessage, "AgentJobFailed"))
}

func TestRetryAdmissionIsAtomicAndUsesFreshAttemptTime(t *testing.T) {
	db := setupDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(16)
	repo := repositories.NewAgentRunRepository(db)
	old := time.Now().UTC().Add(-time.Hour)
	run, _, err := repo.CreateOrGet("retry-race", &models.AgentRun{State: "failed", ExecutionMode: "plan_creation", CreatedAt: old, StartedAt: &old, CompletedAt: &old, Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")})
	require.NoError(t, err)
	var snapshots []*models.AgentRun
	for i := 0; i < 16; i++ {
		snapshot, err := repo.GetByID(run.ID)
		require.NoError(t, err)
		snapshots = append(snapshots, snapshot)
	}
	results := make(chan error, 16)
	for _, snapshot := range snapshots {
		go func(snapshot *models.AgentRun) {
			_, err := repo.(repositories.AtomicRetryAdmission).BeginRetry(snapshot, 5)
			results <- err
		}(snapshot)
	}
	winners := 0
	for range snapshots {
		err := <-results
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, repositories.ErrConcurrentLifecycle)
		}
	}
	require.Equal(t, 1, winners)
	current, err := repo.GetByID(run.ID)
	require.NoError(t, err)
	require.Equal(t, 1, current.RetryCount)
	require.Equal(t, "started", current.State)
	require.NotNil(t, current.JobName)
	require.True(t, current.StartedAt.After(old.Add(50*time.Minute)))
	require.Nil(t, current.CompletedAt)
	observer := &observedJobs{}
	r, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) { return observer, nil }, nil, services.DefaultReconcileOptions())
	require.NoError(t, err)
	require.NoError(t, r.ReconcileOnce(t.Context()))
	current, err = repo.GetByID(run.ID)
	require.NoError(t, err)
	require.Equal(t, "started", current.State, "old CreatedAt must not expire a newly admitted retry")
}

func TestReconcilerReleasesOnlyItsReviewReservation(t *testing.T) {
	for _, newerLink := range []bool{false, true} {
		t.Run(fmt.Sprint(newerLink), func(t *testing.T) {
			db := setupDB(t)
			repo := repositories.NewAgentRunRepository(db)
			require.NoError(t, db.Exec(`CREATE TABLE review_feedback (id INTEGER PRIMARY KEY,plan_agent_run_id INTEGER,execution_agent_run_id INTEGER,plan_creation_status TEXT,updated_at DATETIME)`).Error)
			old := time.Now().UTC().Add(-time.Hour)
			reviewID := 1
			run, _, err := repo.CreateOrGet("review-failure", &models.AgentRun{State: "started", ExecutionMode: "plan_creation", ReviewFeedbackID: &reviewID, StartedAt: &old, Input: datatypes.JSON("{}"), Output: datatypes.JSON("{}")})
			require.NoError(t, err)
			name := run.AttemptJobName()
			run.JobName = &name
			require.NoError(t, repo.Update(run))
			linkedRun := run.ID
			if newerLink {
				linkedRun++
			}
			require.NoError(t, db.Exec("INSERT INTO review_feedback(id,plan_agent_run_id,plan_creation_status) VALUES(1,?,'creating')", linkedRun).Error)
			r, err := services.NewAgentRunReconciler(db, func() (services.JobObserver, error) { return &observedJobs{}, nil }, nil, services.DefaultReconcileOptions())
			require.NoError(t, err)
			require.NoError(t, r.ReconcileOnce(t.Context()))
			var status string
			require.NoError(t, db.Raw("SELECT plan_creation_status FROM review_feedback WHERE id=1").Scan(&status).Error)
			if newerLink {
				require.Equal(t, "creating", status)
			} else {
				require.Equal(t, "rejected", status)
			}
		})
	}
}
