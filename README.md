# Agentic Automation

GitHub Issue のコメントを起点に、AI エージェントの実行を Kubernetes Job として起動し、変更を Pull Request に反映するシステムです。Operator が GitHub webhook と実行状態を管理し、Agent Runner が Job 内でリポジトリを変更します。

## システム構成

```text
GitHub Issue comment (/run-agent)
          |
          v
Operator (webhook / state / GitHub API / Job creation)
          |
          v
Kubernetes Job -> Agent Runner -> commit / push / Pull Request
          |
          +-> Operator API report
```

- **Operator** (`cmd/operator`): GitHub App で webhook を検証し、Issue の状態・AgentRun を管理します。Agent Runner 用の Kubernetes Job を作成し、Runner からの結果報告を受け付けます。
- **Agent Runner** (`agent-runner`): Job 内で対象リポジトリを取得し、選択されたエージェント、フック、validation を実行します。変更があればブランチへ commit/push し、Pull Request を作成して Operator API に結果を報告します。
- **Kubernetes Job**: 1 回の AgentRun の実行単位です。Job 自体の再起動は行わず、必要なリトライやセッション復元は Operator と Runner が扱います。
- **DB**: AgentRun、Issue、Pull Request、CI/レビュー状態を保存します。アプリケーションは MySQL 互換の接続方式を使用します。
- **S3 互換ストレージ**: Agent Runner のセッション保存・復元に使用します。開発環境ではリポジトリの MinIO マニフェストを利用できます。

## Issue から実行する

1. 対象リポジトリに open な Issue を作成します。
2. Issue に `/run-agent` を含むコメントを投稿します。コメント本文の `/run-agent` より後ろはエージェントへの追加指示として扱われます。
3. GitHub が `issue_comment` webhook を Operator の `/webhooks/github` に送信します。
4. Operator が署名と重複配信を検証し、Issue のコンテキストを収集して plan creation 用の Job を作成します。
5. Job の Agent Runner がエージェントを実行し、結果を Operator に報告します。通常の実装では、その後の plan execution も Job として実行されます。
6. Runner は変更を `feature/issue-<番号>`（既存の open PR がある場合はそのブランチ）に反映し、Pull Request を作成または更新します。

closed な Issue、`created` 以外のコメントイベント、署名不正の webhook は実行対象になりません。

## 対応エージェントと選択方法

対応する値は次の 3 つです。

- `claude-code`
- `cursor-agent`
- `codex`

選択の優先順位は次のとおりです。

1. Issue ラベル `agent:claude-code`、`agent:cursor-agent`、`agent:codex`
2. Operator の `AI_AGENT_DEFAULT_TYPE`
3. デフォルトの `claude-code`

Issue に複数の対応ラベルがある場合は、実装が最初に検出したラベルが使われます。ラベル名以外の任意のコメント構文でエージェントを選択する機能はありません。

Runner Job には選択されたエージェント用の認証情報を Secret から注入します。Claude Code は `ANTHROPIC_API_KEY`、Cursor は `CURSOR_API_KEY`、Codex は `CODEX_API_KEY`/`OPENAI_API_KEY` または Codex OAuth の `auth.json` を使用します。

## 必要な前提と設定

### 外部サービス

- Go 1.24.9（`go.mod` の toolchain）
- MySQL 互換データベース。開発用には `k8s/mysql-deployment.yaml` の MySQL を使用できます。本番構成で TiDB を使う場合も、MySQL 互換の接続先として `DATABASE_URL` を設定します。
- S3 互換ストレージ。開発用には `k8s/minio-deployment.yaml`、本番では利用する S3 互換サービスを設定します。
- Kubernetes。Operator は Agent Runner Job を作成するため、対象 namespace の Job/Pod 権限が必要です。
- Docker（イメージをビルドする場合）と `kubectl`（Kubernetes を使う場合）。

### GitHub App と webhook

GitHub App は GitHub API 操作と webhook の送信元を担います。対象リポジトリにインストールし、少なくとも Contents、Issues、Pull requests、Metadata の操作に必要な権限と、利用する webhook イベントを設定してください。必要な権限は対象運用に合わせて GitHub App の設定で確認してください。

- Webhook URL: `https://<公開ホスト名>/webhooks/github`
- Webhook secret: GitHub App に設定した値を `GITHUB_WEBHOOK_SECRET` に供給します。Operator は HMAC-SHA256 で署名を検証します。
- Operator の認証: `GITHUB_APP_ID` と `GITHUB_PRIVATE_KEY` を使用します。PAT はサポートしていません。

Operator は主に `DATABASE_URL`、`GITHUB_APP_ID`、`GITHUB_PRIVATE_KEY`、`GITHUB_WEBHOOK_SECRET`、`OPERATOR_API_TOKEN` を必要とします。ポートは `PORT`（既定値 `3000`）、環境は `ENV`、ログレベルは `LOG_LEVEL` で設定できます。Runner Job のイメージ、タイムアウト、同時実行数、S3 接続先、Secret 名などは [`.env.example`](.env.example) と `internal/clients/kubernetes.go` を参照してください。

