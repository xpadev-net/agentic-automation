# Agentic Automation

GitHub Issue のコメントを起点に AI agent を実行し、変更を Pull Request として反映するシステムです。Operator が GitHub webhook と実行状態を管理し、agent-runner が Kubernetes Job 内でリポジトリを操作します。

## システム構成

```text
GitHub Issue comment (/run-agent)
        |
        v
Operator (/webhooks/github)
        |
        +-- plan creation Job -- agent-runner -- report --+
        |                                                  |
        +-- plan execution Job <---------------------------+
                                                           |
                                      commit / push / Pull Request
```

- **Operator** (`cmd/operator`): GitHub App で GitHub API を呼び出し、webhook の署名と重複配信を検証します。Issue と AgentRun を保存し、agent-runner 用の Job を作成します。Runner の結果を `/api/agent-runs/:id/report` で受け取ります。
- **agent-runner** (`agent-runner`): Job 内で対象リポジトリを clone し、指定された agent と validation hook を実行します。変更を commit/push し、結果を Operator に報告します。
- **Kubernetes Job**: 1 回の plan creation、plan execution、または通常実行の単位です。Job は Operator が必要なときに動的に作成します。
- **データベース**: AgentRun、Issue、Pull Request、CI・レビュー状態を保存します。接続は MySQL 互換の `DATABASE_URL` を使用します。
- **S3 互換ストレージ**: agent-runner のセッション保存・復元に使用します。ローカル検証ではリポジトリ内の MinIO マニフェストを利用できます。

## Issue から実行する

1. 対象リポジトリの open な Issue にコメントを投稿します。コメントには `/run-agent` を含め、後続の文章を追加指示として記載できます。
2. GitHub が `issue_comment` webhook を Operator の `POST /webhooks/github` に送信します。`created` 以外の action、closed Issue、署名不正の webhook は対象外です。
3. Operator が Issue のコンテキストを取得して AgentRun を作成し、plan creation 用 Job を起動します。
4. agent-runner が plan を生成して Operator API に報告します。成功すると Operator が plan execution 用 Job を起動します。
5. plan execution の agent-runner が変更を検証し、ブランチに commit/push して Pull Request を作成または更新します。結果は Operator が AgentRun と GitHub に反映します。

通常は `feature/issue-<Issue番号>` ブランチを使用し、既存の Pull Request がある場合はそのブランチを継続利用します。

### bot のアサインによる自動実行

`issues` webhook の `assigned` を有効にすると、設定した bot を open な Issue にアサインした時点で `/run-agent` と同じ処理を開始します。対象 bot は `AGENT_ASSIGNMENT_BOT_USERNAME`（任意で `AGENT_ASSIGNMENT_BOT_USER_ID`）で指定し、`AGENT_ASSIGNMENT_REPOSITORY` を設定すると対象リポジトリを限定できます。未指定時の bot 名は `CODEX_BOT_USERNAME`、さらに未指定なら `codex-bot` です。

通常ユーザーへのアサイン、closed Issue、対象外リポジトリは起動せず、同じ webhook の再送や実行中の Issue に対する再アサインも重複起動しません。アサイン解除では実行中の Job を停止しません。GitHub App には Issues の Read and write 権限と `Issues` webhook event を設定してください。

## 対応 agent と選択方法

対応する `AGENT_TYPE` は次の 3 つです。

- `claude-code`（`ANTHROPIC_API_KEY`）
- `cursor-agent`（`CURSOR_API_KEY`）
- `codex`（`CODEX_API_KEY` または `OPENAI_API_KEY`）

Issue からの実行では、次の優先順位で選択されます。

1. Issue label: `agent:claude-code`、`agent:cursor-agent`、`agent:codex`
2. Operator の `AI_AGENT_DEFAULT_TYPE`
3. `claude-code`

複数の対応 label がある場合は、Issue の label 配列で最初に検出されたものが使われます。`/run-agent claude` のようなコメント本文による agent 選択はありません。

## 必要な前提と認証情報

### 外部サービス

