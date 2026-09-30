package services

import (
	"errors"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"

	"gorm.io/gorm"
)

// AgentRunStateMachine provides methods to transition AgentRun states
// with automatic timestamp updates (StartedAt, CompletedAt).
type AgentRunStateMachine interface {
	// TransitionToStarted transitions an AgentRun from "queued" to "started" state
	// and sets the StartedAt timestamp.
	TransitionToStarted(id int) error

	// TransitionToSucceeded transitions an AgentRun from "started" to "succeeded" state,
	// sets the CompletedAt timestamp, and optionally updates PRID and CommitSHA.
	TransitionToSucceeded(id int, prID *int, commitSHA *string) error

	// TransitionToFailed transitions an AgentRun from "started" to "failed" state,
	// sets the CompletedAt timestamp, and optionally sets an error message.
	TransitionToFailed(id int, errorMessage *string) error

	// TransitionToQueued rolls back an AgentRun from "started" to "queued" state
	// and clears the StartedAt timestamp. This is used for retry scenarios when
	// job creation fails after transitioning to started.
	TransitionToQueued(id int) error
}

// agentRunStateMachine implements AgentRunStateMachine interface
type agentRunStateMachine struct {
	repo   repositories.AgentRunRepository
	logger *config.AppLogger
}

// NewAgentRunStateMachine creates a new AgentRunStateMachine instance.
// It requires an AgentRunRepository and an optional logger.
//
// Parameters:
//   - repo: AgentRunRepository instance (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses config.NewNopLogger())
//
// Returns:
//   - AgentRunStateMachine: Initialized service instance
func NewAgentRunStateMachine(repo repositories.AgentRunRepository, logger *config.AppLogger) AgentRunStateMachine {
	if repo == nil {
		panic("repo is required for AgentRunStateMachine")
	}

	// Use config.NewNopLogger() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = config.NewNopLogger()
	}

	return &agentRunStateMachine{
		repo:   repo,
		logger: logger,
	}
}

// TransitionToStarted transitions an AgentRun from "queued" to "started" state
// and sets the StartedAt timestamp.
func (s *agentRunStateMachine) TransitionToStarted(id int) error {
	if atomic, ok := s.repo.(repositories.AtomicAgentRunLifecycle); ok {
		return atomic.TransitionLifecycle(id, "started", nil, nil, nil)
	}
	s.logger.Info("Transitioning AgentRun to started state",
		config.Int("agent_run_id", id),
		config.String("current_state", "queued"),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for state transition",
				config.Int("agent_run_id", id),
				config.String("target_state", "started"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun",
			config.Int("agent_run_id", id),
			config.Error(err),
		)
		return err
	}

	// Check if already in target state - avoid clobbering timestamps on idempotent transitions
	if run.State == "started" && run.StartedAt != nil {
		s.logger.Info("AgentRun already in started state, skipping idempotent transition",
			config.Int("agent_run_id", id),
			config.Time("existing_started_at", *run.StartedAt),
		)
		return nil
	}

	// Update state using repository's UpdateState method
	// This validates the state transition (queued -> started)
	if err := s.repo.UpdateState(id, "started"); err != nil {
		s.logger.Warn("Failed to transition AgentRun state",
			config.Int("agent_run_id", id),
			config.String("from_state", run.State),
			config.String("to_state", "started"),
			config.Error(err),
		)
		return err
	}

	// Update the struct's State field to match the new state
	// This prevents Update() from reverting the state transition
	run.State = "started"

	// Set StartedAt timestamp only if not already set
	if run.StartedAt == nil {
		now := time.Now()
		run.StartedAt = &now

		// Save the timestamp update
		if err := s.repo.Update(run); err != nil {
			s.logger.Error("Failed to update AgentRun StartedAt timestamp",
				config.Int("agent_run_id", id),
				config.Error(err),
			)
			return err
		}

		s.logger.Info("AgentRun successfully transitioned to started state",
			config.Int("agent_run_id", id),
			config.Time("started_at", now),
		)
	} else {
		s.logger.Info("AgentRun successfully transitioned to started state (timestamp already set)",
			config.Int("agent_run_id", id),
			config.Time("started_at", *run.StartedAt),
		)
	}

	return nil
}

