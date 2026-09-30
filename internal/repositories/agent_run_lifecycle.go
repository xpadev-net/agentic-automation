package repositories

import (
	"agentic-automation/internal/models"
	"gorm.io/datatypes"
	"time"
)

// AtomicAgentRunLifecycle is optional so existing test fakes remain usable.
// The production repository always implements this single-write transition.
type AtomicAgentRunLifecycle interface {
	TransitionLifecycle(id int, state string, prID *int, commitSHA, errorMessage *string) error
}

func (r *agentRunRepository) TransitionLifecycle(id int, state string, prID *int, commitSHA, errorMessage *string) error {
	run, err := r.GetByID(id)
	if err != nil {
		return err
	}
	snapshot, _ := run.ObservedLifecycle()
	now := time.Now().UTC()
	updates := map[string]interface{}{"state": state}
	if run.State == state {
		// Idempotent transition preserves original terminal timestamps.
		if prID != nil {
			updates["pr_id"] = *prID
		}
		if commitSHA != nil {
			updates["commit_sha"] = *commitSHA
		}
		if errorMessage != nil {
			updates["error_message"] = *errorMessage
		}
	} else {
		allowed := run.State == "queued" && state == "started" || run.State == "started" && (state == "failed" || state == "succeeded" || state == "queued")
		if !allowed {
			return &ErrInvalidStateTransition{CurrentState: run.State, NewState: state}
		}
		switch state {
		case "started":
			updates["started_at"] = now
		case "queued":
			updates["started_at"] = nil
		case "succeeded", "failed":
			updates["completed_at"] = now
		}
		if prID != nil {
			updates["pr_id"] = *prID
			run.PRID = prID
		}
		if commitSHA != nil {
			updates["commit_sha"] = *commitSHA
		}
		if errorMessage != nil {
			updates["error_message"] = *errorMessage
		}
	}
	if state == "succeeded" && run.ExecutionMode != "plan_creation" && run.PRID == nil {
		return ErrMissingPRIDForSucceeded
	}
	result := LifecycleQuery(r.db, id, snapshot).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		current, err := r.GetByID(id)
		if err != nil {
			return err
		}
		if current.State != state || current.RetryCount != snapshot.RetryCount {
			return ErrConcurrentLifecycle
		}
	}
	return nil
}

// AtomicRetryAdmission reserves one next attempt with its timestamp and identity
// in the same conditional write. Concurrent callers cannot increment twice.
type AtomicRetryAdmission interface {
	BeginRetry(run *models.AgentRun, maxRetries int) (*models.AgentRun, error)
}

func (r *agentRunRepository) BeginRetry(run *models.AgentRun, maxRetries int) (*models.AgentRun, error) {
	if run.State != "failed" && run.State != "succeeded" {
		return nil, ErrConcurrentLifecycle
	}
	next := *run
	next.RetryCount++
	now := time.Now().UTC()
	if next.RetryCount >= maxRetries {
		next.State = "failed"
		next.CompletedAt = &now
	} else {
		next.State = "started"
		next.StartedAt = &now
		next.CompletedAt = nil
		next.ErrorMessage = nil
		next.Output = datatypes.JSON("{}")
		name := next.AttemptJobName()
		next.JobName = &name
	}
	if err := r.Update(&next); err != nil {
		return nil, err
	}
	return &next, nil
}
