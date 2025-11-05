package services_test

import (
	"context"
	"errors"
	"testing"

	"agentic-automation/internal/services"
	"go.uber.org/zap"
)

type fakeCI struct {
	state services.CIState
	err   error
}

func (f fakeCI) GetAggregatedState(ctx context.Context, owner, repo string, prNumber int) (services.CIState, error) {
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
	status services.MergeConflictStatus
	err    error
}

func (f fakeConflict) Detect(ctx context.Context, owner, repo string, prNumber int) (services.MergeConflictStatus, error) {
	return f.status, f.err
}

func TestMergeCondition_Satisfied(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
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
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStatePending},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		zap.NewNop(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mergeable {
		t.Fatalf("expected mergeable=false, got true")
	}
	if res.CIState != services.CIStatePending {
		t.Fatalf("expected CIState=pending, got %s", res.CIState)
	}
}

func TestMergeCondition_ConflictDetectError_BlocksMerge(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict, err: errors.New("boom")},
		zap.NewNop(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mergeable {
		t.Fatalf("expected mergeable=false due to conflict detection error, got true")
	}
	if res.Conflict != services.MergeConflictStatusUnknown {
		t.Fatalf("expected conflict=unknown, got %s", res.Conflict)
	}
}