- Go（ルート module の `go.mod` に記載されたバージョン）
- MySQL 互換データベース。ローカルでは `k8s/mysql-deployment.yaml` の MySQL、本番では TiDB などを利用できます。
- S3 互換ストレージ。ローカルでは `k8s/minio-deployment.yaml` の MinIO などを利用できます。
- Kubernetes。Operator が Job を作成するため、Operator の ServiceAccount に対象 namespace の Job/Pod 操作権限が必要です。
- Docker（コンテナイメージを作成する場合）と `kubectl`（Kubernetes を利用する場合）

### GitHub App と webhook

GitHub App は GitHub API の認証と webhook の送信元として使用します。対象リポジトリに App をインストールし、実際に利用する Issue、Contents、Pull requests、Metadata などの API 操作に必要な権限と webhook event を設定してください。

- Webhook URL: `https://<公開ホスト名>/webhooks/github`
- Webhook secret: GitHub App の webhook に設定した値を Operator の `GITHUB_WEBHOOK_SECRET` に供給します。Operator は HMAC-SHA256 で署名を検証します。
- App 認証: `GITHUB_APP_ID` と `GITHUB_PRIVATE_KEY` を Operator および Job に供給します。GitHub App 認証のみを使用します。
- Operator API: Job からの結果報告を `OPERATOR_API_TOKEN` で Bearer 認証します。

Operator には少なくとも `DATABASE_URL`、`GITHUB_APP_ID`、`GITHUB_PRIVATE_KEY`、`GITHUB_WEBHOOK_SECRET`、`OPERATOR_API_TOKEN` が必要です。`PORT`（既定値 `3000`）、`ENV`、`LOG_LEVEL` などの設定はコードとマニフェストを参照してください。

`PUBLIC_URL`（任意）を Operator の外部公開 URL（例: `https://agents.example.com`）に設定すると、実行開始・進捗・失敗などの GitHub コメントに `${PUBLIC_URL}/runs/<AgentRun ID>` 形式の WebUI ログリンクが付与されます。未設定時はリンクを付けません。

WebUI の GitHub OAuth ログインを有効化するには `PUBLIC_URL`（Operator の外部到達 URL）に加えて `GITHUB_OAUTH_CLIENT_ID` と `GITHUB_OAUTH_CLIENT_SECRET` を設定します（既存 GitHub App の Client ID/Secret を流用可）。いずれかが未設定の場合、UI 関連ルート（`/auth/github/*`、`/api/ui/*`）は登録されず従来どおりの動作になります。セッションは `ui_sessions` テーブルに保存され、`UI_SESSION_TTL_HOURS`（既定値 `168` 時間）で失効します。保存される OAuth トークンは AES-256-GCM で暗号化され、専用鍵を分けたい場合は `UI_TOKEN_ENC_KEY` を設定してください（未設定時は Client Secret から鍵を導出します）。GitHub App の Client ID/Secret ではなく専用 OAuth App を使う場合は `GITHUB_OAUTH_SCOPE`（例: `repo`）でスコープを指定できます（GitHub App のユーザートークンは scope を使わないため既定は空です）。

`/api/ui` 配下の閲覧 API はログイン済みユーザーの GitHub トークンで対象リポジトリの参照権限を確認します。既定は repo を「見える人」（`read` 相当）で、`UI_MIN_REPO_PERMISSION=write` にすると write 権限保持者に限定できます。runner が push する実行ログは `agent_run_logs` に保存され、`AGENT_RUN_LOG_RETENTION_DAYS`（既定値 `30` 日、`0` で削除無効）を超えた行は定期削除されます。push ログが無い実行については Kubernetes Pod ログのスナップショットにフォールバックします。

WebUI のフロントエンドは `webui/` の React + Vite プロジェクトで、`cd webui && npm ci && npm run build` を実行すると `internal/webui/static/` にバンドルが生成され、Go の `go:embed` で Operator バイナリに同梱されます（生成物はコミット対象）。UI を変更した場合はビルドを再実行して `internal/webui/static/` を更新してください。

秘密情報はローカルの環境変数または Kubernetes Secret から供給し、実値を Git に保存しないでください。本番では Vault と ExternalSecret を使い、Vault の値を Kubernetes Secret に同期して Operator と Job から参照する構成を想定します。ExternalSecret、SecretStore、Argo CD Application の定義はこのリポジトリにはありません。運用側の IaC で namespace、RBAC、Secret、Ingress とともに管理してください。

## ローカル開発・テスト

