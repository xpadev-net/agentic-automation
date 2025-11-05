# AIエージェント実行ガイド（claude-code / cursor-agent）

このドキュメントは、本リポジトリにおけるエージェント実行方式と運用手順をまとめたものです。現行実装では Kubernetes の Pod 内で Go 製ラッパー `agent-runner` がエージェント（`claude-code` または `cursor-agent`）を起動し、結果を Operator API にプッシュ通知します。

## 概要

- 実行主体: `agent-runner`（Go バイナリ）
- 実行環境: Kubernetes Pod（Job）
- 通知方式: Pod → Operator API への Push（REST）
- サポートエージェント: `claude-code`（Claude Code CLI）, `cursor-agent`（Cursor Headless）

参考仕様:
- `specs/001-github-agent-automation/contracts/ai-agent-execution.md`
- `specs/001-github-agent-automation/contracts/agent-runner-detail.md`

## ブランチ運用

- デフォルトブランチ: `master`
- 作業ブランチ規約: `feature/issue-{番号}` を `master` から作成

例:
```bash
git checkout -b feature/issue-123 master
```

## 実行フロー（要点）

1. 対象リポジトリを `WORKSPACE_DIR` にクローン
2. `master` から `feature/issue-{番号}` を作成
   - specs以下のタスク以外を依頼された場合はこのブランチの命名規則を無視してよいです
3. エージェント（`claude-code` または `cursor-agent`）を実行（Issue文脈・過去試行・CIログをプロンプトに付与）
4. Lint/型チェックを実行
5. 変更検知（変更があればコミット/Push）
6. `gh` コマンドで PR 作成（ベース: `master`）
7. 実行レポートを Operator API へ送信

## サポートエージェント

### 1) Claude Code（`claude-code`）

- インストール（Dockerfile の例）:
  ```dockerfile
  RUN npm install -g @anthropic/claude-code
  ```
- 呼び出し例（agent-runner 内部からの起動イメージ）:
  ```bash
  claude-code \
    --workspace /workspace \
    --task "Fix issue: ${ISSUE_TITLE}

  ${ISSUE_BODY}

  Previous attempts:
  ${PREVIOUS_ATTEMPTS}

  CI failures:
  ${CI_LOGS}" \
    --non-interactive \
    --max-tokens 8000
  ```
- 必須環境変数:
  - `ANTHROPIC_API_KEY`

### 2) Cursor Headless（`cursor-agent`）

- インストール（Dockerfile の例）:
  ```dockerfile
  # Cursor CLI installation
  RUN curl https://cursor.com/install -fsS | bash
  ```
- 呼び出し例（agent-runner 内部からの起動イメージ）:
  ```bash
  cursor-agent -p "Fix issue #${ISSUE_NUMBER}: ${ISSUE_TITLE}

  Description:
  ${ISSUE_BODY}

  Previous Attempts:
  ${PREVIOUS_ATTEMPTS}

  Please fix the issue and ensure all tests pass."
  ```
- 必須環境変数:
  - `CURSOR_API_KEY`

## エージェント選択ロジック

優先度順:
1. Issue ラベル
   - `agent:claude-code` → `claude-code`
   - `agent:cursor-agent` → `cursor-agent`
2. 環境変数 `AI_AGENT_DEFAULT_TYPE`
3. 何も指定がなければ `claude-code`

## 主要環境変数

共通:
- `KUBERNETES_NAMESPACE`: Kubernetes namespace（Downward API から自動注入）
- `OPERATOR_SERVICE_NAME`: Operator サービス名（例: `agent-operator`）
- `OPERATOR_SERVICE_PORT`: Operator サービスポート（例: `3000`）
  - これら3つから Operator API URL を自動構築: `http://{service}.{namespace}.svc.cluster.local:{port}`
- `OPERATOR_API_TOKEN`: API 認証トークン
- `AGENT_RUN_ID`: AgentRun レコードID
- `AGENT_TYPE`: `claude-code` または `cursor-agent`
- `WORKSPACE_DIR`: 作業ディレクトリ（例: `/workspace`）

エージェント別:
- `ANTHROPIC_API_KEY`（`claude-code` 用）
- `CURSOR_API_KEY`（`cursor-agent` 用）

GitHub App（必須）:
- `GITHUB_APP_ID`: GitHub App ID
- `GITHUB_PRIVATE_KEY`: GitHub App 秘密鍵（PEM 本文、改行含む）
- `GITHUB_WEBHOOK_SECRET`: Webhook 署名検証シークレット（Operator のみ保持。agent-runner Pod には注入しない）

**注**: 以前は `OPERATOR_API_URL` を直接設定していましたが、現在は Kubernetes Downward API を利用して自動的に URL を構築します。

Kubernetes での注入例は `k8s/pod-template.yaml` および `internal/clients/kubernetes.go` を参照してください。

## 認証フロー（GitHub App 前提）

本リポジトリは GitHub App を前提とします（PAT フォールバックなし）。概要:

1. App 認証用 JWT を生成
2. リポジトリに紐づく Installation ID を取得
3. Installation Token を発行（有効期限 1 時間）
4. 発行済みトークンで GitHub API と Git 操作を実行

注意:
- トークンはログ出力しないこと
- 必要権限（例: Contents: RW, Issues: RW, Pull Requests: RW, Metadata: R）を付与
- トークンは各操作前に再取得する実装を推奨（長時間実行対策）

## specs 配下タスクの運用ポリシー

`specs/` 以下のタスク（仕様・契約・計画・チェックリスト等）に取り組むよう依頼された場合は、タスクを完了した後に必ず次の手順でブランチ作成・コミット・PR 作成を行ってください（ベースは `master`）。

```bash
# 1) ブランチ作成（master から）
git checkout -b feature/issue-<番号> master

# 2) 変更のステージングとコミット
git add specs/
git commit -m "docs(specs): 完了したタスクを反映 (#<番号>)"

# 3) Push（現在の HEAD をそのまま）
git push origin HEAD

# 4) PR 作成（gh コマンド、ベースは master）
gh pr create --base master --head feature/issue-<番号> \
  --title "Docs: specs タスク完了 #<番号>" \
  --body "specs 配下のタスクを完了し、ドキュメントを更新しました。"
```

補足:
- 既存の規約どおり、デフォルトブランチは `master` です。
- タスクが複数ファイルに跨る場合も 1 PR にまとめて構いません（レビュー粒度に応じて適宜分割可）。
- コード変更が伴う場合は、該当コードと `specs/` の更新を同一ブランチでコミットし、PR の説明に両者の関係を明記してください。
- タスク完了時は、`specs/001-github-agent-automation/tasks.md` の該当チェック項目を `[x]` に更新してください（例: `[ ] T001` → `[x] T001`）。

## 参考ファイル

- 実行契約: `specs/001-github-agent-automation/contracts/ai-agent-execution.md`
- 詳細仕様: `specs/001-github-agent-automation/contracts/agent-runner-detail.md`
- Pod テンプレート例: `k8s/pod-template.yaml`
- 実装抜粋: `agent-runner/pkg/agent/executor.go`, `internal/models/agent_run.go`, `internal/clients/kubernetes.go`
