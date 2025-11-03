# data-model.md: GitHub Agent Automation データモデル

**Last Updated**: 2025-10-31
**Database**: MySQL 8.0+ with GORM (Go)
**Schema Location**: GORM models in `internal/models`, SQL migrations in `migrations/` (goose)

## エンティティ定義・属性

### Issue
- id: INT (PK, auto-increment)
- repo: STRING (e.g., "owner/repo-name")
- number: INT (GitHub Issue number)
- title: STRING
- body: TEXT (nullable)
- labels: JSON (array of label names)
- state: ENUM(open/closed)
- created_at: DATETIME
- updated_at: DATETIME

**Relationships**:
- has many AgentRun (one Issue can trigger multiple runs)
- has many BlockerGraphEdges (as task or dependent task)

**Indexes**:
- UNIQUE(repo, number) - prevent duplicate Issue records

### PullRequest
- id: INT (PK, auto-increment)
- repo: STRING
- number: INT (GitHub PR number)
- issue_id: INT (FK to Issue.id, nullable) - **NEW**: tracks which Issue triggered this PR
- branch: STRING (e.g., "feature/issue-123")
- base_branch: STRING (e.g., "main", default: "main")
- status: ENUM(open/closed/merged)
- mergeable: BOOL (nullable) - **NEW**: GitHub mergeable status
- created_at: DATETIME
- updated_at: DATETIME

**Relationships**:
- has many ReviewFeedback
- has many CIStatus
- belongs to Issue (optional, via issue_id)
- has many AgentRun (one PR can have multiple retry attempts)

**Indexes**:
- UNIQUE(repo, number)
- INDEX(issue_id) - for querying PRs by Issue

### AgentRun
- id: INT (PK, auto-increment)
- idempotency_key: STRING (UK) - **X-GitHub-Delivery header for deduplication**
- issue_id: INT (FK to Issue.id) - **NEW**: which Issue is being processed
- pr_id: INT (FK to PullRequest.id, nullable) - PR created by this run
- state: ENUM(queued/started/succeeded/failed)
- agent_type: ENUM(claude-code/cursor-agents) - **NEW**: Which AI agent was used
- input: JSON (Issue context, comments, dependencies, **manifest config**)
- output: JSON (AI agent response, changes made)
- retry_count: INT (default: 0, max: 50)
- error_message: TEXT (nullable) - **NEW**: last error if state=failed
- commit_sha: STRING (nullable) - **NEW**: Git commit SHA if succeeded
- s3_session_key: STRING (nullable) - **NEW**: S3 key for session data (e.g., "sessions/123/session.tar.gz")
- session_saved_at: DATETIME (nullable) - **NEW**: Timestamp when session was last saved to S3
- started_at: DATETIME (nullable)
- completed_at: DATETIME (nullable)
- created_at: DATETIME
- updated_at: DATETIME

**Input JSON Structure**:
```json
{
  "issue_title": "Fix authentication bug",
  "issue_body": "Users cannot login...",
  "comments": [...],
  "dependencies": [...],
  "manifest_config": {
    "version": "1.0",
    "hooks": {
      "pre": [...],
      "post": [...]
    },
    "validation": [...]
  }
}
```

**Note**: The `manifest_config` field stores the parsed `.agent-config.yaml` content for audit trail and debugging. If no manifest exists in the repository, this field is null.

**State Transition**:
- queued → started (on execution begin)
- started → succeeded (on PR creation + approval)
- started → failed (on max retries or unrecoverable error)
- No transition from succeeded/failed (terminal states)

**Relationships**:
- belongs to Issue (via issue_id)
- belongs to PullRequest (optional, via pr_id)

**Indexes**:
- UNIQUE(idempotency_key) - enforce idempotency
- INDEX(state) - for querying active runs
- INDEX(issue_id) - for finding runs by Issue
- INDEX(pr_id) - for finding runs by PR

**Validation**:
- retry_count MUST be <= 50
- state=succeeded requires pr_id to be non-null

### ReviewFeedback
- id: INT (PK, auto-increment)
- pr_id: INT (FK to PullRequest.id)
- source: ENUM(Codex) - extensible for future review sources
- content: TEXT (review comments/suggestions)
- status: ENUM(requested/received/commented)
- approval_detected: BOOL (default: false) - **NEW**: true if "didn't find major issues" detected
- github_comment_id: INT (nullable) - **NEW**: GitHub comment ID for tracking
- created_at: DATETIME
- updated_at: DATETIME

