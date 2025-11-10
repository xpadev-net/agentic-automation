## プロジェクト概要

本プロジェクトは、GitHub の Issue/PR と連携して、AIエージェントが自動で開発タスクを実行・提案・レビュー・マージまで行う自動化システムです。Operator（常駐サービス）と Agent Runner（Kubernetes Job で起動する実行ポッド）が協調し、Issueコメントなどのトリガーからエージェントを起動、コード変更を提案し、PR作成・レビュー（Codex）・自動マージ・依存関係の管理までを支援します。

### アーキテクチャ概要
- **Operator（Go サービス）**: トリガー検知、状態管理、GitHub 連携、ジョブ起動制御
- **Agent Runner（Go バイナリ）**: Kubernetes Pod 内で AI エージェントを実行し、変更をコミット/PR 化
- **データベース（MySQL 8.0+）**: AgentRun、Issue、PR、CI ステータス、レビュー結果などの管理
- **オブジェクトストレージ（S3/MinIO）**: エージェントセッションやログの保存・復元
- **GitHub App**: 認証・権限委譲に GitHub App を使用（PAT フォールバックなし）

### 主要機能
- **Issueコメントトリガー**: 指定コメント・ラベル・条件で AI 実行を開始
- **AI 実行（claude-code / cursor-agent）**: 指定プロンプトでリポジトリに対する変更を提案・実装
- **PR 作成**: 変更を `feature/issue-{番号}` ブランチにコミットし PR 化
- **Codex レビュー**: 変更の自動レビューと改善提案
- **自動マージ**: 条件（CI成功・承認など）を満たせば自動マージ
- **依存関係管理**: Issue/PR 間の依存とブロッカー関係をモデル化
- **ブランチ同期とAIコンフリクト解決**: 作業開始前とコミット後にデフォルトブランチとの同期を自動検知・実行。コンフリクト発生時はAIで自動解消

---

## クイックスタート（簡易）
ローカル開発の詳細手順は以下を参照してください。
- `specs/001-github-agent-automation/quickstart.md`

デプロイ手順の詳細は以下を参照してください。
- `docs/deployment-manual.md`

---

## ディレクトリ構成
- `cmd/`: エントリーポイント（`operator` 等）
- `internal/`: Operator のアプリケーションロジック（clients/config/models/repositories/services/webhooks 等）
- `agent-runner/`: Agent Runner（実行バイナリ・Dockerfile・内部パッケージ）
- `k8s/`: 開発用 MySQL/MinIO、RBAC、Pod テンプレート等
- `migrations/`: MySQL マイグレーション
- `docs/`: デプロイ手順などのドキュメント
- `specs/001-github-agent-automation/`: 仕様・契約・計画・クイックスタート
- `tests/`: unit / integration / contract テスト
- `resources/` `scripts/` `Makefile`: 付随スクリプトやビルド・実行補助

---

## 開発者向け
### 前提
- Go 1.22+
- MySQL 8.0+
- Kubernetes（開発用クラスタ）
- Docker / kubectl / gh（必要に応じて）

### よく使う Make ターゲット
```bash
make help                 # ターゲット一覧
make deps                 # 依存取得
make build                # バイナリビルド（operator / agent-runner）
make test                 # 全テスト実行
make vet                  # go vet

# マイグレーション
make migrate-up           # up
make migrate-down         # down
make migrate-status       # 状態確認

# ローカル起動（Operator）
make run                  # cmd/operator を実行

# Docker イメージ（Agent Runner / Operator）
make docker-build         # agent-runner イメージ build
make docker-push          # agent-runner イメージ push
make docker-build-push    # agent-runner build+push
make docker-build-operator
make docker-push-operator
make docker-build-push-operator

# 開発用デプロイ補助
make deploy-infra         # MySQL + MinIO を適用
make deploy-operator      # Operator を適用
make deploy-all           # まとめて適用
make k8s-secrets          # 必要な Secret 作成コマンドを表示
```

### テスト
```bash
make test
```

---

## 関連ドキュメント
- デプロイ: `docs/deployment-manual.md`
- クイックスタート: `specs/001-github-agent-automation/quickstart.md`
- 仕様概要: `specs/001-github-agent-automation/spec.md`
- 実行契約: `specs/001-github-agent-automation/contracts/ai-agent-execution.md`
- Agent Runner 詳細: `specs/001-github-agent-automation/contracts/agent-runner-detail.md`
- マニフェスト仕様: `specs/001-github-agent-automation/contracts/agent-manifest.md`

---

## 技術スタック
- **言語**: Go 1.22+
- **DB**: MySQL 8.0+
- **オブジェクトストレージ**: S3 / MinIO
- **オーケストレーション**: Kubernetes
- **AI エージェント**: Claude Code（`@anthropic/claude-code`）、Cursor Headless（`cursor-agent`）
- **その他**: GitHub App（認証）, Docker, Pressly Goose（migrations）

---

## Agent Runner について
Agent Runner 単体の詳細は `agent-runner/README.md` を参照してください。