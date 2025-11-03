# US1とUS2の並行作業フロー

## 📋 概要

US1（コメント起動で自動実行）とUS2（AI処理とPR作成）を並行して効率的に実装するための作業フローです。

**前提条件**: Phase 2 (Foundational) は完了済み

## 🎯 現在の進捗状況

### US1 - 完了済み
- ✅ T066: Webhook event types定義
- ✅ T067: Trigger detection service
- ✅ T068: Comment parser
- ✅ T069: Authorization service

### US1 - 未完了
- ⏳ T070-T076: コア実装タスク
- ⏳ T077-T080: テストタスク

### US2 - すべて未完了
- ⏳ T081-T089: 実装・テストタスク

---

## 📅 並行作業フェーズ

### フェーズ1: 基盤サービスの並行実装（同時開始可能）

**Developer A (US1担当)** と **Developer B (US2担当)** が並行作業

#### Developer A: US1基盤サービス実装

1. **T071** - AgentRun state machine (`internal/services/agent_run_state_machine.go`)
   - 状態遷移: queued → started → succeeded/failed
   - 依存: なし（独立）

2. **T073** - Issue context collector (`internal/services/issue_context.go`)
   - Issue body、comments、labelsの収集
   - 依存: T067 (完了済み)

3. **T074** - Agent type detector (`internal/services/agent_type_detector.go`)
   - Issue labelsからエージェントタイプ検出
   - 依存: なし（T073と並行可能）

**所要時間見積**: 2-3日

#### Developer B: US2独立実装（US1完了を待たない）

1. **T081** [P] - PullRequest upsert logic (`internal/repositories/pull_request.go`)
   - Repository層の実装
   - PullRequestモデルの作成・更新ロジック
   - **依存**: なし（独立して実装可能）
   - **注意**: AgentRunは使用するが、既存のAgentRunモデルはPhase 2で準備済み

2. **T086** [P] - PullRequest upsertのユニットテスト
   - T081とセットで実装
   - モックを使用して独立してテスト可能

3. **T083** - GitHub status comment拡張 (`internal/services/github_notification.go`)
   - PR作成成功時のステータスコメント
   - **依存**: T076（US1の通知サービス）が存在すれば拡張、なければ新規作成
   - **注意**: US1のT076と競合する可能性があるため、GitHub通知サービスの基本構造を先に確認

4. **T084** - Discord notification (`internal/services/discord_notification.go`)
   - PR作成時のDiscord通知
   - **依存**: なし（完全独立）

5. **T087** [P] - GitHub notification serviceのユニットテスト
   - T083とセットで実装

**所要時間見積**: 2-3日

---

### フェーズ2: US1コア統合（Developer Aのみ）

#### Developer A: Webhookハンドラー実装

**前提**: T071, T073, T074が完了していること

1. **T072** - Kubernetes Job creation service (`internal/services/kubernetes_job.go`)
   - K8s Job作成ロジック
   - AgentRun state machine (T071) を使用
   - Issue context (T073) とAgent type (T074) を使用
   - **依存**: T071, T073, T074

2. **T070** - Issue comment webhook handler (`internal/webhooks/handlers/issue_comment.go`)
   - T067, T069, T071, T072, T073, T074を統合
   - Webhook受信 → トリガー検出 → 認証 → Job作成
   - **依存**: T067, T069, T071, T072, T073, T074（すべて）

3. **T075** - ロギング追加（T070内）
   - トリガー検出失敗、認証失敗のロギング
   - **依存**: T070

4. **T076** - GitHub status comment（実行開始通知）
   - **依存**: T070（Job作成後）
   - **注意**: Developer BのT083と協調が必要

**所要時間見積**: 2-3日

#### Developer B: 待機または補助作業

- T081, T083, T084, T086, T087が完了していない場合は継続
- US1のT070-T076の実装状況をレビューして統合準備
- 統合テストの準備

---

### フェーズ3: US2統合実装（US1完了後）

**前提条件**: T070-T076が完了し、US1のエンドツーエンドフローが動作すること

#### Developer B: AgentReportハンドラー拡張

1. **T082** - PR URL生成とAgentRun更新 (`internal/webhooks/handlers/agent_report.go`)
   - agent-runnerからの成功報告を受信
   - PullRequestレコード作成（T081を使用）
   - AgentRunにPR URLを保存
   - **依存**: T070（AgentRunレコードが作成される）、T081

2. **T085** - 失敗報告処理（同じハンドラー内）
   - agent-runnerからの失敗報告を処理
   - エラーログ抽出
   - **依存**: T070, T081

3. **T083** - GitHub status comment統合（実行開始時とは別の通知）
   - PR作成成功の通知
   - **依存**: T082（PR作成後）

4. **T084** - Discord notification統合
   - PR作成時のDiscord通知
   - **依存**: T082

**所要時間見積**: 2日

---

### フェーズ4: テスト実装（並行可能）

#### Developer A: US1テスト

1. **T077** [P] - Contract tests（webhook payload検証）
2. **T078** [P] - TriggerDetectionServiceユニットテスト
3. **T079** [P] - AuthorizationServiceユニットテスト
4. **T080** - 統合テスト（webhook → K8s Job作成）

**所要時間見積**: 1-2日

#### Developer B: US2テスト

