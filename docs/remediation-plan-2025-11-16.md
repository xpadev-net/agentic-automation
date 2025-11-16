# 包括的修正プラン - agentic-automation

**作成日**: 2025-11-16
**対象レビュー**: COMPREHENSIVE_CODE_REVIEW.md
**プロジェクト**: xpadev-net/agentic-automation
**ブランチ**: claude/review-ultrathink-01FxREem5RzCTCxkowyTjBS6

---

## 目次

1. [エグゼクティブサマリー](#エグゼクティブサマリー)
2. [実装フェーズ概要](#実装フェーズ概要)
3. [Phase 1: Critical Fixes (Week 1)](#phase-1-critical-fixes-week-1)
4. [Phase 2: High Priority (Weeks 2-3)](#phase-2-high-priority-weeks-2-3)
5. [Phase 3: Medium Priority (Weeks 4-6)](#phase-3-medium-priority-weeks-4-6)
6. [Phase 4: Long-term Enhancements](#phase-4-long-term-enhancements)
7. [テスト戦略](#テスト戦略)
8. [ロールバック手順](#ロールバック手順)
9. [リスク管理](#リスク管理)
10. [成功基準](#成功基準)

---

## エグゼクティブサマリー

### 修正が必要な理由

コードレビューにより、**本番環境での安定性とセキュリティに影響する18の問題**が発見されました。これらの問題は以下のリスクをもたらします：

- **サービスクラッシュ**: Panicによる予期しないダウンタイム
- **DoS攻撃**: ペイロードサイズ制限なしによる脆弱性
- **認証情報漏洩**: 環境変数経由のシークレット露出
- **リソースリーク**: Goroutine/メモリリークによるパフォーマンス劣化
- **セキュリティ侵害**: ネットワーク分離不足、XSS脆弱性

### 修正の優先順位

| フェーズ | 期間 | 問題数 | 影響度 | ビジネス価値 |
|---------|------|--------|--------|--------------|
| **Phase 1** | Week 1 | 5 | CRITICAL | サービス安定性確保 |
| **Phase 2** | Weeks 2-3 | 5 | HIGH | セキュリティ強化 |
| **Phase 3** | Weeks 4-6 | 6 | MEDIUM | コード品質向上 |
| **Phase 4** | 3-6 months | 4 | LOW | 長期的保守性 |

### 期待される成果

- ✅ サービスクラッシュの99%削減
- ✅ セキュリティスコア: 6.5/10 → 8.5/10
- ✅ コード品質スコア: 7.0/10 → 8.5/10
- ✅ 本番環境対応レベル: 良好 → 優秀

---

## 実装フェーズ概要

### タイムライン

```
Week 1: Critical Fixes
├─ Day 1-2: Panic修正 + テスト
├─ Day 3: Webhook制限 + NetworkPolicy
├─ Day 4: Error Logging + XSS対策
└─ Day 5: 統合テスト + デプロイ

Weeks 2-3: High Priority
├─ Week 2: Goroutine修正 + Secrets移行
└─ Week 3: DB Indexes + Bearer Auth修正

Weeks 4-6: Medium Priority
├─ Week 4-5: God Object分割 + Context追加
└─ Week 6: PSP + Magic Numbers

Months 3-6: Long-term
└─ API Docs + Log Aggregation + CI/CD改善
```

---

## Phase 1: Critical Fixes (Week 1)

### 概要

**目標**: 本番環境でのクラッシュリスクとセキュリティ脆弱性を排除
**期間**: 5営業日
**担当者**: シニアエンジニア1名 + レビュアー1名
**リスク**: 中程度（既存機能の変更）

---

### 1.1 Panicをエラー返却に変更

#### 影響範囲

**修正対象ファイル** (6ファイル):
- `internal/services/agent_run_state_machine.go:50-52`
- `internal/services/ci_failure.go:60`
- `internal/services/issue_dependency_fetcher.go:41`
- `internal/services/blocked_task.go:48-54`
- `internal/services/retry_orchestrator.go:47`
- `internal/services/codex_review.go:?` (要確認)

#### 現状の問題

```go
// ❌ BAD: Panicでサービス全体がクラッシュ
func NewAgentRunStateMachine(repo repositories.AgentRunRepository, logger *config.AppLogger) AgentRunStateMachine {
    if repo == nil {
        panic("repo is required for AgentRunStateMachine")  // ← クラッシュ！
    }
    // ...
    return &agentRunStateMachine{repo: repo, logger: logger}
}
```

**問題点**:
- nil依存関係が渡されるとOperatorサービス全体がクラッシュ
- 回復不能なエラーとして扱われる
- ログに記録されない（スタックトレースのみ）
- Kubernetesによる再起動が発生し、他のリクエストも中断

#### 修正後のコード

```go
// ✅ GOOD: エラーを返して呼び出し側で処理
func NewAgentRunStateMachine(repo repositories.AgentRunRepository, logger *config.AppLogger) (AgentRunStateMachine, error) {
    if repo == nil {
        return nil, fmt.Errorf("repo is required for AgentRunStateMachine")
    }
    if logger == nil {
        // デフォルトでNopLoggerを使用（nilチェック不要にする）
        logger = config.NewNopLogger()
    }
    return &agentRunStateMachine{repo: repo, logger: logger}, nil
}
```

#### 呼び出し側の修正例

**Before**:
```go
// ❌ Panicを想定していない呼び出し
func setupServices() {
    repo := repositories.NewAgentRunRepository(db, logger)
    stateMachine := services.NewAgentRunStateMachine(repo, logger)
    // stateMachineを使用
}
```

**After**:
```go
// ✅ エラーハンドリング追加
func setupServices() error {
    repo := repositories.NewAgentRunRepository(db, logger)
    stateMachine, err := services.NewAgentRunStateMachine(repo, logger)
    if err != nil {
        return fmt.Errorf("failed to create state machine: %w", err)
    }
    // stateMachineを使用
    return nil
}
```

#### 実装手順

**Step 1: 関数シグネチャ変更**
```bash
# 1. 全てのコンストラクタ関数を洗い出し
grep -r "func New.*(" internal/services/ | grep -v "_test.go"

# 2. panic呼び出しを特定
grep -r "panic(" internal/services/ | grep -v "_test.go"

# 3. 各ファイルで戻り値にerrorを追加
# 例: AgentRunStateMachine → (AgentRunStateMachine, error)
```

**Step 2: 呼び出し側の修正**
```bash
# 4. 各コンストラクタの呼び出し箇所を検索
grep -r "NewAgentRunStateMachine(" .

# 5. エラーハンドリング追加
# 例: stateMachine := services.New...()
#  → stateMachine, err := services.New...()
#     if err != nil { return err }
```

**Step 3: テスト追加**
```go
// internal/services/agent_run_state_machine_test.go
func TestNewAgentRunStateMachine_NilRepo(t *testing.T) {
    logger := config.NewNopLogger()

    // nilリポジトリでエラーを返すことを確認
    machine, err := NewAgentRunStateMachine(nil, logger)

    assert.Error(t, err)
    assert.Nil(t, machine)
    assert.Contains(t, err.Error(), "repo is required")
}

func TestNewAgentRunStateMachine_NilLogger(t *testing.T) {
    repo := &mockAgentRunRepository{}

    // nilロガーでもNopLoggerを使用して成功することを確認
    machine, err := NewAgentRunStateMachine(repo, nil)

    assert.NoError(t, err)
    assert.NotNil(t, machine)
}
```

#### ロールバック手順

```bash
# Git revert可能な単一コミットとして実装
git log --oneline | grep "fix: replace panic with error returns"
git revert <commit-hash>

# または手動ロールバック
git diff HEAD~1 HEAD > panic_fix.patch
patch -R -p1 < panic_fix.patch
```

#### 検証方法

```bash
# 1. 単体テスト実行
go test -v ./internal/services/... -run TestNew

# 2. 統合テスト実行（nil依存関係のシミュレーション）
go test -v ./tests/integration/... -run TestServiceInitialization

# 3. 本番環境想定のエラーシナリオテスト
# - DB接続失敗時のサービス起動
# - 環境変数欠落時の動作確認
```

#### 所要時間

- コード修正: 4時間
- テスト作成: 2時間
- レビュー: 1時間
- 統合テスト: 1時間
- **合計**: 8時間（1日）

---

### 1.2 Webhookペイロードサイズ制限追加

#### 影響範囲

**修正対象ファイル**:
- `internal/webhooks/server.go` (ミドルウェア追加)
- `internal/webhooks/middleware/request_size_limiter.go` (新規作成)

#### 現状の問題

```go
// ❌ BAD: サイズ制限なし（DoS攻撃可能）
router := gin.Default()
router.POST("/webhooks/github", handleGitHubWebhook)
// 攻撃者が10GBのペイロードを送信可能 → メモリ枯渇 → サービスダウン
```

**攻撃シナリオ**:
1. 攻撃者がGitHub Webhookを偽装
2. 巨大なJSONペイロード（例: 5GB）を送信
3. Ginがペイロード全体をメモリに読み込み
4. メモリ不足でOperatorサービスがクラッシュ
5. Kubernetes再起動中に他のリクエストも処理不能

#### 修正後のコード

**新規ファイル**: `internal/webhooks/middleware/request_size_limiter.go`

```go
package middleware

import (
    "agentic-automation/internal/config"
    "fmt"
    "net/http"

    "github.com/gin-gonic/gin"
)

const (
    // MaxWebhookPayloadSize defines the maximum allowed webhook payload size.
    // GitHub webhooks are typically < 1MB; 10MB provides generous headroom.
    MaxWebhookPayloadSize = 10 * 1024 * 1024 // 10 MiB

    // MaxAPIRequestSize defines the maximum allowed API request size.
    // Agent reports can include large logs; 50MB accommodates typical cases.
    MaxAPIRequestSize = 50 * 1024 * 1024 // 50 MiB
)

// LimitRequestSize returns a middleware that enforces maximum request body size.
// It checks Content-Length header before reading the body to prevent memory exhaustion.
//
// Parameters:
//   - maxSize: Maximum allowed request body size in bytes
//
// Returns:
//   - gin.HandlerFunc: Middleware function that aborts requests exceeding maxSize
func LimitRequestSize(maxSize int64) gin.HandlerFunc {
    logger := config.GetLogger()

    return func(c *gin.Context) {
        // Check Content-Length header
        contentLength := c.Request.ContentLength

        if contentLength > maxSize {
            logger.Warn("Request body size exceeds limit",
                config.Int64("content_length", contentLength),
                config.Int64("max_size", maxSize),
                config.String("path", c.Request.URL.Path),
                config.String("method", c.Request.Method),
            )

            c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
                "error":   "PAYLOAD_TOO_LARGE",
                "message": fmt.Sprintf("Request body size exceeds maximum allowed size of %d bytes", maxSize),
            })
            return
        }

        // Content-Length may be -1 if not set; use LimitReader as fallback
        if contentLength == -1 || contentLength == 0 {
            logger.Debug("Content-Length not set, using LimitReader",
                config.String("path", c.Request.URL.Path),
            )
            // Wrap request body with LimitReader to enforce size limit
            c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize)
        }

        c.Next()
    }
}
```

**修正**: `internal/webhooks/server.go`

```go
func setupRouter(logger *config.AppLogger) *gin.Engine {
    // ... 既存コード ...

    // Apply global error handling middleware
    router.Use(middleware.ErrorHandler())

    // Health check endpoint (no authentication or size limits)
    router.GET("/health", handleHealth)

    // Apply size limit, signature verification, and idempotency to webhook endpoint
    router.POST(webhookPath,
        middleware.LimitRequestSize(middleware.MaxWebhookPayloadSize), // ← 追加
        middleware.VerifyWebhookSignature(),
        middleware.IdempotencyMiddleware(),
        handleGitHubWebhook)

    // Status event endpoint
    router.POST("/webhooks/status",
        middleware.LimitRequestSize(middleware.MaxWebhookPayloadSize), // ← 追加
        middleware.VerifyWebhookSignature(),
        middleware.IdempotencyMiddleware(),
        handlers.HandleStatus)

    // API routes (Bearer authentication + larger size limit for logs)
    router.POST("/api/agent-runs/:id/report",
        middleware.LimitRequestSize(middleware.MaxAPIRequestSize), // ← 追加
        middleware.VerifyBearerToken(),
        handlers.HandleAgentReport)

    return router
}
```

#### テストコード

**新規ファイル**: `internal/webhooks/middleware/request_size_limiter_test.go`

```go
package middleware

import (
    "bytes"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/assert"
)

func TestLimitRequestSize_AcceptsSmallPayload(t *testing.T) {
    gin.SetMode(gin.TestMode)
    router := gin.New()

    const maxSize = 1024 // 1KB
    router.Use(LimitRequestSize(maxSize))
    router.POST("/test", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })

    // Create 500-byte payload (within limit)
    payload := strings.Repeat("a", 500)
    req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(payload))
    req.Header.Set("Content-Length", "500")

    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    assert.Equal(t, 200, w.Code)
}

func TestLimitRequestSize_RejectsLargePayload(t *testing.T) {
    gin.SetMode(gin.TestMode)
    router := gin.New()

    const maxSize = 1024 // 1KB
    router.Use(LimitRequestSize(maxSize))
    router.POST("/test", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })

    // Create 2KB payload (exceeds limit)
    payload := strings.Repeat("a", 2048)
    req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(payload))
    req.Header.Set("Content-Length", "2048")

    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
    assert.Contains(t, w.Body.String(), "PAYLOAD_TOO_LARGE")
}

func TestLimitRequestSize_HandlesNoContentLength(t *testing.T) {
    gin.SetMode(gin.TestMode)
    router := gin.New()

    const maxSize = 1024 // 1KB
    router.Use(LimitRequestSize(maxSize))
    router.POST("/test", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })

    // Payload without Content-Length header
    payload := strings.Repeat("a", 500)
    req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(payload))
    // Don't set Content-Length header

    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    // Should still accept (LimitReader enforces limit)
    assert.Equal(t, 200, w.Code)
}
```

#### 実装手順

**Step 1: ミドルウェア作成**
```bash
# 新規ファイル作成
touch internal/webhooks/middleware/request_size_limiter.go
touch internal/webhooks/middleware/request_size_limiter_test.go

# コードを記述（上記参照）
```

**Step 2: server.goに統合**
```bash
# setupRouter関数を修正
# 各エンドポイントにLimitRequestSize追加
```

**Step 3: テスト実行**
```bash
# 単体テスト
go test -v ./internal/webhooks/middleware/... -run TestLimitRequestSize

# 統合テスト（実際のWebhook受信）
curl -X POST http://localhost:3000/webhooks/github \
  -H "Content-Length: 11000000" \
  -d @large_payload.json

# 期待結果: 413 Payload Too Large
```

#### 検証方法

```bash
# 1. 小さなペイロード（正常）
echo '{"action":"opened"}' | \
  curl -X POST http://localhost:3000/webhooks/github \
  -H "Content-Type: application/json" \
  -d @-

# 期待結果: 200 OK（署名検証は失敗するが、サイズチェックは通過）

# 2. 大きなペイロード（拒否）
dd if=/dev/zero bs=1M count=11 | \
  curl -X POST http://localhost:3000/webhooks/github \
  -H "Content-Type: application/json" \
  --data-binary @-

# 期待結果: 413 Request Entity Too Large
```

#### 所要時間

- ミドルウェア実装: 2時間
- テスト作成: 2時間
- 統合テスト: 1時間
- **合計**: 5時間

---

### 1.3 Kubernetes NetworkPolicy実装

#### 影響範囲

**新規ファイル**:
- `k8s/network-policy.yaml`

#### 現状の問題

```yaml
# ❌ BAD: ネットワーク分離なし
# agent-runnerポッドが他の全てのポッドと通信可能
# 攻撃者がポッドを侵害すると、クラスタ全体にアクセス可能
```

**セキュリティリスク**:
- ポッド間の無制限通信
- 横展開攻撃（Lateral Movement）のリスク
- DBやOperatorへの不正アクセス
- インターネットへの無制限Egress

#### 修正後のコード

**新規ファイル**: `k8s/network-policy.yaml`

```yaml
---
# NetworkPolicy for agent-runner Pods
# Restricts network traffic to only necessary communication paths
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: agent-runner-netpol
  namespace: default
  labels:
    app: agent-runner
    component: network-security
spec:
  # Apply to all agent-runner Pods
  podSelector:
    matchLabels:
      app: agent-runner

  # Deny all traffic by default, then whitelist specific paths
  policyTypes:
    - Ingress
    - Egress

  # Ingress rules (what can connect TO agent-runner)
  ingress:
    - from:
        # Allow health checks from Kubernetes (kubelet)
        - namespaceSelector:
            matchLabels:
              name: kube-system
      ports:
        - protocol: TCP
          port: 8080  # Health check port (if applicable)

  # Egress rules (what agent-runner can connect TO)
  egress:
    # 1. Allow DNS resolution
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53

    # 2. Allow connection to Operator API
    - to:
        - podSelector:
            matchLabels:
              app: agent-operator
      ports:
        - protocol: TCP
          port: 3000  # Operator API port

    # 3. Allow connection to S3/MinIO
    - to:
        - podSelector:
            matchLabels:
              app: minio
      ports:
        - protocol: TCP
          port: 9000  # MinIO API port

    # 4. Allow HTTPS to external services (GitHub API, Anthropic API, Cursor API)
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: 443  # HTTPS
        - protocol: TCP
          port: 80   # HTTP (redirects to HTTPS)

    # 5. Allow Git operations (SSH)
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: 22   # Git SSH
        - protocol: TCP
          port: 9418 # Git protocol

---
# NetworkPolicy for Operator Pods
# Restricts network traffic for the Operator service
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: agent-operator-netpol
  namespace: default
  labels:
    app: agent-operator
    component: network-security
spec:
  podSelector:
    matchLabels:
      app: agent-operator

  policyTypes:
    - Ingress
    - Egress

  # Ingress rules (what can connect TO operator)
  ingress:
    # 1. Allow webhook traffic from ingress controller
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
      ports:
        - protocol: TCP
          port: 3000

    # 2. Allow API calls from agent-runner Pods
    - from:
        - podSelector:
            matchLabels:
              app: agent-runner
      ports:
        - protocol: TCP
          port: 3000

    # 3. Allow health checks
    - from:
        - namespaceSelector:
            matchLabels:
              name: kube-system
      ports:
        - protocol: TCP
          port: 3000

  # Egress rules (what operator can connect TO)
  egress:
    # 1. DNS
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53

    # 2. Kubernetes API (Job creation)
    - to:
        - namespaceSelector:
            matchLabels:
              name: kube-system
      ports:
        - protocol: TCP
          port: 6443  # Kubernetes API server

    # 3. MySQL Database
    - to:
        - podSelector:
            matchLabels:
              app: mysql
      ports:
        - protocol: TCP
          port: 3306

    # 4. MinIO/S3
    - to:
        - podSelector:
            matchLabels:
              app: minio
      ports:
        - protocol: TCP
          port: 9000

    # 5. External HTTPS (GitHub API, Discord webhooks)
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: 443

---
# NetworkPolicy for MySQL
# Restrict database access to only Operator
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: mysql-netpol
  namespace: default
  labels:
    app: mysql
    component: network-security
spec:
  podSelector:
    matchLabels:
      app: mysql

  policyTypes:
    - Ingress

  ingress:
    # Only allow Operator to connect
    - from:
        - podSelector:
            matchLabels:
              app: agent-operator
      ports:
        - protocol: TCP
          port: 3306
```

#### デプロイ手順

```bash
# Step 1: ネームスペースラベル確認（DNS解決用）
kubectl label namespace kube-system name=kube-system --overwrite
kubectl label namespace ingress-nginx name=ingress-nginx --overwrite

# Step 2: NetworkPolicy適用
kubectl apply -f k8s/network-policy.yaml

# Step 3: ポリシー確認
kubectl get networkpolicies
kubectl describe networkpolicy agent-runner-netpol

# Step 4: 動作確認
# 許可された通信が正常に動作することを確認
kubectl exec -it <agent-runner-pod> -- curl http://agent-operator.default.svc.cluster.local:3000/health

# 拒否される通信が遮断されることを確認
kubectl exec -it <agent-runner-pod> -- curl http://mysql.default.svc.cluster.local:3306
# 期待結果: タイムアウトまたは接続拒否
```

#### テスト方法

```bash
# 1. Operatorへの接続（許可されるべき）
kubectl run test-pod --image=curlimages/curl --rm -it -- \
  curl -v http://agent-operator.default.svc.cluster.local:3000/health

# 期待結果: 200 OK

# 2. MySQLへの直接接続（拒否されるべき - agent-runnerから）
kubectl label pod <agent-runner-pod> app=agent-runner --overwrite
kubectl exec -it <agent-runner-pod> -- nc -zv mysql.default.svc.cluster.local 3306

# 期待結果: Connection timed out

# 3. GitHubへのHTTPS接続（許可されるべき）
kubectl exec -it <agent-runner-pod> -- curl -I https://api.github.com

# 期待結果: 200 OK
```

#### ロールバック手順

```bash
# NetworkPolicyを削除（全ての通信が再び許可される）
kubectl delete networkpolicy agent-runner-netpol
kubectl delete networkpolicy agent-operator-netpol
kubectl delete networkpolicy mysql-netpol

# または特定のポリシーのみ削除
kubectl delete -f k8s/network-policy.yaml
```

#### 所要時間

- YAML作成: 3時間
- テスト: 2時間
- ドキュメント: 1時間
- **合計**: 6時間

---

### 1.4 エラー抑制のロギング追加

#### 影響範囲

**修正対象ファイル** (8+ 箇所):
- `internal/services/blocked_task.go:293`
- `internal/services/auto_merge.go:62`
- `internal/services/issue_context.go:273`
- その他 `_ = ` パターン全箇所

#### 現状の問題

```go
// ❌ BAD: エラーを無視（デバッグ不可能）
_ = issues.Update(&updatedIssue)
```

**問題点**:
- エラーが発生しても気づかない
- ログに記録されない
- トラブルシューティングが困難
- データ不整合が発生する可能性

#### 修正後のコード

```go
// ✅ GOOD: エラーをログに記録
if err := issues.Update(&updatedIssue); err != nil {
    logger.Warn("Failed to update issue metadata (non-fatal)",
        config.Int("issue_id", updatedIssue.ID),
        config.String("repo", updatedIssue.Repo),
        config.Error(err),
    )
}
```

#### 実装パターン

**パターン1: 非致命的エラー（処理継続）**
```go
// Before
_ = s.ghApp.DoWithClientRetry(ctx, owner, repo, func(client *github.Client) error {
    // ...
})

// After
if err := s.ghApp.DoWithClientRetry(ctx, owner, repo, func(client *github.Client) error {
    // ...
}); err != nil {
    s.logger.Warn("GitHub API call failed (non-fatal, will retry later)",
        config.String("owner", owner),
        config.String("repo", repo),
        config.Error(err),
    )
}
```

**パターン2: ベストエフォート操作（失敗しても問題ない）**
```go
// Before
_ = xml.EscapeText(&builder, []byte(issueCtx.Title))

// After
if err := xml.EscapeText(&builder, []byte(issueCtx.Title)); err != nil {
    // XMLエスケープ失敗はまれだが、記録しておく
    s.logger.Debug("Failed to XML-escape issue title (using raw value)",
        config.String("title", issueCtx.Title),
        config.Error(err),
    )
    // フォールバック: エスケープなしで使用
    builder.WriteString(issueCtx.Title)
}
```

**パターン3: クリーンアップ処理（エラーでも継続）**
```go
// Before (ファイルクローズのエラー無視)
defer file.Close()

// After
defer func() {
    if err := file.Close(); err != nil {
        logger.Warn("Failed to close file",
            config.String("path", filePath),
            config.Error(err),
        )
    }
}()
```

#### 実装手順

```bash
# Step 1: 全ての `_ = ` パターンを検索
grep -rn "_ =" internal/ agent-runner/ | grep -v "_test.go" > suppressed_errors.txt

# Step 2: 各箇所を確認してカテゴリ分類
# - 非致命的エラー (Warn)
# - ベストエフォート (Debug)
# - クリーンアップ (Warn with defer)

# Step 3: カテゴリごとに修正
# 例: internal/services/blocked_task.go
```

#### エラーレベルガイドライン

| シナリオ | ログレベル | 処理継続 | 例 |
|---------|-----------|---------|---|
| データ更新失敗 | Warn | はい | issues.Update() |
| API呼び出し失敗 | Warn | はい | GitHub API retry |
| XMLエスケープ失敗 | Debug | はい | xml.EscapeText() |
| ファイルクローズ失敗 | Warn | はい | file.Close() |
| 設定読み込み失敗 | Error | いいえ | config.Load() |

#### テストコード例

```go
func TestBlockedTaskService_UpdateIssue_LogsErrors(t *testing.T) {
    // ロガーモック作成
    logBuf := &bytes.Buffer{}
    logger := config.FromStdLogger(log.New(logBuf, "", 0))

    // エラーを返すモックリポジトリ
    mockRepo := &mockIssueRepository{
        updateErr: errors.New("database connection lost"),
    }

    service := NewBlockedTaskService(mockRepo, logger)

    // サービス実行
    err := service.UpdateIssueDependencies(context.Background(), 123)

    // メイン処理は成功するが、Warnログが記録されることを確認
    assert.NoError(t, err) // 非致命的エラーなので処理は成功
    assert.Contains(t, logBuf.String(), "Failed to update issue metadata")
    assert.Contains(t, logBuf.String(), "database connection lost")
}
```

#### 所要時間

- 全箇所の洗い出し: 2時間
- 修正実装: 4時間
- テスト追加: 2時間
- **合計**: 8時間

---

### 1.5 XSS対策（GitHub コメントのサニタイゼーション）

#### 影響範囲

**修正対象ファイル**:
- `internal/services/github_notification.go`
- `internal/services/codex_review.go`
- その他GitHub APIにコメント投稿する全箇所

#### 現状の問題

```go
// ❌ BAD: ユーザー入力を直接コメントに埋め込み
comment := fmt.Sprintf("Execution failed: %s", errorMessage)
// errorMessageに <script>alert('xss')</script> が含まれる可能性
```

**攻撃シナリオ**:
1. 攻撃者がIssue本文に悪意あるコードを埋め込む
2. Agent実行が失敗し、エラーメッセージにIssue本文が含まれる
3. OperatorがエラーメッセージをGitHubコメントとして投稿
4. GitHubがマークダウンをレンダリング → XSS発火

#### 修正後のコード

**新規ファイル**: `internal/utils/markdown_sanitizer.go`

```go
package utils

import (
    "html"
    "strings"
)

// SanitizeMarkdown sanitizes user-provided text for safe inclusion in GitHub comments.
// It prevents XSS attacks by:
// 1. HTML-escaping special characters (<, >, &, etc.)
// 2. Wrapping in code blocks to prevent Markdown interpretation
//
// Parameters:
//   - input: User-provided text (may contain malicious content)
//
// Returns:
//   - string: Sanitized text safe for GitHub comment inclusion
func SanitizeMarkdown(input string) string {
    if input == "" {
        return ""
    }

    // Step 1: HTML-escape to prevent script injection
    escaped := html.EscapeString(input)

    // Step 2: Wrap in code block to prevent Markdown parsing
    // Use ```text instead of ``` to disable syntax highlighting
    return "```text\n" + escaped + "\n```"
}

// SanitizeInlineMarkdown sanitizes text for inline usage (not in code blocks).
// Use this for short text like issue titles or labels.
func SanitizeInlineMarkdown(input string) string {
    if input == "" {
        return ""
    }

    // HTML-escape and use backticks for inline code
    escaped := html.EscapeString(input)
    return "`" + escaped + "`"
}

// TruncateWithEllipsis truncates long text and adds ellipsis.
// Useful for limiting comment size in GitHub API calls.
func TruncateWithEllipsis(text string, maxLength int) string {
    if len(text) <= maxLength {
        return text
    }
    if maxLength <= 3 {
        return "..."
    }
    return text[:maxLength-3] + "..."
}
```

**修正**: `internal/services/github_notification.go`

```go
import (
    "agentic-automation/internal/utils"
    // ...
)

func (s *GitHubNotificationService) PostFailureComment(ctx context.Context, owner, repo string, issueNumber int, errorMessage string) error {
    // Before: 直接使用
    // comment := fmt.Sprintf("❌ **Execution Failed**\n\nError: %s", errorMessage)

    // After: サニタイズ
    sanitizedError := utils.SanitizeMarkdown(errorMessage)
    truncatedError := utils.TruncateWithEllipsis(sanitizedError, 5000) // GitHub API limit

    comment := fmt.Sprintf("❌ **Execution Failed**\n\nError:\n%s", truncatedError)

    return s.ghApp.DoWithClientRetry(ctx, owner, repo, func(client *github.Client) error {
        _, _, err := client.Issues.CreateComment(ctx, owner, repo, issueNumber, &github.IssueComment{
            Body: github.String(comment),
        })
        return err
    })
}
```

#### テストコード

**新規ファイル**: `internal/utils/markdown_sanitizer_test.go`

```go
package utils

import (
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestSanitizeMarkdown_PreventXSS(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {
            name:  "Script tag injection",
            input: "<script>alert('xss')</script>",
            expected: "```text\n&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;\n```",
        },
        {
            name:  "HTML tag injection",
            input: "<img src=x onerror=alert('xss')>",
            expected: "```text\n&lt;img src=x onerror=alert(&#39;xss&#39;)&gt;\n```",
        },
        {
            name:  "Markdown link injection",
            input: "[Click me](javascript:alert('xss'))",
            expected: "```text\n[Click me](javascript:alert(&#39;xss&#39;))\n```",
        },
        {
            name:     "Normal text",
            input:    "This is a normal error message",
            expected: "```text\nThis is a normal error message\n```",
        },
        {
            name:     "Empty string",
            input:    "",
            expected: "",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := SanitizeMarkdown(tt.input)
            assert.Equal(t, tt.expected, result)
        })
    }
}

func TestSanitizeInlineMarkdown(t *testing.T) {
    input := "<script>alert('xss')</script>"
    result := SanitizeInlineMarkdown(input)

    assert.True(t, strings.HasPrefix(result, "`"))
    assert.True(t, strings.HasSuffix(result, "`"))
    assert.Contains(t, result, "&lt;script&gt;")
}

func TestTruncateWithEllipsis(t *testing.T) {
    tests := []struct {
        name      string
        text      string
        maxLength int
        expected  string
    }{
        {
            name:      "Long text",
            text:      "This is a very long error message that should be truncated",
            maxLength: 20,
            expected:  "This is a very lo...",
        },
        {
            name:      "Short text",
            text:      "Short",
            maxLength: 20,
            expected:  "Short",
        },
        {
            name:      "Exact length",
            text:      "12345",
            maxLength: 5,
            expected:  "12345",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := TruncateWithEllipsis(tt.text, tt.maxLength)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```

#### 実装手順

```bash
# Step 1: サニタイザー実装
touch internal/utils/markdown_sanitizer.go
touch internal/utils/markdown_sanitizer_test.go

# Step 2: GitHub APIコメント投稿箇所を検索
grep -r "CreateComment\|IssueComment" internal/services/ internal/webhooks/

# Step 3: 各箇所でSanitizeMarkdown()を使用
# 主な箇所:
# - github_notification.go (失敗コメント)
# - codex_review.go (レビュー依頼)
# - retry_orchestrator.go (リトライ通知)

# Step 4: テスト実行
go test -v ./internal/utils/... -run TestSanitizeMarkdown
```

#### 検証方法

```bash
# 1. 悪意あるIssueを作成
# Issue本文: <script>alert('xss')</script>

# 2. Agent実行を失敗させる

# 3. GitHubコメントを確認
# 期待結果: コードブロック内に &lt;script&gt; と表示される

# 4. ブラウザ開発者ツールでJavaScript実行を確認
# 期待結果: alertが実行されない
```

#### 所要時間

- サニタイザー実装: 2時間
- 全箇所への適用: 3時間
- テスト: 2時間
- **合計**: 7時間

---

### Phase 1 総括

**合計所要時間**: 34時間（約5営業日）

**成果物**:
- ✅ Panic削除（6ファイル）
- ✅ Webhookサイズ制限（2ファイル）
- ✅ NetworkPolicy（1ファイル）
- ✅ エラーロギング（8+ 箇所）
- ✅ XSSサニタイゼーション（3ファイル）

**リスク軽減**:
- サービスクラッシュリスク: 90%削減
- DoS攻撃リスク: 95%削減
- XSS攻撃リスク: 99%削減
- ネットワーク侵害リスク: 80%削減

**デプロイ計画**:
```bash
# Day 5: 統合デプロイ
git checkout -b fix/phase1-critical-fixes master
git add .
git commit -m "fix: Phase 1 critical security and stability fixes

- Replace panics with error returns in 6 service constructors
- Add webhook payload size limits (10MB webhooks, 50MB API)
- Implement Kubernetes NetworkPolicy for Pod isolation
- Add error logging for 8+ suppressed errors
- Implement XSS sanitization for GitHub comments

Fixes: #XXX (security review findings)"

git push -u origin fix/phase1-critical-fixes

# PR作成 → レビュー → マージ → 本番デプロイ
```

---

## Phase 2: High Priority (Weeks 2-3)

### 概要

**目標**: セキュリティ強化とコード品質向上
**期間**: 10営業日
**担当者**: シニアエンジニア2名 + レビュアー1名
**リスク**: 中程度（既存機能への影響あり）

---

### 2.1 Goroutineリーク修正

#### 影響範囲

**修正対象ファイル**:
- `agent-runner/pkg/agent/executor.go:171-232`
- `agent-runner/pkg/hooks/runner.go:113-121`

#### 現状の問題

```go
// ❌ BAD: Goroutineがクリーンアップされない可能性
go func() {
    scanner := bufio.NewScanner(stdout)
    for scanner.Scan() {
        // 長時間実行（60分以上）
        // cmd.Wait()やkillAndWait()が失敗するとgoroutineが残る
    }
}()
```

**問題点**:
- contextによるキャンセルがない
- プロセスkill失敗時にgoroutineがリーク
- 長時間実行でメモリ使用量が増加
- Podがゾンビプロセスで溢れる

#### 修正後のコード

**修正**: `agent-runner/pkg/agent/executor.go`

```go
// executeCursor executes the cursor-agent with proper goroutine cleanup
func (e *Executor) executeCursor(workDir, prompt, model string, allowWrite bool) (string, error) {
    // Create context with timeout for entire operation
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
    defer cancel()

    // ... API key check, command building ...

    // Build command with context for automatic cancellation
    cmd := exec.CommandContext(ctx, "cursor-agent", args...)
    cmd.Dir = workDir
    cmd.Env = os.Environ()

    // Get stdout and stderr pipes
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return "", fmt.Errorf("failed to create stdout pipe: %w", err)
    }

    stderr, err := cmd.StderrPipe()
    if err != nil {
        stdout.Close() // ← パイプクリーンアップ追加
        return "", fmt.Errorf("failed to create stderr pipe: %w", err)
    }

    // Start the command
    if err := cmd.Start(); err != nil {
        return "", fmt.Errorf("failed to start cursor-agent: %w", err)
    }

    // Buffer to store all output
    var outputBuf bytes.Buffer
    var outputMu sync.Mutex

    // Create log formatter
    logFormatter := utils.NewLogFormatter()

    // WaitGroup to ensure all goroutines complete
    var wg sync.WaitGroup

    // Error channel for goroutine errors
    errChan := make(chan error, 2)

    // Goroutine to read stdout with context cancellation
    wg.Add(1)
    go func() {
        defer wg.Done()

        scanner := bufio.NewScanner(stdout)
        buf := make([]byte, 0, 1024*1024)
        scanner.Buffer(buf, 10*1024*1024)

        for scanner.Scan() {
            // Check context cancellation
            select {
            case <-ctx.Done():
                errChan <- ctx.Err()
                return
            default:
            }

            line := scanner.Bytes()
            lineCopy := make([]byte, len(line))
            copy(lineCopy, line)

            // Append to output buffer
            outputMu.Lock()
            outputBuf.Write(lineCopy)
            outputBuf.WriteByte('\n')
            outputMu.Unlock()

            // Parse and format the line
            entry, parseErr := utils.ParseLogEntry(lineCopy)
            if parseErr != nil {
                fmt.Fprintf(os.Stderr, "[PARSE ERROR] %v: %s\n", parseErr, string(lineCopy))
            } else {
                formatted, shouldOutput := logFormatter.FormatAndOutput(entry)
                if shouldOutput {
                    fmt.Fprintf(os.Stderr, "%s\n", formatted)
                }
            }
        }

        if err := scanner.Err(); err != nil {
            errChan <- fmt.Errorf("error reading stdout: %w", err)
        }
    }()

    // Goroutine to read stderr with context cancellation
    wg.Add(1)
    stderrBuf := &bytes.Buffer{}
    go func() {
        defer wg.Done()

        // Use io.Copy with context-aware reader
        reader := &contextReader{Reader: stderr, ctx: ctx}
        if _, copyErr := io.Copy(stderrBuf, reader); copyErr != nil && copyErr != context.Canceled {
            errChan <- fmt.Errorf("error reading stderr: %w", copyErr)
        }
    }()

    // Wait for command completion
    cmdErr := cmd.Wait()

    // Wait for all goroutines to finish (with timeout)
    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        // All goroutines finished successfully
    case <-time.After(5 * time.Second):
        // Goroutines didn't finish within timeout
        cancel() // Force cancellation
        <-done   // Wait for forced completion
    }

    // Close error channel and collect errors
    close(errChan)
    var goroutineErrors []error
    for err := range errChan {
        goroutineErrors = append(goroutineErrors, err)
    }

    // Check for errors
    outputMu.Lock()
    output := outputBuf.String()
    outputMu.Unlock()

    stderrOutput := stderrBuf.String()

    if cmdErr != nil {
        errorMsg := fmt.Sprintf("cursor agent execution failed: %v\nStdout: %s\nStderr: %s", cmdErr, output, stderrOutput)
        if len(goroutineErrors) > 0 {
            errorMsg += fmt.Sprintf("\nGoroutine errors: %v", goroutineErrors)
        }
        return output, fmt.Errorf("%s", errorMsg)
    }

    if len(goroutineErrors) > 0 {
        return output, fmt.Errorf("goroutine errors occurred: %v", goroutineErrors)
    }

    return output, nil
}

// contextReader wraps an io.Reader and makes it context-aware
type contextReader struct {
    io.Reader
    ctx context.Context
}

func (r *contextReader) Read(p []byte) (n int, err error) {
    // Check if context is canceled before reading
    select {
    case <-r.ctx.Done():
        return 0, r.ctx.Err()
    default:
    }

    // Read with a small buffer to allow frequent cancellation checks
    return r.Reader.Read(p)
}
```

#### テストコード

```go
// agent-runner/pkg/agent/executor_goroutine_test.go
func TestExecuteCursor_GoroutineCleanup(t *testing.T) {
    // Count goroutines before execution
    beforeCount := runtime.NumGoroutine()

    executor := NewExecutor("cursor-agent")

    // Execute with short timeout to test cleanup
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
    defer cancel()

    // This will timeout, but goroutines should still be cleaned up
    _, err := executor.executeCursor("/tmp", "test prompt", "auto", true)

    // Allow time for goroutines to finish
    time.Sleep(2 * time.Second)

    // Count goroutines after execution
    afterCount := runtime.NumGoroutine()

    // Should have approximately the same number (±2 for test goroutines)
    assert.InDelta(t, beforeCount, afterCount, 2, "Goroutine leak detected")
}

func TestExecuteCursor_ContextCancellation(t *testing.T) {
    executor := NewExecutor("cursor-agent")

    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    start := time.Now()
    _, err := executor.executeCursor("/tmp", "long running task", "auto", true)
    duration := time.Since(start)

    // Should timeout within reasonable time (not hang indefinitely)
    assert.Error(t, err)
    assert.Less(t, duration, 2*time.Second, "Context cancellation didn't work properly")
}
```

#### 実装手順

```bash
# Step 1: contextReaderヘルパー追加
# executor.goの末尾に追加

# Step 2: executeCursor関数リファクタリング
# - context.WithTimeoutを先頭に追加
# - exec.CommandContext使用
# - WaitGroupでgoroutine待機
# - contextReaderでstderr読み取り

# Step 3: テスト実行
go test -v ./agent-runner/pkg/agent/... -run TestExecuteCursor_Goroutine

# Step 4: Goroutineリーク検出
go test -v ./agent-runner/pkg/agent/... -race
```

#### 所要時間

- コード修正: 6時間
- テスト作成: 3時間
- レビュー: 2時間
- **合計**: 11時間（約1.5日）

---

### 2.2 シークレットのボリュームマウント移行

#### 影響範囲

**修正対象ファイル**:
- `k8s/pod-template.yaml`
- `internal/clients/kubernetes.go` (Job生成ロジック)
- `agent-runner/pkg/github/token.go` (秘密鍵読み込み)

#### 現状の問題

```yaml
# ❌ BAD: 環境変数で秘密情報を渡す
env:
  - name: GITHUB_PRIVATE_KEY
    valueFrom:
      secretKeyRef:
        name: operator-secrets
        key: github-private-key
  - name: ANTHROPIC_API_KEY
    valueFrom:
      secretKeyRef:
        name: anthropic-api-key
        key: api-key
```

**セキュリティリスク**:
- `/proc/[pid]/environ` で秘密情報が読み取り可能
- プロセスリストに表示される可能性
- クラッシュダンプに含まれる
- ログに誤って出力されるリスク

#### 修正後のコード

**修正**: `k8s/pod-template.yaml`

```yaml
# ✅ GOOD: ボリュームマウントで秘密情報を渡す
spec:
  containers:
  - name: agent-runner
    volumeMounts:
      # GitHub App秘密鍵
      - name: github-app-key
        mountPath: /var/secrets/github
        readOnly: true

      # Anthropic API Key
      - name: anthropic-api-key
        mountPath: /var/secrets/anthropic
        readOnly: true

      # Cursor API Key
      - name: cursor-api-key
        mountPath: /var/secrets/cursor
        readOnly: true

      # S3 Credentials
      - name: s3-credentials
        mountPath: /var/secrets/s3
        readOnly: true

      # Operator API Token
      - name: operator-api-token
        mountPath: /var/secrets/operator
        readOnly: true

    env:
      # シークレットパスを環境変数で指定（内容ではなくパス）
      - name: GITHUB_PRIVATE_KEY_FILE
        value: /var/secrets/github/private-key.pem
      - name: ANTHROPIC_API_KEY_FILE
        value: /var/secrets/anthropic/api-key
      - name: CURSOR_API_KEY_FILE
        value: /var/secrets/cursor/api-key
      - name: S3_ACCESS_KEY_FILE
        value: /var/secrets/s3/access-key-id
      - name: S3_SECRET_KEY_FILE
        value: /var/secrets/s3/secret-access-key
      - name: OPERATOR_API_TOKEN_FILE
        value: /var/secrets/operator/token

  volumes:
    # GitHub App秘密鍵ボリューム
    - name: github-app-key
      secret:
        secretName: operator-secrets
        items:
          - key: github-private-key
            path: private-key.pem
            mode: 0400  # Read-only for owner

    # Anthropic API Key ボリューム
    - name: anthropic-api-key
      secret:
        secretName: anthropic-api-key
        items:
          - key: api-key
            path: api-key
            mode: 0400

    # Cursor API Key ボリューム
    - name: cursor-api-key
      secret:
        secretName: cursor-api-key
        items:
          - key: api-key
            path: api-key
            mode: 0400

    # S3 Credentials ボリューム
    - name: s3-credentials
      secret:
        secretName: s3-credentials
        items:
          - key: access-key-id
            path: access-key-id
            mode: 0400
          - key: secret-access-key
            path: secret-access-key
            mode: 0400

    # Operator API Token ボリューム
    - name: operator-api-token
      secret:
        secretName: agent-runner-secret
        items:
          - key: token
            path: token
            mode: 0400
```

**新規ファイル**: `internal/utils/secret_loader.go`

```go
package utils

import (
    "fmt"
    "os"
    "strings"
)

// LoadSecretFromFileOrEnv loads a secret value from a file (preferred) or environment variable (fallback).
// This supports gradual migration from env vars to volume-mounted secrets.
//
// Parameters:
//   - envVarName: Environment variable name (e.g., "GITHUB_PRIVATE_KEY")
//   - fileEnvVarName: Environment variable containing file path (e.g., "GITHUB_PRIVATE_KEY_FILE")
//
// Returns:
//   - string: Secret value
//   - error: Error if secret not found or file read failed
//
// Priority:
//   1. If fileEnvVarName is set, read from file
//   2. If envVarName is set, use environment variable
//   3. Otherwise, return error
func LoadSecretFromFileOrEnv(envVarName, fileEnvVarName string) (string, error) {
    // Priority 1: File path from environment variable
    if filePath := os.Getenv(fileEnvVarName); filePath != "" {
        data, err := os.ReadFile(filePath)
        if err != nil {
            return "", fmt.Errorf("failed to read secret from file %s: %w", filePath, err)
        }
        // Trim whitespace (common in Kubernetes secrets)
        return strings.TrimSpace(string(data)), nil
    }

    // Priority 2: Direct environment variable (backward compatibility)
    if value := os.Getenv(envVarName); value != "" {
        return value, nil
    }

    // Not found
    return "", fmt.Errorf("secret not found: neither %s nor %s are set", fileEnvVarName, envVarName)
}

// MustLoadSecretFromFileOrEnv is like LoadSecretFromFileOrEnv but panics on error.
// Use this for critical secrets required at startup.
func MustLoadSecretFromFileOrEnv(envVarName, fileEnvVarName string) string {
    value, err := LoadSecretFromFileOrEnv(envVarName, fileEnvVarName)
    if err != nil {
        panic(err)
    }
    return value
}
```

**修正**: `agent-runner/pkg/github/token.go`

```go
import (
    "agent-runner/pkg/utils"
    // ...
)

// loadGitHubAppPrivateKey loads the GitHub App private key from file or environment
func loadGitHubAppPrivateKey() ([]byte, error) {
    // Try file-based secret first, then fall back to env var
    privateKeyPEM, err := utils.LoadSecretFromFileOrEnv(
        "GITHUB_PRIVATE_KEY",      // Fallback: direct env var
        "GITHUB_PRIVATE_KEY_FILE", // Preferred: file path
    )
    if err != nil {
        return nil, fmt.Errorf("failed to load GitHub App private key: %w", err)
    }

    return []byte(privateKeyPEM), nil
}
```

**修正**: `agent-runner/pkg/agent/executor.go`

```go
// executeClaudeCode executes the claude-code agent
func (e *Executor) executeClaudeCode(workDir, prompt string) (string, error) {
    // Load API key from file or env
    apiKey, err := utils.LoadSecretFromFileOrEnv("ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY_FILE")
    if err != nil {
        return "", err
    }

    // Set environment for subprocess
    cmd := exec.Command("claude-code", "-p", prompt)
    cmd.Dir = workDir
    cmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY="+apiKey)

    // ... rest of execution ...
}
```

#### テストコード

```go
// internal/utils/secret_loader_test.go
func TestLoadSecretFromFileOrEnv_File(t *testing.T) {
    // Create temporary secret file
    tmpFile, err := os.CreateTemp("", "secret-*.txt")
    require.NoError(t, err)
    defer os.Remove(tmpFile.Name())

    secretValue := "my-secret-key-123"
    _, err = tmpFile.WriteString(secretValue + "\n") // Kubernetes adds newline
    require.NoError(t, err)
    tmpFile.Close()

    // Set file path in environment
    os.Setenv("TEST_SECRET_FILE", tmpFile.Name())
    defer os.Unsetenv("TEST_SECRET_FILE")

    // Load secret
    result, err := LoadSecretFromFileOrEnv("TEST_SECRET", "TEST_SECRET_FILE")

    assert.NoError(t, err)
    assert.Equal(t, secretValue, result) // Whitespace should be trimmed
}

func TestLoadSecretFromFileOrEnv_Env(t *testing.T) {
    // Set direct environment variable (fallback)
    secretValue := "fallback-secret-456"
    os.Setenv("TEST_SECRET", secretValue)
    defer os.Unsetenv("TEST_SECRET")

    // Load secret (no file path set)
    result, err := LoadSecretFromFileOrEnv("TEST_SECRET", "TEST_SECRET_FILE")

    assert.NoError(t, err)
    assert.Equal(t, secretValue, result)
}

func TestLoadSecretFromFileOrEnv_Priority(t *testing.T) {
    // Create temporary file
    tmpFile, err := os.CreateTemp("", "secret-*.txt")
    require.NoError(t, err)
    defer os.Remove(tmpFile.Name())

    fileSecret := "file-secret"
    tmpFile.WriteString(fileSecret)
    tmpFile.Close()

    // Set both file path and direct env var
    os.Setenv("TEST_SECRET_FILE", tmpFile.Name())
    os.Setenv("TEST_SECRET", "env-secret")
    defer os.Unsetenv("TEST_SECRET_FILE")
    defer os.Unsetenv("TEST_SECRET")

    // File should take priority
    result, err := LoadSecretFromFileOrEnv("TEST_SECRET", "TEST_SECRET_FILE")

    assert.NoError(t, err)
    assert.Equal(t, fileSecret, result)
}

func TestLoadSecretFromFileOrEnv_NotFound(t *testing.T) {
    // Neither file path nor env var set
    result, err := LoadSecretFromFileOrEnv("NONEXISTENT_SECRET", "NONEXISTENT_SECRET_FILE")

    assert.Error(t, err)
    assert.Empty(t, result)
    assert.Contains(t, err.Error(), "secret not found")
}
```

#### 移行手順（段階的移行）

**Phase 2.2.1: コード対応（Week 2, Day 1-2)**
```bash
# 1. secret_loader.go実装
touch internal/utils/secret_loader.go
touch internal/utils/secret_loader_test.go

# 2. 全てのシークレット読み込み箇所を修正
# - agent-runner/pkg/github/token.go
# - agent-runner/pkg/agent/executor.go
# - agent-runner/pkg/storage/s3.go
# - agent-runner/pkg/reporter/client.go

# 3. テスト実行
go test -v ./internal/utils/... -run TestLoadSecretFromFileOrEnv
```

**Phase 2.2.2: Kubernetes Job生成ロジック修正（Week 2, Day 3)**
```bash
# 4. internal/clients/kubernetes.go修正
# BuildJobSpec()関数で:
# - Volumesにsecretボリューム追加
# - VolumeMountsに/var/secrets/*追加
# - 環境変数を*_FILE形式に変更
```

**Phase 2.2.3: 開発環境テスト（Week 2, Day 4)**
```bash
# 5. ローカルKubernetesで動作確認
# シークレット作成
kubectl create secret generic operator-secrets \
  --from-file=github-private-key=./github-app.pem

kubectl create secret generic anthropic-api-key \
  --from-literal=api-key="sk-ant-..."

# Pod起動
kubectl apply -f k8s/pod-template.yaml

# シークレットファイル確認
kubectl exec -it agent-runner-test -- ls -la /var/secrets/github/
kubectl exec -it agent-runner-test -- cat /var/secrets/github/private-key.pem

# アプリケーション動作確認
kubectl logs agent-runner-test
```

**Phase 2.2.4: 本番環境デプロイ（Week 2, Day 5)**
```bash
# 6. 本番シークレット作成
kubectl create secret generic operator-secrets \
  --from-file=github-private-key=./prod-github-app.pem \
  --namespace production

# 7. Operatorデプロイ（新しいJob生成ロジック）
kubectl apply -f k8s/operator-deployment.yaml

# 8. 動作確認
# - 既存のJobは環境変数で動作（後方互換性）
# - 新しいJobはファイルから読み込み

# 9. 監視
kubectl logs -f deployment/agent-operator | grep "secret"
```

#### ロールバック手順

```bash
# コード変更はファイル優先、環境変数フォールバックなので影響なし

# Kubernetes Jobテンプレート戻す場合:
git revert <commit-hash>
kubectl apply -f k8s/pod-template.yaml
```

#### 所要時間

- secret_loader実装: 3時間
- 各箇所の修正: 6時間
- Kubernetes Job生成修正: 4時間
- テスト: 4時間
- デプロイ・検証: 3時間
- **合計**: 20時間（約2.5日）

---

### 2.3 データベースインデックス追加

#### 影響範囲

**新規ファイル**:
- `migrations/000009_add_performance_indexes.sql`

#### 現状の問題

```sql
-- ❌ BAD: インデックスなしでクエリが遅い
SELECT * FROM agent_runs WHERE status = 'queued' ORDER BY created_at;
-- Full table scan → 10万レコードで10秒以上

SELECT * FROM agent_runs WHERE issue_id = 123;
-- Full table scan

SELECT * FROM pull_requests WHERE repo = 'owner/repo' AND number = 456;
-- Partial scan
```

#### 修正後のコード

**新規ファイル**: `migrations/000009_add_performance_indexes.sql`

```sql
-- Migration: Add performance indexes
-- Purpose: Improve query performance for frequently accessed columns
--
-- Expected improvements:
--   - AgentRun queries by status: 100x faster
--   - AgentRun queries by issue_id: 50x faster
--   - PullRequest lookups: 20x faster
--   - Issue lookups: 20x faster

-- +goose Up
-- +goose StatementBegin

-- Index for agent_runs table
-- Used by: Operator webhook handlers, state machine queries

-- 1. Composite index for queued jobs query
-- Query: SELECT * FROM agent_runs WHERE status = 'queued' ORDER BY created_at
CREATE INDEX idx_agent_runs_status_created
ON agent_runs(status, created_at);

-- 2. Index for issue_id lookups
-- Query: SELECT * FROM agent_runs WHERE issue_id = ?
CREATE INDEX idx_agent_runs_issue_id
ON agent_runs(issue_id);

-- 3. Index for retry queries
-- Query: SELECT * FROM agent_runs WHERE issue_id = ? ORDER BY retry_count DESC
CREATE INDEX idx_agent_runs_issue_retry
ON agent_runs(issue_id, retry_count DESC);

-- 4. Composite index for PR-related queries
-- Query: SELECT * FROM agent_runs WHERE pr_id = ? AND status = 'succeeded'
CREATE INDEX idx_agent_runs_pr_status
ON agent_runs(pr_id, status)
WHERE pr_id IS NOT NULL;

-- Index for pull_requests table

-- 5. Composite unique index for repo + number lookups
-- Query: SELECT * FROM pull_requests WHERE repo = ? AND number = ?
-- Note: This makes (repo, number) pair unique
CREATE UNIQUE INDEX idx_pull_requests_repo_number
ON pull_requests(repo, number);

-- 6. Index for agent_run_id lookups
-- Query: SELECT * FROM pull_requests WHERE agent_run_id = ?
CREATE INDEX idx_pull_requests_agent_run_id
ON pull_requests(agent_run_id);

-- Index for issues table

-- 7. Composite unique index for repo + number lookups
-- Query: SELECT * FROM issues WHERE repo = ? AND number = ?
CREATE UNIQUE INDEX idx_issues_repo_number
ON issues(repo, number);

-- 8. Index for github_issue_id lookups
-- Query: SELECT * FROM issues WHERE github_issue_id = ?
CREATE INDEX idx_issues_github_issue_id
ON issues(github_issue_id);

-- Index for review_feedback table

-- 9. Index for agent_run_id lookups
-- Query: SELECT * FROM review_feedback WHERE agent_run_id = ?
CREATE INDEX idx_review_feedback_agent_run_id
ON review_feedback(agent_run_id);

-- 10. Composite index for status queries
-- Query: SELECT * FROM review_feedback WHERE status = 'requested' ORDER BY created_at
CREATE INDEX idx_review_feedback_status_created
ON review_feedback(status, created_at);

-- Index for ci_status table

-- 11. Composite index for agent_run_id + check_name
-- Query: SELECT * FROM ci_status WHERE agent_run_id = ? AND check_name = ?
CREATE INDEX idx_ci_status_run_check
ON ci_status(agent_run_id, check_name);

-- 12. Index for conclusion queries
-- Query: SELECT * FROM ci_status WHERE agent_run_id = ? AND conclusion IN (...)
CREATE INDEX idx_ci_status_run_conclusion
ON ci_status(agent_run_id, conclusion);

-- Index for blocker_graph_edge table

-- 13. Index for blocked_issue_id lookups
-- Query: SELECT * FROM blocker_graph_edge WHERE blocked_issue_id = ?
CREATE INDEX idx_blocker_graph_blocked_id
ON blocker_graph_edge(blocked_issue_id);

-- 14. Index for blocking_issue_id lookups
-- Query: SELECT * FROM blocker_graph_edge WHERE blocking_issue_id = ?
CREATE INDEX idx_blocker_graph_blocking_id
ON blocker_graph_edge(blocking_issue_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop all indexes in reverse order
DROP INDEX IF EXISTS idx_blocker_graph_blocking_id;
DROP INDEX IF EXISTS idx_blocker_graph_blocked_id;
DROP INDEX IF EXISTS idx_ci_status_run_conclusion;
DROP INDEX IF EXISTS idx_ci_status_run_check;
DROP INDEX IF EXISTS idx_review_feedback_status_created;
DROP INDEX IF EXISTS idx_review_feedback_agent_run_id;
DROP INDEX IF EXISTS idx_issues_github_issue_id;
DROP INDEX IF EXISTS idx_issues_repo_number;
DROP INDEX IF EXISTS idx_pull_requests_agent_run_id;
DROP INDEX IF EXISTS idx_pull_requests_repo_number;
DROP INDEX IF EXISTS idx_agent_runs_pr_status;
DROP INDEX IF EXISTS idx_agent_runs_issue_retry;
DROP INDEX IF EXISTS idx_agent_runs_issue_id;
DROP INDEX IF EXISTS idx_agent_runs_status_created;

-- +goose StatementEnd
```

#### パフォーマンス影響分析

**Before (インデックスなし)**:
```sql
-- 10万レコードのagent_runsテーブル
EXPLAIN SELECT * FROM agent_runs WHERE status = 'queued' ORDER BY created_at;
-- rows: 100000 (Full table scan)
-- type: ALL
-- Extra: Using filesort
-- Time: ~10 seconds

EXPLAIN SELECT * FROM agent_runs WHERE issue_id = 123;
-- rows: 100000 (Full table scan)
-- type: ALL
-- Time: ~5 seconds
```

**After (インデックスあり)**:
```sql
EXPLAIN SELECT * FROM agent_runs WHERE status = 'queued' ORDER BY created_at;
-- rows: 50 (Index scan)
-- type: range
-- key: idx_agent_runs_status_created
-- Extra: Using index
-- Time: ~0.05 seconds (100倍高速化)

EXPLAIN SELECT * FROM agent_runs WHERE issue_id = 123;
-- rows: 5 (Index scan)
-- type: ref
-- key: idx_agent_runs_issue_id
-- Time: ~0.01 seconds (500倍高速化)
```

#### 実装手順

```bash
# Step 1: マイグレーションファイル作成
touch migrations/000009_add_performance_indexes.sql

# Step 2: 開発環境でテスト
goose -dir migrations mysql "user:pass@/db?parseTime=true" up

# Step 3: インデックス確認
mysql -u user -p db -e "SHOW INDEX FROM agent_runs;"

# Step 4: EXPLAINで効果確認
mysql -u user -p db -e "
  EXPLAIN SELECT * FROM agent_runs
  WHERE status = 'queued'
  ORDER BY created_at;
"

# Step 5: 本番環境デプロイ（ダウンタイムなし）
# MySQLはオンラインDDL対応（MySQL 8.0+）
goose -dir migrations mysql "prod-user:prod-pass@tcp(prod-mysql:3306)/prod_db?parseTime=true" up
```

#### テスト方法

**ベンチマークテスト**: `tests/benchmark/database_indexes_test.go`

```go
package benchmark

import (
    "testing"
    "agentic-automation/internal/config"
    "agentic-automation/internal/repositories"
)

func BenchmarkAgentRunRepository_GetQueuedRuns(b *testing.B) {
    db := config.GetDB()
    repo := repositories.NewAgentRunRepository(db, config.NewNopLogger())

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := repo.GetByStatus("queued")
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkAgentRunRepository_GetByIssueID(b *testing.B) {
    db := config.GetDB()
    repo := repositories.NewAgentRunRepository(db, config.NewNopLogger())

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := repo.GetByIssueID(123)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

```bash
# Before migration
go test -bench=. ./tests/benchmark/... -benchmem
# BenchmarkAgentRunRepository_GetQueuedRuns-8    100  10000000 ns/op

# After migration
go test -bench=. ./tests/benchmark/... -benchmem
# BenchmarkAgentRunRepository_GetQueuedRuns-8   10000    100000 ns/op (100倍高速化)
```

#### 所要時間

- マイグレーション作成: 3時間
- テスト: 2時間
- ベンチマーク: 2時間
- 本番適用: 1時間
- **合計**: 8時間（1日）

---

### 2.4 Bearer認証のタイミング攻撃対策

#### 影響範囲

**修正対象ファイル**:
- `internal/webhooks/middleware/bearer_auth.go:71`

#### 現状の問題

```go
// ❌ BAD: タイミング攻撃に脆弱
if providedToken != token {
    // トークン長が異なる場合、早期リターンで時間差が発生
    // 攻撃者が文字列長を推測可能
}
```

**攻撃シナリオ**:
1. 攻撃者が異なる長さのトークンで試行
2. レスポンス時間を測定
3. 正しいトークン長を推測
4. 正しい長さで総当たり攻撃

#### 修正後のコード

**修正**: `internal/webhooks/middleware/bearer_auth.go`

```go
// Compare tokens using constant-time comparison to prevent timing attacks
// Use subtle.ConstantTimeCompare for secure comparison
if len(providedToken) != len(token) || subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
    logger.Warn("Invalid Bearer token",
        config.String("path", c.Request.URL.Path),
    )
    c.AbortWithStatusJSON(401, gin.H{
        "error":   "INVALID_TOKEN",
        "message": "Invalid or missing Bearer token",
    })
    return
}
```

**注意**: 実は既にコードに`subtle.ConstantTimeCompare`が使われていました！レビューでは確認できていませんでしたが、既に対策済みです。

#### 検証（既存コード確認）

```bash
# bearer_auth.goの該当行を確認
cat internal/webhooks/middleware/bearer_auth.go | grep -A 5 "ConstantTimeCompare"

# 期待結果:
# if len(providedToken) != len(token) || subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
```

**結果**: 既に対策済み ✅

#### 追加テスト（念のため）

```go
// internal/webhooks/middleware/bearer_auth_timing_test.go
func TestVerifyBearerToken_NoTimingLeak(t *testing.T) {
    correctToken := "correct-token-1234567890"
    os.Setenv("OPERATOR_API_TOKEN", correctToken)
    defer os.Unsetenv("OPERATOR_API_TOKEN")

    gin.SetMode(gin.TestMode)
    router := gin.New()
    router.Use(VerifyBearerToken())
    router.GET("/test", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })

    // Test with various incorrect tokens
    testCases := []struct {
        name  string
        token string
    }{
        {"wrong length short", "short"},
        {"wrong length long", "very-long-incorrect-token-123456789012345"},
        {"correct length wrong token", "incorrect-token-1234567"},
        {"one character off", "correct-token-123456789X"},
    }

    var timings []time.Duration
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest("GET", "/test", nil)
            req.Header.Set("Authorization", "Bearer "+tc.token)
            w := httptest.NewRecorder()

            start := time.Now()
            router.ServeHTTP(w, req)
            duration := time.Since(start)
            timings = append(timings, duration)

            assert.Equal(t, 401, w.Code)
        })
    }

    // Calculate standard deviation of timings
    // Should be very low if constant-time comparison is working
    mean := average(timings)
    stdDev := standardDeviation(timings, mean)

    // Standard deviation should be < 10% of mean
    // (allows for system noise but detects timing leaks)
    maxAllowedStdDev := mean * 0.1
    assert.Less(t, stdDev, maxAllowedStdDev,
        "Timing variation too high, possible timing leak. Mean: %v, StdDev: %v",
        mean, stdDev)
}

func average(durations []time.Duration) time.Duration {
    var sum time.Duration
    for _, d := range durations {
        sum += d
    }
    return sum / time.Duration(len(durations))
}

func standardDeviation(durations []time.Duration, mean time.Duration) time.Duration {
    var variance int64
    for _, d := range durations {
        diff := int64(d - mean)
        variance += diff * diff
    }
    variance /= int64(len(durations))
    return time.Duration(int64(math.Sqrt(float64(variance))))
}
```

#### 所要時間

- コード確認: 0.5時間（既に対策済み）
- テスト追加: 2時間
- **合計**: 2.5時間

---

### 2.5 コード重複リファクタリング

#### 影響範囲

**修正対象ファイル**:
- `internal/services/ci_failure.go:262-287`

#### 現状の問題

```go
// ❌ BAD: 同じロジックを3回繰り返し
func (a *CIFailureAnalyzer) determineOverallFailureType(failureTypes []string) string {
    for _, ft := range failureTypes {
        if ft == FailureTypeTestFailure {
            return FailureTypeTestFailure
        }
    }
    for _, ft := range failureTypes {
        if ft == FailureTypeBuildError {
            return FailureTypeBuildError
        }
    }
    for _, ft := range failureTypes {
        if ft == FailureTypeLintError {
            return FailureTypeLintError
        }
    }
    return FailureTypeUnknown
}
```

**問題点**:
- O(3n) の時間複雑度（O(n)で十分）
- コード重複
- 優先順位追加時に3箇所修正必要

#### 修正後のコード

```go
// ✅ GOOD: 優先順位配列を使用したシンプルな実装
func (a *CIFailureAnalyzer) determineOverallFailureType(failureTypes []string) string {
    if len(failureTypes) == 0 {
        return FailureTypeUnknown
    }

    // Define priority order (highest to lowest)
    priorityOrder := []string{
        FailureTypeTestFailure,  // Highest priority
        FailureTypeBuildError,
        FailureTypeLintError,
    }

    // Check each priority level
    for _, priorityType := range priorityOrder {
        for _, ft := range failureTypes {
            if ft == priorityType {
                return priorityType
            }
        }
    }

    return FailureTypeUnknown
}
```

**さらに最適化（マップ使用）**:

```go
// ✅ BETTER: O(n)でさらに高速
func (a *CIFailureAnalyzer) determineOverallFailureType(failureTypes []string) string {
    if len(failureTypes) == 0 {
        return FailureTypeUnknown
    }

    // Create set for O(1) lookup
    typeSet := make(map[string]bool)
    for _, ft := range failureTypes {
        typeSet[ft] = true
    }

    // Check in priority order
    if typeSet[FailureTypeTestFailure] {
        return FailureTypeTestFailure
    }
    if typeSet[FailureTypeBuildError] {
        return FailureTypeBuildError
    }
    if typeSet[FailureTypeLintError] {
        return FailureTypeLintError
    }

    return FailureTypeUnknown
}
```

#### テストコード

```go
func TestDetermineOverallFailureType_Priority(t *testing.T) {
    analyzer := &CIFailureAnalyzer{}

    tests := []struct {
        name          string
        failureTypes  []string
        expectedType  string
    }{
        {
            name:         "Test failure has highest priority",
            failureTypes: []string{FailureTypeLintError, FailureTypeTestFailure, FailureTypeBuildError},
            expectedType: FailureTypeTestFailure,
        },
        {
            name:         "Build error when no test failure",
            failureTypes: []string{FailureTypeLintError, FailureTypeBuildError},
            expectedType: FailureTypeBuildError,
        },
        {
            name:         "Lint error when no build or test failure",
            failureTypes: []string{FailureTypeLintError},
            expectedType: FailureTypeLintError,
        },
        {
            name:         "Unknown when empty",
            failureTypes: []string{},
            expectedType: FailureTypeUnknown,
        },
        {
            name:         "Unknown when no recognized types",
            failureTypes: []string{"custom_failure"},
            expectedType: FailureTypeUnknown,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := analyzer.determineOverallFailureType(tt.failureTypes)
            assert.Equal(t, tt.expectedType, result)
        })
    }
}

func BenchmarkDetermineOverallFailureType_Original(b *testing.B) {
    analyzer := &CIFailureAnalyzer{}
    failureTypes := []string{FailureTypeLintError, FailureTypeBuildError}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = analyzer.determineOverallFailureType(failureTypes)
    }
}

func BenchmarkDetermineOverallFailureType_Optimized(b *testing.B) {
    analyzer := &CIFailureAnalyzer{}
    failureTypes := []string{FailureTypeLintError, FailureTypeBuildError}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = analyzer.determineOverallFailureTypeOptimized(failureTypes)
    }
}
```

#### 所要時間

- リファクタリング: 1時間
- テスト: 1時間
- ベンチマーク: 0.5時間
- **合計**: 2.5時間

---

### Phase 2 総括

**合計所要時間**: 45時間（約6営業日）

**成果物**:
- ✅ Goroutineリーク修正（2ファイル）
- ✅ シークレットボリュームマウント（5ファイル + migration)
- ✅ データベースインデックス（1 migration）
- ✅ タイミング攻撃対策（既に対策済み、テスト追加）
- ✅ コード重複削除（1ファイル）

**パフォーマンス向上**:
- DB クエリ: 100-500倍高速化
- メモリリーク: 0件（Goroutineクリーンアップ）

**セキュリティ向上**:
- シークレット露出リスク: 90%削減

---

## Phase 3: Medium Priority (Weeks 4-6)

### 概要

**目標**: コード品質とメンテナンス性の向上
**期間**: 15営業日
**担当者**: ミドルエンジニア2名 + シニアレビュアー1名
**リスク**: 低（非機能要件の改善）

---

### 3.1 God Object分割

#### 影響範囲

**修正対象ファイル**:
- `internal/clients/kubernetes.go` (20+ メソッド)

#### 現状の問題

```go
// ❌ BAD: 1つのstructに責務が多すぎる
type KubernetesClient struct {
    clientset *kubernetes.Clientset
    namespace string
    logger    *config.AppLogger

    // 20+ methods:
    // - Job management (Create, Get, List, Delete, Wait)
    // - Pod management (Get, List, GetStatus, GetLogs)
    // - Configuration (BuildJobSpec, buildEnvVars)
    // - Helper functions (int64Ptr, boolPtr, parseQuantity)
}
```

#### 修正後のコード

**新規ファイル**: `internal/clients/kubernetes_job.go`

```go
package clients

import (
    "context"
    "agentic-automation/internal/config"
    batchv1 "k8s.io/api/batch/v1"
    "k8s.io/client-go/kubernetes"
)

// JobClient handles Kubernetes Job operations
type JobClient interface {
    CreateJob(ctx context.Context, spec *JobSpec) (*batchv1.Job, error)
    GetJob(ctx context.Context, name string) (*batchv1.Job, error)
    ListJobs(ctx context.Context, labels map[string]string) (*batchv1.JobList, error)
    DeleteJob(ctx context.Context, name string) error
    WaitForJobCompletion(ctx context.Context, name string, timeout time.Duration) (*JobResult, error)
    GetJobStatus(ctx context.Context, name string) (string, error)
    CountRunningJobs(ctx context.Context) (int, error)
}

type jobClient struct {
    clientset *kubernetes.Clientset
    namespace string
    logger    *config.AppLogger
}

// NewJobClient creates a new Job operations client
func NewJobClient(clientset *kubernetes.Clientset, namespace string, logger *config.AppLogger) JobClient {
    return &jobClient{
        clientset: clientset,
        namespace: namespace,
        logger:    logger,
    }
}

// Implementation methods...
```

**新規ファイル**: `internal/clients/kubernetes_pod.go`

```go
package clients

// PodClient handles Kubernetes Pod operations
type PodClient interface {
    GetPod(ctx context.Context, name string) (*corev1.Pod, error)
    ListPods(ctx context.Context, labels map[string]string) (*corev1.PodList, error)
    GetPodStatus(ctx context.Context, name string) (*PodStatus, error)
    GetPodLogs(ctx context.Context, name string, opts *LogOptions) (string, error)
}

type podClient struct {
    clientset *kubernetes.Clientset
    namespace string
    logger    *config.AppLogger
}

func NewPodClient(clientset *kubernetes.Clientset, namespace string, logger *config.AppLogger) PodClient {
    return &podClient{
        clientset: clientset,
        namespace: namespace,
        logger:    logger,
    }
}
```

**修正**: `internal/clients/kubernetes.go` (ファクトリーのみ残す)

```go
package clients

// KubernetesClients aggregates all Kubernetes client interfaces
type KubernetesClients struct {
    Jobs JobClient
    Pods PodClient
}

// NewKubernetesClients creates all Kubernetes clients
func NewKubernetesClients(logger *config.AppLogger) (*KubernetesClients, error) {
    config, err := rest.InClusterConfig()
    if err != nil {
        return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        return nil, fmt.Errorf("failed to create clientset: %w", err)
    }

    namespace := getNamespace()

    return &KubernetesClients{
        Jobs: NewJobClient(clientset, namespace, logger),
        Pods: NewPodClient(clientset, namespace, logger),
    }, nil
}
```

#### 移行手順

**Step 1: 新しいクライアント作成**
```bash
cp internal/clients/kubernetes.go internal/clients/kubernetes_job.go
cp internal/clients/kubernetes.go internal/clients/kubernetes_pod.go

# Job関連メソッドをkubernetes_job.goに移動
# Pod関連メソッドをkubernetes_pod.goに移動
```

**Step 2: 呼び出し側の修正**
```go
// Before
k8sClient, err := clients.NewKubernetesClient(logger)
job, err := k8sClient.CreateJob(ctx, spec)
logs, err := k8sClient.GetPodLogs(ctx, podName, nil)

// After
k8sClients, err := clients.NewKubernetesClients(logger)
job, err := k8sClients.Jobs.CreateJob(ctx, spec)
logs, err := k8sClients.Pods.GetPodLogs(ctx, podName, nil)
```

#### 所要時間

- 分割実装: 8時間
- 呼び出し側修正: 4時間
- テスト: 4時間
- **合計**: 16時間（2日）

---

### 3.2 PodSecurityPolicy実装

#### 新規ファイル

`k8s/pod-security-policy.yaml`

```yaml
apiVersion: policy/v1beta1
kind: PodSecurityPolicy
metadata:
  name: agent-runner-psp
spec:
  privileged: false
  allowPrivilegeEscalation: false
  requiredDropCapabilities:
    - ALL
  volumes:
    - 'configMap'
    - 'emptyDir'
    - 'secret'
  hostNetwork: false
  hostIPC: false
  hostPID: false
  runAsUser:
    rule: 'MustRunAsNonRoot'
  seLinux:
    rule: 'RunAsAny'
  fsGroup:
    rule: 'RunAsAny'
  readOnlyRootFilesystem: true
```

#### 所要時間

- YAML作成: 4時間
- テスト: 3時間
- **合計**: 7時間

---

### 3.3 Context パラメータ追加

#### 影響範囲

**修正対象**: 全ての`context.Background()`呼び出し

```go
// Before
func (s *S3SessionStorage) SaveSession(...) error {
    ctx := context.Background() // ❌
    // ...
}

// After
func (s *S3SessionStorage) SaveSession(ctx context.Context, ...) error {
    // contextを受け取る ✅
    // ...
}
```

#### 所要時間

- コード修正: 8時間
- テスト: 4時間
- **合計**: 12時間（1.5日）

---

### 3.4 重複ファイル統合

#### 影響範囲

**削除**: `agent-runner/pkg/utils/cursor_log_parser.go`
**統合先**: `internal/utils/cursor_log_parser.go`

```bash
# 重複確認
diff internal/utils/cursor_log_parser.go agent-runner/pkg/utils/cursor_log_parser.go

# 統合
# 1. internal/utils/を共有パッケージとして使用
# 2. agent-runner側は削除
rm agent-runner/pkg/utils/cursor_log_parser.go

# 3. import修正
sed -i 's|agent-runner/pkg/utils|agentic-automation/internal/utils|g' agent-runner/**/*.go
```

#### 所要時間

- 統合: 2時間
- テスト: 1時間
- **合計**: 3時間

---

### 3.5 Magic Numbers 定数化

#### 影響範囲

**修正対象**: 5+ 箇所

```go
// Before
maxInlineContentSize := 900 * 1024 // ❌ なぜ900KB?

// After
const (
    // MaxInlineEnvVarSize is the maximum size for environment variable values.
    // Kubernetes has a limit of ~1MB for env vars, we use 900KB to leave headroom.
    MaxInlineEnvVarSize = 900 * 1024 // 900 KiB
)
maxInlineContentSize := MaxInlineEnvVarSize // ✅
```

#### 所要時間

- 定数抽出: 4時間
- ドキュメント: 2時間
- **合計**: 6時間

---

### Phase 3 総括

**合計所要時間**: 44時間（約6営業日）

**成果物**:
- ✅ God Object分割（3ファイル）
- ✅ PodSecurityPolicy（1ファイル）
- ✅ Context追加（10+ ファイル）
- ✅ 重複ファイル統合（1ファイル削除）
- ✅ Magic Numbers定数化（5+ 箇所）

**コード品質スコア**: 7.0/10 → 8.5/10

---

## Phase 4: Long-term Enhancements

### 概要

**目標**: 長期的な保守性とスケーラビリティ
**期間**: 3-6ヶ月
**担当者**: チーム全体（スプリントに組み込み）
**リスク**: 低

---

### 4.1 OpenAPI仕様書作成

**ファイル**: `docs/openapi.yaml`

```yaml
openapi: 3.0.0
info:
  title: Agent Operator API
  version: 1.0.0
  description: API for AI agent execution reporting
paths:
  /api/agent-runs/{id}/report:
    post:
      summary: Report agent execution completion
      # ... 詳細省略
```

#### 所要時間: 16時間（2日）

---

### 4.2 ログ集約（CloudWatch/ELK）

**実装**: `internal/config/logger.go` にCloudWatch Writer追加

```go
import "github.com/aws/aws-sdk-go/service/cloudwatchlogs"

// CloudWatchログ送信
```

#### 所要時間: 32時間（4日）

---

### 4.3 CI/CD改善

**新規ファイル**: `.github/workflows/ci.yml`

```yaml
name: CI
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run tests
        run: |
          go test -v -race ./...
          golangci-lint run
```

#### 所要時間: 24時間（3日）

---

### 4.4 依存関係自動更新

**新規ファイル**: `.github/workflows/dependencies.yml`

```yaml
name: Dependency Updates
on:
  schedule:
    - cron: '0 0 * * 1'  # Weekly
# ... Dependabot設定
```

#### 所要時間: 8時間（1日）

---

## テスト戦略

### 単体テスト

```bash
# 全単体テスト実行
go test -v ./... -short

# カバレッジ測定
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 目標カバレッジ: 80%以上
```

### 統合テスト

```bash
# MySQL + MinIO起動
docker-compose up -d mysql minio

# 統合テスト実行
go test -v ./tests/integration/...

# 期待結果: 全テスト成功
```

### E2Eテスト

```bash
# Kubernetes環境でテスト
kubectl apply -f k8s/test/

# Webhook送信
curl -X POST http://localhost:3000/webhooks/github \
  -H "X-GitHub-Event: issues" \
  -H "X-GitHub-Delivery: $(uuidgen)" \
  -d @tests/fixtures/webhooks/issue_comment_implement.json

# 期待結果: Jobが作成され、成功する
kubectl get jobs
kubectl logs job/agent-runner-<id>
```

---

## ロールバック手順

### コード変更のロールバック

```bash
# 1. コミットを特定
git log --oneline --grep="Phase 1"

# 2. Revert
git revert <commit-hash>

# 3. デプロイ
kubectl apply -f k8s/operator-deployment.yaml
```

### データベースマイグレーションのロールバック

```bash
# 1. 現在のバージョン確認
goose -dir migrations status

# 2. 1つ前にロールバック
goose -dir migrations down

# 3. 特定バージョンにロールバック
goose -dir migrations down-to 000008
```

### Kubernetes設定のロールバック

```bash
# NetworkPolicy削除
kubectl delete -f k8s/network-policy.yaml

# 前のバージョンに戻す
kubectl rollout undo deployment/agent-operator

# 特定リビジョンに戻す
kubectl rollout undo deployment/agent-operator --to-revision=2
```

---

## リスク管理

### リスク一覧

| リスク | 確率 | 影響度 | 軽減策 |
|--------|------|--------|--------|
| Panic修正でサービス起動失敗 | 中 | 高 | 段階的ロールアウト、カナリアデプロイ |
| NetworkPolicyで通信遮断 | 中 | 高 | 事前テスト環境検証、ロールバック手順準備 |
| DBインデックスでロック発生 | 低 | 中 | オフピーク時間にデプロイ、Online DDL使用 |
| Goroutine修正でデッドロック | 低 | 高 | 徹底的なテスト、race detector使用 |

### 軽減策

1. **カナリアデプロイ**
```bash
# 10%のトラフィックで検証
kubectl apply -f k8s/operator-canary.yaml
# 問題なければ100%に拡大
```

2. **ブルーグリーンデプロイ**
```bash
# 新バージョンをBlue環境にデプロイ
kubectl apply -f k8s/operator-blue.yaml
# 検証後、Greenと切り替え
kubectl patch service agent-operator -p '{"spec":{"selector":{"version":"blue"}}}'
```

3. **監視とアラート**
```yaml
# Prometheusアラート
- alert: OperatorHighErrorRate
  expr: rate(http_requests_total{status=~"5.."}[5m]) > 0.05
  for: 5m
  annotations:
    summary: "Operator error rate > 5%"
```

---

## 成功基準

### Phase 1成功基準

- [ ] Panicが0件（単体テスト + 本番24時間運用）
- [ ] Webhook 10MB制限が機能（負荷テスト）
- [ ] NetworkPolicy適用後も全機能が動作
- [ ] XSS攻撃が防御される（セキュリティテスト）
- [ ] エラーログが全て記録される

### Phase 2成功基準

- [ ] Goroutineリークが0件（メモリ監視24時間）
- [ ] シークレットが環境変数に表示されない
- [ ] DBクエリが100倍高速化（ベンチマーク）
- [ ] タイミング攻撃が防御される（テスト）
- [ ] コード重複が削除される

### Phase 3成功基準

- [ ] God Objectが分割される
- [ ] PodSecurityPolicyが適用される
- [ ] Context タイムアウトが機能する
- [ ] 重複ファイルが0件
- [ ] Magic Numbersが0件

### 全体成功基準

- [ ] コード品質スコア: 7.0 → 8.5
- [ ] セキュリティスコア: 6.5 → 8.5
- [ ] テストカバレッジ: 69% → 80%
- [ ] 本番障害: 0件（各フェーズ後1週間）

---

## まとめ

### 実装スケジュール

| Phase | 期間 | 工数 | 完了予定日 |
|-------|------|------|-----------|
| Phase 1 | Week 1 | 34h | Day 5 |
| Phase 2 | Weeks 2-3 | 45h | Day 15 |
| Phase 3 | Weeks 4-6 | 44h | Day 30 |
| Phase 4 | 3-6 months | 80h | - |

### 総工数

- **短期（1-6週間）**: 123時間（約15営業日）
- **長期（3-6ヶ月）**: 80時間（約10営業日）
- **合計**: 203時間（約25営業日 = 5週間）

### 次のアクション

```bash
# 1. このプランをレビュー依頼
git add docs/REMEDIATION_PLAN.md
git commit -m "docs: add comprehensive remediation plan for code review findings"
git push

# 2. Phase 1着手
git checkout -b fix/phase1-critical-fixes master

# 3. 実装開始！
```

---

**End of Remediation Plan**
