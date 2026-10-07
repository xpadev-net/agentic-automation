# Plan: AgentRun WebUI（リアルタイムログ閲覧 + GitHub 認証 + public_url 連携）

- status: draft
- generated: 2026-10-07
- last_updated: 2026-10-07
- work_type: feature

## Goal
- Operator に WebUI を追加し、AgentRun の一覧・詳細・リアルタイムログをブラウザから閲覧できるようにする。
- `PUBLIC_URL` を設定可能にし、AgentRun 詳細ページへのリンクを GitHub コメント（実行開始・進捗・失敗・PR 作成通知）に埋め込む。
- GitHub OAuth でログインし、リポジトリ権限ベースで閲覧を認可する。
- ログを DB に永続化し、保持期間経過後は自動削除する。

## Definition of Done
- `PUBLIC_URL` 環境変数を設定すると、GitHub への通知コメントに `PUBLIC_URL/runs/<id>` 形式のリンクが付く。未設定時は従来どおりリンクなし。
- `PUBLIC_URL` にブラウザでアクセスし GitHub ログインすると、自分が権限を持つリポジトリの AgentRun 一覧・詳細が見える。
- 実行中の Run の詳細ページで、ログがリアルタイム（SSE）で流れる。実行完了後も DB 保存分を閲覧できる。
- 権限のないユーザーはログインできても対象 Run を閲覧できない（403）。
- 保存ログは `AGENT_RUN_LOG_RETENTION_DAYS`（既定 30 日）経過後に自動削除される。
- 既存の webhook / report API / Job 起動フローに非互換な変更を入れない。

## Context (workspace)
- Operator: `cmd/operator` + `internal/webhooks/server.go`（Gin、PORT 既定 3000）。既存ルートは `/health`、`/webhooks/github`、`/webhooks/status`、`/api/agent-runs/:id/report`（Bearer 認証）。
- AgentRun: `internal/models/agent_run.go`。`state`（queued/started/succeeded/failed）、`job_name`、`agent_type`、`execution_mode`、Issue/PR への外部キー。ログは現状 report の `logs` → `output.logs_excerpt` に抜粋のみ保存。
- Job/Pod 対応付け: Job に `agent-run-id=<id>` ラベル。`FindJobByAgentRunID`・`GetPodLogs`（TailLines 指定の一回読み）・`ListPods` 済み実装あり。Operator SA は `pods` get/list/watch + `pods/log` get 済み。
- ログの中身: cursor-agent / codex は runner が JSONL を逐次フォーマットして Pod stderr に出力（`agent-runner/pkg/agent/executor.go`、progressWriter 経由）。push 対象はこのフォーマット済み行。
- 通知: `internal/services/github_notification.go` が実行開始・plan 進捗・retry・PR 作成・merge の各コメントを組み立て。
- 認可: `services.AuthorizationService.CheckPermission`（`GetPermissionLevel` で write/maintain/admin = Collaborator+）が既存。
- フロントエンド資産なし（Go のみのリポジトリ）。

## 決定事項（2026-10-07 確認済み）
1. 閲覧権限: **対象 repo が見える人（read+）** を既定。`UI_MIN_REPO_PERMISSION=read|write` で write+ に切替可能。
2. OAuth の器: **既存 GitHub App の Client ID/Client Secret を流用**（GitHub App はユーザー OAuth フローを兼用可）。環境変数は `GITHUB_OAUTH_CLIENT_ID` / `GITHUB_OAUTH_CLIENT_SECRET`。
3. ログ長期保管: **runner→Operator の push 型永続化を本計画のスコープに含める**。保存ログは一定期間（既定 30 日）で自動削除。

## Design

