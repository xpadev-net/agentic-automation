package services

import (
	"context"
	"errors"
	"fmt"

	"agentic-automation/internal/clients"
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

	// マージ実行（非冪等のため一般リトライは行わない。トークン更新のみ DoWithClientRetry に委譲）
	var mergeResult *github.PullRequestMergeResult
	mergeErr := s.ghApp.DoWithClientRetry(ctx, owner, repo, func(c *github.Client) (*github.Response, error) {
		opt := &github.PullRequestOptions{MergeMethod: "merge"}
		res, resp, err := c.PullRequests.Merge(ctx, owner, repo, prNumber, "", opt)
		if err == nil {
			mergeResult = res
		}
		return resp, err
	})

	// エラー時: 応答喪失などに備え IsMerged を確認し、既にマージ済なら成功扱い
	if mergeErr != nil {
		var isMerged bool
		_ = s.ghApp.DoWithClientRetry(ctx, owner, repo, func(c *github.Client) (*github.Response, error) {
			merged, resp, ierr := c.PullRequests.IsMerged(ctx, owner, repo, prNumber)
			if ierr == nil {
				isMerged = merged
			}
			return resp, ierr
		})
		if isMerged {
			s.logger.Info("merge succeeded but initial response failed; treating as success",
				zap.String("owner", owner), zap.String("repo", repo), zap.Int("pr_number", prNumber))
			// 成功扱いにしてブランチ削除へ進む
			mergeResult = &github.PullRequestMergeResult{}
		} else {
			// 未マージ → エラーを返却（上位で明示的に再試行判断）
			return nil, mergeErr
		}
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
			s.logger.Info("skip branch delete: missing PR head ref")
			return resp, nil
		}

		// Guard: only delete when head repo is the same as base owner/repo (avoid fork branch deletion)
		head := pr.GetHead()
		headRepo := head.GetRepo()
		baseFullName := fmt.Sprintf("%s/%s", owner, repo)
		isSameRepo := false
		if headRepo != nil {
			if headRepo.GetFullName() == baseFullName {
				isSameRepo = true
			} else if headRepo.GetOwner() != nil && headRepo.GetOwner().GetLogin() == owner && headRepo.GetName() == repo {
				isSameRepo = true
			}
		}
		if !isSameRepo {
			s.logger.Info("skip branch delete: head repo differs (likely fork)",
				zap.String("base", baseFullName),
				zap.String("head_full_name", func() string {
					if headRepo != nil {
						return headRepo.GetFullName()
					}
					return ""
				}()),
			)
			return resp, nil
		}

		// Extra safety: never delete protected branch names
		headRef := head.GetRef()
		if headRef == "main" || headRef == "master" {
			s.logger.Info("skip branch delete: protected branch name", zap.String("ref", headRef))
			return resp, nil
		}

		ref := fmt.Sprintf("heads/%s", headRef)
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
