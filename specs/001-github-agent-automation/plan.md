# Implementation Plan: GitHub Agent Automation

**Branch**: `001-github-agent-automation` | **Date**: 2025-10-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-github-agent-automation/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

Automated system that responds to GitHub Issue comments with trigger phrase "/run-agent", launches Kubernetes Pod with agent-runner (Go binary) to execute AI agent (claude-code/cursor-agents), creates PR, requests Codex review via GitHub comment, automatically retries based on CI/review feedback (max 50 times), and auto-merges on approval. Pod pushes completion status to Operator REST API. Includes dependency management for blocked tasks.

## Technical Context

**Language/Version**: Go 1.22+
**Primary Dependencies**:
- Gin (lightweight web framework for webhook server)
- GORM (type-safe database ORM)
- github.com/google/go-github/v62 (GitHub API client and webhook event handling)
- k8s.io/client-go (Kubernetes API client for Job/Pod management)
- go.uber.org/zap (structured logging)
- github.com/golang-migrate/migrate (database migrations - goose)
- github.com/stretchr/testify (testing framework)
- github.com/spf13/cobra (CLI framework)

**Agent Runner** (Go binary in Pod):
- Go 1.22+ with cobra CLI framework
- Runs in Kubernetes Pod
- Executes claude-code or cursor-agents
- Executes pre-hooks, validations, and post-hooks from `.agent-config.yaml` (see [agent-manifest.md](./contracts/agent-manifest.md))
- Commits and pushes changes
- Reports results to Operator REST API

**Storage**: MySQL 8.0+ with GORM models for Issue, PullRequest, AgentRun, ReviewFeedback, BlockerGraphEdges, CIStatus, AuditLog

**Testing**: Go standard testing package + testify for assertions and mocks, contract tests for GitHub/Codex APIs

**Target Platform**: Linux server (Docker container), requires Go 1.22+ runtime

**Project Type**: Single backend service (event-driven webhook processor)

**Performance Goals**:
- Webhook response < 2 seconds (acknowledgment)
- Agent execution start < 2 minutes from trigger (SC-001)
- PR creation < 15 minutes from start (SC-002)
- Support 10+ concurrent agent executions
- Stay within GitHub API rate limit (5000 req/hour)

**Constraints**:
- Idempotency required (X-GitHub-Delivery header)
- Exponential backoff for API retries (max 3-5 attempts)
- AI retry limit: 50 attempts per Issue
- No queue system (event-driven, stateless webhook handlers)
 - No global queue/lock; PR/branch-level locking enforced (per FR-020/021)

**Scale/Scope**:
- Handle 10-50 Issues per hour
- Support multiple repositories
- Store execution history for audit
- Webhook events: issue_comment, pull_request, pull_request_review, check_suite, status

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Library-First ✅ PASS

**Compliance**:
- Core services (AgentExecutionService, CodexReviewService, GitHubClient) designed as independent packages with clear boundaries
- Each service has single responsibility and minimal external dependencies
- Repository layer abstracts database access through GORM
- External API clients (GitHub, Codex, Discord) wrapped in dedicated packages

**Verification**:
- Each service module exports public API and hides implementation
- Services independently testable with mocks
- Clear separation: repositories (data), services (business logic), handlers (webhook)

### Principle II: CLI Interface ⚠️ PARTIAL

**Compliance**:
- Primary interface is webhook server (event-driven)
- Will add CLI commands for:
  - `agent-automation server` - start webhook server
  - `agent-automation trigger <issue-id>` - manual trigger for testing
  - `agent-automation status <run-id>` - check execution status
  - `agent-automation retry <run-id>` - manual retry

**Verification**:
- CLI outputs structured JSON and human-readable formats
- Non-interactive mode with stable exit codes
- stdin/args input, stdout/stderr output per constitution

### Principle III: Test-First (NON-NEGOTIABLE) ✅ PASS

**Compliance**:
- Go standard testing package + testify configured with coverage reporting
- TDD workflow: acceptance test → failing test → implementation → refactor
- Test structure: unit/, integration/, contract/
- All webhook handlers and services require tests before implementation

**Verification**:
- Each User Story has acceptance scenarios (spec.md)
- Tests verify observable behavior, not internal implementation
- CI enforces test passage before merge

### Principle IV: Integration Testing ✅ PASS

**Compliance**:
- Contract tests for GitHub webhook payloads
- Contract tests for Codex API requests/responses
- Integration tests for multi-service flows (trigger → execution → PR → review)
- Database integration tests with test containers

**Verification**:
- tests/contract/ validates external API contracts
- tests/integration/ validates cross-module workflows
- Mock servers for GitHub/Codex during testing

### Principle V: Observability, Versioning & Simplicity ✅ PASS

**Compliance**:
- Structured logging with zap (JSON format)
- SemVer versioning for releases
- Audit log table for all operations
- Simple architecture: webhook → service → database (no complex orchestration)

**Verification**:
- All external calls logged with request/response
- Breaking changes require MAJOR version bump
- YAGNI: no queue system, no microservices split (single service)

### Additional Constraints ✅ PASS

**Security**:
- Minimum privilege: GitHub App with scoped permissions
- Webhook signature verification (crypto/hmac standard library)
- Environment variables for secrets (never logged)
- Collaborator+ authorization check (FR-018)
- Dependency scanning with go list -m all and go mod verify

**Performance**:
- p95 goals in spec (SC-001 through SC-009)
- Rate limiting for GitHub API
- Metrics collection for execution times

**Deployment**:
- Docker container with health checks
- Database migrations via goose (idempotent)
- Rollback: database migration down, container rollback

### Gate Result: ✅ PASS WITH NOTES

