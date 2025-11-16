package services

import (
	"context"
	"strings"

	"agentic-automation/internal/config"
	"agentic-automation/internal/errors"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// ErrBlockedDependencies は、未クローズの依存Issueが存在する場合に返される業務エラー。
var ErrBlockedDependencies = &errors.CodedError{
	Code:    errors.ERR_DEPENDENCY_BLOCKED,
	Message: "blocked dependencies found",
}

// ValidationResult は、依存関係バリデーションの結果。
type ValidationResult struct {
	// BlockedDeps は未クローズの依存Issue一覧。
	BlockedDeps []models.Issue
}

// DependencyValidator は、対象Issueがブロックされていないことを検証する。
type DependencyValidator interface {
	// ValidateUnblocked は、(owner/repo#issueNumber) の依存が全てクローズされていることを検証する。
	// 未クローズ依存がある場合は ErrBlockedDependencies を返す。
	ValidateUnblocked(ctx context.Context, owner, repo string, issueNumber int) (*ValidationResult, error)
}

type dependencyValidator struct {
	builder BlockerGraphBuilder
	issues  *repositories.IssueRepository
	edges   *repositories.BlockerGraphRepository
	logger  *config.AppLogger
}

// NewDependencyValidator を作成する。
func NewDependencyValidator(
	builder BlockerGraphBuilder,
	issues *repositories.IssueRepository,
	edges *repositories.BlockerGraphRepository,
	logger *config.AppLogger,
) DependencyValidator {
	if builder == nil {
		panic("builder is required for DependencyValidator")
	}
	if issues == nil {
		panic("issue repository is required for DependencyValidator")
	}
	if edges == nil {
		panic("edge repository is required for DependencyValidator")
	}
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &dependencyValidator{builder: builder, issues: issues, edges: edges, logger: logger}
}

// ValidateUnblocked は実装タスク(impl-validate)で本体を追加する。
func (v *dependencyValidator) ValidateUnblocked(ctx context.Context, owner, repo string, issueNumber int) (*ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1) 依存情報の最新化
	if err := v.builder.BuildForIssue(ctx, owner, repo, issueNumber); err != nil {
		v.logger.Error("failed to build blocker graph",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
			config.Error(err),
		)
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 2) ルートIssueの取得
	repoKey := makeRepoKey(owner, repo)
	root, err := v.issues.FindByRepoAndNumber(repoKey, issueNumber)
	if err != nil {
		return nil, err
	}

	// 3) 依存エッジ取得
	edges, err := v.edges.GetDependenciesForTask(root.ID)
	if err != nil {
		return nil, err
	}

	// 4) 依存Issue状態チェック
	blocked := make([]models.Issue, 0)
	for _, e := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dep, derr := v.issues.FindByID(e.DependsOnTaskID)
		if derr != nil {
			return nil, derr
		}
		if !strings.EqualFold(dep.State, "closed") {
			blocked = append(blocked, *dep)
		}
	}

	if len(blocked) > 0 {
		return &ValidationResult{BlockedDeps: blocked}, ErrBlockedDependencies
	}

	return &ValidationResult{BlockedDeps: nil}, nil
}
