# research.md: GitHub Agent Automation - Phase 0 Research

**Last Updated**: 2025-10-31
**Status**: Complete - All technology decisions finalized

## 1. Feature Summary

- **Trigger**: Issue comment with "/run-agent" (write permission required), PR comment "@codex review"
- **Flow**: AI implements Issue → Create PR → Codex review → Auto-retry up to 50 times on feedback/CI failure → Auto-merge on Codex & CI success → Trigger dependent tasks if unblocked
- **Notification**: Failures notified to GitHub Issue and Discord, progress visible via auto-comments

## 2. Technology Stack Decisions

### 2.1 Runtime & Language

**Decision**: Node.js 22 LTS + TypeScript 5.x (strict mode)

**Rationale**:
- Node.js 22: Long-term support, excellent async I/O for webhook processing
- TypeScript strict mode: Type safety for complex business logic and API integrations
- Native ESM support for modern module system
- Strong ecosystem for GitHub/webhook integrations

**Alternatives Considered**:
- Python + FastAPI: Rejected due to inferior async webhook handling at scale
- Go: Rejected due to less mature GitHub API libraries and team familiarity
- Rust: Rejected as overkill for CRUD-heavy event processing

### 2.2 Web Framework

**Decision**: Hono

**Rationale**:
- Lightweight and fast (minimal overhead for webhook endpoints)
- TypeScript-first design with excellent type inference
- Built-in middleware support for signature verification, error handling
- Simple routing for webhook endpoints
- Better performance than Express with smaller bundle size

**Alternatives Considered**:
- Express: Rejected due to larger footprint and callback-based design
- Fastify: Considered but Hono has better TypeScript ergonomics
- NestJS: Rejected as too opinionated/heavy for simple webhook server

### 2.3 Database & ORM

**Decision**: MySQL 8.0+ with Prisma ORM

**Rationale**:
- MySQL: Proven reliability, ACID compliance for critical audit trail
- Prisma: Type-safe queries, automatic migrations, excellent DX
- Schema-first approach aligns with data-model.md workflow
- Built-in connection pooling and query optimization
- Supports composite keys for BlockerGraphEdges

**Alternatives Considered**:
- PostgreSQL: Rejected per user specification (MySQL required)
- MongoDB: Rejected due to lack of ACID for financial/audit data
- TypeORM: Rejected due to inferior type safety vs Prisma
- Raw SQL: Rejected due to lack of type safety and migration management

### 2.4 Testing Framework

**Decision**: Vitest

**Rationale**:
- Native ESM support (matches Node.js 22)
- Fast execution with smart parallelization
- Jest-compatible API (easy migration if needed)
- Excellent TypeScript integration
- Built-in coverage reporting
- Watch mode for TDD workflow

**Alternatives Considered**:
- Jest: Rejected due to ESM complexity and slower execution
- Mocha + Chai: Rejected due to fragmented ecosystem
- AVA: Rejected due to smaller community and plugin ecosystem

### 2.5 GitHub Integration

**Decision**: @octokit/webhooks + @octokit/rest

**Rationale**:
- Official GitHub libraries with first-class support
- @octokit/webhooks: Signature verification, typed webhook payloads
- @octokit/rest: Complete GitHub API coverage with retry logic
- Auto-generated types from GitHub's OpenAPI spec
- Built-in rate limiting and error handling

**Alternatives Considered**:
- Octokit App: Rejected as too heavyweight (we need simple webhook + API)
- Probot: Rejected due to framework lock-in and unnecessary abstraction
- Raw HTTP: Rejected due to reinventing signature verification

### 2.6 Logging

**Decision**: Pino (structured logging)

**Rationale**:
- Fastest Node.js logger (critical for high-volume webhooks)
- Structured JSON logs for easy parsing
- Low overhead (doesn't block event loop)
- Child loggers for request tracing
- Production-ready transports (file, stdout, syslog)

**Alternatives Considered**:
- Winston: Rejected due to slower performance and callback-based API
- Bunyan: Rejected due to archived status and lack of maintenance
- Console.log: Rejected due to lack of structure and log levels

### 2.7 Queue System

**Decision**: None (event-driven, stateless)

**Rationale**:
- Webhook events are naturally async - GitHub retries on failure
- No need for job queue complexity (YAGNI principle)
- Database state machine (AgentRun.state) tracks execution
- Simpler architecture = easier testing and debugging

**Deferred**: If scale requires (>100 concurrent executions), consider BullMQ + Redis

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
- Verify webhook signature using @octokit/webhooks
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

**Libraries**: Use @octokit/rest built-in retry, implement custom for Codex

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
- Connection pooling: Prisma default (max 10 connections)

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

**Container**: Docker with Node.js 22 Alpine base
**Health Check**: GET /health endpoint (checks DB connection)
**Startup**: Run Prisma migrations, then start Hono server
**Shutdown**: Graceful shutdown (finish processing webhooks, close DB)

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

- npm audit on every build
- Snyk or Dependabot for CVE monitoring
- Pin exact versions in package.json
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
