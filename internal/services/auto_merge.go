package services

import (
	"context"
	"errors"
	"fmt"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"

	"github.com/google/go-github/v76/github"
)

// AutoMergeService はPR自動マージを担うサービス
type AutoMergeService interface {
	AttemptAutoMerge(ctx context.Context, owner, repo string, prNumber int) (*AutoMergeResult, error)
}

// 実装構造体
type autoMergeService struct {
	ghApp  *clients.GitHubClient
	logger *config.AppLogger
}

// AutoMergeResult は自動マージの結果を表す
type AutoMergeResult struct {
	Merged       bool
	MergeSHA     string
	Message      string // 成功/失敗の要約
	ErrorType    string // 失敗時の分類済みエラー種別
	ErrorMessage string // 失敗時の元エラーメッセージ
}

// NewAutoMergeService は AutoMergeService のコンストラクタ
func NewAutoMergeService(ghApp *clients.GitHubClient, logger *config.AppLogger) AutoMergeService {
	if logger == nil {
		logger = config.NewNopLogger()
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
				config.String("owner", owner), config.String("repo", repo), config.Int("pr_number", prNumber))
			// 成功扱いにしてブランチ削除へ進む
			mergeResult = &github.PullRequestMergeResult{}
		} else {
			// 未マージ → エラーを分類して結果として返却（上位で通知・ハンドリング）
			classified := ClassifyMergeError(mergeErr)
			return &AutoMergeResult{
				Merged:       false,
				MergeSHA:     "",
				Message:      "merge_failed",
				ErrorType:    classified,
				ErrorMessage: mergeErr.Error(),
			}, nil
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
			s.logger.Warn("failed to fetch PR after merge", config.Error(err))
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
				config.String("base", baseFullName),
				config.String("head_full_name", func() string {
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
			s.logger.Info("skip branch delete: protected branch name", config.String("ref", headRef))
			return resp, nil
		}

		ref := fmt.Sprintf("heads/%s", headRef)
		delResp, delErr := c.Git.DeleteRef(ctx, owner, repo, ref)
		if delErr != nil {
			s.logger.Warn("branch delete failed", config.String("ref", ref), config.Error(delErr))
		} else {
			s.logger.Info("branch deleted", config.String("ref", ref))
		}
		return delResp, delErr
	})

	return result, nil
}

// ClassifyMergeError は GitHub API からのエラーをユーザ向けに分類する
// ステータスコードに応じて代表的な分類名を返す。該当しない場合は err.Error() を返す。
func ClassifyMergeError(err error) string {
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
