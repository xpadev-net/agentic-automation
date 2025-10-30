# data-model.md: GitHub Agent Automation データモデル

## エンティティ定義・属性

### Issue
- id: INT (PK)
- repo: STRING
- number: INT
- title: STRING
- body: TEXT
- labels: ARRAY<STRING>
- comments: ARRAY<Comment>
- created_at: DATETIME
- updated_at: DATETIME

### PullRequest
- id: INT (PK)
- repo: STRING
- number: INT
- branch: STRING
- status: ENUM(open/closed/merged)
- merged_flag: BOOL
- reviews: ARRAY<ReviewFeedback>
- created_at: DATETIME
- updated_at: DATETIME

### AgentRun
- id: INT (PK)
- idempotency_key: STRING (UK)
- state: ENUM(queued/started/succeeded/failed)
- input: JSON
- output: JSON
- pr_id: INT (PullRequestへのFK, NULL可)
- retry_count: INT (最大50)
- created_at: DATETIME
- updated_at: DATETIME

### ReviewFeedback
- id: INT (PK)
- pr_id: INT (PullRequestへのFK)
- source: ENUM(Codex)
- content: TEXT
- status: ENUM(requested/received/commented)
- created_at: DATETIME
- updated_at: DATETIME

### BlockerGraphEdges
- task_id: INT (PK, Issue.id)
- depends_on_task_id: INT (PK, Issue.id)

## 状態遷移・バリデーション要件
- AgentRun: state = [queued → started → (succeeded｜failed)]
  - 最大50回retriesで強制failed判定
- PullRequest, Issue間のblocking依存はBlockerGraphEdges管理
- PRのapprove条件: Codexボット「Codex Review: Didn't find any major issues.」コメントで可決
- 全リレーションは冪等性考慮（GitHub Delivery ID, idempotency_key）

## 制約情報（MySQL想定）
- AgentRun: PK(id), UNIQUE(idempotency_key), pr_idはNULL可・外部キー
- ReviewFeedback: source=Codex限定
- BlockerGraphEdges: 主キー(task_id, depends_on_task_id), 双方Issue.id外部キー

## 用語補足
- 冪等化: X-GitHub-Deliveryヘッダでイベント1度のみ処理
- 再試行: 失敗時は指数バックオフ付き最大50回再実行
