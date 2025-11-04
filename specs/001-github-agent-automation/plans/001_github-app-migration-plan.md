# GitHub App 認証への移行実装プラン

## 概要

現在の Personal Access Token (PAT) ベースの認証を GitHub App の Installation Token に移行する実装計画。

## 背景

### 現状の問題点
- `GITHUB_TOKEN` (PAT) を使用
- トークンが無期限で失効しない（セキュリティリスク）
- ユーザーアカウント依存
- 細かい権限制御が困難

### GitHub App のメリット
- トークンが 1 時間で自動失効（セキュリティ向上）
- リポジトリ単位での細かい権限制御
- ユーザーアカウント変更の影響を受けない
- 監査証跡が明確
- レート制限が緩和（5000 req/hour per installation）

### 既存インフラの状況
- GitHub App 認証のインフラは 80% 実装済み
- 環境変数 `GITHUB_APP_ID`, `GITHUB_PRIVATE_KEY` は設定済み
- `internal/clients/github.go:43` に「GitHub App installation token」のコメントあり
- 実際には PAT を使用中

### 決定事項（本移行での前提）
- `GITHUB_PRIVATE_KEY` は PEM 文字列をそのまま環境変数に格納する（改行含む）
- PAT フォールバックは導入しない（`GITHUB_TOKEN` 依存コードは削除）

## 実装計画

### Phase 1: Installation Token 生成機能の実装（Operator サービス）

**目的**: GitHub App として Installation Token を生成・管理する機能を実装

**修正対象ファイル**:
- `internal/clients/github.go`
- `go.mod` / `go.sum`

**実装内容**:

1. **依存ライブラリの追加**
   ```go
   // go.mod に追加
   github.com/bradleyfalzon/ghinstallation/v2
   ```

2. **Installation Token 管理構造体の追加**
   ```go
   type InstallationTokenCache struct {
       tokens     map[int64]*TokenEntry // installation ID → token
       mutex      sync.RWMutex
       appID      int64
       privateKey []byte
   }

   type TokenEntry struct {
       Token     string
       ExpiresAt time.Time
   }
   ```

3. **Installation Token 取得メソッドの実装**
   - JWT 生成（GitHub App の認証）
   - Installation ID の取得（リポジトリから）
   - Installation Token の取得
   - トークンキャッシュ（1 時間 TTL）

4. **GitHub Client の拡張**
   ```go
   // PAT フォールバックなし、常に GitHub App を利用
   type GitHubClient struct {
       tokenCache *InstallationTokenCache
       appID      int64
       privateKey []byte // 環境変数から読み込んだ PEM
   }

   // リポジトリに対する認可済みクライアントを返す
   func (c *GitHubClient) ForRepo(ctx context.Context, owner, repo string) (*github.Client, error) {
       // Installation ID を取得
       // キャッシュをチェック、有効期限内なら token を使用
       // 期限切れなら新しい token を取得
       // token で認証済み *github.Client を返す
   }
   ```

**テスト項目**:
- [ ] JWT 生成が正常に動作すること
- [ ] Installation ID が取得できること
- [ ] Installation Token が取得できること
- [ ] トークンキャッシュが機能すること
- [ ] 有効期限切れ時に再取得されること

---

### Phase 2: Operator サービスでの GitHub App 利用

**目的**: Webhook ハンドラーで GitHub App の Installation Token を使用

**修正対象ファイル**:
- `internal/webhooks/handlers/issue_comment.go`
- `internal/webhooks/handlers/agent_report.go`
- `internal/clients/github.go`

**実装内容**:

1. **GitHub Client 初期化の変更**
   ```go
   // internal/clients/github.go
   // GitHub App 前提（PAT フォールバックなし）
   func NewGitHubAppClient() (*GitHubClient, error) {
       appID := os.Getenv("GITHUB_APP_ID")
       pem := os.Getenv("GITHUB_PRIVATE_KEY") // PEM 本文
       if appID == "" || pem == "" {
           return nil, errors.New("missing GitHub App credentials")
       }
       return newGitHubAppClient(strings.TrimSpace(appID), []byte(pem))
   }
   ```

2. **API 呼び出し前のクライアント取得**
   ```go
   // internal/webhooks/handlers/issue_comment.go
   func (h *Handler) handleIssueComment(ctx context.Context, event *github.IssueCommentEvent) error {
       owner := event.Repo.GetOwner().GetLogin()
       repo := event.Repo.GetName()

       // リポジトリ用の認証済みクライアントを取得
       client, err := h.githubClient.ForRepo(ctx, owner, repo)
       if err != nil {
           return err
       }

       // 以降は client を直接使用
       // ...
   }
   ```

3. **全 API 呼び出し箇所の更新**
   - `Issues.Get()`
   - `Issues.ListComments()`
   - `Issues.CreateComment()`
   - `PullRequests.Get()`
   - `PullRequests.Create()`
   - `Repositories.Get()`

**テスト項目**:
- [ ] Issue 情報の取得が動作すること
- [ ] Issue コメントの投稿が動作すること
- [ ] PR 作成が動作すること
- [ ] 複数リポジトリで動作すること
- [ ] トークン更新が自動的に行われること