秘密情報は `.env`、Kubernetes Secret、または本番の Vault/ExternalSecret から供給し、実値を Git に保存しないでください。Vault を使う場合は ExternalSecret が Vault の値を Kubernetes Secret に同期し、Operator と Job はその Secret を参照します。ExternalSecret と Argo CD のリソースはこのリポジトリには含まれていないため、運用 IaC 側で namespace、Secret、RBAC、Operator Deployment、公開 Ingress を管理してください。

## ローカル開発・テスト

ルート Operator と `agent-runner` は別々の Go module です。

```bash
# 依存関係
go mod download
(cd agent-runner && go mod download)

# ビルド
mkdir -p bin
go build -o bin/operator ./cmd/operator
(cd agent-runner && go build -o ../bin/agent-runner .)

# テストと静的検査
CGO_ENABLED=1 go test ./... -v
(cd agent-runner && go test ./... -v)
go vet ./...
(cd agent-runner && go vet ./...)
```

Operator のローカル起動は、接続可能なデータベース、GitHub App の設定、webhook secret などを用意したうえで実行します。

```bash
cp .env.example .env
# .env の値をローカル環境の値に置き換える
make migrate-up
make run
```

ローカル Kubernetes の補助ターゲットは次のとおりです。これらは開発・検証用であり、本番デプロイの GitOps 手順ではありません。

```bash
make deploy-infra       # MySQL と MinIO
make deploy-operator    # RBAC と Operator Service/Deployment
make deploy-all
./scripts/setup-minio.sh
```

Agent Runner の CLI を直接確認する場合は、必須フラグを指定します。実際のエージェント実行には、Kubernetes 内部の Operator API、GitHub App、選択したエージェントの認証情報などが必要です。

```bash
(cd agent-runner && go run . \
  --issue-id <issue-id> \
  --repo <owner>/<repo> \
  --prompt "<instruction>")
```

## Kubernetes と Argo CD によるデプロイ

### 手動検証用マニフェスト

リポジトリ内の `k8s/` には開発用の MySQL/MinIO、Ingress、RBAC、Job のテンプレートがあります。Operator の Deployment/Service と RBAC の例は `specs/001-github-agent-automation/k8s/operator/service.yaml` にあります。Job は Operator が API 経由で動的に作成するため、Runner Job を常時 Deployment として起動しません。

`make deploy-infra`、`make deploy-operator`、`make deploy-all` は、現在の kube-context と namespace（`K8S_NAMESPACE`、既定値 `default`）に対して `kubectl apply` します。イメージ、Secret、Ingress、namespace は環境に合わせて確認・置換してください。

### 本番 GitOps

本番では、次の構成を Argo CD の Application から同期する形を想定します。

- 専用 namespace と namespace-scoped RBAC
- Operator Deployment/Service と `/webhooks/github` に到達する HTTPS Ingress
- Vault/ExternalSecret が生成する GitHub App、Operator API、DB、S3、エージェント認証用 Secret
- MySQL 互換の TiDB などの DB と S3 互換セッションストレージ
- Operator が参照する Runner イメージと Job 用の ServiceAccount

Argo CD の Application や Vault の SecretStore/ExternalSecret は運用 IaC リポジトリで管理します。このアプリケーションリポジトリには Argo CD Application 定義がないため、Argo CD の同期先・namespace・イメージタグは運用側の定義を確認してください。認証情報や実環境のホスト名、DB 名、バケット名は README に記載しません。

DB スキーマは Operator 起動時に自動適用されません。デプロイ前に `make migrate-up` 相当のマイグレーションを実行し、成功を確認してから Operator をロールアウトしてください。

## トラブルシューティング

- `/run-agent` で起動しない: Issue が open であること、コメント action が `created` であること、Issue に対応ラベルがある場合は綴りが正しいことを確認します。
- webhook が拒否される: GitHub App の webhook URL が `/webhooks/github` であること、`GITHUB_WEBHOOK_SECRET` が一致すること、HTTPS 経由で到達できることを確認します。
- Job が作成されない: Operator の Kubernetes ServiceAccount/RBAC、Runner イメージ、namespace、`AGENT_RUNNER_IMAGE` を確認します。
- Runner が報告できない: Job に `OPERATOR_API_TOKEN` が注入され、`OPERATOR_SERVICE_NAME`/`OPERATOR_SERVICE_PORT` から Operator Service に到達できることを確認します。
- エージェントが認証できない: 選択した Agent Type と対応する Secret を確認します。Codex OAuth を使う場合は Secret の `auth.json` が空でないことを確認し、認証ファイルの内容をログに出さないでください。
- `/health` は Operator の稼働状態と DB 接続を確認します。HTTP 200 でも GitHub webhook の公開経路や Runner Job の実行成功までは保証しません。

## 関連ドキュメント

- [Agent Runner README](agent-runner/README.md)
- [ローカルクイックスタート](specs/001-github-agent-automation/quickstart.md)
- [デプロイ手順（補足）](docs/deployment-manual.md)
- [AI エージェント実行契約](specs/001-github-agent-automation/contracts/ai-agent-execution.md)
- [Agent Runner 詳細契約](specs/001-github-agent-automation/contracts/agent-runner-detail.md)
- [Agent マニフェスト仕様](specs/001-github-agent-automation/contracts/agent-manifest.md)