### 全体構成
```
Browser ──HTTPS──▶ Ingress ──▶ Operator(Gin)
                                 ├─ /webhooks/*（既存・署名検証）
                                 ├─ /api/agent-runs/:id/report（既存・Bearer）
                                 ├─ /api/agent-runs/:id/logs    （新規・Bearer: runner→Operator push）
                                 ├─ /auth/github/login, /auth/github/callback, /auth/logout
                                 ├─ /api/ui/*（セッション Cookie 認証 + リポジトリ権限チェック）
                                 │     ├─ GET /api/ui/me
                                 │     ├─ GET /api/ui/agent-runs
                                 │     ├─ GET /api/ui/agent-runs/:id
                                 │     ├─ GET /api/ui/agent-runs/:id/logs?after_seq=N
                                 │     └─ GET /api/ui/agent-runs/:id/logs/stream  (SSE)
                                 ├─ /, /assets/*（埋め込み SPA）
                                 └─ 定期クリーンアップ（保持期間超過ログを削除）

agent-runner ──POST /logs(バッチ)──▶ Operator ──DB──▶ agent_run_logs
                                             └──▶ インメモリ hub ──SSE──▶ Browser
```

### 1. ログ配信方式（採用: runner push + DB 永続化 + SSE）
- **取り込み**: `POST /api/agent-runs/:id/logs`（既存と同じ `OPERATOR_API_TOKEN` Bearer 認証）。runner の executor はフォーマット済み行をバッファし、N 行または T 秒ごとにバッチ POST。`seq`（連番）で順序保証・冪等再送。report API と同じリトライ/バックオフ方針。
- **保存**: `agent_run_logs` テーブル（`id`, `run_id`, `seq`, `line`(text), `created_at`；`(run_id, seq)` unique）。既存 `logs_excerpt` レポートはそのまま。
- **配信**: Operator 内のインメモリ pub/sub（run_id 単位の hub）が、保存成功した行を SSE 購読者へ即時配送。ブラウザはまず REST で履歴（`after_seq` ページング）を取得し、続きを SSE で受信。
- **フォールバック**: push ログが無い Run（旧 runner / push 失敗）は、Pod が残っていれば K8s `GetLogs` で tail 取得して表示（既存権限内）。実行中フォローも SSE 経路を優先。
- **保持期間**: Operator 起動時に定期 goroutine を起こし、`AGENT_RUN_LOG_RETENTION_DAYS`（既定 30、0 で無効）より古い行をバッチ DELETE。
- 代替（却下）: K8s pull 一本（Pod GC で消失・履歴弱い）、SSE をインメモリのみ（再起動で欠損・履歴なし）。

### 2. GitHub 認証
- OAuth フロー: `/auth/github/login` → `https://github.com/login/oauth/authorize?client_id=...&state=...` → `/auth/github/callback` で code を token に交換 → `GET /user` で login を取得。
- クレデンシャル: 既存 GitHub App の Client ID + 発行した Client Secret を流用（決定事項 2）。
- セッション: `ui_sessions` テーブル（`id`, `github_login`, `access_token`（暗号化）, `expires_at`, `created_at`）+ HttpOnly/Secure/SameSite=Lax Cookie（`SESSION_SECRET` で署名）。revoke 可能。
- 認可（Run 閲覧）: Run → Issue → repo(owner/name) を引き、**ユーザーの OAuth token で `GET /repos/{owner}/{repo}`** を叩き、200 = 閲覧可。`UI_MIN_REPO_PERMISSION=write` 時は `GetPermissionLevel` で write+ を要求。404/権限なしは 403。

### 3. public_url → コメント埋め込み
- env `PUBLIC_URL`（例 `https://agentic-automation.example.com`、末尾スラッシュ除去で正規化）。
- `GitHubNotificationService` に runURL ヘルパーを追加し、以下の各コメント本文に `**Logs**: ${PUBLIC_URL}/runs/<agentRunID>` 行を差し込む:
  - 実行開始（`formatPlanCreationProgressMessage`）/ plan 実行進捗（`formatPlanExecutionProgressMessage`）
  - retry 進捗（`FormatRetryProgressMessage`）
  - PR 作成（`makePRCreatedBody`）、merge 成功/失敗、max retries、plan 却下