// TransitionToSucceeded transitions an AgentRun from "started" to "succeeded" state,
// sets the CompletedAt timestamp, and optionally updates PRID and CommitSHA.
func (s *agentRunStateMachine) TransitionToSucceeded(id int, prID *int, commitSHA *string) error {
	if atomic, ok := s.repo.(repositories.AtomicAgentRunLifecycle); ok {
		return atomic.TransitionLifecycle(id, "succeeded", prID, commitSHA, nil)
	}
	s.logger.Info("Transitioning AgentRun to succeeded state",
		config.Int("agent_run_id", id),
		config.String("current_state", "started"),
		config.Any("pr_id", prID),
		config.Any("commit_sha", commitSHA),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for state transition",
				config.Int("agent_run_id", id),
				config.String("target_state", "succeeded"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun",
			config.Int("agent_run_id", id),
			config.Error(err),
		)
		return err
	}

	// Check if already in target state - avoid clobbering timestamps on idempotent transitions
	if run.State == "succeeded" && run.CompletedAt != nil {
		// Update PRID/CommitSHA if provided and different from current values
		needsUpdate := false
		if prID != nil && (run.PRID == nil || *run.PRID != *prID) {
			run.PRID = prID
			needsUpdate = true
		}
		if commitSHA != nil && (run.CommitSHA == nil || *run.CommitSHA != *commitSHA) {
			run.CommitSHA = commitSHA
			needsUpdate = true
		}

		if needsUpdate {
			if err := s.repo.Update(run); err != nil {
				s.logger.Error("Failed to update AgentRun PRID/CommitSHA in idempotent transition",
					config.Int("agent_run_id", id),
					config.Error(err),
				)
				return err
			}
			s.logger.Info("AgentRun already in succeeded state, updated metadata only",
				config.Int("agent_run_id", id),
				config.Time("existing_completed_at", *run.CompletedAt),
			)
		} else {
			s.logger.Info("AgentRun already in succeeded state, skipping idempotent transition",
				config.Int("agent_run_id", id),
				config.Time("existing_completed_at", *run.CompletedAt),
			)
		}
		return nil
	}

	// Update PRID and CommitSHA if provided (before state transition)
	if prID != nil {
		run.PRID = prID
	}
	if commitSHA != nil {
		run.CommitSHA = commitSHA
	}

	// Save PRID/CommitSHA updates before state transition
	// This ensures pr_id is set when UpdateState validates it for succeeded state
	if prID != nil || commitSHA != nil {
		if err := s.repo.Update(run); err != nil {
			s.logger.Error("Failed to update AgentRun PRID/CommitSHA",
				config.Int("agent_run_id", id),
				config.Error(err),
			)
			return err
		}
	}

	// Update state using repository's UpdateState method
	// This validates the state transition (started -> succeeded) and checks for pr_id
	if err := s.repo.UpdateState(id, "succeeded"); err != nil {
		s.logger.Warn("Failed to transition AgentRun state",
			config.Int("agent_run_id", id),
			config.String("from_state", run.State),
			config.String("to_state", "succeeded"),
			config.Error(err),
		)
		return err
	}

	// Update the struct's State field to match the new state
	// This prevents Update() from reverting the state transition
	run.State = "succeeded"

	// Set CompletedAt timestamp only if not already set
	if run.CompletedAt == nil {
		now := time.Now()
		run.CompletedAt = &now

		// Save the timestamp update
		if err := s.repo.Update(run); err != nil {
			s.logger.Error("Failed to update AgentRun CompletedAt timestamp",
				config.Int("agent_run_id", id),
				config.Error(err),
			)
			return err
		}

		s.logger.Info("AgentRun successfully transitioned to succeeded state",
			config.Int("agent_run_id", id),
			config.Time("completed_at", now),
			config.Any("pr_id", prID),
		)
	} else {
		s.logger.Info("AgentRun successfully transitioned to succeeded state (timestamp already set)",
			config.Int("agent_run_id", id),
			config.Time("completed_at", *run.CompletedAt),
			config.Any("pr_id", prID),
		)
	}

	return nil
}