**Relationships**:
- belongs to PullRequest (via pr_id)

**Indexes**:
- INDEX(pr_id) - for querying reviews by PR
- INDEX(approval_detected) - for finding approved PRs

### CIStatus

**NEW ENTITY** - Tracks GitHub Check Suite results for PR validation

- id: INT (PK, auto-increment)
- pr_id: INT (FK to PullRequest.id)
- check_suite_id: STRING (GitHub check suite ID)
- check_run_id: STRING (nullable, specific check run ID)
- name: STRING (e.g., "build", "test", "lint")
- status: ENUM(queued/in_progress/completed)
- conclusion: ENUM(success/failure/cancelled/skipped/neutral) (nullable until completed)
- logs: TEXT (nullable) - **CI failure logs for AI retry context**
- logs_url: STRING (nullable) - **GitHub logs URL**
- started_at: DATETIME (nullable)
- completed_at: DATETIME (nullable)
- created_at: DATETIME
- updated_at: DATETIME

**Relationships**:
- belongs to PullRequest (via pr_id)

**Indexes**:
- UNIQUE(pr_id, check_suite_id, check_run_id) - prevent duplicate CI records
- INDEX(pr_id, status) - for querying active CI runs
- INDEX(conclusion) - for finding failed checks

**Business Rules**:
- A PR is "CI passing" when ALL CIStatus records for latest commit have conclusion=success
- Failed checks (conclusion=failure) trigger AI retry with logs as context

### BlockerGraphEdges

Adjacency list for Issue dependency graph (directed graph)

- task_id: INT (FK to Issue.id, PK composite)
- depends_on_task_id: INT (FK to Issue.id, PK composite)
- created_at: DATETIME

**Relationships**:
- task_id references Issue (the blocked task)
- depends_on_task_id references Issue (the blocking task)

**Indexes**:
- PRIMARY KEY(task_id, depends_on_task_id)
- INDEX(depends_on_task_id) - for finding all tasks blocked by a specific Issue

**Business Rules**:
- When Issue X closes, query `depends_on_task_id = X` to find blocked tasks
- Blocked task Y becomes unblocked when ALL its dependencies are closed
- Cycle detection via DFS before starting execution

**Parsing**: Extract from Issue body/comments using regex:
- "blocked by #123" or "depends on #456"
- "blocking #789" (inverse relationship)

### AuditLog

**NEW ENTITY** - Immutable audit trail for all system operations

- id: INT (PK, auto-increment)
- event_type: STRING (e.g., "webhook.received", "agent.started", "pr.created", "review.received", "ci.failed")
- actor: STRING (GitHub username or "system")
- resource_type: STRING (e.g., "Issue", "PullRequest", "AgentRun")
- resource_id: INT (ID of the affected resource)
- payload: JSON (full event payload for debugging)
- idempotency_key: STRING (nullable) - **X-GitHub-Delivery if from webhook**
- ip_address: STRING (nullable) - **for webhook source tracking**
- user_agent: STRING (nullable) - **webhook User-Agent header**
- created_at: DATETIME (immutable, never updated)

**Relationships**:
- None (standalone audit log, referenced by resource_type + resource_id)

**Indexes**:
- INDEX(event_type, created_at DESC) - for filtering by event type
- INDEX(resource_type, resource_id) - for querying history by resource
- INDEX(actor) - for user activity tracking
- INDEX(idempotency_key) - for webhook deduplication audit

**Business Rules**:
- Records are NEVER updated or deleted (append-only log)
- Retention policy: keep for 90 days minimum (configurable)
- Critical events to log:
  - All webhook receptions
  - Agent execution start/stop
  - PR creation/merge
  - Review requests/results
  - CI status changes
  - Failures and errors
  - Retry attempts

### OperationLog

Immutable per-operation log to enforce operation-level idempotency and trace actions tied to an AgentRun.

- id: INT (PK, auto-increment)
- run_id: INT (FK to AgentRun.id)
- operation_type: ENUM(pr-create, post-comment, request-review, merge)
- operation_id: STRING (UK) - client-supplied idempotency key per operation
- status: ENUM(pending, succeeded, failed)
- created_at: DATETIME

