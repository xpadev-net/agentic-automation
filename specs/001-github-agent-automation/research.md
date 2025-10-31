# research.md: GitHub Agent Automation - Phase 0 Research

**Last Updated**: 2025-10-31
**Status**: Complete - All technology decisions finalized

## 1. Feature Summary

- **Trigger**: Issue comment with "/run-agent" (write permission required), PR comment "@codex review"
- **Flow**: AI implements Issue → Create PR → Codex review → Auto-retry up to 50 times on feedback/CI failure → Auto-merge on Codex & CI success → Trigger dependent tasks if unblocked
- **Notification**: Failures notified to GitHub Issue and Discord, progress visible via auto-comments

## 2. Technology Stack Decisions

### 2.1 Runtime & Language

**Decision**: Go 1.22+

**Rationale**:
- Go 1.22: Long-term support, excellent concurrency with goroutines for webhook processing
- Strong type safety at compile time (no need for separate TypeScript layer)
- Native compilation to single binary for easy deployment
- Excellent ecosystem for GitHub/webhook integrations (go-github)
- High performance for concurrent webhook processing
- Unified codebase with agent-runner (both in Go)

**Alternatives Considered**:
- Node.js + TypeScript: Rejected to unify stack with agent-runner (already Go)
- Python + FastAPI: Rejected due to inferior async webhook handling at scale
- Rust: Rejected as overkill for CRUD-heavy event processing

### 2.2 Web Framework

**Decision**: Gin

**Rationale**:
- Lightweight and fast (minimal overhead for webhook endpoints)
- Excellent performance with httprouter-based routing
- Built-in middleware support for signature verification, error handling
- Simple routing for webhook endpoints
- Extensive middleware ecosystem
- Native Go performance without runtime overhead

**Alternatives Considered**:
- Echo: Considered but Gin has larger community and more middleware
- chi: Considered but Gin offers better performance out-of-the-box
- net/http (standard library): Rejected due to lack of middleware ecosystem
- Fiber: Rejected as it's Express-inspired (we want Go-native approach)

### 2.3 Database & ORM

**Decision**: MySQL 8.0+ with GORM + goose for migrations

**Rationale**:
- MySQL: Proven reliability, ACID compliance for critical audit trail
- GORM: Type-safe queries, excellent DX, supports composite keys
- AutoMigrate for development (rapid iteration)
- goose for production migrations (SQL-based, version controlled, rollback support)
- Built-in connection pooling and query optimization
- Supports composite keys for BlockerGraphEdges
- Native Go integration (no Node.js runtime needed)

**Alternatives Considered**:
- PostgreSQL: Rejected per user specification (MySQL required)
- MongoDB: Rejected due to lack of ACID for financial/audit data
- sqlx: Rejected due to lack of ORM features (migrations, relationships)
- Ent (Facebook): Considered but GORM has larger community and better documentation
- Raw SQL: Rejected due to lack of type safety and migration management
- migrate (golang-migrate): Using goose instead (better tooling, same foundation)

### 2.4 Testing Framework

**Decision**: Go standard testing package + testify

**Rationale**:
- Go standard testing: Built-in, no additional dependencies
- testify: Rich assertions (assert, require), mocking support (mockery)
- Fast execution with native Go test runner
- Excellent IDE integration (VS Code, GoLand)
- Built-in coverage reporting (go test -cover)
- Parallel test execution built-in
- Clean test structure (TestXxx naming convention)

**Alternatives Considered**:
- ginkgo/gomega: Rejected as overkill (BDD-style not needed)
- GoCheck: Rejected due to smaller community
- GoConvey: Rejected due to web UI dependency (not needed for CI)
- Standard testing only: Rejected due to lack of assertions (testify adds value)

### 2.5 GitHub Integration

**Decision**: github.com/google/go-github/v62

**Rationale**:
- Official GitHub Go library with first-class support
- Complete GitHub API coverage with retry logic
- Strong typing with Go structs (type safety)
- Webhook event parsing support (github.com/google/go-github/v62/webhooks)
- Built-in rate limiting and error handling
- Active maintenance and community support

**Alternatives Considered**:
- Raw HTTP client: Rejected due to reinventing signature verification and API wrapper
- github.com/bradleyfalzon/ghinstallation: Considered but go-github includes this
- github.com/go-playground/webhooks: Rejected as go-github provides webhook handling
- Manual implementation: Rejected due to complexity and maintenance burden

### 2.6 Logging

