# Tasks: GitHub Agent Automation

**Input**: Design documents from `/specs/001-github-agent-automation/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, quickstart.md

**Tests**: Test-First development is mandatory per Constitution Principle III. Test tasks are included for each User Story.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Single project**: `cmd/`, `internal/`, `pkg/`, `tests/` at repository root
- Paths assume Go project structure based on plan.md

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure

- [ ] T001 Create project structure with cmd/, internal/, pkg/, tests/, migrations/, k8s/, agent-runner/ directories
- [ ] T002 Initialize Go module with `go mod init` and base dependencies (gin, gorm, go-github, zap, testify)
- [ ] T003 [P] Setup Makefile or task runner (build, test, goose up/down)
- [ ] T004 [P] Configure logger (zap) and config loader in `internal/config`
- [x] T006 [P] Create .env.example with required environment variables (includes CODEX_BOT_USERNAME, OPERATOR_API_URL, OPERATOR_API_TOKEN)
- [ ] T007 [P] Setup .gitignore for `bin/`, `.env`, `migrations/*.sql`, `*.pem`, `k8s/secrets/`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Database & ORM Setup (GORM + goose)

- [ ] T008 Create GORM models in `internal/models/` (Issue, PullRequest, AgentRun, ReviewFeedback, CIStatus, BlockerGraphEdges, AuditLog, OperationLog)
- [ ] T009 Implement GORM database connection in `internal/config/database.go`
- [ ] T010 Create initial goose SQL migration `migrations/000001_init.up.sql` and `down.sql`

### Repository Layer (GORM-based)

- [ ] T011 [P] Implement IssueRepository using GORM in `internal/repositories/issue.go`
- [ ] T012 [P] Implement PullRequestRepository using GORM in `internal/repositories/pull_request.go`
- [ ] T013 [P] Implement AgentRunRepository with idempotency check using GORM in `internal/repositories/agent_run.go`
- [ ] T014 [P] Implement ReviewFeedbackRepository using GORM in `internal/repositories/review_feedback.go`
- [ ] T015 [P] Implement BlockerGraphRepository using GORM in `internal/repositories/blocker_graph.go`

### Infrastructure & Services (Go)

- [ ] T016 Setup GitHub webhook server (Gin) with signature verification in `internal/webhooks/server.go`
- [ ] T017 [P] Implement GitHub API client wrapper using go-github in `internal/clients/github.go`
- [ ] T018 [P] Implement Discord webhook client in `internal/clients/discord.go`
- [ ] T019 [P] Implement Kubernetes client wrapper using client-go in `internal/clients/kubernetes.go`
- [ ] T020 Create global error handler middleware in `internal/webhooks/middleware/error_handler.go`
- [ ] T021 [P] Setup structured logging with zap in `internal/config/logger.go`
- [ ] T022 [P] Implement retry utility with exponential backoff and jitter in `internal/utils/retry.go`
- [ ] T023 [P] Create environment configuration loader in `internal/config/env.go`
- [ ] T024 Implement idempotency middleware using X-GitHub-Delivery header in `internal/webhooks/middleware/idempotency.go`

### Operator API for Agent Report (NEW - Push型通知)

- [ ] T025 [P] Create agent-report-handler in `internal/webhooks/handlers/agent_report.go` with Bearer token validation
- [ ] T026 Add POST /api/agent-runs/:id/report route to Gin server in `internal/webhooks/server.go`
- [ ] T027 [P] Implement AgentRunRepository.UpdateState for state transitions
- [ ] T028 [P] Write unit tests for agent-report-handler (Bearer token validation, state updates) in `tests/unit/webhooks/`
- [ ] T029 [P] Write integration test for Pod → Operator report flow with mock requests in `tests/integration/`

### Agent Runner Setup (NEW - Go binary in Pod)

- [ ] T030 Create agent-runner Go project structure (agent-runner/main.go, pkg/, Dockerfile)
- [ ] T031 Initialize go.mod with dependencies (cobra CLI framework, HTTP client)
- [ ] T032 [P] Implement main.go CLI entry point with cobra command structure
- [ ] T033 [P] Implement pkg/agent/executor.go (claude-code/cursor-agents execution)
- [ ] T034 [P] Implement pkg/lint/runner.go (npm run lint, npm run type-check)
- [ ] T035 [P] Implement pkg/git/committer.go (git commit/push operations, return branch+SHA)
- [ ] T036 [P] Implement pkg/git/diff.go (git diff detection for file changes)
- [ ] T037 [P] Implement pkg/context/issue.go (Issue context parsing from args)
- [ ] T038 [P] Implement pkg/reporter/client.go (Operator API client with exponential backoff retry)
- [ ] T039 Integrate reporter into main.go execution flow (call on success/failure)
- [ ] T040 Create Dockerfile for agent-runner (multi-stage: Go build → Node.js runtime with agents)
- [ ] T041 Create k8s/rbac.yaml (ServiceAccount, Role, RoleBinding for Pod permissions)
- [ ] T042 Create k8s/pod-template.yaml using agent-runner image
- [ ] T043 Build and push Docker image to container registry (ghcr.io or Docker Hub)
- [ ] T044 [P] Write unit tests for agent executor (mock agent commands)
- [ ] T045 [P] Write unit tests for lint runner (mock npm commands)
- [ ] T046 [P] Write unit tests for git committer (mock git commands)
- [ ] T047 [P] Write unit tests for reporter client (mock HTTP requests, retry logic)
- [ ] T048 Write integration test for full agent-runner execution flow (end-to-end)

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: User Story 1 - コメント起動で自動実行 (Priority: P1) 🎯 MVP

**Goal**: Issue に所定のトリガ文字列 "/run-agent" を含むコメントが投稿されると、システムが当該 Issue 内容を取得し、AI エージェント実行を開始する

**Independent Test**: テストリポジトリでトリガコメント投稿→エージェント実行開始イベントが記録されること

### Implementation for User Story 1

- [ ] T049 [P] [US1] Create webhook event types definition in src/types/webhook-events.ts
- [ ] T050 [P] [US1] Implement trigger detection service in src/services/TriggerDetectionService.ts
- [ ] T051 [US1] Implement comment parser to detect "/run-agent" in src/utils/comment-parser.ts
- [ ] T052 [US1] Implement GitHub user permission checker (Collaborator+) in src/services/AuthorizationService.ts
- [ ] T053 [US1] Create webhook handler for issue_comment events in src/webhooks/handlers/issue-comment-handler.ts
- [ ] T054 [US1] Implement AgentRun state machine (queued→started→succeeded/failed) in src/services/AgentRunStateMachine.ts
- [ ] T055 [US1] Implement Kubernetes Job creation service in src/services/KubernetesJobService.ts
- [ ] T056 [US1] Implement Issue context collector (body, comments, labels) in src/services/IssueContextCollector.ts
- [ ] T057 [US1] Implement agent type detector from Issue labels in src/services/AgentTypeDetector.ts
- [ ] T058 [US1] Add logging for trigger detection and authorization failures in src/webhooks/handlers/issue-comment-handler.ts
- [ ] T059 [US1] Implement GitHub status comment poster for execution start in src/services/GitHubNotificationService.ts

### Tests for User Story 1 (Test-First Development)

- [ ] T060 [P] [US1] Write contract tests for issue_comment webhook payload validation
- [ ] T061 [P] [US1] Write unit tests for TriggerDetectionService ("/run-agent" detection)
- [ ] T062 [P] [US1] Write unit tests for AuthorizationService (Collaborator+ check)
- [ ] T063 [US1] Write integration test for webhook → K8s Job creation flow

**Checkpoint**: At this point, User Story 1 should be fully functional and testable independently - webhook triggers K8s Pod execution

---

## Phase 4: User Story 2 - AI処理とPR作成 (Priority: P1)

**Goal**: AI エージェントが Issue 内容を元に変更を作成し、コミット・プッシュ・PR 作成までを自動化する

**Independent Test**: 変更対象がない最小リポジトリで、ブランチ作成→コミット→PR作成が確認できること

### Implementation for User Story 2

**Note**: US2 is mostly handled by agent-runner in Pod. Operator側は結果受信とPR管理のみ。

- [ ] T064 [P] [US2] Implement PullRequest upsert logic in PullRequestRepository (from agent-runner report)
- [ ] T065 [US2] Implement PR URL generation and storage to AgentRun record in agent-report-handler.ts
- [ ] T066 [US2] Add GitHub status comment for PR creation success in src/services/GitHubNotificationService.ts
- [ ] T067 [US2] Implement Discord notification for PR creation in src/services/DiscordNotificationService.ts
- [ ] T068 [US2] Handle agent-runner failure reports and extract error logs in agent-report-handler.ts

### Tests for User Story 2 (Test-First Development)

- [ ] T069 [P] [US2] Write unit tests for PullRequest upsert logic
- [ ] T070 [P] [US2] Write unit tests for GitHub notification service (PR created message)
- [ ] T071 [US2] Write integration test for agent-runner success report → PR record creation
- [ ] T072 [US2] Write integration test for agent-runner failure report → retry trigger

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently - full automation from comment to PR (via Pod)

---

## Phase 5: User Story 3 - Codexレビューと再試行 (Priority: P2)

**Goal**: 作成した PR を Codex にレビュー依頼し、戻り結果を AI にフィードバックして修正を再試行する（最大50回）

**Independent Test**: ダミーのレビュー応答を与えた場合、再実行が行われ新しいコミットが積み上がること

### Implementation for User Story 3

- [ ] T073 [P] [US3] Implement webhook handler for pull_request_review_comment events in src/webhooks/handlers/pr-review-comment-handler.ts
- [ ] T074 [P] [US3] Implement Codex review request service (post "@codex review" comment) in src/services/CodexReviewService.ts
- [ ] T075 [US3] Implement "@codex review" comment detection in src/utils/comment-parser.ts
- [ ] T076 [US3] Create Codex approval detector ("Codex Review: Didn't find any major issues.") in src/services/CodexApprovalDetector.ts
- [ ] T077 [US3] Implement webhook handler for check_suite events (CI results) in src/webhooks/handlers/check-suite-handler.ts
- [ ] T078 [US3] Create CI failure analyzer parsing check logs in src/services/CIFailureAnalyzer.ts
- [ ] T079 [US3] Implement retry orchestrator managing retry_count (max 50) in src/services/RetryOrchestrator.ts
- [ ] T080 [US3] Create feedback aggregator combining review + CI results in src/services/FeedbackAggregator.ts
- [ ] T081 [US3] Implement K8s Job re-creation with aggregated feedback for AI retry
- [ ] T082 [US3] Add retry count validation and failure threshold (50) in src/services/RetryOrchestrator.ts
- [ ] T083 [US3] Implement failure notification to GitHub Issue on max retries in src/services/GitHubNotificationService.ts
- [ ] T084 [US3] Implement failure notification to Discord webhook on max retries in src/services/DiscordNotificationService.ts
- [ ] T085 [US3] Add GitHub status comment updates for retry progress in src/services/GitHubNotificationService.ts
- [ ] T086 [US3] Store ReviewFeedback records for each review cycle in ReviewFeedbackRepository

### Tests for User Story 3 (Test-First Development)

- [ ] T087 [P] [US3] Write unit tests for CodexApprovalDetector (approval pattern matching)
- [ ] T088 [P] [US3] Write unit tests for RetryOrchestrator (max 50 retries logic)
- [ ] T089 [P] [US3] Write unit tests for FeedbackAggregator (review + CI combination)
- [ ] T090 [US3] Write integration test for CI failure → retry → success flow

**Checkpoint**: All user stories 1-3 should now be independently functional - quality loop with automatic improvements

---

## Phase 6: User Story 4 - Approveと自動マージ (Priority: P2)

**Goal**: PR が承認状態 (CI success + Codex approve + no conflicts) になったら自動でマージする

**Independent Test**: PR に Approve を付与すると自動でマージされること

### Implementation for User Story 4

- [ ] T091 [P] [US4] Implement merge condition checker (CI + Codex + conflicts) in src/services/MergeConditionChecker.ts
- [ ] T092 [P] [US4] Create CI status aggregator from check_suite events in src/services/CIStatusAggregator.ts
- [ ] T093 [US4] Implement merge conflict detector via GitHub API in src/services/MergeConflictDetector.ts
- [ ] T094 [US4] Create auto-merge service with merge API call in src/services/AutoMergeService.ts
- [ ] T095 [US4] Implement webhook handler for status events (CI completion) in src/webhooks/handlers/status-handler.ts
- [ ] T096 [US4] Add merge condition re-evaluation on Codex approval comment in src/webhooks/handlers/pr-review-comment-handler.ts
- [ ] T097 [US4] Add merge condition re-evaluation on CI success in src/webhooks/handlers/check-suite-handler.ts
- [ ] T098 [US4] Implement merge failure handling and notification in src/services/AutoMergeService.ts
- [ ] T099 [US4] Add GitHub status comment for merge success/failure in src/services/GitHubNotificationService.ts
- [ ] T100 [US4] Add Discord notification for merge events in src/services/DiscordNotificationService.ts

### Tests for User Story 4 (Test-First Development)

- [ ] T101 [P] [US4] Write unit tests for MergeConditionChecker (DoD validation)
- [ ] T102 [P] [US4] Write unit tests for AutoMergeService (GitHub merge API call)
- [ ] T103 [US4] Write integration test for approve + CI success → auto-merge flow

**Checkpoint**: All user stories 1-4 should now be independently functional - complete automation from comment to merge

---

## Phase 7: User Story 5 - ブロック解除によるタスク再開 (Priority: P3)

**Goal**: 依存関係を持つタスクがブロックされている場合、完了によりブロックが解消されたタスクに自動着手する

**Independent Test**: 依存関係グラフでブロック解除時に次タスク実行が開始されること

### Implementation for User Story 5

- [ ] T104 [P] [US5] Implement Issue blocker parser ("blocked by" / "blocking" syntax) in src/services/IssueBlockerParser.ts
- [ ] T105 [P] [US5] Create directed graph data structure for dependencies in src/lib/dependency-graph.ts
- [ ] T106 [US5] Implement blocker graph builder from Issue metadata in src/services/BlockerGraphBuilder.ts
- [ ] T107 [US5] Create circular dependency detector with cycle detection algorithm in src/services/CircularDependencyDetector.ts
- [ ] T108 [US5] Implement webhook handler for issues events (closed/reopened) in src/webhooks/handlers/issues-handler.ts
- [ ] T109 [US5] Create blocked task resolver finding unblocked tasks in src/services/BlockedTaskResolver.ts
- [ ] T110 [US5] Implement dependency validation preventing out-of-order execution in src/services/DependencyValidator.ts
- [ ] T111 [US5] Implement automatic K8s Job trigger for unblocked tasks in src/services/BlockedTaskResolver.ts
- [ ] T112 [US5] Add GitHub status comment for dependency violations in src/services/GitHubNotificationService.ts
- [ ] T113 [US5] Add logging for blocker graph updates and task resumption in src/services/BlockedTaskResolver.ts

### Tests for User Story 5 (Test-First Development)

- [ ] T114 [P] [US5] Write unit tests for IssueBlockerParser (dependency parsing)
- [ ] T115 [P] [US5] Write unit tests for CircularDependencyDetector (cycle detection)
- [ ] T116 [US5] Write integration test for Issue close → unblock → auto-trigger flow

**Checkpoint**: All user stories should now be independently functional - complete system with dependency management

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T117 [P] Add comprehensive error messages and user-facing error codes in src/utils/error-codes.ts
- [ ] T118 [P] Implement rate limiting for GitHub API calls in src/lib/github-client.ts
- [ ] T119 [P] Add metrics collection for execution times and success rates in src/services/MetricsService.ts
- [ ] T120 [P] Create health check endpoint for webhook server in src/webhooks/server.ts
- [ ] T121 [P] Add environment variable validation on startup in src/config/env.ts
- [ ] T122 [P] Implement graceful shutdown handler in src/webhooks/server.ts
- [ ] T123 [P] Add/optimize SQL indexes via goose migrations in `migrations/*.sql`
- [ ] T124 [P] Create README.md with setup and deployment instructions
- [ ] T125 [P] Document API client configurations in docs/api-clients.md
- [ ] T126 [P] Add inline code documentation with JSDoc comments
- [ ] T127 Run quickstart.md validation checklist
- [ ] T128 Perform security audit for secret handling and authorization
- [ ] T129 [P] Add performance optimization for blocker graph queries with SQL indexes (goose)
- [ ] T130 [P] Implement caching for GitHub API responses where appropriate

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Story 1 (Phase 3)**: Depends on Foundational phase completion
- **User Story 2 (Phase 4)**: Depends on Foundational phase completion + integrates with US1
- **User Story 3 (Phase 5)**: Depends on Foundational phase completion + requires US2 PR creation
- **User Story 4 (Phase 6)**: Depends on Foundational phase completion + requires US2 PR + US3 reviews
- **User Story 5 (Phase 7)**: Depends on Foundational phase completion + can work independently
- **Polish (Phase 8)**: Depends on all desired user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 2 (P1)**: Requires US1 trigger mechanism, builds on top of it
- **User Story 3 (P2)**: Requires US2 PR creation to have something to review
- **User Story 4 (P2)**: Requires US2 PRs and US3 review results to evaluate merge conditions
- **User Story 5 (P3)**: Can start after Foundational (Phase 2) - Independent, manages execution order

**Note**: US1 and US5 are relatively independent; US2→US3→US4 form a sequential enhancement chain.

### Within Each User Story

- Models before services (repositories depend on models)
- Core services before orchestrators
- Webhook handlers integrate existing services
- Notification services are last in each phase

### Parallel Opportunities

**Setup Phase (Phase 1):**
- T003, T004, T005, T006, T007 can all run in parallel

**Foundational Phase (Phase 2):**
- T008-T010 must run sequentially (GORM models/DB init → goose migration)
- T011-T015 (all repositories) can run in parallel after DB setup
- T017-T019 (all API clients) can run in parallel
- T021-T023 (logging, retry, config) can run in parallel

**User Story 1 (Phase 3):**
- T025, T026 can run in parallel
- T034 (notifications) independent of core flow

**User Story 2 (Phase 4):**
- T035, T036 can run in parallel

**User Story 3 (Phase 5):**
- T046, T047 can run in parallel
- Review and CI handling are parallel concerns

**User Story 4 (Phase 6):**
- T060, T061 can run in parallel

**User Story 5 (Phase 7):**
- T070, T071 can run in parallel

**Polish Phase (Phase 8):**
- Almost all tasks marked [P] can run in parallel (T081-T090, T093-T094)

**Once Foundational phase completes:**
- US1 can start immediately
- US5 can start in parallel with US1 (independent)
- US2-US4 form a sequential chain but different developers can work ahead with stubs

---

## Parallel Example: User Story 2

```bash
# Launch API clients in parallel (after DB setup):
Task: "T035 - Create AI agent client interface in src/lib/ai-agent-client.ts"
Task: "T036 - Implement Git operations wrapper in src/lib/git-operations.ts"

# After AI integration is ready, these can be parallelized:
Task: "T040 - Implement commit message generator from AI output in src/utils/commit-message-generator.ts"
Task: "T043 - Implement error handling for Git operation failures in src/services/AgentExecutionService.ts"
```

---

## Implementation Strategy

### MVP First (User Stories 1 + 2 Only)

1. Complete Phase 1: Setup (7 tasks)
2. Complete Phase 2: Foundational (17 tasks) - CRITICAL foundation with GORM + goose
3. Complete Phase 3: User Story 1 (10 tasks)
4. Complete Phase 4: User Story 2 (11 tasks)
5. **STOP and VALIDATE**: Test end-to-end flow from comment to PR
6. Deploy to staging environment

**MVP Value**: Automated PR creation from Issue comments - immediate productivity gain

### Incremental Delivery

1. **Foundation** (Phase 1+2): 24 tasks → GORM + goose, webhooks, core infrastructure ready
2. **MVP** (+US1+US2): 21 tasks → Comment triggers AI agent, creates PR automatically
3. **Quality Loop** (+US3): 14 tasks → Automatic improvements via Codex review + CI retry (max 50)
4. **Full Automation** (+US4): 10 tasks → Auto-merge on approval - zero human intervention
5. **Dependency Management** (+US5): 11 tasks → Sequential task execution with blocking
6. **Production Ready** (+Polish): 14 tasks → Monitoring, docs, security hardening

Total: 94 tasks (comparable scope using GORM + goose)

### Parallel Team Strategy

With multiple developers after Foundational phase:

1. **Team completes Phase 1+2 together** (24 tasks, foundational with GORM + goose)
2. **Split work:**
   - Developer A: User Story 1 (webhook triggers)
   - Developer B: User Story 2 (AI + PR creation) - can work with stub from US1
   - Developer C: User Story 5 (dependency graph) - completely independent
3. **Sequential enhancements:**
   - Developer B continues to US3 (review loop) after US2
   - Developer B continues to US4 (auto-merge) after US3
4. **Final integration:** Merge all stories, test interactions
5. **Polish together:** Phase 8 can be parallelized across team

---

## Notes

- **[P] tasks**: Different files, no dependencies - safe to parallelize
- **[Story] labels**: Map tasks to user stories from spec.md for traceability
- **GORM**: Type-safe database access with ORM (T008-T010)
- **Idempotency**: Every webhook handler uses X-GitHub-Delivery for deduplication (T024)
- **Retry logic**: All external API calls use exponential backoff utility (T022)
- **Max 50 retries**: Enforced in RetryOrchestrator (T052, T055)
- **Notifications**: Dual channel (GitHub + Discord) for all failures (FR-008, FR-014)
- **Authorization**: Collaborator+ check in every webhook handler (FR-018, T028)
- **DoD**: CI success + Codex approve + no conflicts (SC-009)

### Success Criteria Mapping

- SC-001 (2min to start): US1 webhook processing + K8s Job creation (T053, T055)
- SC-002 (15min to PR): agent-runner execution in Pod (T030-T048)
- SC-003 (10min retry): US3 feedback loop + K8s Job re-creation (T079-T081)
- SC-004 (no duplicates): Idempotency middleware (T024)
- SC-005 (5min unblock): US5 blocker resolution (T109-T111)
- SC-006 (5min CI retry): US3 check_suite handler (T077-T078)
- SC-007 (2min merge): US4 auto-merge (T094)
- SC-008 (immediate notification): US3 failure notifications (T083-T084)
- SC-009 (DoD conditions): US4 merge checker (T091)

### Edge Cases Addressed

- Duplicate webhooks: T024 idempotency middleware
- Review response missing: T079 retry orchestrator with timeout
- Merge conflicts: T093 conflict detector blocks auto-merge
- Unlimited concurrency: No locking (運用側考慮 per FR-012)
- Circular dependencies: T107 cycle detector prevents deadlock
- Rate limits: T118 rate limiting for GitHub API
- Database queries: T123, T129 optimize with SQL indexes (goose)
- Operator API unreachable: agent-runner exponential backoff retry (T038, T047)

### GORM Model Highlights

```go
type AgentRun struct {
  IdempotencyKey string `gorm:"uniqueIndex"` // X-GitHub-Delivery
  RetryCount     int    `gorm:"default:0"`   // Max 50
  State          string // queued/started/succeeded/failed
  // ... other fields
}

type BlockerGraphEdge struct {
  TaskID          int `gorm:"primaryKey"`
  DependsOnTaskID int `gorm:"primaryKey"`
}
```

---

**Total Tasks**: 130 tasks organized across 8 phases

**Breakdown**:
- Phase 1 (Setup): 7 tasks
- Phase 2 (Foundational): 41 tasks (includes agent-runner + Operator API)
- Phase 3 (US1): 15 tasks
- Phase 4 (US2): 9 tasks
- Phase 5 (US3): 18 tasks
- Phase 6 (US4): 13 tasks
- Phase 7 (US5): 13 tasks
- Phase 8 (Polish): 14 tasks

**Critical Path**: Phase 1 → Phase 2 (with agent-runner) → US1 → US2 → US3 → US4

**MVP Scope**: Phase 1 + Phase 2 + US1 + US2 = **72 tasks** for basic K8s Pod-based automation

**Suggested First Delivery**:
1. Complete Phase 1-2 (Foundation with agent-runner): 48 tasks
2. Add US1 (Trigger): 15 tasks
3. Add US2 (PR Creation): 9 tasks
4. **Stop and validate**: End-to-end flow from GitHub comment → K8s Pod → PR creation

**Technology Stack**:
- Operator: Go 1.22 + Gin + GORM + client-go
- agent-runner: Go 1.22 + cobra CLI + Kubernetes Pod runtime
- Database: MySQL 8.0+ with goose migrations
- Testing: Go testing + testify (unit + integration + contract tests)
