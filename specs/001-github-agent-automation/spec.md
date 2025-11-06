# Feature Specification: GitHub Agent Automation

**Feature Branch**: `001-github-agent-automation`
**Created**: 2025-10-30  
**Status**: Draft  
**Input**: User description: "githubのissue, prと連携してプロダクトの開発に取り組むシステム\n\n- issueに特定の文字列とともにコメントされた際に起動する\n\n- AIエージェントにissueの内容を食わせて処理する\n\n- 完了したらcommit + push + pr作成する\n\n- codexにレビューを依頼する\n\n- レビューが返ってきたらAIにそれを返して再試行させる\n\n- approveされたらマージする\n\n- 取り組んでいたタスクがブロックしていて、完了によってブロックタスクがなくなった場合にそのタスクに対する取り組みを開始する"

## Clarifications

### Session 2025-10-30

- Q: 主要な利用者ロールは？ → A: エンドユーザーおよび管理者の両方が操作可能
- Q: 外部API連携失敗時は？ → A: 内部リトライ後に失敗ならエラー通知
- Q: "失敗"の判定ロジック？ → A: CIが落ちている場合にAI差し戻し。50回繰り返した場合は失敗とみなす
- Q: AI再試行は最大何回？ → A: 最大50回まで自動実施とし、それを超えた場合は失敗扱い
- Q: 再試行全失敗時の通知先は？ → A: GitHub IssueおよびDiscord Webhook両方に通知
- Q: 再試行IDの一意性は？ → A: GitHub のイベントID（X-GitHub-Delivery）を用いる
- Q: 再実行ループ防止・閾値？ → A: 制限なし（50回上限超えで失敗）
- Q: 再試行状況・失敗理由の確認UIは？ → A: GitHub上の自動コメント
- Q: Codex レビュー応答が欠落・遅延した場合は？ → A: 手動で対処し、イベント駆動を維持する（システムは待機やポーリングを行わない）
- Q: 空振り要件不一致時は？ → A: 全て通知
- Q: 全失敗時の通知検出ラグ？ → A: 即時、1分以内
- Q: 監視可視化は？ → A: Discord Webhook通知
- Q: リトライ/レート制限は？ → A: 必要。指数バックオフ＋ジッターと最大回数で制御する
- Q: ポーリングは？ → A: 原則不要だが、必要に応じて使用可。採用イベントは仕様内で適時明確化する
  （詳細は「Webhook Events (authoritative)」を参照）
- Q: 「AI再試行」用語補足 → A: レビュー指摘事項 or CI落ちでAI自動修正を行うこと
- Q: 完了判定（DoD）は？ → A: CIが通り、コンフリクト無しでCodexからapproveが出ている状態

## User Scenarios & Testing (mandatory)

### User Story 1 - コメント起動で自動実行 (Priority: P1)

Issue に所定のトリガ文字列を含むコメントが投稿されると、システムが当該 Issue 内容を
取得し、AI エージェント実行を開始する。

**Why this priority**: 自動フローの入口であり全体価値の基盤。

**Independent Test**: テストリポジトリでトリガコメント投稿→エージェント実行開始イベント
が記録されること。

**Acceptance Scenarios**:
1. Given 対象 Issue にトリガコメントが投稿済み When Webhook を受信 Then エージェント
   実行がキューイングされ状態=started となる
2. Given トリガ条件に不一致 When Webhook を受信 Then 実行は開始されない

---

### User Story 2 - AI処理とPR作成 (Priority: P1)

AI エージェントが Issue 内容を元に変更を作成し、コミット・プッシュ・PR 作成までを自動
化する。

**Why this priority**: ユーザ価値（変更提案）の中核。

**Independent Test**: 変更対象がない最小リポジトリで、ブランチ作成→コミット→PR作成が
確認できること。

**Acceptance Scenarios**:
1. Given 実行が started When 変更生成が成功 Then 新規ブランチとコミットが作成される
2. Given コミットが作成済み When プッシュが成功 Then 新規 PR が作成されリンクが保存される

Note (Scope Boundary): US2では失敗時の「記録・要約抽出」までを対象とし、自動再試行（retry_count増分とK8s Job再作成）はUS3で扱う。

---

### User Story 3 - Codex レビューと再試行 (Priority: P2)

作成した PR を Codex にレビュー依頼し、戻り結果を AI にフィードバックして修正を再試
行する。

**Why this priority**: 品質向上と自動改善ループの実現。

