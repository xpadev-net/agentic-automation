# AIエージェント実行ガイド（claude-code / cursor-agents）

このドキュメントは、本リポジトリにおけるエージェント実行方式と運用手順をまとめたものです。現行実装では Kubernetes の Pod 内で Go 製ラッパー `agent-runner` がエージェント（`claude-code` または `cursor-agents`）を起動し、結果を Operator API にプッシュ通知します。

## 概要

- 実行主体: `agent-runner`（Go バイナリ）
- 実行環境: Kubernetes Pod（Job）
- 通知方式: Pod → Operator API への Push（REST）
- サポートエージェント: `claude-code`（Claude Code CLI）, `cursor-agents`（Cursor Headless）

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
3. エージェント（`claude-code` または `cursor-agents`）を実行（Issue文脈・過去試行・CIログをプロンプトに付与）
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

### 2) Cursor Headless（`cursor-agents`）

- インストール（Dockerfile の例）:
  ```dockerfile
  # Cursor CLI installation
  RUN curl -fsSL https://download.cursor.com/install.sh | sh
  ```
- 呼び出し例（agent-runner 内部からの起動イメージ）:
  ```bash
  cursor agent \
    --cwd /workspace \
    --prompt "Fix issue #${ISSUE_NUMBER}: ${ISSUE_TITLE}

  Description:
  ${ISSUE_BODY}

  Previous Attempts:
  ${PREVIOUS_ATTEMPTS}

  Please fix the issue and ensure all tests pass." \
    --headless \
    --no-confirm
  ```
- 必須環境変数:
  - `CURSOR_API_KEY`

## エージェント選択ロジック

優先度順:
1. Issue ラベル
   - `agent:claude-code` → `claude-code`
   - `agent:cursor-agents` → `cursor-agents`
2. 環境変数 `AI_AGENT_DEFAULT_TYPE`
3. 何も指定がなければ `claude-code`

## 主要環境変数

共通:
- `OPERATOR_API_URL`: Operator API ベースURL
- `OPERATOR_API_TOKEN`: API 認証トークン
- `AGENT_RUN_ID`: AgentRun レコードID
- `AGENT_TYPE`: `claude-code` または `cursor-agents`
- `GITHUB_TOKEN`: GitHub PAT（クローン/Push/PR 作成に使用）
- `WORKSPACE_DIR`: 作業ディレクトリ（例: `/workspace`）

エージェント別:
- `ANTHROPIC_API_KEY`（`claude-code` 用）
- `CURSOR_API_KEY`（`cursor-agents` 用）

Kubernetes での注入例は `k8s/pod-template.yaml` および `internal/clients/kubernetes.go` を参照してください。

## PR 作成（`gh` コマンド）

`GITHUB_TOKEN` による認証を前提に、次のように PR を作成します（ベースは `master`）。

```bash
git checkout -b feature/issue-123 master
git push origin HEAD
gh pr create --base master --head feature/issue-123 \
  --title "Fix: issue #123" \
  --body "自動生成: エージェントによる修正"
```

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
