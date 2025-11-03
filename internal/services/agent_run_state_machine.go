package services

import (
	"errors"
	"time"

	"agentic-automation/internal/repositories"
	"go.uber.org/zap"
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
}

// agentRunStateMachine implements AgentRunStateMachine interface
type agentRunStateMachine struct {
	repo   repositories.AgentRunRepository
	logger *zap.Logger
}

// NewAgentRunStateMachine creates a new AgentRunStateMachine instance.
// It requires an AgentRunRepository and an optional logger.
//
// Parameters:
//   - repo: AgentRunRepository instance (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - AgentRunStateMachine: Initialized service instance
func NewAgentRunStateMachine(repo repositories.AgentRunRepository, logger *zap.Logger) AgentRunStateMachine {
	if repo == nil {
		panic("repo is required for AgentRunStateMachine")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &agentRunStateMachine{
		repo:   repo,
		logger: logger,
	}
}

// TransitionToStarted transitions an AgentRun from "queued" to "started" state
// and sets the StartedAt timestamp.
func (s *agentRunStateMachine) TransitionToStarted(id int) error {
	s.logger.Info("Transitioning AgentRun to started state",
		zap.Int("agent_run_id", id),
		zap.String("current_state", "queued"),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for state transition",
				zap.Int("agent_run_id", id),
				zap.String("target_state", "started"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun",
			zap.Int("agent_run_id", id),
			zap.Error(err),
		)
		return err
	}

	// Check if already in target state - avoid clobbering timestamps on idempotent transitions
	if run.State == "started" && run.StartedAt != nil {
		s.logger.Info("AgentRun already in started state, skipping idempotent transition",
			zap.Int("agent_run_id", id),
			zap.Time("existing_started_at", *run.StartedAt),
		)
		return nil
	}

	// Update state using repository's UpdateState method
	// This validates the state transition (queued -> started)
	if err := s.repo.UpdateState(id, "started"); err != nil {
		s.logger.Warn("Failed to transition AgentRun state",
			zap.Int("agent_run_id", id),
			zap.String("from_state", run.State),
			zap.String("to_state", "started"),
			zap.Error(err),
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
				zap.Int("agent_run_id", id),
				zap.Error(err),
			)
			return err
		}

		s.logger.Info("AgentRun successfully transitioned to started state",
			zap.Int("agent_run_id", id),
			zap.Time("started_at", now),
		)
	} else {
		s.logger.Info("AgentRun successfully transitioned to started state (timestamp already set)",
			zap.Int("agent_run_id", id),
			zap.Time("started_at", *run.StartedAt),
		)
	}

	return nil
}

// TransitionToSucceeded transitions an AgentRun from "started" to "succeeded" state,
// sets the CompletedAt timestamp, and optionally updates PRID and CommitSHA.
func (s *agentRunStateMachine) TransitionToSucceeded(id int, prID *int, commitSHA *string) error {
	s.logger.Info("Transitioning AgentRun to succeeded state",
		zap.Int("agent_run_id", id),
		zap.String("current_state", "started"),
		zap.Any("pr_id", prID),
		zap.Any("commit_sha", commitSHA),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for state transition",
				zap.Int("agent_run_id", id),
				zap.String("target_state", "succeeded"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun",
			zap.Int("agent_run_id", id),
			zap.Error(err),
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
					zap.Int("agent_run_id", id),
					zap.Error(err),
				)
				return err
			}
			s.logger.Info("AgentRun already in succeeded state, updated metadata only",
				zap.Int("agent_run_id", id),
				zap.Time("existing_completed_at", *run.CompletedAt),
			)
		} else {
			s.logger.Info("AgentRun already in succeeded state, skipping idempotent transition",
				zap.Int("agent_run_id", id),
				zap.Time("existing_completed_at", *run.CompletedAt),
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
				zap.Int("agent_run_id", id),
				zap.Error(err),
			)
			return err
		}
	}

	// Update state using repository's UpdateState method
	// This validates the state transition (started -> succeeded) and checks for pr_id
	if err := s.repo.UpdateState(id, "succeeded"); err != nil {
		s.logger.Warn("Failed to transition AgentRun state",
			zap.Int("agent_run_id", id),
			zap.String("from_state", run.State),
			zap.String("to_state", "succeeded"),
			zap.Error(err),
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
				zap.Int("agent_run_id", id),
				zap.Error(err),
			)
			return err
		}

		s.logger.Info("AgentRun successfully transitioned to succeeded state",
			zap.Int("agent_run_id", id),
			zap.Time("completed_at", now),
			zap.Any("pr_id", prID),
		)
	} else {
		s.logger.Info("AgentRun successfully transitioned to succeeded state (timestamp already set)",
			zap.Int("agent_run_id", id),
			zap.Time("completed_at", *run.CompletedAt),
			zap.Any("pr_id", prID),
		)
	}

	return nil
}

// TransitionToFailed transitions an AgentRun from "started" to "failed" state,
// sets the CompletedAt timestamp, and optionally sets an error message.
func (s *agentRunStateMachine) TransitionToFailed(id int, errorMessage *string) error {
	s.logger.Info("Transitioning AgentRun to failed state",
		zap.Int("agent_run_id", id),
		zap.String("current_state", "started"),
		zap.Any("error_message", errorMessage),
	)

	// Get current AgentRun to verify it exists
	run, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warn("AgentRun not found for state transition",
				zap.Int("agent_run_id", id),
				zap.String("target_state", "failed"),
			)
			return err
		}
		s.logger.Error("Failed to retrieve AgentRun",
			zap.Int("agent_run_id", id),
			zap.Error(err),
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
					zap.Int("agent_run_id", id),
					zap.Error(err),
				)
				return err
			}
			s.logger.Info("AgentRun already in failed state, updated error message only",
				zap.Int("agent_run_id", id),
				zap.Time("existing_completed_at", *run.CompletedAt),
			)
		} else {
			s.logger.Info("AgentRun already in failed state, skipping idempotent transition",
				zap.Int("agent_run_id", id),
				zap.Time("existing_completed_at", *run.CompletedAt),
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
			zap.Int("agent_run_id", id),
			zap.String("from_state", run.State),
			zap.String("to_state", "failed"),
			zap.Error(err),
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
				zap.Int("agent_run_id", id),
				zap.Error(err),
			)
			return err
		}

		s.logger.Info("AgentRun successfully transitioned to failed state",
			zap.Int("agent_run_id", id),
			zap.Time("completed_at", now),
			zap.Bool("has_error_message", errorMessage != nil),
		)
	} else {
		// Save error message update if needed (timestamp already set)
		if errorMessage != nil {
			if err := s.repo.Update(run); err != nil {
				s.logger.Error("Failed to update AgentRun ErrorMessage",
					zap.Int("agent_run_id", id),
					zap.Error(err),
				)
				return err
			}
		}

		s.logger.Info("AgentRun successfully transitioned to failed state (timestamp already set)",
			zap.Int("agent_run_id", id),
			zap.Time("completed_at", *run.CompletedAt),
			zap.Bool("has_error_message", errorMessage != nil),
		)
	}

	return nil
}
