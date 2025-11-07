# Phase 1 フォローアップメモ

## 影響範囲メモ

- `internal/repositories/review_feedback.go`
  - `UpdateToReceived` など既存更新系は新フィールドを未操作。Phase 2 で `PlanCreationStatus` などを更新する処理追加が必要。
  - `CreateRequestedReview` / `CreateReceivedReview` も同様に plan 関連初期化が未実装。
- `internal/repositories/agent_run.go`
  - 新しい `ExecutionMode` / `ReviewFeedbackID` による検索・フィルタリングは未実装。Phase 2 で追加予定。
- `internal/webhooks/handlers/agent_report.go`
  - レポート処理は plan 実行終了時の状態遷移をまだ扱わないため、Phase 5 で `PlanCreationStatus` 更新を組み込む。
- `internal/services/kubernetes_job.go`
  - JobConfig 拡張と plan 用モード切り替えは未実装。Phase 3〜4 で対応。

## マイグレーション検証手順

1. `goose -dir migrations status` で現在の適用状況を確認。
2. `goose -dir migrations mysql "${DSN}" up` を実行し、000006 と 000007 が適用されることを確認。
3. `DESCRIBE review_feedback;` / `DESCRIBE agent_runs;` で列とデフォルト値を確認。
4. `goose -dir migrations mysql "${DSN}" down` を 2 回実行し、新設した列とインデックスが削除されることを確認。

## ユニットテスト案

- ReviewFeedback: `CreateReceivedReview` → `PlanCreationStatus` が `pending` で保存されること。
- AgentRun: 新規生成時に `ExecutionMode` が `normal` となること。
- マイグレーション後の GORM 保存・取得テストを追加し、`PlanContent` 等の nil/値ありケースを検証。