**Relationships**:
- belongs to AgentRun (via run_id)

**Indexes**:
- UNIQUE(operation_id)
- INDEX(run_id, operation_type)

**Business Rules**:
- Each external side-effecting action MUST record an OperationLog
- Duplicate operation_id must not create a second side-effect (idempotent)

### BranchLock

**NEW ENTITY** - Enforces per-branch concurrency = 1 (FR-020)

- branch_name: STRING (PK, max 255 chars) - Git branch name
- agent_run_id: INT (FK to AgentRun.id) - Current executing AgentRun
- locked_at: DATETIME - Lock acquisition timestamp

**Relationships**:
- belongs to AgentRun (via agent_run_id)

**Indexes**:
- PRIMARY KEY(branch_name)
- FK to AgentRun(id) with ON DELETE CASCADE

**Business Rules**:
- Only one AgentRun can hold a lock on a specific branch at any time
- Lock is automatically released when AgentRun completes (succeeded/failed state)
- Lock is also released when AgentRun is deleted (CASCADE)
- Before creating Kubernetes Job, system MUST acquire lock:
  ```sql
  INSERT INTO branch_locks (branch_name, agent_run_id, locked_at)
  VALUES ('feature/issue-42', 123, NOW())
  ON DUPLICATE KEY UPDATE branch_name = branch_name; -- Fail if exists
  ```
- If INSERT fails due to duplicate key → branch is locked by another run
- Action on lock failure: Return error to user, do not queue AgentRun
- Lock release on completion:
  ```sql
  DELETE FROM branch_locks WHERE agent_run_id = 123;
  ```

**Deadlock Prevention**:
- Locks are always acquired in branch name alphabetical order
- Locks have no timeout (released only on completion or failure)
- If Pod crashes, lock persists until AgentRun state transitions to failed (via timeout detection)

## 状態遷移・バリデーション要件

### AgentRun State Machine

```
queued ──[start execution]──> started
started ──[PR created & approved & CI passed]──> succeeded
started ──[retry_count >= 50 OR unrecoverable error]──> failed
```

**Validation**:
- retry_count incremented on each AI retry (CI failure or review feedback)
- Max 50 retries enforced by RetryOrchestrator
- state=succeeded requires pr_id to be non-null AND PR.status=merged
- Automatic Discord + GitHub notification on state transition to failed

### Retry Logic Details

**Retry Interval**: Event-driven (no artificial delay between retries)

**Retry Triggers**:
1. **Agent execution failed** (agent-runner reports failure)
   - Extract error logs from agent-runner report
   - Pass error context to next AI attempt
   - Increment retry_count +1

2. **CI failure detected** (via check_suite webhook)
   - Extract CI logs from GitHub API
   - Parse failure reasons (test failures, build errors)
   - Aggregate with review feedback if any
   - Increment retry_count +1

3. **Review feedback received** (Codex review without approval)
   - Extract review comments from GitHub PR
   - Combine with CI status
   - Pass aggregated feedback to AI
   - Increment retry_count +1

**Same Error Detection**:
- Hash of (agent error + CI logs + review feedback)
- If hash matches previous 3 consecutive attempts: notify user, pause auto-retry
- User can manually reset via CLI: `agent-automation retry <run-id> --reset-count`

**Retry Context Aggregation**:
```json
{
  "previous_attempts": [
    {
      "attempt": 1,
      "agent_error": "Lint failed: missing semicolon",
      "ci_logs": "Error at line 42: Expected ';'",
      "review_comments": ["Missing type annotation for variable 'foo'"]
    },
    {
      "attempt": 2,
      "agent_error": null,
      "ci_logs": "Tests failed: parser_test.ts:12",
      "review_comments": []
    }
  ],
  "total_retries": 2,
  "max_retries": 50
}
```

**50 Retries Reached**:
- Set AgentRun.state = `failed`
- Post GitHub Issue comment: "❌ Maximum retry limit (50) reached. Manual intervention required."
- Send Discord notification (embed with error history)
- Stop auto-retry loop
- Manual re-trigger available via CLI with `--reset-count` flag

### PullRequest Merge Conditions (Definition of Done)

A PR can be auto-merged when ALL of the following are true:

1. **CI Success**: All CIStatus records have conclusion=success
2. **Codex Approval**: At least one ReviewFeedback has approval_detected=true
3. **No Conflicts**: PullRequest.mergeable=true (from GitHub API)
4. **Status**: PullRequest.status=open