**Decision**: zap (Uber's structured logging)

**Rationale**:
- Fastest Go logger (critical for high-volume webhooks)
- Structured JSON logs for easy parsing
- Low overhead (zero-allocation JSON encoder)
- Contextual logging with fields (child loggers)
- Production-ready with multiple log levels
- Excellent performance benchmarks

**Alternatives Considered**:
- logrus: Rejected due to slower performance (reflection-based)
- glog: Rejected due to Google-specific design (not general purpose)
- zerolog: Considered but zap has larger community and better documentation
- Standard log: Rejected due to lack of structured logging and levels

### 2.7 Queue / Locking

**Decision**: No global queue. Event-driven, stateless handlers with PR/branch-level locking.

**Rationale**:
- Webhook events are naturally async - GitHub retries on failure
- Avoid global queue complexity (YAGNI)
- Enforce per-branch concurrency=1 and PR/ブランチ単位のロックで重複操作を防止（FR-020/021）
- Optional in-memory slot control for Pod同時数の制限は許容（構成で最大値を設定）

**Deferred**: If scale requires (>100 concurrent executions), consider BullMQ + Redis for global scheduling

**Alternatives Considered**:
- BullMQ + Redis: Rejected as premature optimization
- RabbitMQ: Rejected as operational complexity outweighs benefits
- AWS SQS: Rejected to avoid cloud lock-in for MVP

## 3. External Services & Authorization

### 3.1 GitHub Authentication

**Decision**: GitHub App with installation tokens

**Rationale**:
- Fine-grained permissions (Contents: write, Issues: write, PRs: write)
- Installation tokens auto-refresh (no manual rotation)
- Per-repository or organization scope
- Webhook signature verification via app secret

**Implementation**:
- Check user permission level via GitHub API (Collaborator+)
- Verify webhook signature using crypto/hmac (standard library)
- Store app private key in environment variable (not in DB)

### 3.2 Codex Review Integration

**Decision**: GitHub PR comment-based integration (no dedicated API)

**Integration Method**:
- Post comment `@codex review` on PR to trigger Codex review
- Codex bot responds with review comment on the same PR
- System monitors PR comments via GitHub webhook (pull_request_review_comment)
- Approval detected by parsing comment body for "Codex Review: Didn't find any major issues."

**Implementation**:
- Use GitHub API to post review request comment
- Subscribe to pull_request_review and pull_request_review_comment webhooks
- Parse Codex bot responses (identified by bot username)
- No separate Codex API client required

**Rate Limiting**: Governed by GitHub API limits (5000 req/hour)

### 3.4 AI Agent Execution (Kubernetes Pod + Push型通知)

**Decision**: Kubernetes Job/Pod execution with agent-runner (Go binary) and Push-type notification

**Architecture**:
- Operator creates Kubernetes Job with Pod from template (environment variables)
- Pod runs `agent-runner` Go binary as entrypoint
- agent-runner executes claude-code or cursor-agents with Issue context
- agent-runner runs lint/typecheck validation (npm run lint, npm run type-check)
- agent-runner commits and pushes changes, creates PR via gh CLI
- **Push notification**: agent-runner calls Operator REST API to report result
- Operator receives POST /api/agent-runs/{id}/report with status (succeeded/failed)
- Operator updates AgentRun state based on report

**Agent Selection**:
- Issue label: `agent:claude-code` or `agent:cursor-agents`
- Default: claude-code (if no label specified)
- Detected by AgentTypeDetector service, passed to Pod as AGENT_TYPE env var

**Timeout & Concurrency**:
- Configurable via environment variables
- Default: 30min timeout (K8s Job activeDeadlineSeconds), max 10 concurrent Pods
- Job failures (timeout, OOM) detected via missing report → retry

**API Authentication**:
- Bearer token (OPERATOR_API_TOKEN) for agent-runner → Operator authentication
- Exponential backoff retry (max 5 attempts) if API unreachable

**Result Retrieval**:
- Primary: Push notification from agent-runner
- Fallback: Pod logs (kubectl logs) for debugging if API call fails

### 3.5 Discord Notifications

**Decision**: Discord Webhook URL (no bot required)

**Rationale**:
- Simple POST request, no OAuth flow
- Sufficient for one-way notifications
- URL stored in environment variable
- Retry on failure (3 attempts with backoff)

### 3.4 CI Integration

**Decision**: GitHub Check Suites API

**Implementation**:
- Subscribe to check_suite webhook events
- Parse conclusion field: success, failure, cancelled
- Extract logs via GitHub API (check runs endpoint)
- Store in CIStatus table for retry analysis

## 4. Technical Design Decisions

### 4.1 Idempotency Strategy

**Decision**: X-GitHub-Delivery header as idempotency key

**Implementation**:
- Store delivery ID in AgentRun.idempotencyKey (unique index)
- Check existence before processing webhook
- Return 200 OK for duplicates (GitHub expects success)
- TTL: Keep records indefinitely for audit trail

### 4.2 Retry Strategy (API Calls)

**Decision**: Exponential backoff with jitter

**Parameters**:
- Initial delay: 1 second
- Max delay: 60 seconds
- Max attempts: 3-5 (depending on API)
- Jitter: ±25% to avoid thundering herd

**Libraries**: Use go-github built-in retry, implement custom for Codex using golang.org/x/time/rate

### 4.3 Retry Strategy (AI/CI Failures)

**Decision**: State machine with retry counter

**Implementation**:
- AgentRun.retryCount: increment on each AI retry
- Max retries: 50 (FR-014)
- On failure: aggregate CI logs + review comments
- Pass to AI agent as context for next iteration
- After 50 attempts: set state=failed, notify GitHub + Discord

### 4.4 Dependency Graph Management

**Decision**: Adjacency list in BlockerGraphEdges table

**Algorithm**:
- On Issue close: query dependents (tasks blocked by this Issue)
- Check if all dependencies resolved for each dependent
- If yes: trigger execution for unblocked tasks
- Cycle detection: DFS with visited set (warn but don't block)

**Data Model**:
- Composite PK: (taskId, dependsOnTaskId)
- Both columns FK to Issue.id
- Parse "blocked by #123" from Issue body using regex

### 4.5 Concurrent Execution

**Decision**: No locking, rely on Git merge conflicts

**Rationale**:
- Spec requires unlimited concurrency (FR-012)
- File conflicts detected at PR merge time
- Simpler than distributed locks
- User resolves conflicts or closes duplicate Issues

**Risk Mitigation**: Document in quickstart.md that users should manage Issue dependencies

## 5. Performance & Scalability

### 5.1 Rate Limiting

**GitHub API**: 5000 requests/hour for authenticated apps
- Implement request counter in MetricsService
- Warn at 80% threshold (4000 req/hr)
- Backoff if rate limit exceeded

**Codex API**: TBD - implement per-API documentation

### 5.2 Database Optimization

- Index on AgentRun.idempotencyKey (unique)
- Index on AgentRun.state for querying active runs
- Index on BlockerGraphEdges.dependsOnTaskId for dependency lookup
- Index on CIStatus.prId for CI result queries
- Connection pooling: GORM default (sql.DB with SetMaxOpenConns, SetMaxIdleConns)

### 5.3 Observability

**Metrics**:
- Webhook processing time (p50, p95, p99)
- Agent execution time
- Retry counts per Issue
- GitHub API request count
- Success/failure rates

**Logging**:
- All webhook events (DEBUG level)
- All external API calls (INFO level)
- Errors with stack traces (ERROR level)
- Structured JSON format for log aggregation

### 5.4 Deployment

**Container**: Docker with Go 1.22 Alpine base (multi-stage build)
**Health Check**: GET /health endpoint (checks DB connection)
**Startup**: Run goose migrations, then start Gin server
**Shutdown**: Graceful shutdown using context.Context (finish processing webhooks, close DB)

## 6. Security Considerations

### 6.1 Secret Management

- Environment variables for all secrets (no .env in git)
- GitHub App private key (GITHUB_PRIVATE_KEY)
- Webhook secret (GITHUB_WEBHOOK_SECRET)
- Codex API key (CODEX_API_KEY)
- Discord webhook URL (DISCORD_WEBHOOK_URL)
- Database URL (DATABASE_URL)

**Validation**: Fail fast on startup if any required env var missing

### 6.2 Input Validation

- Webhook signature verification (reject invalid signatures)
- User permission check (reject non-Collaborators)
- Issue/PR ID validation (prevent injection)
- Comment body sanitization (escape for Discord markdown)

### 6.3 Dependency Scanning

- go list -m all and go mod verify on every build
- govulncheck (Go vulnerability database) for CVE monitoring
- Dependabot for automated dependency updates
- Pin exact versions in go.mod
- Review major version bumps for breaking changes

## 7. Open Questions (All Resolved)

~~1. Codex API authentication method?~~ → Bearer token assumed
~~2. Codex rate limits?~~ → TBD, implement backoff
~~3. AI agent API contract?~~ → Abstracted in CodeGenerationService interface
~~4. Trigger string customization?~~ → Fixed to "/run-agent" per spec
~~5. Queue system needed?~~ → No, event-driven sufficient

## 8. Next Steps (Phase 1)

1. ✅ Complete data-model.md with CIStatus and AuditLog tables
2. ✅ Define API contracts in contracts/
3. ✅ Write comprehensive quickstart.md
4. ✅ Update agent context files with technology stack
5. ⏭ Generate tasks.md via /speckit.tasks command

---

**Research Complete**: All technology decisions finalized. Ready for Phase 1 design artifacts.
