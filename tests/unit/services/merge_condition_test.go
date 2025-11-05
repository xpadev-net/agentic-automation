package services

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

type fakeCI struct {
	state CIState
	err   error
}

func (f fakeCI) GetAggregatedState(ctx context.Context, owner, repo string, prNumber int) (CIState, error) {
	return f.state, f.err
}

type fakeCodex struct {
	approved bool
	err      error
}

func (f fakeCodex) IsApproved(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	return f.approved, f.err
}

type fakeConflict struct {
	status MergeConflictStatus
	err    error
}

func (f fakeConflict) Detect(ctx context.Context, owner, repo string, prNumber int) (MergeConflictStatus, error) {
	return f.status, f.err
}

func TestMergeCondition_Satisfied(t *testing.T) {
	checker := NewMergeConditionChecker(
		fakeCI{state: CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: MergeConflictStatusNoConflict},
		zap.NewNop(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Mergeable {
		t.Fatalf("expected mergeable=true, got false; reasons=%v", res.Reasons)
	}
}

func TestMergeCondition_PendingCI(t *testing.T) {
	checker := NewMergeConditionChecker(
		fakeCI{state: CIStatePending},
		fakeCodex{approved: true},
		fakeConflict{status: MergeConflictStatusNoConflict},
		zap.NewNop(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mergeable {
		t.Fatalf("expected mergeable=false, got true")
	}
	if res.CIState != CIStatePending {
		t.Fatalf("expected CIState=pending, got %s", res.CIState)
	}
}
