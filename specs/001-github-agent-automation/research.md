# research.md: GitHub Agent Automation - Phase 0リサーチまとめ

## 1. 仕様要約
- トリガ：Issueコメント内「/run-agent」（書込権限必須）、PRコメント「@codex review」
- フロー：AIがIssue実装 → PR作成 → Codexレビュー → 指摘・CI失敗は最大50回自動再試行 → Codex & CI成功で自動マージ → 必要なら依存タスク着手
- 通知・可視化：失敗時はGitHub IssueとDiscord双方に通知、状況は自動コメントで可視化。Codexのapprove条件も厳密明記

## 2. 外部サービス・認可課題
- GitHub権限管理・Webhook署名検証（Collaborator以上限定処理、本番Repoでの運用を前提とする）
- Codex連携：認証方式・レートリミット下での安定呼び出し
- Discord通知：Webhook障害時のリトライ方針要調査
- CI連携：CIの解析・WebHook経由ログ集約処理

## 3. 技術課題・未決事項
- 冪等化設計（X-GitHub-Deliveryキー対応）、同一Delivery ID重複受信への防御
- 外部API通信の再試行設計（指数バックオフ+ジッター推奨、失敗時は即通知）
- BlockerGraphによる依存関係解決のアルゴリズム・循環依存時の安全停止設計
- PR/Issueの「blocked by」「blocking」自動パース仕様と運用負担
- レビュー応答欠落、CI競合、GitHub API rate limit時の復旧戦略（回数無制限リトライ／エスカレーション条件）

## 4. 運用・その他落とし穴
- 無制限同時実行時の競合：同一ファイルへの多重編集・重複PRの扱い
- Codexコメントの「真正性」判定ロジック（Bot識別法）
- 再試行「最大50回」達成時の復旧導線（人手介入/自動Closeか要選択）
- [NEEDS CLARIFICATION]判定点はほぼ解消済。Codexレビュー条件、トリガ判定、CI結果解析箇所は詳細設計・要試験。