**Notes**:
- CLI commands to be added for testing/operations (addresses Principle II)
- All other principles fully satisfied
- No violations requiring justification

## Project Structure

### Documentation (this feature)

```text
specs/[###-feature]/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
cmd/
├── operator/                     # Operator binary (main entry point)
│   └── main.go
└── agent-runner/                 # Agent runner binary (separate project)
    └── main.go

internal/
├── webhooks/                     # Webhook handlers
│   ├── server.go                # Gin webhook server
│   ├── handlers/
│   │   ├── issue_comment.go     # FR-001: Trigger detection
│   │   ├── pull_request.go      # PR events
│   │   ├── pull_request_review.go # FR-010: Codex review
│   │   ├── check_suite.go       # FR-014: CI results
│   │   ├── status.go            # CI status events
│   │   └── issues.go            # FR-013: Dependency management
│   └── middleware/
│       ├── idempotency.go       # FR-017: X-GitHub-Delivery
│       ├── signature.go         # Webhook signature verification
│       └── error_handler.go     # Global error handling
├── services/                     # Business logic (Library-First)
│   ├── trigger_detection.go     # Detect "/run-agent"
│   ├── authorization.go         # FR-018: Collaborator check
│   ├── issue_context.go         # FR-002: Gather context
│   ├── agent_execution.go       # FR-003: Orchestrate execution
│   ├── code_generation.go       # AI agent integration
│   ├── codex_review.go          # FR-004: Post @codex review comment via GitHub API
│   ├── codex_approval.go        # FR-011: Parse Codex bot comments for approval
│   ├── ci_failure.go            # FR-014: CI log parsing
│   ├── retry_orchestrator.go    # FR-005: Max 50 retries
│   ├── feedback_aggregator.go   # Combine review + CI
│   ├── merge_condition.go       # FR-006: DoD validation
│   ├── auto_merge.go            # FR-006: Auto-merge
│   ├── blocker_graph.go         # FR-007: Dependency graph
│   ├── blocked_task.go          # FR-013: Unblock detection
│   ├── dependency_validator.go  # FR-013: Order validation
│   ├── branch_lock.go           # FR-020: Per-branch concurrency control
│   ├── github_notification.go   # GitHub comments
│   ├── discord_notification.go  # FR-014: Discord webhooks
│   └── metrics.go               # Performance metrics
├── repositories/                 # Data access (GORM)
│   ├── issue.go
│   ├── pull_request.go
│   ├── agent_run.go
│   ├── review_feedback.go
│   ├── blocker_graph.go
│   ├── ci_status.go
│   ├── audit_log.go
│   └── branch_lock.go           # FR-020: Branch lock management
├── models/                       # GORM models
│   ├── issue.go
│   ├── pull_request.go
│   ├── agent_run.go
│   ├── review_feedback.go
│   ├── blocker_graph.go
│   ├── ci_status.go
│   ├── audit_log.go
│   └── branch_lock.go           # FR-020: Branch lock model
├── clients/                      # External integrations
│   ├── github.go                # go-github client wrapper
│   ├── discord.go               # Discord webhook client
│   └── kubernetes.go            # k8s.io/client-go wrapper
├── utils/
│   ├── retry.go                 # FR-016: Exponential backoff
│   ├── comment_parser.go        # Parse trigger strings
│   ├── branch_name.go           # Generate branch names
│   ├── commit_message.go
│   └── error_codes.go           # User-facing error codes
└── config/
    ├── database.go              # GORM connection
    └── env.go                   # Environment validation

pkg/                              # Public packages (if needed)
└── types/
    ├── webhook.go               # GitHub webhook types
    └── api.go                   # Internal API types

migrations/                       # Database migrations (goose)
├── 000001_init.up.sql
├── 000001_init.down.sql
└── ...

tests/
├── contract/                     # External API contracts
│   ├── github_webhooks_test.go  # Validate webhook payloads
│   ├── github_api_test.go       # GitHub REST API
│   └── codex_api_test.go        # Codex API
├── integration/                  # Cross-service flows
│   ├── trigger_to_pr_test.go    # US1 + US2
│   ├── review_retry_test.go     # US3
│   ├── auto_merge_test.go       # US4
│   └── dependency_chain_test.go # US5
└── unit/                         # Service unit tests
    ├── services/
    ├── repositories/
    ├── webhooks/
    └── utils/

.specify/                         # Project documentation
├── memory/
│   └── constitution.md
├── templates/
└── scripts/

specs/                            # Feature specifications
└── 001-github-agent-automation/
    ├── spec.md
    ├── plan.md (this file)
    ├── research.md
    ├── data-model.md
    ├── quickstart.md
    ├── tasks.md
    ├── checklists/
    └── contracts/
```

**Structure Decision**: Single backend service architecture selected. No frontend or mobile components. Event-driven webhook processor with library-first design - each service independently testable and composable. CLI layer added per Constitution Principle II. Repository pattern abstracts GORM for testability. Standard Go project layout (cmd/, internal/, pkg/) used for clean separation of concerns.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

No violations. All constitution principles satisfied.

## Phase 0: Research (Current Phase)

Technology decisions and best practices research completed and documented in research.md.

## Phase 1: Design Artifacts (Next Phase)

Generate comprehensive design documentation:

1. **data-model.md**: Complete entity definitions with all tables including CIStatus and AuditLog
2. **contracts/**: API contracts for internal and external integrations
3. **quickstart.md**: Enhanced setup guide with detailed instructions
4. **Agent context update**: Sync technology choices to agent-specific files

## Phase 2: Task Generation

Use `/speckit.tasks` command to generate actionable tasks from this plan and design artifacts.