ルート（Operator）と `agent-runner` は別々の Go module です。

```bash
go mod download
(cd agent-runner && go mod download)

# Operator
go build -o /tmp/operator ./cmd/operator
go test ./... -v
go vet ./...

# agent-runner
(cd agent-runner && go build -o /tmp/agent-runner .)
(cd agent-runner && go test ./... -v)
(cd agent-runner && go vet ./...)
```

Operator を起動するには、接続可能な DB、GitHub App、webhook secret、Operator API token を環境変数で用意します。DB スキーマを適用してから起動します。

```bash
make migrate-up
make run
```

`agent-runner` の CLI を単体で確認する場合は、必須引数に加えて Operator API、GitHub App、agent の認証情報などを用意します。

```bash
(cd agent-runner && go run . \
  --issue-id <issue-id> \
  --repo <owner>/<repo> \
  --prompt "<instruction>")
```

ローカル Kubernetes の補助ターゲットは次のとおりです。現在の kube-context と `K8S_NAMESPACE`（既定値 `default`）に適用します。

```bash
make deploy-infra       # MySQL と MinIO
make deploy-operator    # RBAC と Operator
make deploy-all
```

`make deploy-infra` は MySQL と MinIO を起動し、`make deploy-operator` は `k8s/rbac.yaml` と Operator の Service/Deployment マニフェストを適用します。Secret、DB migration、S3 bucket の準備は別途必要です。

## Kubernetes と Argo CD によるデプロイ

リポジトリ内の `k8s/` と `specs/001-github-agent-automation/k8s/operator/service.yaml` は開発・手動検証用のマニフェストです。`k8s/pod-template.yaml` は Job の構成例ですが、通常の Job は Operator が動的に作成します。Runner を常時 Deployment として起動する構成ではありません。

本番では、運用側の Argo CD Application から次のリソースを同期します。

- 専用 namespace、namespace-scoped RBAC、Operator の Deployment/Service
- `/webhooks/github` に到達する HTTPS Ingress
- Vault/ExternalSecret が生成する GitHub App、Operator API、DB、S3、agent 認証用 Secret
- TiDB などの MySQL 互換 DB と S3 互換セッションストレージ
- Operator が参照する Runner image と Job 用 ServiceAccount

Argo CD Application 自体や同期先、namespace、イメージタグはこのリポジトリで固定されていないため、運用 IaC の定義を確認してください。デプロイ前に `make migrate-up` 相当の DB migration を実行し、成功後に Operator をロールアウトします。ホスト名、namespace、DB 名、bucket 名、Secret の値などの環境固有値は README に記載しません。

## トラブルシューティング

- `/run-agent` で起動しない: Issue が open で、コメント action が `created` であること、trigger 文字列が含まれていること、label の綴りが正しいことを確認します。
- webhook が拒否される: HTTPS で公開されていること、URL が `/webhooks/github` であること、`GITHUB_WEBHOOK_SECRET` が GitHub App の設定と一致することを確認します。
- Job が作成されない: Operator の ServiceAccount/RBAC、対象 namespace、Runner image、Job が参照する Secret を確認します。
- Runner が報告できない: `OPERATOR_SERVICE_NAME`、`OPERATOR_SERVICE_PORT`、`KUBERNETES_NAMESPACE` から Operator Service に到達できることと、`OPERATOR_API_TOKEN` が一致することを確認します。
- agent が認証できない: `AGENT_TYPE` と対応する API key の Secret を確認します。Codex は API key または構成済み OAuth 認証を使用できます。
- 状態を確認する: Operator の `/health` と Kubernetes の Job/Pod ログを確認します。`/health` は主に Operator と DB の状態を示し、Runner の成功までは保証しません。

## 関連ドキュメント

- [Agent Runner README](agent-runner/README.md)
- [ローカルクイックスタート](specs/001-github-agent-automation/quickstart.md)
- [デプロイ手順（補足）](docs/deployment-manual.md)
- [AI agent 実行契約](specs/001-github-agent-automation/contracts/ai-agent-execution.md)
- [Agent Runner 詳細契約](specs/001-github-agent-automation/contracts/agent-runner-detail.md)
- [Agent manifest 仕様](specs/001-github-agent-automation/contracts/agent-manifest.md)