**Checking Strategy**:
- Re-evaluate on every check_suite.completed webhook
- Re-evaluate on every Codex review comment webhook
- Use MergeConditionChecker service to validate all conditions
- Call AutoMergeService if conditions met

### BlockerGraph Dependencies

**Dependency Resolution**:
- Task Y is blocked if EXISTS BlockerGraphEdges WHERE task_id=Y AND depends_on_task_id IN (open Issues)
- Task Y is unblocked when ALL dependencies have Issue.state=closed
- On Issue close: query BlockerGraphEdges to find newly unblocked tasks
- Trigger new AgentRun for each unblocked task

**Cycle Detection**:
- Run DFS before starting execution
- If cycle detected: log warning, add to AuditLog, notify in GitHub comment
- Do NOT block execution (spec allows human resolution)

## 制約情報（MySQL/GORM）

### Unique Constraints
- Issue: UNIQUE(repo, number)
- PullRequest: UNIQUE(repo, number)
- AgentRun: UNIQUE(idempotency_key)
- CIStatus: UNIQUE(pr_id, check_suite_id, check_run_id)
- BlockerGraphEdges: PRIMARY KEY(task_id, depends_on_task_id)

### Foreign Key Constraints
- AgentRun.issue_id → Issue.id (ON DELETE CASCADE)
- AgentRun.pr_id → PullRequest.id (ON DELETE SET NULL)
- PullRequest.issue_id → Issue.id (ON DELETE SET NULL)
- ReviewFeedback.pr_id → PullRequest.id (ON DELETE CASCADE)
- CIStatus.pr_id → PullRequest.id (ON DELETE CASCADE)
- BlockerGraphEdges.task_id → Issue.id (ON DELETE CASCADE)
- BlockerGraphEdges.depends_on_task_id → Issue.id (ON DELETE CASCADE)

### Enum Constraints
- AgentRun.state: queued, started, succeeded, failed
- PullRequest.status: open, closed, merged
- ReviewFeedback.source: Codex (extensible)
- ReviewFeedback.status: requested, received, commented
- CIStatus.status: queued, in_progress, completed
- CIStatus.conclusion: success, failure, cancelled, skipped, neutral

### Check Constraints (application-level)
- AgentRun.retry_count <= 50
- AgentRun.state=succeeded implies pr_id IS NOT NULL
- CIStatus.status=completed implies conclusion IS NOT NULL

## 用語補足

### Idempotency
- **idempotency_key**: X-GitHub-Delivery HTTP header value
- Stored in AgentRun.idempotency_key (unique constraint)
- Prevents duplicate processing of same webhook event
- Also logged in AuditLog for correlation

### Retry Semantics
- **API Retry (FR-016)**: External API call failures (GitHub, Codex) → exponential backoff, max 3-5 attempts
- **AI Retry (FR-014)**: CI failure or review feedback → increment AgentRun.retry_count, max 50 attempts

### Approval Detection
- ReviewFeedback.approval_detected set to true when content matches:
  - Regex: `/didn't find.*major issues/i`
  - Or exact: "Codex Review: Didn't find any major issues."
- Used by MergeConditionChecker to validate DoD

## GORM Model Hints (Go)

```go
// internal/models/issue.go
type Issue struct {
    ID        int       `gorm:"primaryKey;autoIncrement"`
    Repo      string    `gorm:"size:255;index:idx_issue_repo_number,unique"`
    Number    int       `gorm:"index:idx_issue_repo_number,unique"`
    Title     string    `gorm:"size:512"`
    Body      *string   `gorm:"type:text"`
    Labels    datatypes.JSON `gorm:"type:json"`
    State     string    `gorm:"type:enum('open','closed');default:'open'"`
    CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
    UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`

    AgentRuns    []AgentRun
    PullRequests []PullRequest
}

