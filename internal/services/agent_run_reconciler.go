package services

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// JobObserver is read-only. Reconciliation never creates/retries/deletes Jobs.
type JobObserver interface {
	GetJob(context.Context, string) (*batchv1.Job, error)
	ListJobs(context.Context, string) (*batchv1.JobList, error)
	ListPods(context.Context, string) (*corev1.PodList, error)
}

type ReconcileOptions struct {
	Interval        time.Duration
	ReportGrace     time.Duration
	MissingJobGrace time.Duration
	APITimeout      time.Duration
	Now             func() time.Time
}

func DefaultReconcileOptions() ReconcileOptions {
	return ReconcileOptions{Interval: 30 * time.Second, ReportGrace: 5 * time.Minute, MissingJobGrace: 10 * time.Minute, APITimeout: 30 * time.Second, Now: time.Now}
}

type AgentRunReconciler struct {
	db            *gorm.DB
	jobs          func() (JobObserver, error)
	logger        *config.AppLogger
	options       ReconcileOptions
	firstTerminal map[string]time.Time
}

func NewAgentRunReconciler(db *gorm.DB, jobs func() (JobObserver, error), logger *config.AppLogger, options ReconcileOptions) (*AgentRunReconciler, error) {
	if db == nil || jobs == nil {
		return nil, fmt.Errorf("database and Job observer are required")
	}
	if options.Interval <= 0 || options.ReportGrace <= 0 || options.MissingJobGrace <= 0 || options.APITimeout <= 0 {
		return nil, fmt.Errorf("reconciliation durations must be positive")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &AgentRunReconciler{db: db, jobs: jobs, logger: logger, options: options, firstTerminal: make(map[string]time.Time)}, nil
}

// Run performs the startup pass immediately, then serial periodic passes. A
// Kubernetes initialization/outage error is retried next pass; HTTP stays usable.
func (r *AgentRunReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.options.Interval)
	defer ticker.Stop()
	for {
		if err := r.ReconcileOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("AgentRun reconciliation incomplete", config.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *AgentRunReconciler) ReconcileOnce(ctx context.Context) error {
	jobs, err := r.jobs()
	if err != nil {
		return err
	}
	lastID := 0
	for {
		var runs []models.AgentRun
		if err := r.db.WithContext(ctx).Where("id > ? AND state IN ?", lastID, []string{"queued", "started"}).Order("id").Limit(100).Find(&runs).Error; err != nil {
			return err
		}
		if len(runs) == 0 {
			return nil
		}
		for i := range runs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			run := &runs[i]
			lastID = run.ID
			observationCtx, cancel := context.WithTimeout(ctx, r.options.APITimeout)
			err := r.reconcileRun(observationCtx, jobs, run)
			cancel()
			if err != nil && ctx.Err() == nil {
				r.logger.Warn("Could not reconcile AgentRun", config.Int("agent_run_id", run.ID), config.Error(err))
			}
		}
	}
}

func (r *AgentRunReconciler) reconcileRun(ctx context.Context, jobs JobObserver, run *models.AgentRun) error {
	now := r.options.Now().UTC()
	snapshot, _ := run.ObservedLifecycle()
	var job *batchv1.Job
	var err error
	if run.JobName != nil && *run.JobName != "" {
		job, err = jobs.GetJob(ctx, *run.JobName)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		listed, listErr := jobs.ListJobs(ctx, fmt.Sprintf("agent-run-id=%d", run.ID))
		if listErr != nil {
			return listErr
		}
		var candidates []*batchv1.Job
		for i := range listed.Items {
			candidate := &listed.Items[i]
			attempt, hasAttempt := candidate.Labels["retry-count"]
			if (hasAttempt && attempt == strconv.Itoa(run.RetryCount)) || (!hasAttempt && run.RetryCount == 0) {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) > 1 {
			return fmt.Errorf("ambiguous jobs for attempt; leaving state unchanged")
		}
		if len(candidates) == 1 {
			job = candidates[0]
		}
	}
	if job == nil {
		// A legacy queued/no-name run may be waiting on dependencies. Its absence
		// alone is not evidence of a failed dispatch, so do not invent a failure.
		if run.State == "queued" && (run.JobName == nil || *run.JobName == "") {
			return nil
		}
		since := run.CreatedAt
		if run.StartedAt != nil {
			since = *run.StartedAt
		}
		if now.Before(since.Add(r.options.MissingJobGrace)) {
			return nil
		}
		name := run.AttemptJobName()
		if run.JobName != nil && *run.JobName != "" {
			name = *run.JobName
		}
		// Reconfirm exact absence immediately before the guarded terminal write.
		if _, err := jobs.GetJob(ctx, name); err == nil {
			return nil
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		return r.failObserved(ctx, run, snapshot, "AgentJobMissing", "Kubernetes Job is absent after dispatch grace; execution result is unknown", map[string]any{"job_name": name})
	}
	if job.Labels["agent-run-id"] != strconv.Itoa(run.ID) {
		return fmt.Errorf("job ownership does not match AgentRun")
	}
	if attempt, ok := job.Labels["retry-count"]; ok && attempt != strconv.Itoa(run.RetryCount) {
		return fmt.Errorf("job attempt does not match AgentRun")
	}
	// Recover a lost post-create metadata write. The CAS prevents a callback or
	// new retry from being overwritten while the Kubernetes lookup was in flight.
	if run.JobName == nil || *run.JobName == "" {
		result := repositories.LifecycleQuery(r.db.WithContext(ctx), run.ID, snapshot).Update("job_name", job.Name)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		run.JobName = &job.Name
		run.CaptureLifecycle()
		snapshot, _ = run.ObservedLifecycle()
	}
	var terminal *batchv1.JobCondition
	for i := range job.Status.Conditions {
		condition := &job.Status.Conditions[i]
		if condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobFailed || condition.Type == batchv1.JobComplete) {
			terminal = condition
			break
		}
	}
	if terminal == nil {
		if run.State == "queued" {
			started := job.CreationTimestamp.Time
			if started.IsZero() {
				started = now
			}
			return repositories.LifecycleQuery(r.db.WithContext(ctx), run.ID, snapshot).Updates(map[string]any{"state": "started", "started_at": started}).Error
		}
		return nil
	}
	terminalAt := terminal.LastTransitionTime.Time
	if terminalAt.IsZero() {
		key := fmt.Sprintf("%d/%d/%s/%s", run.ID, run.RetryCount, job.Name, job.UID)
		if first, ok := r.firstTerminal[key]; ok {
			terminalAt = first
		} else {
			r.firstTerminal[key] = now
			terminalAt = now
		}
	}
	if now.Before(terminalAt.Add(r.options.ReportGrace)) {
		return nil
	}
	evidence := map[string]any{"job_name": job.Name, "job_uid": string(job.UID), "terminal_condition": string(terminal.Type)}
	code := "AgentReportMissing"
	message := "Kubernetes Job completed without an accepted agent report; execution result is unknown"
	if terminal.Type == batchv1.JobFailed {
		code = "AgentJobFailed"
		message = "Kubernetes Job failed before an accepted agent report"
		switch terminal.Reason {
		case "DeadlineExceeded", "BackoffLimitExceeded", "PodFailurePolicy", "FailedIndexes", "MaxFailedIndexesExceeded":
			evidence["reason"] = terminal.Reason
		}
		// Use only structured termination evidence from Pods owned by this exact
		// Job UID. No Pod logs or arbitrary diagnostic messages are collected.
		if pods, err := jobs.ListPods(ctx, "job-name="+job.Name); err == nil {
			var terminations []map[string]any
			for _, pod := range pods.Items {
				owned := false
				for _, owner := range pod.OwnerReferences {
					if job.UID != "" && owner.UID == job.UID && owner.Kind == "Job" {
						owned = true
					}
				}
				if !owned {
					continue
				}
				statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
				for _, status := range statuses {
					if ended := status.State.Terminated; ended != nil {
						reason := "Terminated"
						switch ended.Reason {
						case "OOMKilled", "Error", "Completed", "ContainerCannotRun":
							reason = ended.Reason
						}
						terminations = append(terminations, map[string]any{"container": status.Name, "reason": reason, "exit_code": ended.ExitCode})
					}
				}
			}
			evidence["container_terminations"] = terminations
		}
	}
	return r.failObserved(ctx, run, snapshot, code, message, evidence)
}
func (r *AgentRunReconciler) failObserved(ctx context.Context, run *models.AgentRun, snapshot models.AgentRunLifecycle, code, message string, evidence map[string]any) error {
	evidence["schema_version"] = "1"
	evidence["status"] = "failed"
	evidence["error_code"] = code
	evidence["result_known"] = false
	output, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := repositories.LifecycleQuery(tx, run.ID, snapshot).Updates(map[string]any{"state": "failed", "completed_at": r.options.Now().UTC(), "error_message": code + ": " + message, "output": string(output)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		// Release only the review latch owned by this exact failed attempt.
		if run.ReviewFeedbackID != nil {
			query := tx.Model(&models.ReviewFeedback{}).Where("id = ? AND plan_creation_status = ?", *run.ReviewFeedbackID, "creating")
			if run.ExecutionMode == "plan_creation" {
				query = query.Where("plan_agent_run_id = ?", run.ID)
				if err := query.Update("plan_creation_status", "rejected").Error; err != nil {
					return err
				}
			}
			if run.ExecutionMode == "plan_execution" {
				query = query.Where("execution_agent_run_id = ?", run.ID)
				if err := query.Update("plan_creation_status", "created").Error; err != nil {
					return err
				}
			}
		}
		r.logger.Warn("AgentRun closed from Kubernetes evidence", config.Int("agent_run_id", run.ID), config.String("reason", code))
		return nil
	})
}