- `PUBLIC_URL` 未設定時は行ごと省略（後方互換）。テストで差分を固定。

### 4. WebUI フロントエンド
- `web/` に React + Vite + TypeScript の SPA（discord-transcript と同系構成）。ビルド成果物を `internal/webui/dist` に出力し、Go `embed` で Operator バイナリに同梱、SPA fallback 配信。
- ページ: ログイン（Sign in with GitHub のみ）、Run 一覧（state バッジ/agent/repo・issue リンク/経過時間、state・repo フィルタ、ページング）、Run 詳細（status・Issue/PR/plan リンク、ログビューア）。
- ログビューア: 履歴を `GET logs?after_seq=` でページング取得 → EventSource で SSE 追従、自動スクロール（追従トグル）、Run 完了で接続終了。生テキスト表示。

## 実装フェーズ（PR 分割）
1. **public_url 埋め込み**: env 追加 + 通知本文へのリンク差込 + テスト。単独で価値が出る最小差分。
2. **GitHub OAuth + セッション基盤**: `ui_sessions` migration、login/callback/logout、session middleware、`/api/ui/me`。
3. **ログ取り込み + 閲覧 API + SSE**: `agent_run_logs` migration、`POST /api/agent-runs/:id/logs`（Bearer）、runner 側のバッファ/バッチ送信、runs 一覧/詳細/ログ/SSE の UI API、保持期間クリーンアップ。runner 側変更は agent-runner module 内に限定。
4. **SPA 本体**: `web/` + embed 配信 + 各ページ。

## 影響ファイル（予定）
- `internal/config/env.go`（PUBLIC_URL / GITHUB_OAUTH_* / SESSION_SECRET / UI_MIN_REPO_PERMISSION / AGENT_RUN_LOG_RETENTION_DAYS）
- `internal/services/github_notification.go` ＋通知系テスト
- `internal/webhooks/server.go`（ルート追加。`/api/*` の Bearer 認証系と `/api/ui/*` の Cookie 認証系は middleware 分離）
- `internal/webui/`（auth handler, session store, ui api handlers, log hub, embed SPA）※新規
- `internal/repositories/`（ui_sessions, agent_run_logs）※新規
- `internal/models/` + `migrations/`（`ui_sessions`, `agent_run_logs`）
- `agent-runner/pkg/reporter/` + `pkg/agent/executor.go`（ログ行バッファ + バッチ POST）
- `web/`（Vite SPA）※新規、`Makefile`/`Dockerfile`/`k8s`/`README.md`/`docs/deployment-manual.md`（ビルド・env・Ingress 追記）

## セキュリティ/運用メモ
- webhook ルート（署名検証）、runner 向け Bearer 認証 API、UI の Cookie 認証 API は middleware を分離。`/api/ui/*` に Bearer を受け付けない。
- OAuth token は `ui_sessions` に暗号化保存（`SESSION_SECRET` とは別の `UI_TOKEN_ENC_KEY` を想定）し、権限チェック結果は短期キャッシュで GitHub API 呼び出しを抑制。
- SSE は Ingress の `proxy-read-timeout` を延長対象にする必要あり（UI/SSE 用 location を分離 or 全体引上げ）。
- GitHub 側設定: 既存 App の Callback URL に `${PUBLIC_URL}/auth/github/callback` を追加登録。
- ログ行に秘密情報が混入し得るため、runner 側で送信前に既存の sanitize（`sanitizeLogs` と同等方針）を各行へ適用。閲覧も read+ 認可済みユーザーのみ。

## 残リスク
- runner が push に失敗し続けた場合の欠損検知: `seq` の連番欠けを UI 側で検出して警告表示。重大化する場合は report API にログ欠損フラグを載せる拡張を検討。
- Operator が複数レプリカ化した場合の SSE hub はレプリカ間で共有されない（現状単一レプリカ前提。将来は DB ポーリング型 SSE へ切替可能な設計に留める）。
