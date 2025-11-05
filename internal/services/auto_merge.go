package services

import (
	"context"
	"errors"
	"fmt"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/utils"
	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
)

// AutoMergeService はPR自動マージを担うサービス
type AutoMergeService interface {
	AttemptAutoMerge(ctx context.Context, owner, repo string, prNumber int) (*AutoMergeResult, error)
}

// 実装構造体
type autoMergeService struct {
	ghApp  *clients.GitHubClient
	logger *zap.Logger
}

// AutoMergeResult は自動マージの結果を表す
type AutoMergeResult struct {
	Merged   bool
	MergeSHA string
	Message  string // 成功/失敗の要約
}

// NewAutoMergeService は AutoMergeService のコンストラクタ
func NewAutoMergeService(ghApp *clients.GitHubClient, logger *zap.Logger) AutoMergeService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &autoMergeService{ghApp: ghApp, logger: logger}
}

// AttemptAutoMerge は merge commit 方式でマージし、成功時にソースブランチを削除する
func (s *autoMergeService) AttemptAutoMerge(ctx context.Context, owner, repo string, prNumber int) (*AutoMergeResult, error) {
	if owner == "" || repo == "" || prNumber <= 0 {
		return nil, fmt.Errorf("invalid input: owner/repo/prNumber are required")
	}

	// マージ実行（トークン失効時の再試行は GitHubClient.DoWithClientRetry に委譲）
	var mergeResult *github.PullRequestMergeResult
	mergeFn := func() error {
		return s.ghApp.DoWithClientRetry(ctx, owner, repo, func(c *github.Client) (*github.Response, error) {
			opt := &github.PullRequestOptions{MergeMethod: "merge"}
			// commit message を空にすると GitHub 側で既定メッセージ
			res, resp, err := c.PullRequests.Merge(ctx, owner, repo, prNumber, "", opt)
			if err == nil {
				mergeResult = res
			}
			return resp, err
		})
	}

	// 一時的障害に対して指数バックオフでリトライ
	if err := utils.Retry(ctx, mergeFn, utils.DefaultRetryConfig(), s.logger); err != nil {
		// 論理失敗（409/422など）は DoWithClientRetry から error として返る
		// ユーザ向けメッセージへ分類
		return &AutoMergeResult{Merged: false, Message: classifyMergeError(err)}, nil
	}

	// 成功時: Merge SHA を取得
	result := &AutoMergeResult{Merged: true, MergeSHA: "", Message: "merged"}
	if mergeResult != nil {
		result.MergeSHA = mergeResult.GetSHA()
	}

	// ソースブランチ削除（best-effort）
	_ = s.ghApp.DoWithClientRetry(ctx, owner, repo, func(c *github.Client) (*github.Response, error) {
		pr, resp, err := c.PullRequests.Get(ctx, owner, repo, prNumber)
		if err != nil {
			s.logger.Warn("failed to fetch PR after merge", zap.Error(err))
			return resp, err
		}
		if pr == nil || pr.Head == nil || pr.Head.Ref == nil {
			return resp, nil
		}
		ref := fmt.Sprintf("heads/%s", pr.GetHead().GetRef())
		delResp, delErr := c.Git.DeleteRef(ctx, owner, repo, ref)
		if delErr != nil {
			s.logger.Warn("branch delete failed", zap.String("ref", ref), zap.Error(delErr))
		} else {
			s.logger.Info("branch deleted", zap.String("ref", ref))
		}
		return delResp, delErr
	})

	return result, nil
}

// classifyMergeError は GitHub API からのエラーをユーザ向けに分類する
func classifyMergeError(err error) string {
	if err == nil {
		return ""
	}
	// github.ErrorResponse を検出してステータスに応じて分類
	var ghe *github.ErrorResponse
	if errors.As(err, &ghe) && ghe != nil && ghe.Response != nil {
		code := ghe.Response.StatusCode
		switch code {
		case 409:
			return "merge_conflict_or_not_mergeable"
		case 422:
			return "merge_rejected_by_protection_or_reviews"
		case 429:
			return "rate_limited"
		default:
			if code >= 500 {
				return "github_server_error"
			}
		}
	}
	return err.Error()
}
