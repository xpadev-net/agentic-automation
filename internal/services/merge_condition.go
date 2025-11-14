package services

import (
	"agentic-automation/internal/config"
	"context"
)

// CIState represents aggregated CI state for a PR.
// Expected values are mapped from check runs aggregation.
type CIState string

const (
	CIStateSuccess CIState = "success"
	CIStateFailed  CIState = "failed"
	CIStatePending CIState = "pending"
	CIStateUnknown CIState = "unknown"
)

// MergeConditionResult is the outcome of merge gate evaluation.
type MergeConditionResult struct {
	Mergeable     bool
	CIState       CIState
	CodexApproved bool
	Conflict      MergeConflictStatus
	Reasons       []string
}

// MergeConditionChecker provides a single entrypoint to evaluate merge readiness.
type MergeConditionChecker interface {
	Check(ctx context.Context, owner, repo string, prNumber int) (MergeConditionResult, error)
}

// CIStatusProvider abstracts how CI state is obtained for a PR.
// Implementation can read from DB aggregation or call GitHub directly.
type CIStatusProvider interface {
	GetAggregatedState(ctx context.Context, owner, repo string, prNumber int) (CIState, error)
}

// CodexApprovalChecker abstracts Codex approval determination for a PR.
type CodexApprovalChecker interface {
	IsApproved(ctx context.Context, owner, repo string, prNumber int) (bool, error)
}

type mergeConditionChecker struct {
	ci        CIStatusProvider
	codex     CodexApprovalChecker
	conflicts MergeConflictDetector
	logger    *config.AppLogger
}

// NewMergeConditionChecker constructs a new MergeConditionChecker.
func NewMergeConditionChecker(ci CIStatusProvider, codex CodexApprovalChecker, conflicts MergeConflictDetector, logger *config.AppLogger) MergeConditionChecker {
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &mergeConditionChecker{ci: ci, codex: codex, conflicts: conflicts, logger: logger}
}

// Check evaluates CI, Codex approval and conflicts to determine mergeability.
// Mergeable = (CI == success) && CodexApproved && (Conflict == no_conflict)
// Any unknown/pending/error leads to Mergeable=false with Reasons populated.
func (m *mergeConditionChecker) Check(ctx context.Context, owner, repo string, prNumber int) (MergeConditionResult, error) {
	result := MergeConditionResult{
		Mergeable:     false,
		CIState:       CIStateUnknown,
		CodexApproved: false,
		Conflict:      MergeConflictStatusUnknown,
		Reasons:       nil,
	}

	// CI state
	ciState, err := m.ci.GetAggregatedState(ctx, owner, repo, prNumber)
	if err != nil {
		m.logger.Warn("failed to get aggregated CI state", config.Error(err), config.String("owner", owner), config.String("repo", repo), config.Int("pr_number", prNumber))
		result.Reasons = append(result.Reasons, "ci_state_error")
	}
	if ciState == "" {
		ciState = CIStateUnknown
	}
	result.CIState = ciState

	// Codex approval
	approved, err := m.codex.IsApproved(ctx, owner, repo, prNumber)
	if err != nil {
		m.logger.Warn("failed to get Codex approval", config.Error(err), config.String("owner", owner), config.String("repo", repo), config.Int("pr_number", prNumber))
		result.Reasons = append(result.Reasons, "codex_approval_error")
	}
	result.CodexApproved = approved

	// Conflict status
	conflict, err := m.conflicts.Detect(ctx, owner, repo, prNumber)
	if err != nil {
		m.logger.Warn("failed to detect merge conflicts", config.Error(err), config.String("owner", owner), config.String("repo", repo), config.Int("pr_number", prNumber))
		// Guard against stale non-conflict status leaking through on error
		conflict = MergeConflictStatusUnknown
		result.Reasons = append(result.Reasons, "conflict_detection_error")
	}
	if conflict == "" {
		conflict = MergeConflictStatusUnknown
	}
	result.Conflict = conflict

	// Final decision
	mergeable := (result.CIState == CIStateSuccess) && result.CodexApproved && (result.Conflict == MergeConflictStatusNoConflict)
	if !mergeable {
		// Populate human-understandable reasons when possible
		switch result.CIState {
		case CIStateFailed:
			result.Reasons = append(result.Reasons, "ci_failed")
		case CIStatePending, CIStateUnknown:
			result.Reasons = append(result.Reasons, "ci_not_green")
		}
		if !result.CodexApproved {
			result.Reasons = append(result.Reasons, "codex_not_approved")
		}
		switch result.Conflict {
		case MergeConflictStatusHasConflict:
			result.Reasons = append(result.Reasons, "has_conflict")
		case MergeConflictStatusUnknown:
			result.Reasons = append(result.Reasons, "conflict_unknown")
		}
		m.logger.Info("merge conditions not satisfied",
			config.String("ci", string(result.CIState)),
			config.Bool("codex", result.CodexApproved),
			config.String("conflict", string(result.Conflict)),
			config.Int("reasons_count", len(result.Reasons)),
		)
		return result, nil
	}

	result.Mergeable = true
	m.logger.Info("merge conditions satisfied",
		config.String("ci", string(result.CIState)),
		config.Bool("codex", result.CodexApproved),
		config.String("conflict", string(result.Conflict)),
	)
	return result, nil
}

// Usage:
//   checker := NewMergeConditionChecker(ciProvider, codexChecker, conflictDetector, logger)
//   res, err := checker.Check(ctx, owner, repo, prNumber)
//   if err != nil { /* handle error */ }
//   if res.Mergeable { /* proceed to auto-merge */ }
//
// 前提:
// - CIStatusProvider は PR 単位の最新 head に対する集約状態（success/failed/pending/unknown）を返すこと。
// - CodexApprovalChecker は Codex bot の承認有無を返すこと（実装はUS3の成果を再利用可能）。
// - MergeConflictDetector は GitHub API から mergeable/mergeable_state を用いて競合有無を判定する。