// internal/models/agent_run.go
type AgentRun struct {
    ID             int            `gorm:"primaryKey;autoIncrement"`
    IdempotencyKey string         `gorm:"column:idempotency_key;uniqueIndex;size:191"`
    IssueID        int            `gorm:"column:issue_id;index"`
    PRID           *int           `gorm:"column:pr_id;index"`
    State          string         `gorm:"type:enum('queued','started','succeeded','failed');index"`
    AgentType      string         `gorm:"column:agent_type;type:enum('claude-code','cursor-agents');index"`
    Input          datatypes.JSON `gorm:"type:json"`
    Output         datatypes.JSON `gorm:"type:json"`
    RetryCount     int            `gorm:"column:retry_count;default:0"`
    ErrorMessage   *string        `gorm:"column:error_message;type:text"`
    CommitSHA      *string        `gorm:"column:commit_sha;size:191"`
    S3SessionKey   *string        `gorm:"column:s3_session_key;size:512"`
    SessionSavedAt *time.Time     `gorm:"column:session_saved_at"`
    StartedAt      *time.Time     `gorm:"column:started_at"`
    CompletedAt    *time.Time     `gorm:"column:completed_at"`
    CreatedAt      time.Time      `gorm:"column:created_at;autoCreateTime"`
    UpdatedAt      time.Time      `gorm:"column:updated_at;autoUpdateTime"`

    Issue       Issue        `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
    PullRequest *PullRequest `gorm:"foreignKey:PRID;constraint:OnDelete:SET NULL"`
}

// internal/models/ci_status.go
type CIStatus struct {
    ID           int        `gorm:"primaryKey;autoIncrement"`
    PRID         int        `gorm:"column:pr_id;index"`
    CheckSuiteID string     `gorm:"column:check_suite_id;size:191"`
    CheckRunID   *string    `gorm:"column:check_run_id;size:191"`
    Name         string     `gorm:"size:191"`
    Status       string     `gorm:"type:enum('queued','in_progress','completed');index"`
    Conclusion   *string    `gorm:"type:enum('success','failure','cancelled','skipped','neutral');index"`
    Logs         *string    `gorm:"type:text"`
    LogsURL      *string    `gorm:"column:logs_url;size:512"`
    StartedAt    *time.Time `gorm:"column:started_at"`
    CompletedAt  *time.Time `gorm:"column:completed_at"`
    CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime"`
    UpdatedAt    time.Time  `gorm:"column:updated_at;autoUpdateTime"`

    PullRequest PullRequest `gorm:"foreignKey:PRID;constraint:OnDelete:CASCADE"`

    // Unique composite index
    // Add via goose migration: UNIQUE KEY (pr_id, check_suite_id, check_run_id)
}

// internal/models/operation_log.go
type OperationLog struct {
    ID            int       `gorm:"primaryKey;autoIncrement"`
    RunID         int       `gorm:"column:run_id;index:idx_run_type"`
    OperationType string    `gorm:"column:operation_type;type:enum('pr-create','post-comment','request-review','merge');index:idx_run_type"`
    OperationID   string    `gorm:"column:operation_id;uniqueIndex;size:191"`
    Status        string    `gorm:"type:enum('pending','succeeded','failed')"`
    CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"`
}

// internal/models/audit_log.go
type AuditLog struct {
    ID             int            `gorm:"primaryKey;autoIncrement"`
    EventType      string         `gorm:"column:event_type;index;size:191"`
    Actor          string         `gorm:"size:191;index"`
    ResourceType   string         `gorm:"column:resource_type;index;size:191"`
    ResourceID     int            `gorm:"column:resource_id;index"`
    Payload        datatypes.JSON `gorm:"type:json"`
    IdempotencyKey *string        `gorm:"column:idempotency_key;index;size:191"`
    IPAddress      *string        `gorm:"column:ip_address;size:45"`
    UserAgent      *string        `gorm:"column:user_agent;type:text"`
    CreatedAt      time.Time      `gorm:"column:created_at;autoCreateTime"`
}
```

## Migration Strategy (goose)

1. Create initial SQL migration `migrations/000001_init.sql` with single file sections `-- +goose Up` and `-- +goose Down`
2. Apply migrations:
   - Development: `goose -dir migrations mysql "<dsn>" up`
   - Production: run goose in CI/CD with manual approval for down migrations
3. Add explicit indexes and unique constraints in SQL (composite keys, unique keys)
4. Use GORM AutoMigrate only in development if needed (never in production)

**Rollback Plan**: Use goose down migrations (e.g., `goose down`, `goose down-to <version>`) to revert schema changes safely

---

**Data Model Complete**: All entities defined with relationships, constraints, and business rules. Ready for GORM model implementation and goose SQL migrations.