---

### Phase 3: Agent-Runner での GitHub App 利用

**目的**: Agent-Runner Pod での git 操作に GitHub App の Installation Token を使用

**修正対象ファイル**:
- `internal/clients/kubernetes.go`
- `agent-runner/main.go`
- `agent-runner/pkg/git/git.go`
- `agent-runner/pkg/git/committer.go`

**実装内容**:

1. **Pod への認証情報注入の変更**
   ```go
   // internal/clients/kubernetes.go
   func (c *Client) CreateAgentRunnerPod(...) {
       env := []corev1.EnvVar{
           {
               Name: "GITHUB_APP_ID",
               ValueFrom: &corev1.EnvVarSource{
                   SecretKeyRef: &corev1.SecretKeySelector{
                       LocalObjectReference: corev1.LocalObjectReference{
                           Name: "operator-secrets",
                       },
                       Key: "github-app-id",
                   },
               },
           },
           {
               Name: "GITHUB_PRIVATE_KEY",
               ValueFrom: &corev1.EnvVarSource{
                   SecretKeyRef: &corev1.SecretKeySelector{
                       LocalObjectReference: corev1.LocalObjectReference{
                           Name: "operator-secrets",
                       },
                       Key: "github-private-key",
                   },
               },
           },
           // REPO_OWNER と REPO_NAME も追加（token 取得に必要）
           {
               Name:  "REPO_OWNER",
               Value: owner,
           },
           {
               Name:  "REPO_NAME",
               Value: repo,
           },
       }
   }
   ```

2. **Agent-Runner での Token 取得（App 前提）**
   ```go
   // agent-runner/main.go または新規 pkg/github/token.go
   func GetGitHubToken(owner, repo string) (string, error) {
       appID := os.Getenv("GITHUB_APP_ID")
       privateKey := os.Getenv("GITHUB_PRIVATE_KEY") // PEM 本文（パスではない）

       if appID == "" || privateKey == "" {
           return "", errors.New("no GitHub credentials available")
       }

       // Installation token を取得
       // (Phase 1 と同様のロジック)
   }
   ```

3. **Git 操作での動的 Token 使用**
   ```go
   // agent-runner/pkg/git/git.go
   func (g *Git) Clone(owner, repo, branch string) error {
       token, err := GetGitHubToken(owner, repo)
       if err != nil {
           return err
       }

       url := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repo)
       // ...
   }

   // agent-runner/pkg/git/committer.go
   func (c *Committer) Push() error {
       token, err := GetGitHubToken(c.owner, c.repo)
       if err != nil {
           return err
       }

       // remote URL を動的 token で更新
       // ...
   }
   ```

4. **長時間実行への対応**
   - Agent 実行が 1 時間を超える可能性がある場合の対策
   - 各 git 操作前に token を再取得（キャッシュから or 新規取得）

**テスト項目**:
- [ ] リポジトリのクローンが動作すること
- [ ] ブランチの作成・Push が動作すること
- [ ] PR 作成が動作すること
- [ ] 1 時間以上の実行でもトークンが更新されること
- [ ] 複数リポジトリでの動作

---

### Phase 4: ドキュメント更新

**目的**: GitHub App 認証方式をドキュメントに反映

**修正対象ファイル**:
- `CLAUDE.md`
- `specs/001-github-agent-automation/contracts/ai-agent-execution.md`
- `specs/001-github-agent-automation/contracts/agent-runner-detail.md`
- `.env.example`
- `README.md` (もしあれば)
- `Dockerfile`

**実装内容**:

1. **CLAUDE.md の更新**
   - 「主要環境変数」セクションから `GITHUB_TOKEN` を削除
   - GitHub App 関連変数を追加・強調
   - 認証フローの説明を追加

2. **契約ドキュメントの更新**
   - `ai-agent-execution.md` に GitHub App 認証フローを記載
   - `agent-runner-detail.md` に Token 取得ロジックを記載

3. **.env.example の更新**
   ```bash
   # GitHub App Authentication (Required)
   GITHUB_APP_ID=123456
   # PEM 本文を環境変数へ直接格納（改行を含む）
   GITHUB_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkq...snip...\n-----END PRIVATE KEY-----\n"
   GITHUB_WEBHOOK_SECRET=your-webhook-secret
   ```

4. **Dockerfile のコメント更新**
   - GitHub App 認証が標準であることを明記

**テスト項目**:
- [ ] ドキュメントの記載が正確であること
- [ ] 新規セットアップ手順が明確であること

---

### Phase 5: （適用済み方針）PAT 後方互換は導入しない

本計画では初期段階から PAT フォールバックを実装しない。すべてのコード・ドキュメントから `GITHUB_TOKEN` 依存を排除し、GitHub App 認証のみを前提とする。

---

## 実装順序の推奨

### 推奨順序
1. **Phase 1** → **Phase 2** → **Phase 4** → **Phase 3** → **Phase 5**

