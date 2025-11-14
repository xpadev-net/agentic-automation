package services_test

import (
	"context"
	"errors"
	"testing"

	"agentic-automation/internal/config"
	"agentic-automation/internal/services"

	"github.com/stretchr/testify/require"
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
		config.NewNopLogger(),
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
		config.NewNopLogger(),
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
		config.NewNopLogger(),
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

// CI状態のバリエーション

func TestMergeCondition_CIFailed(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateFailed},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateFailed, res.CIState)
	require.True(t, res.CodexApproved)
	require.Equal(t, services.MergeConflictStatusNoConflict, res.Conflict)
	require.Contains(t, res.Reasons, "ci_failed")
	require.NotContains(t, res.Reasons, "codex_not_approved")
	require.NotContains(t, res.Reasons, "has_conflict")
}

func TestMergeCondition_CIUnknown(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateUnknown},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateUnknown, res.CIState)
	require.Contains(t, res.Reasons, "ci_not_green")
}

func TestMergeCondition_CIError(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: "", err: errors.New("CI fetch failed")},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err) // Checkメソッドはエラーを返さず、result.Reasonsに記録
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateUnknown, res.CIState) // エラー時はunknownにフォールバック
	require.Contains(t, res.Reasons, "ci_state_error")
}

func TestMergeCondition_CIEmptyString(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: ""}, // 空文字列
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.Equal(t, services.CIStateUnknown, res.CIState)
	require.False(t, res.Mergeable)
	require.Contains(t, res.Reasons, "ci_not_green")
}

// Codex承認のバリエーション

func TestMergeCondition_CodexNotApproved(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: false},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateSuccess, res.CIState)
	require.False(t, res.CodexApproved)
	require.Equal(t, services.MergeConflictStatusNoConflict, res.Conflict)
	require.Contains(t, res.Reasons, "codex_not_approved")
	require.NotContains(t, res.Reasons, "ci_failed")
	require.NotContains(t, res.Reasons, "has_conflict")
}

func TestMergeCondition_CodexError(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: false, err: errors.New("Codex fetch failed")},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.False(t, res.CodexApproved) // エラー時もfalse
	require.Contains(t, res.Reasons, "codex_approval_error")
}

// 競合状態のバリエーション

func TestMergeCondition_HasConflict(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusHasConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateSuccess, res.CIState)
	require.True(t, res.CodexApproved)
	require.Equal(t, services.MergeConflictStatusHasConflict, res.Conflict)
	require.Contains(t, res.Reasons, "has_conflict")
	require.NotContains(t, res.Reasons, "ci_failed")
	require.NotContains(t, res.Reasons, "codex_not_approved")
}

func TestMergeCondition_ConflictUnknown(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusUnknown},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.MergeConflictStatusUnknown, res.Conflict)
	require.Contains(t, res.Reasons, "conflict_unknown")
}

func TestMergeCondition_ConflictEmptyString(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: ""}, // 空文字列
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.Equal(t, services.MergeConflictStatusUnknown, res.Conflict)
	require.False(t, res.Mergeable)
	require.Contains(t, res.Reasons, "conflict_unknown")
}

func TestMergeCondition_ConflictDetectError(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict, err: errors.New("conflict check failed")},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.MergeConflictStatusUnknown, res.Conflict) // エラー時はunknownにフォールバック
	require.Contains(t, res.Reasons, "conflict_detection_error")
}

// 複合ケース

func TestMergeCondition_MultipleFailures(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateFailed},
		fakeCodex{approved: false},
		fakeConflict{status: services.MergeConflictStatusHasConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateFailed, res.CIState)
	require.False(t, res.CodexApproved)
	require.Equal(t, services.MergeConflictStatusHasConflict, res.Conflict)
	require.Contains(t, res.Reasons, "ci_failed")
	require.Contains(t, res.Reasons, "codex_not_approved")
	require.Contains(t, res.Reasons, "has_conflict")
	require.GreaterOrEqual(t, len(res.Reasons), 3)
}

func TestMergeCondition_CIAndCodexFail(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateFailed},
		fakeCodex{approved: false},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Contains(t, res.Reasons, "ci_failed")
	require.Contains(t, res.Reasons, "codex_not_approved")
	require.NotContains(t, res.Reasons, "has_conflict")
}

// エッジケース

func TestMergeCondition_LoggerNil(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStateSuccess},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		nil, // loggerがnil
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.True(t, res.Mergeable) // 正常に動作
}

func TestMergeCondition_AllErrors(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: "", err: errors.New("CI error")},
		fakeCodex{approved: false, err: errors.New("Codex error")},
		fakeConflict{status: "", err: errors.New("Conflict error")},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStateUnknown, res.CIState)
	require.False(t, res.CodexApproved)
	require.Equal(t, services.MergeConflictStatusUnknown, res.Conflict)
	require.Contains(t, res.Reasons, "ci_state_error")
	require.Contains(t, res.Reasons, "codex_approval_error")
	require.Contains(t, res.Reasons, "conflict_detection_error")
	require.GreaterOrEqual(t, len(res.Reasons), 3)
}

func TestMergeCondition_PendingCIWithCodexApproved(t *testing.T) {
	checker := services.NewMergeConditionChecker(
		fakeCI{state: services.CIStatePending},
		fakeCodex{approved: true},
		fakeConflict{status: services.MergeConflictStatusNoConflict},
		config.NewNopLogger(),
	)

	res, err := checker.Check(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, res.Mergeable)
	require.Equal(t, services.CIStatePending, res.CIState)
	require.True(t, res.CodexApproved)
	require.Contains(t, res.Reasons, "ci_not_green")
	require.NotContains(t, res.Reasons, "codex_not_approved")
}
