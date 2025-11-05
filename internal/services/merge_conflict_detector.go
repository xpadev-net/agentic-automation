package services

import (
	"context"
	"time"

	"agentic-automation/internal/clients"
	"go.uber.org/zap"
)

// MergeConflictStatus represents merge conflict evaluation result for a PR.
type MergeConflictStatus string

const (
	// MergeConflictStatusUnknown indicates GitHub is still computing mergeability
	// or it could not be determined within a short retry window.
	MergeConflictStatusUnknown MergeConflictStatus = "unknown"
	// MergeConflictStatusNoConflict indicates no merge conflict is detected.
	MergeConflictStatusNoConflict MergeConflictStatus = "no_conflict"
	// MergeConflictStatusHasConflict indicates a merge conflict is present.
	MergeConflictStatusHasConflict MergeConflictStatus = "has_conflict"
)

// MergeConflictDetector provides conflict detection for a PR.
type MergeConflictDetector interface {
	Detect(ctx context.Context, owner, repo string, prNumber int) (MergeConflictStatus, error)
}

type mergeConflictDetector struct {
	gh     *clients.Client
	logger *zap.Logger
}

// NewMergeConflictDetector creates a new detector.
// Intended usage (from T108/T111):
//
//	status, err := detector.Detect(ctx, owner, repo, prNumber)
//	if err != nil { /* handle/report error, skip re-evaluation */ }
//	switch status {
//	case MergeConflictStatusHasConflict:
//	    // treat as not mergeable due to conflicts
//	case MergeConflictStatusNoConflict:
//	    // conflicts cleared; other conditions to be checked by T108
//	case MergeConflictStatusUnknown:
//	    // re-evaluate on subsequent webhook events (status/check_suite/synchronize)
//	}
func NewMergeConflictDetector(gh *clients.Client, logger *zap.Logger) MergeConflictDetector {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &mergeConflictDetector{gh: gh, logger: logger}
}

// Detect checks whether a PR has merge conflicts.
// Logic:
//   - If mergeable == nil (GitHub computing), retry briefly (0.5s, 1s, 2s). If still nil -> unknown
//   - If mergeable == false -> has_conflict
//   - If mergeable == true -> use mergeable_state for hints:
//     dirty => has_conflict
//     clean/unstable/blocked/unknown => no_conflict (conflicts are not the reason)
func (d *mergeConflictDetector) Detect(ctx context.Context, owner, repo string, prNumber int) (MergeConflictStatus, error) {
	d.logger.Info("merge conflict detection started",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
	)

	// Local helper to fetch PR
	fetch := func() (*bool, *string, error) {
		pr, err := d.gh.GetPullRequest(ctx, owner, repo, prNumber)
		if err != nil {
			return nil, nil, err
		}
		return pr.Mergeable, pr.MergeableState, nil
	}

	mergeable, state, err := fetch()
	if err != nil {
		d.logger.Error("failed to get pull request for conflict detection", zap.Error(err))
		return MergeConflictStatusUnknown, err
	}

	// Retry when GitHub is still computing mergeability
	if mergeable == nil {
		backoffs := []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}
		for i, b := range backoffs {
			d.logger.Debug("mergeable is nil; waiting and retrying",
				zap.Int("attempt", i+1),
				zap.Duration("sleep", b),
			)
			select {
			case <-ctx.Done():
				d.logger.Warn("context cancelled during mergeable computation wait", zap.Error(ctx.Err()))
				return MergeConflictStatusUnknown, ctx.Err()
			case <-time.After(b):
			}
			mergeable, state, err = fetch()
			if err != nil {
				d.logger.Error("failed to re-fetch pull request for conflict detection", zap.Error(err))
				return MergeConflictStatusUnknown, err
			}
			if mergeable != nil {
				break
			}
		}
		if mergeable == nil {
			d.logger.Info("mergeable remains unknown after retries")
			return MergeConflictStatusUnknown, nil
		}
	}

	// Primary decision by mergeable flag
	if !*mergeable {
		// When mergeable=false, GitHub indicates PR cannot be merged now for any reason
		// (e.g., failing checks, review required, draft, behind, or conflicts).
		// We only classify as conflict when mergeable_state explicitly says "dirty".
		if state == nil {
			d.logger.Info("mergeable=false with state=nil -> unknown")
			return MergeConflictStatusUnknown, nil
		}
		s := *state
		switch s {
		case "dirty":
			d.logger.Info("merge conflict detected via mergeable_state=dirty (mergeable=false)")
			return MergeConflictStatusHasConflict, nil
		case "blocked", "behind", "unstable", "draft", "has_hooks", "unknown":
			d.logger.Info("mergeable=false but non-conflict state",
				zap.String("mergeable_state", s),
			)
			return MergeConflictStatusNoConflict, nil
		default:
			// Any other undocumented state -> treat as non-conflict blocker
			d.logger.Info("mergeable=false with unrecognized state treated as non-conflict",
				zap.String("mergeable_state", s),
			)
			return MergeConflictStatusNoConflict, nil
		}
	}

	// mergeable == true; refine with state when available
	if state != nil {
		s := *state
		switch s {
		case "dirty":
			d.logger.Info("merge conflict detected via mergeable_state=dirty")
			return MergeConflictStatusHasConflict, nil
		case "clean", "unstable", "blocked", "behind", "draft", "has_hooks", "unknown":
			d.logger.Info("no merge conflict detected",
				zap.String("mergeable_state", s),
			)
			return MergeConflictStatusNoConflict, nil
		default:
			// Any other undocumented state -> treat as no explicit conflict
			d.logger.Info("no merge conflict detected (unrecognized state treated as non-conflict)",
				zap.String("mergeable_state", s),
			)
			return MergeConflictStatusNoConflict, nil
		}
	}

	// No state available; mergeable==true implies no conflicts
	d.logger.Info("no merge conflict detected (mergeable=true, state=nil)")
	return MergeConflictStatusNoConflict, nil
}