1. **T088** - 統合テスト（agent-runner成功報告 → PR作成）
2. **T089** - 統合テスト（agent-runner失敗報告 → リトライトリガー）

**所要時間見積**: 1-2日

---

## 📊 タイムライン概要

```
Week 1:
├─ Day 1-3: フェーズ1（並行実装）
│   ├─ Developer A: T071, T073, T074
│   └─ Developer B: T081, T083, T084, T086, T087
│
└─ Day 4-5: フェーズ2（US1統合）
    └─ Developer A: T072, T070, T075, T076
    └─ Developer B: レビュー・準備

Week 2:
├─ Day 1-2: フェーズ3（US2統合）
│   └─ Developer B: T082, T085（T081使用）
│
└─ Day 3-5: フェーズ4（テスト）
    ├─ Developer A: T077-T080
    └─ Developer B: T088-T089
```

**合計見積**: 約2週間（2名で並行作業）

---

## 🔄 協調ポイント（注意事項）

### 1. GitHub通知サービスの統合（T076 vs T083）

**問題**: 
- US1のT076: 実行開始通知
- US2のT083: PR作成成功通知
- 同じ`github_notification.go`を拡張する

**解決策**:
- Developer BがT083を実装する前に、Developer AのT076実装状況を確認
- または、GitHub通知サービスの基本構造を先に定義してから並行実装
- 推奨: `github_notification.go`の基本構造を先に確認・合意

### 2. AgentRunモデルの確認

**確認事項**:
- AgentRunモデルに`PRID`フィールドが存在するか
- PR URL保存用のフィールドが必要か（T082）

**対応**:
- Phase 2で作成されたAgentRunモデルを確認
- 必要に応じてマイグレーションを準備（US2統合前に）

### 3. 統合テストの準備

**準備事項**:
- US1とUS2の統合テストは、両方が完了してから実行
- モックの準備（agent-runnerからの報告をシミュレート）

---

## ✅ チェックポイント

### フェーズ1完了時
- [ ] T071, T073, T074が実装済み
- [ ] T081, T083, T084が実装済み
- [ ] 基本的なユニットテストが通る

### フェーズ2完了時（US1完了）
- [ ] コメント投稿 → K8s Job作成のフローが動作
- [ ] AgentRunレコードが正しく作成される
- [ ] GitHub status commentが投稿される

### フェーズ3完了時（US2統合完了）
- [ ] agent-runnerからの報告を受信できる
- [ ] PullRequestレコードが作成される
- [ ] AgentRunにPR情報が保存される
- [ ] 通知（GitHub + Discord）が動作

### フェーズ4完了時（全テスト完了）
- [ ] US1の全テストが通過
- [ ] US2の全テストが通過
- [ ] エンドツーエンドフローが動作（コメント → PR作成）

---

## 🚨 リスクと対策

### リスク1: ファイル競合（github_notification.go）
**対策**: 
- 先に基本構造を定義してから並行実装
- または、Developer AがT076を先に実装してから、Developer BがT083で拡張

### リスク2: AgentRunモデルの不足フィールド
**対策**: 
- フェーズ1開始前にAgentRunモデルを確認
- 必要に応じてマイグレーションを事前準備

### リスク3: 統合時の不整合
**対策**: 
- 各フェーズ終了時にコードレビューを実施
- 統合テストを早期に準備

---

## 📝 作業開始前の確認事項

1. **AgentRunモデルの確認**
   ```bash
   # 確認するファイル
   internal/models/agent_run.go
   ```

2. **既存の通知サービスの確認**
   ```bash
   # 確認するファイル
   internal/services/github_notification.go
   internal/clients/discord.go
   ```

3. **agent-reportハンドラーの現在の実装確認**
   ```bash
   # 確認するファイル
   internal/webhooks/handlers/agent_report.go
   ```

4. **テスト環境の準備**
   - K8sクラスターへの接続確認
   - テスト用リポジトリの準備

---

## 🔗 関連ファイルマッピング

### US1関連
- `internal/utils/comment_parser.go` (T068) ✅
- `internal/services/trigger_detection.go` (T067) ✅
- `internal/services/authorization.go` (T069) ✅
- `internal/services/agent_run_state_machine.go` (T071) ⏳
- `internal/services/issue_context.go` (T073) ⏳
- `internal/services/agent_type_detector.go` (T074) ⏳
- `internal/services/kubernetes_job.go` (T072) ⏳
- `internal/webhooks/handlers/issue_comment.go` (T070, T075) ⏳
- `internal/services/github_notification.go` (T076) ⏳

### US2関連
- `internal/repositories/pull_request.go` (T081) ⏳
- `internal/webhooks/handlers/agent_report.go` (T082, T085) ⏳
- `internal/services/github_notification.go` (T083) ⏳
- `internal/services/discord_notification.go` (T084) ⏳

---

## 💡 並行作業のメリット

1. **時間短縮**: 2週間でUS1+US2を完了（順次なら3-4週間）
2. **早期フィードバック**: 実装中の問題を早期発見
3. **効率的なリソース活用**: 2名の開発者を最大限活用
4. **品質向上**: 並行レビューでコード品質向上

---

**最終更新**: 2025-01-XX
**作成者**: AI Assistant
**承認**: [開発チーム]

