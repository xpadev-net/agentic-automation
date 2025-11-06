# Agentic Automation - 本番環境デプロイマニュアル

このマニュアルは、Agentic Automation システムを本番環境にデプロイするための完全なガイドです。

## 目次

- [システム概要](#システム概要)
- [前提条件](#前提条件)
- [アーキテクチャ](#アーキテクチャ)
- [デプロイ手順](#デプロイ手順)
  - [ステップ 1: インフラストラクチャの準備](#ステップ-1-インフラストラクチャの準備)
  - [ステップ 2: GitHub App の作成と設定](#ステップ-2-github-app-の作成と設定)
  - [ステップ 3: Kubernetes Secrets の作成](#ステップ-3-kubernetes-secrets-の作成)
  - [ステップ 4: Docker イメージのビルドとプッシュ](#ステップ-4-docker-イメージのビルドとプッシュ)
  - [ステップ 5: データベースのセットアップ](#ステップ-5-データベースのセットアップ)
  - [ステップ 6: S3/MinIO のセットアップ](#ステップ-6-s3minio-のセットアップ)
  - [ステップ 7: Kubernetes リソースのデプロイ](#ステップ-7-kubernetes-リソースのデプロイ)
  - [ステップ 8: Ingress の設定](#ステップ-8-ingress-の設定)
  - [ステップ 9: 動作確認](#ステップ-9-動作確認)
- [運用](#運用)
- [トラブルシューティング](#トラブルシューティング)
- [付録](#付録)

---

## システム概要

Agentic Automation は、GitHub の Issue と Pull Request を AI エージェント（Claude Code または Cursor Agents）で自動化するシステムです。

### 主要コンポーネント

1. **Operator**: GitHub webhook を受信し、Agent Runner を Kubernetes Job として起動するオーケストレーションサービス
2. **Agent Runner**: 実際に AI エージェントを実行する Pod（動的に生成される Kubernetes Job）
3. **MySQL**: メタデータとステート管理用データベース
4. **S3/MinIO**: エージェントセッションの永続化ストレージ

### 技術スタック

- **言語**: Go 1.22+
- **フレームワーク**: Gin (Web), GORM (ORM)
- **データベース**: MySQL 8.0+
- **ストレージ**: S3 / MinIO
- **オーケストレーション**: Kubernetes
- **AI エージェント**: Claude Code, Cursor Agents

---

## 前提条件

### 必須要件

- **Kubernetes クラスター** v1.24 以上
  - `kubectl` コマンドラインツール
  - クラスター管理者権限
  - Ingress コントローラー（nginx, traefik など）
- **MySQL 8.0+** データベース
  - マネージドサービス（AWS RDS, Google Cloud SQL, Azure Database など）推奨
  - または自前でホストする MySQL サーバー
- **S3 互換ストレージ**
  - AWS S3
  - MinIO (self-hosted)
  - Google Cloud Storage (S3 互換モード)
- **Docker レジストリ** への push 権限
  - GitHub Container Registry (ghcr.io)
  - Docker Hub
  - または独自のレジストリ
- **GitHub App**
  - Webhook URL 設定権限
  - 組織またはリポジトリへのインストール権限

### 必要な認証情報

以下の認証情報を事前に準備してください:

- GitHub App ID
- GitHub App Private Key (PEM フォーマット)
- GitHub Webhook Secret
- Anthropic API Key (Claude Code 用)
- Cursor API Key (Cursor Agents 用、オプション)
- Discord Webhook URL (通知用、オプション)
- MySQL 接続情報
- S3/MinIO アクセスキーとシークレットキー

### 推奨リソース

本番環境での推奨リソース:

**Operator Pod (2 レプリカ):**
- CPU: 250m リクエスト / 500m リミット
- メモリ: 256Mi リクエスト / 512Mi リミット

**Agent Runner Pod (並行実行数に応じて):**
- CPU: 500m リクエスト / 2000m リミット
- メモリ: 512Mi リクエスト / 2Gi リミット

**MySQL:**
- CPU: 2 コア以上
- メモリ: 4GB 以上
- ストレージ: 100GB 以上（拡張可能）

**S3/MinIO:**
- ストレージ: 500GB 以上（セッション数に応じて）

---

## アーキテクチャ

```
┌─────────────┐
│   GitHub    │
│   Webhook   │
└──────┬──────┘
       │ HTTPS
       ▼
┌─────────────────┐
│    Ingress      │
│ (nginx/traefik) │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│           Operator Service              │
│  ┌─────────────────────────────────┐   │
│  │  - Webhook Handler              │   │
│  │  - Job Orchestrator             │   │
│  │  - REST API (/api/agent-runs)   │   │
│  └─────────────────────────────────┘   │
└──┬──────────────────────────────────┬───┘
   │                                  │
   │ ┌────────────────────────────┐  │
   │ │   Kubernetes API           │  │
   │ │   (Create Jobs)            │  │
   │ └────────────────────────────┘  │
   │                                  │
   ▼                                  ▼
┌──────────────────┐         ┌──────────────┐
│      MySQL       │         │   S3/MinIO   │
│   (Metadata)     │         │  (Sessions)  │
└──────────────────┘         └──────────────┘
                                     ▲
   ┌─────────────────────────────────┤
   │                                 │
   ▼                                 │
┌────────────────────────────────────┴──┐
│       Agent Runner Job (Pod)          │
│  ┌──────────────────────────────┐    │
│  │  1. Clone repository         │    │
│  │  2. Run AI agent             │    │
│  │  3. Create/update PR         │    │
│  │  4. Report status to Operator│    │
│  │  5. Upload session to S3     │    │
│  └──────────────────────────────┘    │
└───────────────────────────────────────┘
```

---

## デプロイ手順

### ステップ 1: インフラストラクチャの準備

#### 1.1 Kubernetes クラスターの確認

```bash
# クラスターに接続できることを確認
kubectl cluster-info

# ノードの状態を確認
kubectl get nodes

# Ingress コントローラーが動作していることを確認
kubectl get pods -n ingress-nginx
# または
kubectl get pods -n traefik
```

#### 1.2 MySQL データベースの準備

**本番環境: マネージドサービスを使用する場合（推奨）**

AWS RDS の例:
```bash
# RDS インスタンスの作成（AWS CLI）
aws rds create-db-instance \
  --db-instance-identifier agentic-automation-db \
  --db-instance-class db.t3.medium \
  --engine mysql \
  --engine-version 8.0 \
  --master-username admin \
  --master-user-password YOUR_SECURE_PASSWORD \
  --allocated-storage 100 \
  --vpc-security-group-ids sg-xxxxx \
  --db-subnet-group-name my-subnet-group \
  --backup-retention-period 7 \
  --preferred-backup-window "03:00-04:00" \
  --preferred-maintenance-window "mon:04:00-mon:05:00"

# エンドポイントの取得
aws rds describe-db-instances \
  --db-instance-identifier agentic-automation-db \
  --query 'DBInstances[0].Endpoint.Address' \
  --output text
```

**開発/検証環境: Kubernetes 内に MySQL をデプロイする場合**

```bash
# MySQL を Kubernetes にデプロイ
make deploy-infra

# または手動で
kubectl apply -f k8s/mysql-deployment.yaml

# Pod が起動するまで待機
kubectl wait --for=condition=ready pod -l app=mysql --timeout=120s

# 接続確認
kubectl run -it --rm mysql-client --image=mysql:8.0 --restart=Never -- \
  mysql -h mysql.default.svc.cluster.local -u agent_user -pagent_password github_agent_automation
```

#### 1.3 S3/MinIO の準備

**本番環境: AWS S3 を使用する場合（推奨）**

```bash
# S3 バケットの作成
aws s3 mb s3://agentic-automation-sessions --region us-east-1

# バージョニングの有効化（オプション）
aws s3api put-bucket-versioning \
  --bucket agentic-automation-sessions \
  --versioning-configuration Status=Enabled

# ライフサイクルポリシーの設定（古いセッションの自動削除）
cat > lifecycle.json <<EOF
{
  "Rules": [
    {
      "Id": "DeleteOldSessions",
      "Status": "Enabled",
      "Prefix": "",
      "Expiration": {
        "Days": 90
      }
    }
  ]
}
EOF

aws s3api put-bucket-lifecycle-configuration \
  --bucket agentic-automation-sessions \
  --lifecycle-configuration file://lifecycle.json
```

**開発/検証環境: MinIO を Kubernetes にデプロイする場合**

```bash
# MinIO を Kubernetes にデプロイ
kubectl apply -f k8s/minio-deployment.yaml

# Pod が起動するまで待機
kubectl wait --for=condition=ready pod -l app=minio --timeout=120s

# MinIO にポートフォワード
kubectl port-forward svc/minio 9000:9000 &

# バケットの作成
S3_ENDPOINT=http://localhost:9000 \
S3_ACCESS_KEY_ID=minioadmin \
S3_SECRET_ACCESS_KEY=minioadmin \
S3_BUCKET=agent-sessions \
S3_REGION=us-east-1 \
./scripts/setup-minio.sh
```

---

### ステップ 2: GitHub App の作成と設定

#### 2.1 GitHub App の作成

1. GitHub にログインし、組織の Settings > Developer settings > GitHub Apps に移動
2. "New GitHub App" をクリック
3. 以下の設定を入力:

**Basic information:**
- GitHub App name: `Agentic Automation`
- Homepage URL: `https://your-domain.com`
- Webhook URL: `https://your-domain.com/webhooks/github` (後で設定する Ingress のドメイン)
- Webhook secret: ランダムな文字列を生成（例: `openssl rand -hex 32`）

**Permissions:**

Repository permissions:
- Contents: Read & Write
- Issues: Read & Write
- Pull Requests: Read & Write
- Metadata: Read-only

**Subscribe to events:**
- Issues
- Issue comment
- Pull request
- Pull request review
- Pull request review comment
- Check run
- Check suite

4. "Create GitHub App" をクリック
5. App ID をメモ
6. "Generate a private key" をクリックして秘密鍵をダウンロード（`your-app-name.2024-01-01.private-key.pem`）

#### 2.2 GitHub App のインストール

1. GitHub App の設定ページで "Install App" タブに移動
2. 対象の組織またはアカウントを選択
3. "All repositories" または "Only select repositories" を選択
4. "Install" をクリック

---

### ステップ 3: Kubernetes Secrets の作成

#### 3.1 環境変数の準備

`.env` ファイルを作成（または既存の `.env` を使用）:

```bash
# データベース
DATABASE_URL="mysql://admin:YOUR_PASSWORD@your-rds-endpoint.rds.amazonaws.com:3306/github_agent_automation?charset=utf8mb4&parseTime=True&loc=Local"

# GitHub App
GITHUB_APP_ID="123456"
GITHUB_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----\n...snip...\n-----END PRIVATE KEY-----\n"
GITHUB_WEBHOOK_SECRET="your-webhook-secret"

# AI エージェント
ANTHROPIC_API_KEY="sk-ant-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
CURSOR_API_KEY="cursor_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"  # オプション

# S3/MinIO
S3_ENDPOINT="https://s3.amazonaws.com"  # または "http://minio.default.svc.cluster.local:9000"
S3_REGION="us-east-1"
S3_BUCKET="agentic-automation-sessions"
S3_ACCESS_KEY_ID="AKIA..."
S3_SECRET_ACCESS_KEY="your-secret-key"
S3_USE_PATH_STYLE="false"  # MinIO: true / AWS: false
S3_MAX_RETRIES="5"

# Operator API
OPERATOR_API_TOKEN="$(openssl rand -hex 32)"

# Discord 通知（オプション）
DISCORD_WEBHOOK_URL="https://discord.com/api/webhooks/..."
```

`.env` をソース:
```bash
source .env
```

#### 3.2 Secrets の作成

**方法 1: Makefile を使用（コマンド表示のみ）**

```bash
make k8s-secrets
```

表示されたコマンドをコピーして実行します。

**方法 2: 手動で作成**

```bash
# 1. operator-secrets
kubectl create secret generic operator-secrets \
  --from-literal=database-url="$DATABASE_URL" \
  --from-literal=github-app-id="$GITHUB_APP_ID" \
  --from-file=github-private-key=./github-app.pem \
  --from-literal=github-webhook-secret="$GITHUB_WEBHOOK_SECRET" \
  --from-literal=discord-webhook-url="$DISCORD_WEBHOOK_URL" \
  --from-literal=operator-api-token="$OPERATOR_API_TOKEN"

# 2. anthropic-api-key
kubectl create secret generic anthropic-api-key \
  --from-literal=api-key="$ANTHROPIC_API_KEY"

# 3. cursor-api-key (オプション)
kubectl create secret generic cursor-api-key \
  --from-literal=api-key="$CURSOR_API_KEY"

# 4. s3-credentials
kubectl create secret generic s3-credentials \
  --from-literal=access-key-id="$S3_ACCESS_KEY_ID" \
  --from-literal=secret-access-key="$S3_SECRET_ACCESS_KEY"
```

#### 3.3 Secrets の確認

```bash
# 作成された Secrets の一覧
kubectl get secrets

# 特定の Secret の内容確認（デコード）
kubectl get secret operator-secrets -o jsonpath='{.data.github-app-id}' | base64 -d
```

---

### ステップ 4: Docker イメージのビルドとプッシュ

#### 4.1 Docker レジストリにログイン

**GitHub Container Registry の場合:**

```bash
echo $CR_PAT | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

**Docker Hub の場合:**

```bash
docker login -u YOUR_DOCKERHUB_USERNAME
```

#### 4.2 Operator イメージのビルドとプッシュ

```bash
# 環境変数の設定
export DOCKER_REGISTRY=ghcr.io
export DOCKER_OWNER=your-org  # または your-username

# ビルドとプッシュ
make docker-build-push-operator

# または手動で
docker build -t ghcr.io/your-org/operator:latest .
docker push ghcr.io/your-org/operator:latest
```

#### 4.3 Agent Runner イメージのビルドとプッシュ

```bash
# ビルドとプッシュ
make docker-build-push

# または手動で
cd agent-runner
docker build -t ghcr.io/your-org/agentic-automation-runner:latest .
docker push ghcr.io/your-org/agentic-automation-runner:latest
cd ..
```

**注意**: GitHub Actions を使用している場合、`master` ブランチへの push で両方のイメージ（operator と runner）が自動的にビルドされます。

#### 4.4 イメージ名の更新

[specs/001-github-agent-automation/k8s/operator/service.yaml](../specs/001-github-agent-automation/k8s/operator/service.yaml) を編集:

```yaml
# Operator Deployment の image を更新
spec:
  template:
    spec:
      containers:
        - name: operator
          image: ghcr.io/your-org/agentic-automation-operator:latest  # 変更

# 環境変数 AGENT_RUNNER_IMAGE を更新
env:
  - name: AGENT_RUNNER_IMAGE
    value: "ghcr.io/your-org/agentic-automation-runner:latest"  # 変更
```

---

### ステップ 5: データベースのセットアップ

#### 5.1 データベースマイグレーションの実行

**ローカルから実行する場合:**

```bash
# .env に DATABASE_URL が設定されていることを確認
cat .env | grep DATABASE_URL

# マイグレーション実行
make migrate-up

# マイグレーションステータスの確認
make migrate-status
```

**Kubernetes Job で実行する場合:**

migration-job.yaml を作成:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: db-migration
  namespace: default
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: migration
          image: ghcr.io/your-org/operator:latest
          command:
            - /bin/sh
            - -c
            - |
              apk add --no-cache curl
              curl -fsSL https://github.com/pressly/goose/releases/download/v3.15.0/goose_linux_x86_64 -o /usr/local/bin/goose
              chmod +x /usr/local/bin/goose
              # Note: migrations は単一ファイル形式（-- +goose Up/Down セクション）を使用
              goose -dir /migrations mysql "$DATABASE_URL" up
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: operator-secrets
                  key: database-url
```

```bash
kubectl apply -f migration-job.yaml
kubectl logs -f job/db-migration
```

#### 5.2 データベース接続の確認

```bash
# MySQL クライアント Pod を起動
kubectl run -it --rm mysql-client --image=mysql:8.0 --restart=Never -- bash

# Pod 内でデータベースに接続
mysql -h YOUR_DB_HOST -u YOUR_DB_USER -p

# テーブルの確認
USE github_agent_automation;
SHOW TABLES;

# 期待されるテーブル:
# - agent_runs
# - audit_logs
# - blocker_graph_edges
# - ci_status
# - issues
# - operation_logs
# - pull_requests
# - review_feedback
# - goose_db_version

EXIT;
```

---

### ステップ 6: S3/MinIO のセットアップ

#### 6.1 バケットの作成（まだの場合）

**AWS S3:**

```bash
aws s3 mb s3://agentic-automation-sessions --region us-east-1
```

**MinIO:**

```bash
# MinIO にポートフォワード（まだの場合）
kubectl port-forward svc/minio 9000:9000 &

# バケット作成
S3_ENDPOINT=http://localhost:9000 \
S3_ACCESS_KEY_ID=minioadmin \
S3_SECRET_ACCESS_KEY=minioadmin \
S3_BUCKET=agent-sessions \
S3_REGION=us-east-1 \
./scripts/setup-minio.sh
```

#### 6.2 バケットの確認

**AWS S3:**

```bash
aws s3 ls s3://agentic-automation-sessions
```

**MinIO:**

```bash
# MinIO Client (mc) を使用
docker run --rm -it --entrypoint=/bin/sh minio/mc
mc alias set myminio http://YOUR_MINIO_ENDPOINT minioadmin minioadmin
mc ls myminio/agent-sessions
```

---

### ステップ 7: Kubernetes リソースのデプロイ

#### 7.1 RBAC の作成

```bash
kubectl apply -f k8s/rbac.yaml
```

確認:
```bash
kubectl get serviceaccount
kubectl get role
kubectl get rolebinding
```

#### 7.2 Operator のデプロイ

**方法 1: Makefile を使用**

```bash
make deploy-operator
```

**方法 2: 手動でデプロイ**

```bash
kubectl apply -f specs/001-github-agent-automation/k8s/operator/service.yaml
```

#### 7.3 デプロイの確認

```bash
# Pod の状態確認
kubectl get pods -l app=agent-operator

# 期待される出力:
# NAME                              READY   STATUS    RESTARTS   AGE
# agent-operator-xxxxxxxxxx-xxxxx   1/1     Running   0          30s
# agent-operator-xxxxxxxxxx-xxxxx   1/1     Running   0          30s

# ログの確認
kubectl logs -l app=agent-operator --tail=50

# Service の確認
kubectl get svc agent-operator

# Health check
kubectl run -it --rm curl --image=curlimages/curl --restart=Never -- \
  curl http://agent-operator.default.svc.cluster.local:3000/health
```

---

### ステップ 8: Ingress の設定

#### 8.1 TLS 証明書の準備

**方法 1: cert-manager を使用（推奨）**

cert-manager がインストールされている場合:

```bash
# ClusterIssuer の作成（Let's Encrypt）
cat <<EOF | kubectl apply -f -
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: your-email@example.com
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          class: nginx
EOF
```

**方法 2: 手動で証明書を作成**

```bash
# 自己署名証明書（開発用）
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout tls.key -out tls.crt \
  -subj "/CN=your-domain.com/O=your-org"

# Kubernetes Secret に登録
kubectl create secret tls agent-operator-tls \
  --cert=tls.crt \
  --key=tls.key
```

#### 8.2 Ingress の設定

[k8s/ingress.yaml](../k8s/ingress.yaml) を編集:

```yaml
spec:
  ingressClassName: nginx  # 使用する Ingress コントローラーに合わせる
  tls:
    - hosts:
        - your-domain.com  # あなたのドメインに変更
      secretName: agent-operator-tls
  rules:
    - host: your-domain.com  # あなたのドメインに変更
      http:
        paths:
          - path: /webhook
            pathType: Prefix
            backend:
              service:
                name: agent-operator
                port:
                  number: 3000
```

cert-manager を使用する場合、アノテーションを追加:

```yaml
metadata:
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
```

#### 8.3 Ingress のデプロイ

```bash
kubectl apply -f k8s/ingress.yaml
```

#### 8.4 Ingress の確認

```bash
# Ingress の状態確認
kubectl get ingress agent-operator-ingress

# External IP の取得
kubectl get ingress agent-operator-ingress \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'

# または hostname の取得（AWS ELB など）
kubectl get ingress agent-operator-ingress \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
```

#### 8.5 DNS の設定

取得した IP または hostname を DNS に登録:

**A レコード（IP の場合）:**
```
your-domain.com  A  <EXTERNAL_IP>
```

**CNAME レコード（hostname の場合）:**
```
your-domain.com  CNAME  <LOAD_BALANCER_HOSTNAME>
```

#### 8.6 HTTPS 接続の確認

```bash
# Health check
curl https://your-domain.com/health

# 期待されるレスポンス:
# {"status":"ok"}
```

---

### ステップ 9: 動作確認

#### 9.1 GitHub Webhook の設定確認

1. GitHub App の設定ページに移動
2. "Webhook" セクションを確認
3. Webhook URL を `https://your-domain.com/webhooks/github` に設定（まだの場合）
4. "Recent Deliveries" で webhook が正常に送信されているか確認

#### 9.2 テスト Issue の作成

1. GitHub App がインストールされているリポジトリで新しい Issue を作成
2. Issue に `/run-agent` のコメントを追加（説明文を含めても検知されます: 例 `/run-agent fix typo in README.md`）
   
   エージェント種別はコメントではなく Issue ラベルで制御します（`agent:claude-code` / `agent:cursor-agents`）。ラベルが無い場合は `AI_AGENT_DEFAULT_TYPE` が使用されます。コメント実行には Collaborator 以上の権限が必要で、Issue は open である必要があります。
3. Operator のログを確認:

```bash
kubectl logs -l app=agent-operator --tail=100 -f
```

期待されるログ:
```
INFO  Received webhook event  event=issue_comment action=created
INFO  Creating agent run      issue_id=123 repo=your-org/your-repo
INFO  Created Kubernetes Job  job_name=agent-run-xxx agent_run_id=456
```

#### 9.3 Agent Runner Job の確認

```bash
# Job の一覧
kubectl get jobs

# Agent Runner Pod のログ
kubectl logs -l job-name=agent-run-xxx --tail=100 -f
```

期待されるログ:
```
INFO  Starting agent runner   agent_run_id=456 agent_type=claude-code
INFO  Cloning repository      repo=your-org/your-repo
INFO  Running Claude Code     task=fix typo in README.md
INFO  Creating pull request   pr_number=789
INFO  Reporting to operator   status=success
INFO  Session uploaded to S3  key=sessions/456/final.tar.gz
```

#### 9.4 Pull Request の確認

1. GitHub リポジトリの Pull Requests タブを開く
2. Agent が作成した PR が表示されることを確認
3. PR の内容を確認

#### 9.5 データベースの確認

```bash
# MySQL クライアント Pod を起動
kubectl run -it --rm mysql-client --image=mysql:8.0 --restart=Never -- \
  mysql -h YOUR_DB_HOST -u YOUR_DB_USER -p

# agent_runs テーブルを確認
USE github_agent_automation;
SELECT id, issue_id, agent_type, status, created_at FROM agent_runs ORDER BY id DESC LIMIT 10;

# pull_requests テーブルを確認
SELECT id, issue_id, pr_number, status, created_at FROM pull_requests ORDER BY id DESC LIMIT 10;
```

#### 9.6 S3/MinIO の確認

**AWS S3:**

```bash
aws s3 ls s3://agentic-automation-sessions/sessions/ --recursive
```

**MinIO:**

```bash
kubectl port-forward svc/minio-console 9001:9001
# ブラウザで http://localhost:9001 を開く
# ログイン: minioadmin / minioadmin
# agent-sessions バケット > sessions/ フォルダを確認
```

---

## 運用

### ログの確認

```bash
# Operator のログ
kubectl logs -l app=agent-operator --tail=100 -f

# 特定の Agent Runner Job のログ
kubectl logs -l job-name=agent-run-xxx --tail=100 -f

# 過去の Agent Runner Job のログを検索
kubectl get jobs --sort-by=.metadata.creationTimestamp
kubectl logs job/agent-run-xxx
```

### スケーリング

```bash
# Operator のレプリカ数を変更
kubectl scale deployment agent-operator --replicas=3

# 自動スケーリングの設定（HPA）
kubectl autoscale deployment agent-operator \
  --cpu-percent=70 \
  --min=2 \
  --max=5
```

### アップデート

```bash
# 新しい Docker イメージをビルド
make docker-build-push-operator

# Deployment を更新（ローリングアップデート）
kubectl set image deployment/agent-operator \
  operator=ghcr.io/your-org/operator:sha-xxxxxxx

# ロールアウトステータスの確認
kubectl rollout status deployment/agent-operator

# ロールバック（問題がある場合）
kubectl rollout undo deployment/agent-operator
```

### バックアップ

#### データベースのバックアップ

**AWS RDS の場合:**

```bash
# スナップショットの作成
aws rds create-db-snapshot \
  --db-instance-identifier agentic-automation-db \
  --db-snapshot-identifier agentic-automation-backup-$(date +%Y%m%d)

# 自動バックアップの設定確認
aws rds describe-db-instances \
  --db-instance-identifier agentic-automation-db \
  --query 'DBInstances[0].BackupRetentionPeriod'
```

**Kubernetes 内の MySQL の場合:**

```bash
# mysqldump でバックアップ
kubectl exec -it mysql-xxx -- \
  mysqldump -u root -p github_agent_automation > backup-$(date +%Y%m%d).sql
```

#### S3 のバックアップ

**AWS S3 の場合:**

```bash
# バージョニングを有効化（削除保護）
aws s3api put-bucket-versioning \
  --bucket agentic-automation-sessions \
  --versioning-configuration Status=Enabled

# レプリケーション設定（別リージョンへ）
aws s3api put-bucket-replication \
  --bucket agentic-automation-sessions \
  --replication-configuration file://replication.json
```

### モニタリング

#### Prometheus と Grafana の統合（推奨）

1. Prometheus Operator をインストール
2. ServiceMonitor を作成:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: agent-operator
  namespace: default
spec:
  selector:
    matchLabels:
      app: agent-operator
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
```

3. Grafana でダッシュボードを作成

#### アラートの設定

PrometheusRule の例:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: agent-operator-alerts
  namespace: default
spec:
  groups:
    - name: agent-operator
      interval: 30s
      rules:
        - alert: OperatorDown
          expr: up{job="agent-operator"} == 0
          for: 5m
          labels:
            severity: critical
          annotations:
            summary: "Operator is down"
        - alert: HighAgentFailureRate
          expr: rate(agent_runs_failed_total[5m]) > 0.5
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: "High agent failure rate detected"
```

---

## トラブルシューティング

### Operator が起動しない

**症状**: Operator Pod が `CrashLoopBackOff` または `Error` 状態

**原因と対処法**:

1. **Secrets が存在しない**

```bash
# Secrets の確認
kubectl get secrets

# 存在しない場合は作成
# ステップ 3 を参照
```

2. **データベースに接続できない**

```bash
# Operator のログを確認
kubectl logs -l app=agent-operator --tail=50

# エラー例: "dial tcp: lookup mysql.default.svc.cluster.local: no such host"
# → MySQL がデプロイされていない、または Service 名が違う

# MySQL の確認
kubectl get pods -l app=mysql
kubectl get svc mysql
```

3. **イメージが pull できない**

```bash
# Pod のイベントを確認
kubectl describe pod agent-operator-xxx

# エラー例: "Failed to pull image ... unauthorized"
# → Docker レジストリの認証情報が必要

# ImagePullSecret の作成
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=YOUR_USERNAME \
  --docker-password=YOUR_TOKEN

# Deployment に imagePullSecrets を追加
kubectl patch deployment agent-operator -p '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"ghcr-secret"}]}}}}'
```

### Webhook が受信されない

**症状**: GitHub で Issue にコメントしても何も起こらない

**原因と対処法**:

1. **Ingress が正しく設定されていない**

```bash
# Ingress の確認
kubectl get ingress agent-operator-ingress
kubectl describe ingress agent-operator-ingress

# External IP が割り当てられているか確認
kubectl get ingress agent-operator-ingress \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

2. **DNS が正しく設定されていない**

```bash
# DNS の確認
nslookup your-domain.com
dig your-domain.com

# Ingress の External IP と一致するか確認
```

3. **TLS 証明書の問題**

```bash
# 証明書の確認
echo | openssl s_client -connect your-domain.com:443 -servername your-domain.com 2>/dev/null | openssl x509 -noout -dates

# cert-manager の確認（使用している場合）
kubectl get certificate
kubectl describe certificate agent-operator-tls
```

4. **GitHub Webhook の設定確認**

- GitHub App の設定ページで "Recent Deliveries" を確認
- レスポンスコードが 200 でない場合、エラーメッセージを確認
- Webhook URL が正しいか確認（`https://your-domain.com/webhooks/github`）

5. **Webhook Secret が一致しない**

```bash
# Operator のログでエラーを確認
kubectl logs -l app=agent-operator --tail=100 | grep -i "signature"

# エラー例: "invalid webhook signature"
# → GITHUB_WEBHOOK_SECRET が GitHub App の設定と一致していない

# Secret を更新
kubectl delete secret operator-secrets
# ステップ 3 の手順で再作成
```

### Agent Runner Job が失敗する

**症状**: Job が `Failed` 状態で終了する

**原因と対処法**:

1. **リソース不足**

```bash
# Node のリソース状況を確認
kubectl top nodes
kubectl describe nodes

# Pod のリソースリクエストを下げる、またはノードを追加
```

2. **GitHub App の権限不足**

```bash
# Agent Runner のログを確認
kubectl logs -l job-name=agent-run-xxx --tail=100

# エラー例: "403 Forbidden"
# → GitHub App に必要なリポジトリ権限が付与されていない
# 必要な権限例: Contents:RW, Issues:RW, Pull Requests:RW, Metadata:R
```

3. **AI API キーの問題**

```bash
# エラー例: "401 Unauthorized" (Claude API)
# → ANTHROPIC_API_KEY が無効または期限切れ

# Secret を更新
kubectl delete secret anthropic-api-key
kubectl create secret generic anthropic-api-key \
  --from-literal=api-key="$ANTHROPIC_API_KEY"

# Pod を再起動して適用
```

4. **S3 接続の問題**

```bash
# エラー例: "failed to upload session to S3"
# → S3 認証情報またはエンドポイントが間違っている

# S3 設定の確認
kubectl get secret s3-credentials -o yaml
kubectl get deployment agent-operator -o yaml | grep -A 10 S3_

# MinIO の場合、S3_USE_PATH_STYLE=true が必要
```

### データベースマイグレーションが失敗する

**症状**: `make migrate-up` がエラーで終了する

**原因と対処法**:

1. **データベースに接続できない**

```bash
# DATABASE_URL の確認
echo $DATABASE_URL

# 接続テスト
mysql -h YOUR_DB_HOST -u YOUR_DB_USER -p
```

2. **既にマイグレーションが適用されている**

```bash
# マイグレーションステータスの確認
make migrate-status

# すべてのマイグレーションが "Applied" になっていれば正常
```

3. **権限不足**

```bash
# エラー例: "Access denied for user"
# → データベースユーザーに必要な権限がない

# MySQL で権限を付与
GRANT ALL PRIVILEGES ON github_agent_automation.* TO 'agent_user'@'%';
FLUSH PRIVILEGES;
```

### パフォーマンス問題

**症状**: Agent の実行が遅い、タイムアウトする

**原因と対処法**:

1. **Agent Runner Pod のリソース不足**

```bash
# Pod のリソース使用状況を確認
kubectl top pods -l job-name=agent-run-xxx

# リソースリミットを引き上げる
# k8s/pod-template.yaml を編集:
resources:
  requests:
    cpu: "1000m"
    memory: "1Gi"
  limits:
    cpu: "4000m"
    memory: "4Gi"
```

2. **並行実行数の調整**

[specs/001-github-agent-automation/k8s/operator/service.yaml](../specs/001-github-agent-automation/k8s/operator/service.yaml) の環境変数を変更:

```yaml
env:
  - name: AI_AGENT_MAX_CONCURRENT_PODS
    value: "20"  # デフォルト: 10
```

3. **タイムアウトの延長**

```yaml
env:
  - name: AI_AGENT_TIMEOUT_MINUTES
    value: "60"  # デフォルト: 30
```

---

## 付録

### A. 環境変数一覧

#### Operator Service

| 環境変数 | 必須 | デフォルト | 説明 |
|---------|------|-----------|------|
| `DATABASE_URL` | はい | - | MySQL 接続文字列 |
| `GITHUB_APP_ID` | はい | - | GitHub App ID |
| `GITHUB_PRIVATE_KEY` | はい | - | GitHub App 秘密鍵（PEM、1行） |
| `GITHUB_WEBHOOK_SECRET` | はい | - | Webhook 署名検証用シークレット |
| `OPERATOR_API_TOKEN` | はい | - | Agent Runner 認証用トークン |
| `AGENT_RUNNER_IMAGE` | はい | - | Agent Runner Docker イメージ |
| `OPERATOR_SERVICE_NAME` | いいえ | `agent-operator` | Operator サービス名 |
| `OPERATOR_SERVICE_PORT` | いいえ | `3000` | Operator サービスポート |
| `S3_ENDPOINT` | はい | - | S3 API エンドポイント |
| `S3_REGION` | はい | - | S3 リージョン |
| `S3_BUCKET` | はい | - | S3 バケット名 |
| `S3_ACCESS_KEY_ID` | はい | - | S3 アクセスキー |
| `S3_SECRET_ACCESS_KEY` | はい | - | S3 シークレットキー |
| `PORT` | いいえ | `3000` | サーバーポート |
| `ENV` | いいえ | `production` | 環境 (development/production) |
| `LOG_LEVEL` | いいえ | `info` | ログレベル (debug/info/warn/error) |
| `CODEX_BOT_USERNAME` | いいえ | `codex-bot` | Codex bot GitHub ユーザー名 |
| `DISCORD_WEBHOOK_URL` | いいえ | - | Discord webhook URL（通知用） |
| `AI_AGENT_TIMEOUT_MINUTES` | いいえ | `30` | Pod 実行タイムアウト（分） |
| `AI_AGENT_MAX_CONCURRENT_PODS` | いいえ | `10` | 最大並行実行数 |
| `AI_AGENT_DEFAULT_TYPE` | いいえ | `claude-code` | デフォルトエージェント種別 |
| `S3_USE_PATH_STYLE` | いいえ | `true` | パススタイル URL 使用（MinIO: true / AWS: false） |
| `S3_MAX_RETRIES` | いいえ | `5` | S3 リトライ回数 |
| `S3_RETRY_INITIAL_INTERVAL` | いいえ | `1s` | 初期バックオフ間隔（現状未使用/将来拡張） |
| `S3_CREDENTIALS_SECRET` | いいえ | `s3-credentials` | S3資格情報Secret名 |

#### Agent Runner Pod

| 環境変数 | 必須 | デフォルト | 説明 |
|---------|------|-----------|------|
| `KUBERNETES_NAMESPACE` | はい | - | Kubernetes namespace（Downward API 経由で自動注入） |
| `OPERATOR_SERVICE_NAME` | はい | - | Operator サービス名 |
| `OPERATOR_SERVICE_PORT` | はい | - | Operator サービスポート |
| `OPERATOR_API_TOKEN` | はい | - | Bearer トークン |
| `AGENT_RUN_ID` | はい | - | AgentRun レコード ID |
| `AGENT_TYPE` | はい | - | エージェント種別（claude-code/cursor-agent） |
| `ANTHROPIC_API_KEY` | 条件付き | - | Claude API キー（AGENT_TYPE=claude-code の場合） |
| `CURSOR_API_KEY` | 条件付き | - | Cursor API キー（AGENT_TYPE=cursor-agent の場合） |
| `RETRY_COUNT` | いいえ | `0` | 現在のリトライ回数 |
| `WORKSPACE_DIR` | いいえ | `/workspace` | 作業ディレクトリ |
| `S3_ENDPOINT` | はい | - | S3 API エンドポイント |
| `S3_REGION` | はい | - | S3 リージョン |
| `S3_BUCKET` | はい | - | S3 バケット名 |
| `S3_ACCESS_KEY_ID` | はい | - | S3 アクセスキー（Secret参照） |
| `S3_SECRET_ACCESS_KEY` | はい | - | S3 シークレットキー（Secret参照） |
| `S3_USE_PATH_STYLE` | いいえ | `true` | パススタイル URL 使用（MinIO: true / AWS: false） |
| `S3_MAX_RETRIES` | いいえ | `5` | S3 リトライ回数 |

**注**: Operator API URL は、`KUBERNETES_NAMESPACE`、`OPERATOR_SERVICE_NAME`、`OPERATOR_SERVICE_PORT` から自動構築されます：`http://{OPERATOR_SERVICE_NAME}.{KUBERNETES_NAMESPACE}.svc.cluster.local:{OPERATOR_SERVICE_PORT}`

### B. Makefile コマンド一覧

| コマンド | 説明 |
|---------|------|
| `make help` | ヘルプメッセージを表示 |
| `make deps` | Go 依存関係をダウンロード |
| `make build` | バイナリをビルド |
| `make test` | テストを実行 |
| `make vet` | go vet を実行 |
| `make migrate-up` | データベースマイグレーションを実行 |
| `make migrate-down` | マイグレーションをロールバック |
| `make migrate-status` | マイグレーションステータスを表示 |
| `make run` | Operator を開発モードで実行 |
| `make clean` | ビルド成果物をクリーンアップ |
| `make docker-build` | Agent Runner Docker イメージをビルド |
| `make docker-push` | Agent Runner イメージを push |
| `make docker-build-push` | Agent Runner をビルドして push |
| `make docker-build-operator` | Operator Docker イメージをビルド |
| `make docker-push-operator` | Operator イメージを push |
| `make docker-build-push-operator` | Operator をビルドして push |
| `make k8s-secrets` | Kubernetes Secrets 作成コマンドを表示 |
| `make deploy-infra` | MySQL と MinIO をデプロイ（開発用） |
| `make deploy-operator` | Operator をデプロイ |
| `make deploy-all` | すべてのコンポーネントをデプロイ |

### C. 推奨セキュリティ設定

#### Network Policies

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: agent-operator-netpol
  namespace: default
spec:
  podSelector:
    matchLabels:
      app: agent-operator
  policyTypes:
    - Ingress
    - Egress
  ingress:
    # Ingress からのトラフィックのみ許可
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
      ports:
        - protocol: TCP
          port: 3000
    # Agent Runner からの API コールを許可
    - from:
        - podSelector:
            matchLabels:
              component: agent-runner
      ports:
        - protocol: TCP
          port: 3000
  egress:
    # MySQL への接続を許可
    - to:
        - podSelector:
            matchLabels:
              app: mysql
      ports:
        - protocol: TCP
          port: 3306
    # S3/MinIO への接続を許可
    - to:
        - podSelector:
            matchLabels:
              app: minio
      ports:
        - protocol: TCP
          port: 9000
    # Kubernetes API への接続を許可
    - to:
        - namespaceSelector:
            matchLabels:
              name: kube-system
      ports:
        - protocol: TCP
          port: 443
    # 外部 API（GitHub, Discord）への接続を許可
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: 443
    # DNS を許可
    - to:
        - namespaceSelector:
            matchLabels:
              name: kube-system
      ports:
        - protocol: UDP
          port: 53
```

#### Pod Security Standards

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
```

#### RBAC 最小権限原則

現在の RBAC 設定を確認:

```bash
kubectl get role agent-operator-role -o yaml
kubectl get rolebinding agent-operator-rolebinding -o yaml
```

必要な権限のみを付与:

```yaml
# operator ServiceAccount
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "get", "list", "watch", "delete"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]

# agent-automation ServiceAccount（Agent Runner 用）
rules: []  # Kubernetes API アクセス不要
```

### D. 関連ドキュメント

- [Quickstart Guide](../specs/001-github-agent-automation/quickstart.md) - ローカル開発環境のセットアップ
- [Feature Specification](../specs/001-github-agent-automation/spec.md) - 機能仕様
- [Implementation Plan](../specs/001-github-agent-automation/plan.md) - 実装計画
- [Data Model](../specs/001-github-agent-automation/data-model.md) - データベーススキーマ
- [Internal API](../specs/001-github-agent-automation/contracts/internal-api.yaml) - REST API 仕様
- [AI Agent Execution](../specs/001-github-agent-automation/contracts/ai-agent-execution.md) - Agent Runner 仕様
- [Discord Notifications](../specs/001-github-agent-automation/contracts/discord-notifications.md) - 通知フォーマット

### E. サポート

問題が発生した場合:

1. **ログを確認**: `kubectl logs` コマンドでログを収集
2. **Issue を作成**: GitHub リポジトリで Issue を作成し、ログを添付
3. **コミュニティに質問**: Discord または Slack チャンネルで質問

---

**最終更新**: 2025-11-03
**ドキュメントバージョン**: 1.0.0