// TransitionToFailed transitions an AgentRun from "started" to "failed" state,
// sets the CompletedAt timestamp, and optionally sets an error message.
func (s *agentRunStateMachine) TransitionToFailed(id int, errorMessage *string) error {
	if atomic, ok := s.repo.(repositories.AtomicAgentRunLifecycle); ok {
		return atomic.TransitionLifecycle(id, "failed", nil, nil, errorMessage)
	}
	s.logger.Info("Transitioning AgentRun to failed state",
		config.Int("agent_run_id", id),
		config.String("current_state", "started"),
		config.Any("error_message", errorMessage),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for state transition",
				config.Int("agent_run_id", id),
				config.String("target_state", "failed"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun",
			config.Int("agent_run_id", id),
			config.Error(err),
		)
		return err
	}

	// Check if already in target state - avoid clobbering timestamps on idempotent transitions
	if run.State == "failed" && run.CompletedAt != nil {
		// Update ErrorMessage if provided and different from current value
		needsUpdate := false
		if errorMessage != nil && (run.ErrorMessage == nil || *run.ErrorMessage != *errorMessage) {
			run.ErrorMessage = errorMessage
			needsUpdate = true
		}

		if needsUpdate {
			if err := s.repo.Update(run); err != nil {
				s.logger.Error("Failed to update AgentRun ErrorMessage in idempotent transition",
					config.Int("agent_run_id", id),
					config.Error(err),
				)
				return err
			}
			s.logger.Info("AgentRun already in failed state, updated error message only",
				config.Int("agent_run_id", id),
				config.Time("existing_completed_at", *run.CompletedAt),
			)
		} else {
			s.logger.Info("AgentRun already in failed state, skipping idempotent transition",
				config.Int("agent_run_id", id),
				config.Time("existing_completed_at", *run.CompletedAt),
			)
		}
		return nil
	}

	// Set error message if provided
	if errorMessage != nil {
		run.ErrorMessage = errorMessage
	}

	// Update state using repository's UpdateState method
	// This validates the state transition (started -> failed)
	if err := s.repo.UpdateState(id, "failed"); err != nil {
		s.logger.Warn("Failed to transition AgentRun state",
			config.Int("agent_run_id", id),
			config.String("from_state", run.State),
			config.String("to_state", "failed"),
			config.Error(err),
		)
		return err
	}

	// Update the struct's State field to match the new state
	// This prevents Update() from reverting the state transition
	run.State = "failed"

	// Set CompletedAt timestamp only if not already set
	if run.CompletedAt == nil {
		now := time.Now()
		run.CompletedAt = &now

		// Save the timestamp and error message (if set)
		if err := s.repo.Update(run); err != nil {
			s.logger.Error("Failed to update AgentRun CompletedAt timestamp and error message",
				config.Int("agent_run_id", id),
				config.Error(err),
			)
			return err
		}

		s.logger.Info("AgentRun successfully transitioned to failed state",
			config.Int("agent_run_id", id),
			config.Time("completed_at", now),
			config.Bool("has_error_message", errorMessage != nil),
		)
	} else {
		// Save error message update if needed (timestamp already set)
		if errorMessage != nil {
			if err := s.repo.Update(run); err != nil {
				s.logger.Error("Failed to update AgentRun ErrorMessage",
					config.Int("agent_run_id", id),
					config.Error(err),
				)
				return err
			}
		}

		s.logger.Info("AgentRun successfully transitioned to failed state (timestamp already set)",
			config.Int("agent_run_id", id),
			config.Time("completed_at", *run.CompletedAt),
			config.Bool("has_error_message", errorMessage != nil),
		)
	}

	return nil
}

// TransitionToQueued rolls back an AgentRun from "started" to "queued" state
// and clears the StartedAt timestamp. This is used for retry scenarios when
// job creation fails after transitioning to started.
// It directly updates the state using Update() instead of UpdateState() to
// bypass normal state transition validation.
func (s *agentRunStateMachine) TransitionToQueued(id int) error {
	if atomic, ok := s.repo.(repositories.AtomicAgentRunLifecycle); ok {
		return atomic.TransitionLifecycle(id, "queued", nil, nil, nil)
	}
	s.logger.Info("Rolling back AgentRun to queued state",
		config.Int("agent_run_id", id),
		config.String("current_state", "started"),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for rollback",
				config.Int("agent_run_id", id),
				config.String("target_state", "queued"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun for rollback",
			config.Int("agent_run_id", id),
			config.Error(err),
		)
		return err
	}

	// Only allow rollback from "started" state
	if run.State != "started" {
		s.logger.Warn("Cannot rollback AgentRun - not in started state",
			config.Int("agent_run_id", id),
			config.String("current_state", run.State),
			config.String("target_state", "queued"),
		)
		return errors.New("cannot rollback AgentRun: not in started state")
	}

	// Update state to queued and clear StartedAt
	run.State = "queued"
	run.StartedAt = nil

	// Save the rollback using Update() directly (bypasses UpdateState validation)
	if err := s.repo.Update(run); err != nil {
		s.logger.Error("Failed to rollback AgentRun to queued state",
			config.Int("agent_run_id", id),
			config.Error(err),
		)
		return err
	}

	s.logger.Info("AgentRun successfully rolled back to queued state",
		config.Int("agent_run_id", id),
	)

	return nil
}