**Independent Test**: ダミーのレビュー応答を与えた場合、再実行が行われ新しいコミットが
積み上がること。

**Acceptance Scenarios**:
1. Given PR が作成済み When システムがレビュー依頼を実行 Then PR に `@codex review` コメントが投稿され Codex サービスへのレビュー依頼が送信・記録される
2. Given レビュー結果がある When AI に再入力 Then 追加コミットが作成される

---

### User Story 4 - Approve と自動マージ (Priority: P2)

PR が承認状態になったら自動でマージする。

**Why this priority**: フロー完結のために必要。

**Independent Test**: PR に Approve を付与すると自動でマージされること。

**Acceptance Scenarios**:
1. Given PR が Approve 済み When マージ条件を満たす Then マージが成功する
2. Given マージ要件を満たさない When 処理実行 Then マージは行われず状態が記録される

---

### User Story 5 - ブロック解除によるタスク再開 (Priority: P3)

依存関係を持つタスクがブロックされている場合、完了によりブロックが解消されたタスクに
自動着手する。

**Why this priority**: 継続的な自動推進。

**Independent Test**: 依存関係グラフでブロック解消時に次タスク実行が開始されること。

**Acceptance Scenarios**:
1. Given タスクB がタスクA に依存 When タスクA が完了 Then タスクB の実行が開始される
2. Given 循環依存 When 完了処理 Then タスク開始は行われず警告が記録される

### Edge Cases

- GitHub Webhook の再送（重複イベント）
- レビュー応答が欠落・遅延する場合（手動対応を前提）
- マージ競合・保護ブランチ条件での失敗
- 無制限同時実行時の競合（同一ファイル衝突、重複 PR）

- 外部API障害時はエラー記録・通知し、指数バックオフ+ジッターで設定した上限回数までリトライする。上限到達時は失敗として通知する。
- Codex レビュー応答が取得できない場合は手動対応とし、システムは追加のポーリングやハング防止処理を行わない。

- 同一ブランチに対する同時実行は 1 に制限（per-branch concurrency = 1）

## Requirements (mandatory)

### Functional Requirements

- FR-001: システムはトリガ文字列を含む Issue コメント受信時に実行を開始する (MUST)
- FR-002: 実行コンテキストは Issue 本文・コメント・関連 PR/コミットを収集する (MUST)
- FR-003: 変更は新規ブランチでコミット・プッシュし PR を作成する (MUST)
- FR-004: 作成 PR を Codex にレビュー依頼し結果を保存する (MUST)
- FR-005: レビュー結果を AI に再入力し再試行コミットを作成する (MUST)
- FR-006: PR が Approve 状態を満たした場合に自動マージする (MUST)
- FR-007: タスク依存グラフを管理しブロック解消で次タスクを開始する (MUST)
- FR-008: すべての外部操作は冪等に設計し重複イベントを安全に処理する (MUST)

- FR-009: トリガはコメント本文に「/run-agent」を含む場合のみ有効とし、コメント投稿者
  は当該リポジトリの書き込み権限メンバーに限定する。またコメントの編集・削除イベントは無視する。書き込み権限があれば一律許可とし、必ずデフォルトブランチから派生ブランチを作成して作業する (MUST)
- FR-010: PR 作成完了時にシステムは PR に「@codex review」とコメント投稿する。これ自体が Codex サービスへのレビュー依頼であり、その結果を保存する。以降、書き込み権限を有するユーザーが当該 PR に
  「@codex review」のみを本文に含む新規コメントを投稿した場合は、それ自体が再レビュー依頼となるためシステムは別途依頼を送信しない（編集・削除は無視、冪等実行） (MUST)
- FR-011: 承認要件は、Codex ボットが PR に「Codex Review: Didn't find any major issues.」という本文のコメント（または同等内容のレビュー）を投稿した時点で approve と見なす。ただしマージには別途 CI 成功が必須。コメント本文の検知を一次根拠とし、投稿者が Codex ボットであることを確認する（必要に応じて Reviews API の state=APPROVED を補助として用いる） (MUST)
- FR-012: 同時実行の上限は設けない（無制限）。ただし重複・競合の回避は Issue 起票側の
  運用で考慮し、依存関係は GitHub Issue Dependencies API（`GET /repos/{owner}/{repo}/issues/{issue_number}/dependencies`）で取得した構造化データに基づいて実行順序を
  決定する (MUST)