### 理由
- Phase 1-2 は Operator サービスのみの変更で影響範囲が小さい
- Phase 3 は Agent-Runner（Pod 実行）に影響するためリスクが高い
- Phase 2 までで GitHub App 認証の動作を十分に検証してから Phase 3 に進む
- Phase 4 は各 Phase の完了後に逐次更新も可能
- Phase 5 は本番環境での十分な検証期間後に実施

## 工数見積もり

| Phase | 内容 | 開発工数 | テスト工数 |
|-------|------|----------|-----------|
| Phase 1 | Installation Token 生成 | 0.5 日 | 0.5 日 |
| Phase 2 | Operator での利用 | 0.5 日 | 0.5 日 |
| Phase 3 | Agent-Runner での利用 | 1.0 日 | 1.0 日 |
| Phase 4 | ドキュメント更新 | 0.5 日 | 0.5 日 |
| Phase 5 | 後方互換性削除 | 0.5 日 | 0.5 日 |
| **合計** | | **3.0 日** | **3.0 日** |

**総工数**: 約 6 人日（開発 3 日 + テスト 3 日）

## リスクと対策

### リスク 1: Token 有効期限（1 時間）
**問題**: Agent 実行が 1 時間を超える場合に token が失効する

**対策**:
- 各 git 操作前に token を再取得
- キャッシュに TTL を設定し、期限前に自動更新
- Token 取得失敗時のリトライロジック

### リスク 2: Installation ID の特定
**問題**: リポジトリから Installation ID を知る必要がある

**対策**:
- 起動時に GitHub API で Installation 一覧を取得
- リポジトリ → Installation ID のマッピングをキャッシュ
- キャッシュミス時は API で再取得

### リスク 3: マルチリポジトリ対応
**問題**: 異なるリポジトリが異なる Installation に属する可能性

**対策**:
- Installation ID 毎に token を管理
- リポジトリ単位で適切な token を選択

### リスク 4: GitHub App の権限不足
**問題**: GitHub App に必要な権限が付与されていない

**対策**:
- 事前に必要な権限リストを確認
  - Contents: Read & Write（git 操作）
  - Issues: Read & Write（コメント投稿）
  - Pull Requests: Read & Write（PR 作成）
  - Metadata: Read（リポジトリ情報）
- GitHub App の設定画面で権限を確認・更新

### リスク 5: 既存 Webhook との互換性
**問題**: Webhook ペイロードの扱いが変わる可能性

**対策**:
- Webhook ハンドラーのロジックは変更しない
- 認証部分のみを置き換え
- 統合テストで Webhook の動作を確認

## 検証項目

### 機能テスト
- [ ] リポジトリのクローン
- [ ] ブランチの作成・Push
- [ ] PR 作成
- [ ] Issue コメント投稿
- [ ] CI チェックの取得
- [ ] PR マージ

### 非機能テスト
- [ ] Token キャッシュの動作
- [ ] Token 自動更新
- [ ] 1 時間以上の長時間実行
- [ ] 複数リポジトリの並行処理
- [ ] レート制限の確認

### セキュリティテスト
- [ ] Private Key の安全な保管
- [ ] Token のログ出力がないこと
- [ ] 不正な Installation へのアクセス防止
- [ ] 権限スコープの確認

## 参考資料

### GitHub 公式ドキュメント
- [Authenticating as a GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app)
- [Generating a JSON Web Token (JWT)](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app)
- [Authenticating as an installation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation)

### ライブラリ
- [bradleyfalzon/ghinstallation](https://github.com/bradleyfalzon/ghinstallation) - GitHub App Installation Token 生成
- [google/go-github](https://github.com/google/go-github) - GitHub API クライアント

### 現在のコードベース
- [internal/clients/github.go](../internal/clients/github.go) - GitHub Client 実装
- [agent-runner/pkg/git/](../agent-runner/pkg/git/) - Git 操作実装
- [k8s/pod-template.yaml](../k8s/pod-template.yaml) - Pod 設定テンプレート

## 移行チェックリスト

### 準備
- [ ] GitHub App の作成（まだの場合）
- [ ] 必要な権限の付与確認
- [ ] Private Key のダウンロードと安全な保管
- [ ] Kubernetes Secret への登録

### 実装
- [ ] Phase 1 完了
- [ ] Phase 2 完了
- [ ] Phase 3 完了
- [ ] Phase 4 完了
- [ ] Phase 5 完了（オプション）

### テスト
- [ ] ユニットテスト実装
- [ ] 統合テスト実施
- [ ] 本番環境での検証

### デプロイ
- [ ] ステージング環境へのデプロイ
- [ ] 本番環境へのデプロイ
- [ ] モニタリング設定
- [ ] ロールバック手順の確認

### ドキュメント
- [ ] 技術ドキュメント更新
- [ ] 運用手順書更新
- [ ] トラブルシューティングガイド作成

## 連絡先・質問

実装中に不明点があれば以下を確認：
1. 本ドキュメント
2. GitHub 公式ドキュメント
3. 既存コードのコメント
4. Issue または PR でディスカッション
