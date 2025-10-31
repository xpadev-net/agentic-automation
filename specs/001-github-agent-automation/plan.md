# Implementation Plan: GitHub Agent Automation

**Branch**: `001-github-agent-automation` | **Date**: 2025-10-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-github-agent-automation/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

Automated system that responds to GitHub Issue comments with trigger phrase "/run-agent", launches Kubernetes Pod with agent-runner (Go binary) to execute AI agent (claude-code/cursor-agents), creates PR, requests Codex review via GitHub comment, automatically retries based on CI/review feedback (max 50 times), and auto-merges on approval. Pod pushes completion status to Operator REST API. Includes dependency management for blocked tasks.

## Technical Context

**Language/Version**: Node.js 22 LTS + TypeScript 5.x (strict mode)
**Primary Dependencies**:
- Hono (lightweight web framework for webhook server)
- Prisma ORM (type-safe database access)
- @octokit/webhooks (GitHub webhook event handling)
- @octokit/rest (GitHub API client)
- @kubernetes/client-node (Kubernetes API client for Job/Pod management)
- Vitest (test runner)

**Agent Runner** (separate Go project):
- Go 1.22+ with cobra CLI framework
- Runs in Kubernetes Pod
- Executes claude-code or cursor-agents
- Performs lint/typecheck (npm run lint, npm run type-check)
- Commits and pushes changes
- Reports results to Operator REST API

**Storage**: MySQL 8.0+ with Prisma schema for Issue, PullRequest, AgentRun, ReviewFeedback, BlockerGraphEdges, CIStatus, AuditLog

**Testing**: Vitest with coverage reporting, contract tests for GitHub/Codex APIs

**Target Platform**: Linux server (Docker container), requires Node.js 22 runtime

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
- Must support concurrent execution without locking

**Scale/Scope**:
- Handle 10-50 Issues per hour
- Support multiple repositories
- Store execution history for audit
- Webhook events: issue_comment, pull_request, pull_request_review, check_suite, status

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Library-First ✅ PASS

**Compliance**:
- Core services (AgentExecutionService, CodexReviewService, GitHubClient) designed as independent modules with clear boundaries
- Each service has single responsibility and minimal external dependencies
- Repository layer abstracts database access through Prisma
- External API clients (GitHub, Codex, Discord) wrapped in dedicated modules

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
- Vitest configured with coverage reporting
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
- Structured logging with Winston/Pino (JSON format)
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
- Webhook signature verification (@octokit/webhooks)
- Environment variables for secrets (never logged)
- Collaborator+ authorization check (FR-018)
- Dependency scanning with npm audit / Snyk

**Performance**:
- p95 goals in spec (SC-001 through SC-009)
- Rate limiting for GitHub API
- Metrics collection for execution times

**Deployment**:
- Docker container with health checks
- Database migrations via Prisma (idempotent)
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
src/
├── cli/                          # CLI commands (Constitution Principle II)
│   ├── commands/
│   │   ├── server.ts            # Start webhook server
│   │   ├── trigger.ts           # Manual trigger for testing
│   │   ├── status.ts            # Check execution status
│   │   └── retry.ts             # Manual retry
│   └── index.ts                 # CLI entry point
├── webhooks/
│   ├── server.ts                # Hono webhook server
│   ├── handlers/
│   │   ├── issue-comment.ts     # FR-001: Trigger detection
│   │   ├── pull-request.ts      # PR events
│   │   ├── pull-request-review.ts # FR-010: Codex review
│   │   ├── check-suite.ts       # FR-014: CI results
│   │   ├── status.ts            # CI status events
│   │   └── issues.ts            # FR-013: Dependency management
│   └── middleware/
│       ├── idempotency.ts       # FR-017: X-GitHub-Delivery
│       ├── signature.ts         # Webhook signature verification
│       └── error-handler.ts     # Global error handling
├── services/                     # Business logic (Library-First)
│   ├── TriggerDetectionService.ts      # Detect "/run-agent"
│   ├── AuthorizationService.ts         # FR-018: Collaborator check
│   ├── IssueContextCollector.ts        # FR-002: Gather context
│   ├── AgentExecutionService.ts        # FR-003: Orchestrate execution
│   ├── CodeGenerationService.ts        # AI agent integration
│   ├── CodexReviewService.ts           # FR-004: Post @codex review comment via GitHub API
│   ├── CodexApprovalDetector.ts        # FR-011: Parse Codex bot comments for approval
│   ├── CIFailureAnalyzer.ts            # FR-014: CI log parsing
│   ├── RetryOrchestrator.ts            # FR-005: Max 50 retries
│   ├── FeedbackAggregator.ts           # Combine review + CI
│   ├── MergeConditionChecker.ts        # FR-006: DoD validation
│   ├── AutoMergeService.ts             # FR-006: Auto-merge
│   ├── BlockerGraphBuilder.ts          # FR-007: Dependency graph
│   ├── BlockedTaskResolver.ts          # FR-013: Unblock detection
│   ├── DependencyValidator.ts          # FR-013: Order validation
│   ├── GitHubNotificationService.ts    # GitHub comments
│   ├── DiscordNotificationService.ts   # FR-014: Discord webhooks
│   └── MetricsService.ts               # Performance metrics
├── repositories/                 # Data access (Prisma)
│   ├── IssueRepository.ts
│   ├── PullRequestRepository.ts
│   ├── AgentRunRepository.ts
│   ├── ReviewFeedbackRepository.ts
│   ├── BlockerGraphRepository.ts
│   ├── CIStatusRepository.ts
│   └── AuditLogRepository.ts
├── lib/                          # External integrations
│   ├── prisma.ts                # Prisma client singleton
│   ├── github-client.ts         # @octokit/rest wrapper
│   ├── discord-client.ts        # Discord webhook client
│   ├── ai-agent-client.ts       # AI agent API client
│   ├── git-operations.ts        # Git commands wrapper
│   └── logger.ts                # Structured logging (Winston/Pino)
├── utils/
│   ├── retry.ts                 # FR-016: Exponential backoff
│   ├── comment-parser.ts        # Parse trigger strings
│   ├── branch-name-generator.ts # Generate branch names
│   ├── commit-message-generator.ts
│   └── error-codes.ts           # User-facing error codes
├── config/
│   └── env.ts                   # Environment validation
└── types/
    ├── webhook-events.ts        # GitHub webhook types
    └── api-contracts.ts         # Internal API types

tests/
├── contract/                     # External API contracts
│   ├── github-webhooks.test.ts  # Validate webhook payloads
│   ├── github-api.test.ts       # GitHub REST API
│   └── codex-api.test.ts        # Codex API
├── integration/                  # Cross-service flows
│   ├── trigger-to-pr.test.ts    # US1 + US2
│   ├── review-retry.test.ts     # US3
│   ├── auto-merge.test.ts       # US4
│   └── dependency-chain.test.ts # US5
└── unit/                         # Service unit tests
    ├── services/
    ├── repositories/
    ├── webhooks/
    └── utils/

prisma/
├── schema.prisma                # Database schema
└── migrations/                  # Migration history

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

**Structure Decision**: Single backend service architecture selected. No frontend or mobile components. Event-driven webhook processor with library-first design - each service independently testable and composable. CLI layer added per Constitution Principle II. Repository pattern abstracts Prisma for testability.

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