- FR-013: 依存関係に反する実行（下流先行など）が検出された場合は当該実行を保留にし、
  ブロック解除イベントで再開する (MUST)

  注記: グローバルには無制限並行実行（FR-012）とするが、per-branch concurrency=1（FR-020）と PR/ブランチ単位のロック（FR-021）を適用して重複操作を防止する。

- FR-014: CI 結果は GitHub Webhook で受領し、失敗時は CI ログ要約/レビュー指摘をAIに再入力して最大50回まで自動修正再試行を行う。50回超えは失敗扱いとし、GitHub IssueおよびDiscord Webhookに失敗通知を送る (MUST)
- FR-015: Codex ボットによる承認（コメント/レビュー）が PR に投稿・統合された時点で、マージ条件を再評価し、自動でマージを試行する。原則イベント駆動（ポーリング不要）だが、必要に応じて限定的にポーリングを使用してよい。対象イベント（issue_comment, pull_request, pull_request_review, check_suite/check_run/status, push）は適時明確化する (MUST)
- FR-016: 外部 API 呼び出しの再試行は、指数バックオフ+ジッターを用い設定した最大リトライ回数まで実施し、上限到達時は失敗として扱う (MUST)
- FR-017: 冪等化キーに X-GitHub-Delivery を用い、同一 Delivery ID のイベントは一度のみ処理する。加えて、各操作（pr-create, post-comment, request-review, merge）に operation_id を付与し、操作レベルでも冪等に実行して重複を防止する (MUST)
- FR-018: 認可は「リポジトリのCollaborator以上」のユーザーのみに限定する (MUST)
- FR-019: コミットと push はオペレーター（自動実行主体）側で実行する。Git pre-commit / pre-push フック等による失敗は最大 50 回の自動再試行に含め、無限待機は不可とし、上限・キャンセル条件を設けて失敗時は通知する (MUST)

- FR-020: 同一ブランチに対するエージェント実行は同時に 1 件のみ許可する（per-branch concurrency = 1）(MUST)
- FR-021: PR/ブランチ単位の操作はロックを取得して実行し、重複操作（PR 作成、コメント投稿、レビュー依頼、マージ）を防止する (MUST)
- FR-022: 再試行には総経過時間上限および各試行のタイムアウトを設定し、いずれかの上限到達時は失敗として扱う (MUST)
- FR-023: データストアは MySQL を使用し、データアクセスは GORM を用い、マイグレーションは goose を用いる (MUST)
- FR-024: 依存関係は GitHub Issue Dependencies API（`GET /repos/{owner}/{repo}/issues/{issue_number}/dependencies`）を使用して取得した構造化データを一次情報として利用する (MUST)

### Webhook Events (authoritative)

- issue_comment
  - Purpose: `/run-agent` トリガ検知、`@codex review` による再レビュー依頼
  - Actions: 投稿者の書き込み権限検証、AgentRun のキュー投入、必要に応じてレビュー依頼コメント投稿
  - Idempotency: X-GitHub-Delivery + operation_id（comment-id ベース）
  - FR refs: FR-001, FR-009, FR-010, FR-017

- pull_request
  - Purpose: PR の open/synchronize/closed を観測し、リンク・マージ結果を反映
  - Actions: PR リンクの保存、merge 検知、merge 後の後続タスク起動
  - FR refs: FR-003, FR-006, FR-021

- pull_request_review
  - Purpose: Codex からの approve を検知（Bot ID 検証 or Reviews API state=APPROVED）
  - Actions: マージ条件の再評価、CI 成功時の自動マージ試行
  - FR refs: FR-011, FR-015

- pull_request_review_comment
  - Purpose: PR 上の `@codex review` 検知による再レビュー依頼トリガ
  - Actions: 権限検証の上でレビュー依頼コメント投稿、ReviewFeedback の更新
  - FR refs: FR-010, FR-017

- check_suite / check_run / status
  - Purpose: PR のコミットに対する CI 結果の追跡
  - Actions: 失敗時は AI へのフィードバックと再試行、成功時はマージ条件再評価
  - FR refs: FR-014, FR-015

- push
  - Purpose: 再試行コミット（追加コミット）の検知、PR head SHA 更新に伴う評価
  - FR refs: FR-005, FR-014, FR-021

- issues
  - Purpose: 依存関係グラフの更新とブロック解除検知（closed/reopened）
  - Actions: BlockerGraph の更新、ブロック解除時の AgentRun 起動
  - FR refs: FR-007, FR-013

