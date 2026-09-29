## Agent Runner 概要

Agent Runner は、Kubernetes の Pod（Job）内で実行される AI エージェント実行コンポーネントです。対象リポジトリをクローンし、プロンプトに基づいて `claude-code` / `cursor-agent` / `codex` を起動、lint/型チェック・フック・バリデーションを実行して変更をコミットし、PR を作成します。実行結果は Operator API に push 通知されます。

- エントリーポイント: `agent-runner/main.go`
- 実行契約: `specs/001-github-agent-automation/contracts/ai-agent-execution.md`
- マニフェスト仕様: `specs/001-github-agent-automation/contracts/agent-manifest.md`
- 詳細仕様: `specs/001-github-agent-automation/contracts/agent-runner-detail.md`

---

## 主な機能
- リポジトリのクローン（`feature/issue-{番号}` ブランチ作成）
- マニフェスト（`.agent-config.yaml`）のロード、pre/post フックと validation 実行
- AI エージェント実行（claude-code / cursor-agent / codex）
- 変更検知、コミット、リモートに push、PR 作成
- セッションの保存・復元（S3/MinIO）
- 実行結果の Operator API へのレポート

---

## ビルド方法
### Docker イメージ
```bash
# ルートで実行
make docker-build          # イメージ作成（latest / sha タグ）
make docker-push           # レジストリへ push
# まとめて
make docker-build-push
```

### ローカルバイナリ
```bash
# ルートで実行
make build                 # bin/agent-runner を生成
# または直接
go build -o bin/agent-runner ./cmd/agent-runner
```

---

## 実行方法
通常は Operator から Kubernetes Job として起動されます。ローカルでの動作確認用に CLI でも実行できます。

### CLI
```bash
./bin/agent-runner \
  --issue-id 188 \
  --repo your-org/your-repo \
  --prompt "Fix issue #188: README 整備"
# 任意
# --previous-attempts '{...JSON...}'
# --ci-logs "<failed CI logs>"
```

必須フラグ: `--issue-id`, `--repo`, `--prompt`

---

## 環境変数
Agent Runner は Kubernetes 環境変数を前提とします（Operator から注入）。必須/任意は以下です。

- 必須（Operator URL 構築）
  - `KUBERNETES_NAMESPACE`: Namespace
  - `OPERATOR_SERVICE_NAME`: Operator サービス名
  - `OPERATOR_SERVICE_PORT`: Operator サービスポート
- 必須（認可・実行）
  - `OPERATOR_API_TOKEN`: Operator API の Bearer トークン
  - `AGENT_RUN_ID`: AgentRun レコード ID（整数、>0）
  - `AGENT_TYPE`: `claude-code` | `cursor-agent` | `codex`
- GitHub App（認証）
  - `GITHUB_APP_ID`: App ID（数値）
  - `GITHUB_PRIVATE_KEY`: App 秘密鍵（PEM 本文、改行含む）
- AI エージェント
  - `ANTHROPIC_API_KEY`: Claude 用（`AGENT_TYPE=claude-code` のとき必須）
  - `CURSOR_API_KEY`: Cursor 用（`AGENT_TYPE=cursor-agent` のとき必須）
  - `CODEX_API_KEY` or `OPENAI_API_KEY`: Codex の API キー認証用（OAuth 認証ファイルを使わない場合、どちらか一方）
- 任意（デフォルト/挙動）
  - `WORKSPACE_DIR`: 作業ディレクトリ（デフォルト `/workspace`）
  - `RETRY_COUNT`: リトライ回数（整数、>=0）>0 でセッション復元
  - `CURSOR_MODEL`: Cursor モデル（デフォルト `auto`）
  - `CURSOR_ALLOW_WRITE`: `true`/`false`（デフォルト `true`、codex の `--sandbox` にも適用）
  - `CODEX_MODEL`: Codex モデル（デフォルト空 = Codex CLI のデフォルト）

### Codex OAuth 認証ファイル

Codex は API キーの代わりに ChatGPT OAuth の `auth.json` を使用できます。Operator が作成する Codex Job は、`CODEX_AUTH_SECRET`（デフォルト `codex-auth`）で指定した Kubernetes Secret の `auth.json` キーだけを read-only 入力としてマウントします。init container が内容を Job 固有の `emptyDir` にコピーし、`/home/agent/.codex/auth.json` を mode `0600` の書き込み可能なファイルとして用意します。これにより Codex CLI によるトークン更新は元の Secret を変更せず、Pod 終了時に破棄されます。API キー用 Secret は OAuth-only 構成との両立のため optional です。

Vault から同期する場合も、Kubernetes Secret の契約は Secret 名 `codex-auth`（または `CODEX_AUTH_SECRET` の値）、データキー `auth.json` です。Vault/Kubernetes のいずれにも JSON をログやマニフェストの平文として出力しないでください。OAuth トークンの更新結果は Secret に逆同期されないため、期限切れや失効時にはローカルで再認証し、Vault の値を手動で更新して Secret を再同期してください。

セッション保存処理では `~/.codex/auth.json` は引き続きアーカイブ対象外です。Codex 起動前に API キーまたは non-empty の認証ファイルが見つからない場合、認証内容を含まないエラーで終了します。

備考:
- Operator API の URL は `http://{OPERATOR_SERVICE_NAME}.{KUBERNETES_NAMESPACE}.svc.cluster.local:{OPERATOR_SERVICE_PORT}` で自動構築されます。
- PAT フォールバックはありません。GitHub App 認証のみを前提とします。

---

## 設定ファイル（.agent-config.yaml）
リポジトリルートに配置可能なマニフェスト（省略可）。存在すれば pre/post フックと validation を順に実行します。

- 位置: ルートの `.agent-config.yaml`
- バージョン: `version: "1.0"` 必須
- スキーマ（抜粋）:
  - `hooks.pre[]`: 実行前フック（`name`, `command`, `timeout`, `required`）
  - `hooks.post[]`: 実行後フック
  - `validation[]`: バリデーション

例:
```yaml
version: "1.0"
hooks:
  pre:
    - name: install deps
      command: make deps
      timeout: 2m
      required: true
  post:
    - name: fmt
      command: go vet ./...
      timeout: 1m
      required: false
validation:
  - name: tests
    command: make test
    timeout: 10m
    required: true
```

詳細は `specs/001-github-agent-automation/contracts/agent-manifest.md` を参照してください。

---

## 開発者向け（ローカルテスト/デバッグ）
- 依存取得: `make deps`
- 単体/結合テスト: `make test`
- ログ: `stderr` にビルド情報と進捗を出力（Operator 通知エラーは警告表示）
- 失敗時の扱い:
  - フック失敗は失敗として Operator に通知
  - バリデーション失敗は PR 作成前にエラー終了（通知はしない）
  - Post-hook 失敗は警告として継続（PR 作成後）

---

## サポートする AI エージェント
- `claude-code`（`@anthropic/claude-code` CLI）
- `cursor-agent`（Cursor Headless）
- `codex`（`@openai/codex` CLI、`codex exec` を非対話実行）

Claude/Cursor の API キーは環境変数 `ANTHROPIC_API_KEY` / `CURSOR_API_KEY`、Codex は `CODEX_API_KEY`（または `OPENAI_API_KEY`）もしくは上記 OAuth 認証ファイルで注入してください。