- workflow_run（必要に応じて）
  - Purpose: 再利用ワークフロー等で check_* が発火しない CI の代替フック
  - Actions: 上記 CI 追跡と同等に扱う（check_* が利用できない場合のみ）
  - FR refs: FR-014

### Key Entities (include if feature involves data)

- Issue: ID, タイトル, 本文, ラベル, コメント
- PullRequest: ID, ブランチ, ステータス, レビュー, マージ状態
- AgentRun: 状態(queued/started/succeeded/failed), 入力, 出力, リンク(PR/コミット), リトライ回数(最大50), github_event_id(X-GitHub-Delivery)
- ReviewFeedback: 出所(Codex), 内容, ステータス, タイムスタンプ
- BlockerGraph: タスクと依存関係の有向グラフ

#### Glossary

- **AI**: Issueをもとにコーディングを行うAIエージェントとして本仕様全体で統一する。

#### MySQL Constraints (sketch)

- AgentRun: PK(id), UK(idempotency_key=X-GitHub-Delivery), state ENUM(queued,started,succeeded,failed), pr_id(FK: PullRequest.id, NULL 可), created_at, updated_at
- ReviewFeedback: PK(id), pr_id(FK: PullRequest.id), source ENUM(Codex), created_at, updated_at
- PullRequest: PK(id), repo, number, branch, status, mergeable, created_at, updated_at
- Issue: PK(id), repo, number, title, created_at, updated_at
- BlockerGraphEdges: PK(task_id, depends_on_task_id), FK(task_id -> Issue.id, depends_on_task_id -> Issue.id)

- Indexes:
- AgentRun: INDEX(idempotency_key)
- PullRequest: INDEX(repo, number)
- BlockerGraphEdges: INDEX(task_id, depends_on_task_id)

- OperationLog: PK(id), run_id(FK: AgentRun.id), operation_type ENUM(pr-create, post-comment, request-review, merge), operation_id(UK), status, created_at

## Success Criteria (mandatory)

### Measurable Outcomes

- SC-001: トリガコメントからエージェント実行が迅速に開始される
- SC-002: 成功フローで最初の PR が自動作成される
- SC-003: レビュー応答受領から再試行コミットが自動作成される
- SC-004: 重複イベントで重複 PR/実行が発生しない（0 件）
- SC-005: 依存タスクのブロック解消から次タスクが自動開始される
- SC-006: CI 失敗検知から AI 再試行が自動的に開始される
- SC-007: 「Codex Review: Didn't find any major issues.」コメント検知からマージ再試行が行われる
- SC-008: 50回再試行してもCI成功／Approve出ずに失敗となった場合、GitHub IssueおよびDiscord Webhookに通知される
- SC-009: DoD条件はCI成功・ノーコンフリクト・Codex approveの全てを満たすこと
  かつ PR が実際にマージ完了（status=merged）であること

- SC-010: 同一ブランチで同時実行が 2 件以上発生しない（0 件）
- SC-011: 承認検知は Bot 検証または Reviews API により誤検知 0 件

## Observability

- 追跡ID: `github_event_id`（X-GitHub-Delivery）+ `agent_run_id` + `operation_id` を全ログ/メトリクス/トレースに付与する
- 最小メトリクス: 実行時間、再試行回数、失敗理由、外部 API 呼数、キュー滞留時間
- 出力先: 運用環境のログ/メトリクス基盤（例: OpenTelemetry）に送出する

## Notifications

- Discord Webhook および GitHub コメントに以下のテンプレートで通知する
  - 起動: 対象 Issue/PR リンク、ブランチ、実行 ID、起動者
  - 成功: PR リンク、マージ可否、CI 状態、承認状態
  - 再試行: 失敗要約、残り回数、次回予定時刻
  - 失敗: 最終失敗理由、実行ログ参照、再開方法

## Security

- Webhook 署名検証（GitHub Secret）を実施する
- Bot/アプリ権限は最小権限で付与する
- Secrets は安全に管理（環境変数/Secret マネージャ等）する
- 承認コメント/レビューの送信元（Bot アカウント）を検証する
- AI 生成物から秘密情報・資格情報をスクラブ/検知する

## Assumptions

- GitHub リポジトリへの必要権限（push, PR 作成, マージ）が付与済み
- Webhook が正しく署名検証される
- Codex は GitHub App として対象リポジトリにインストール済み（GitHub上で連携）
- Codex ボットは PR コメント `@codex review` に自動応答する（システムは Codex API を直接呼び出さない）
- Codex ボットのコメントは識別可能なアカウントから投稿される（ボットユーザー名で判別）
