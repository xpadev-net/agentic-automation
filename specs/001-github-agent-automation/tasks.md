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

- [x] T001 Create project structure with cmd/, internal/, pkg/, tests/, migrations/, k8s/, agent-runner/ directories
- [x] T002 Initialize Go module with `go mod init` and base dependencies (gin, gorm, go-github, zap, testify)
- [x] T003 [P] Setup Makefile or task runner (build, test, goose up/down)
- [x] T004 [P] Configure logger (zap) and config loader in `internal/config`
- [x] T006 [P] Create .env.example with required environment variables (includes CODEX_BOT_USERNAME, OPERATOR_API_URL, OPERATOR_API_TOKEN)
- [x] T007 [P] Setup .gitignore for `bin/`, `.env`, `migrations/*.sql`, `*.pem`, `k8s/secrets/`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Database & ORM Setup (GORM + goose)

- [x] T008 Create GORM models in `internal/models/` (Issue, PullRequest, AgentRun, ReviewFeedback, CIStatus, BlockerGraphEdges, AuditLog, OperationLog)
- [x] T009 Implement GORM database connection in `internal/config/database.go`
- [x] T010 Create initial goose SQL migration `migrations/000001_init.sql` (single file with `-- +goose Up/Down`)

### Repository Layer (GORM-based)

- [x] T011 [P] Implement IssueRepository using GORM in `internal/repositories/issue.go`
- [x] T012 [P] Implement PullRequestRepository using GORM in `internal/repositories/pull_request.go`
- [x] T013 [P] Implement AgentRunRepository with idempotency check using GORM in `internal/repositories/agent_run.go`
- [x] T014 [P] Implement ReviewFeedbackRepository using GORM in `internal/repositories/review_feedback.go`
- [x] T015 [P] Implement BlockerGraphRepository using GORM in `internal/repositories/blocker_graph.go`

### Infrastructure & Services (Go)

- [x] T016 Setup GitHub webhook server (Gin) with signature verification in `internal/webhooks/server.go`
- [x] T017 [P] Implement GitHub API client wrapper using go-github in `internal/clients/github.go`
- [x] T018 [P] Implement Discord webhook client in `internal/clients/discord.go`
- [x] T019 [P] Implement Kubernetes client wrapper using client-go in `internal/clients/kubernetes.go`
- [x] T020 Create global error handler middleware in `internal/webhooks/middleware/error_handler.go`
- [x] T021 [P] Setup structured logging with zap in `internal/config/logger.go`
- [x] T022 [P] Implement retry utility with exponential backoff and jitter in `internal/utils/retry.go`
- [x] T023 [P] Create environment configuration loader in `internal/config/env.go`
- [x] T024 Implement idempotency middleware using X-GitHub-Delivery header in `internal/webhooks/middleware/idempotency.go`

### Operator API for Agent Report (NEW - Push型通知)

- [x] T025 [P] Create agent-report-handler in `internal/webhooks/handlers/agent_report.go` with Bearer token validation
- [x] T026 Add POST /api/agent-runs/:id/report route to Gin server in `internal/webhooks/server.go`
- [x] T027 [P] Implement AgentRunRepository.UpdateState for state transitions
- [x] T028 [P] Write unit tests for agent-report-handler (Bearer token validation, state updates) in `tests/unit/webhooks/`
- [x] T029 [P] Write integration test for Pod → Operator report flow with mock requests in `tests/integration/`

### Agent Runner Setup (NEW - Go binary in Pod)

- [x] T030 Create agent-runner Go project structure (agent-runner/main.go, pkg/, Dockerfile)
- [x] T031 Initialize go.mod with dependencies (cobra CLI framework, HTTP client, gopkg.in/yaml.v3)
- [x] T032 [P] Implement main.go CLI entry point with cobra command structure
- [x] T033 [P] Implement pkg/agent/executor.go (claude-code/cursor-agent execution)
- [x] T034 [P] Implement pkg/config/loader.go (load and parse .agent-config.yaml, see contracts/agent-manifest.md)
- [x] T034b [P] Implement pkg/config/types.go (Manifest, Hooks, Command struct definitions)
- [x] T035 [P] Implement pkg/hooks/runner.go (execute pre/validation/post hooks with timeout and error handling)
- [x] T035b [P] Implement pkg/hooks/errors.go (HookError type with command output capture)
- [x] T036 [P] Implement pkg/git/committer.go (git commit/push operations, return branch+SHA)
- [x] T037 [P] Implement pkg/git/diff.go (git diff detection for file changes)
- [x] T038 [P] Implement pkg/context/issue.go (Issue context parsing from args)
- [x] T039 [P] Implement pkg/reporter/client.go (Operator API client with exponential backoff retry)
- [x] T040 Update main.go execution flow: Clone → Restore session → Load manifest → Pre-hooks → Agent → Validation → Commit → Post-hooks
- [x] T041 Integrate reporter into main.go (call on success/failure, validation failures return error without API report)
- [x] T042 Create Dockerfile for agent-runner (multi-stage: Go build → Node.js runtime with agents)
- [x] T043 Create k8s/rbac.yaml (ServiceAccount, Role, RoleBinding for Pod permissions)
- [x] T044 Create k8s/pod-template.yaml using agent-runner image
- [x] T045 Build and push Docker image to container registry (ghcr.io or Docker Hub)
- [x] T046 [P] Write unit tests for agent executor (mock agent commands)
- [x] T047 [P] Write unit tests for manifest config loader (YAML parsing, version validation)
- [x] T048 [P] Write unit tests for hooks runner (timeout enforcement, required vs optional hooks)
- [x] T049 [P] Write unit tests for git committer (mock git commands)
- [x] T050 [P] Write unit tests for reporter client (mock HTTP requests, retry logic)
- [x] T051 Write integration test for full agent-runner execution flow with manifest (end-to-end)

### S3 Session Persistence (NEW - Session context across retries)

- [x] T052_S3 Add AWS SDK Go v2 and backoff dependencies to agent-runner/go.mod
- [x] T053_S3 [P] Implement pkg/storage/config.go (S3 configuration struct, env var loading)
- [x] T054_S3 [P] Implement pkg/storage/s3.go (S3 client with UsePathStyle support, Upload/Download with exponential backoff)
- [x] T055_S3 [P] Implement pkg/storage/session.go (SaveSession, RestoreSession, tar.gz compression/extraction)
- [x] T056_S3 [P] Implement pkg/storage/exclusion.go (credential file filtering, .env/.pem/.key exclusion)
- [x] T057_S3 Integrate session restore into main.go (step 3: call RestoreSession if retry_count > 0, BEFORE loading manifest)
- [x] T058_S3 Integrate session save into main.go (step 13: call SaveSession before reporting)
- [x] T059_S3 Add RETRY_COUNT environment variable to Kubernetes Pod template (k8s/pod-template.yaml)
- [x] T060_S3 Add S3 environment variables to Kubernetes Pod template (S3_ENDPOINT, S3_REGION, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY, S3_USE_PATH_STYLE, S3_MAX_RETRIES)
- [x] T061_S3 Update internal/clients/kubernetes.go to inject S3 config and RETRY_COUNT into Job creation
- [x] T062_S3 [P] Create scripts/setup-minio.sh for MinIO bucket initialization (create agent-sessions bucket)
- [x] T063_S3 [P] Write unit tests for S3 client (mock S3 API, test retry logic, path-style URLs)
- [x] T064_S3 [P] Write unit tests for session archive/restore (tar.gz operations, file exclusion)
- [x] T065_S3 [P] Write unit tests for exponential backoff retry (mock failures, verify backoff intervals)
- [x] T066_S3 Write integration test for session save/restore flow (MinIO test server, full lifecycle)
- [x] T067_S3 Write integration test for S3 failure scenarios (unavailable S3, Pod exit 1 verification)
- [x] T068_S3 Add migration for AgentRun.s3_session_key and session_saved_at columns (goose migration)

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: User Story 1 - コメント起動で自動実行 (Priority: P1) 🎯 MVP

**Goal**: Issue に所定のトリガ文字列 "/run-agent" を含むコメントが投稿されると、システムが当該 Issue 内容を取得し、AI エージェント実行を開始する

**Independent Test**: テストリポジトリでトリガコメント投稿→エージェント実行開始イベントが記録されること

### Implementation for User Story 1

- [x] T066 [P] [US1] Create webhook event types definition in internal/models/webhook_events.go
- [x] T067 [P] [US1] Implement trigger detection service in internal/services/trigger_detection.go
- [x] T068 [US1] Implement comment parser to detect "/run-agent" in internal/utils/comment_parser.go
- [x] T069 [US1] Implement GitHub user permission checker (Collaborator+) in internal/services/authorization.go
- [x] T070 [US1] Create webhook handler for issue_comment events in internal/webhooks/handlers/issue_comment.go
- [x] T071 [US1] Implement AgentRun state machine (queued→started→succeeded/failed) in internal/services/agent_run_state_machine.go
- [x] T072 [US1] Implement Kubernetes Job creation service in internal/services/kubernetes_job.go
- [x] T073 [US1] Implement Issue context collector (body, comments, labels) in internal/services/issue_context.go
- [x] T074 [US1] Implement agent type detector from Issue labels in internal/services/agent_type_detector.go
- [x] T075 [US1] Add logging for trigger detection and authorization failures in internal/webhooks/handlers/issue_comment.go
- [x] T076 [US1] Implement GitHub status comment poster for execution start in internal/services/github_notification.go

### Tests for User Story 1 (Test-First Development)

- [x] T077 [P] [US1] Write contract tests for issue_comment webhook payload validation
- [x] T078 [P] [US1] Write unit tests for TriggerDetectionService ("/run-agent" detection)
- [x] T079 [P] [US1] Write unit tests for AuthorizationService (Collaborator+ check)
- [x] T080 [US1] Write integration test for webhook → K8s Job creation flow

**Checkpoint**: At this point, User Story 1 should be fully functional and testable independently - webhook triggers K8s Pod execution

---

## Phase 4: User Story 2 - AI処理とPR作成 (Priority: P1)

**Goal**: AI エージェントが Issue 内容を元に変更を作成し、コミット・プッシュ・PR 作成までを自動化する

**Independent Test**: 変更対象がない最小リポジトリで、ブランチ作成→コミット→PR作成が確認できること

### Implementation for User Story 2

**Note**: US2 is mostly handled by agent-runner in Pod. Operator側は結果受信とPR管理のみ。

- [x] T081 [P] [US2] Implement PullRequest upsert logic in internal/repositories/pull_request.go (from agent-runner report)
- [x] T082 [US2] Implement PR URL generation and storage to AgentRun record in internal/webhooks/handlers/agent_report.go
- [x] T083 [US2] Add GitHub status comment for PR creation success in internal/services/github_notification.go
- [x] T084 [US2] Implement Discord notification for PR creation in internal/services/discord_notification.go
- [x] T085 [US2] Handle agent-runner failure reports and extract error logs in internal/webhooks/handlers/agent_report.go

### Tests for User Story 2 (Test-First Development)

- [x] T086 [P] [US2] Write unit tests for PullRequest upsert logic
- [x] T087 [P] [US2] Write unit tests for GitHub notification service (PR created message)
- [x] T088 [US2] Write integration test for agent-runner success report → PR record creation
- [x] T089 [US2] Write integration test for agent-runner failure report → retry trigger (US2では失敗レポートの「記録と要約抽出」までを検証し、自動リトライ実行は含めない。リトライの実行はUS3のT096/T098で検証)

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently - full automation from comment to PR (via Pod)

---

## Phase 5: User Story 3 - Codexレビューと再試行 (Priority: P2)

**Goal**: 作成した PR を Codex にレビュー依頼し、戻り結果を AI にフィードバックして修正を再試行する（最大50回）

**Independent Test**: ダミーのレビュー応答を与えた場合、再実行が行われ新しいコミットが積み上がること

### Implementation for User Story 3

- [x] T090 [P] [US3] Implement webhook handler for pull_request_review_comment events in internal/webhooks/handlers/pr_review_comment.go
- [x] T091 [P] [US3] Implement Codex review request service (post "@codex review" comment) in internal/services/codex_review.go
- [x] T092 [US3] Implement "@codex review" comment detection in internal/utils/comment_parser.go
- [x] T093 [US3] Create Codex approval detector ("Codex Review: Didn't find any major issues.") in internal/services/codex_approval.go
- [x] T094 [US3] Implement webhook handler for check_suite events (CI results) in internal/webhooks/handlers/check_suite.go
- [x] T095 [US3] Create CI failure analyzer parsing check logs in internal/services/ci_failure.go
- [x] T096 [US3] Implement retry orchestrator managing retry_count (max 50) in internal/services/retry_orchestrator.go
- [x] T097 [US3] Create feedback aggregator combining review + CI results in internal/services/feedback_aggregator.go
- [x] T098 [US3] Implement K8s Job re-creation with aggregated feedback for AI retry in internal/services/kubernetes_job.go
- [x] T099 [US3] Add retry count validation and failure threshold (50) in internal/services/retry_orchestrator.go
- [x] T100 [US3] Implement failure notification to GitHub Issue on max retries in internal/services/github_notification.go
- [x] T101 [US3] Implement failure notification to Discord webhook on max retries in internal/services/discord_notification.go
- [x] T102 [US3] Add GitHub status comment updates for retry progress in internal/services/github_notification.go
- [x] T103 [US3] Store ReviewFeedback records for each review cycle in internal/repositories/review_feedback.go

### Tests for User Story 3 (Test-First Development)

- [x] T104 [P] [US3] Write unit tests for CodexApprovalDetector (approval pattern matching)
- [x] T105 [P] [US3] Write unit tests for RetryOrchestrator (max 50 retries logic)
- [x] T106 [P] [US3] Write unit tests for FeedbackAggregator (review + CI combination)
- [ ] T107 [US3] Write integration test for CI failure → retry → success flow

**Checkpoint**: All user stories 1-3 should now be independently functional - quality loop with automatic improvements

---

## Phase 6: User Story 4 - Approveと自動マージ (Priority: P2)

**Goal**: PR が承認状態 (CI success + Codex approve + no conflicts) になったら自動でマージする

**Independent Test**: PR に Approve を付与すると自動でマージされること

### Implementation for User Story 4

- [x] T108 [P] [US4] Implement merge condition checker (CI + Codex + conflicts) in internal/services/merge_condition.go
- [x] T109 [P] [US4] Create CI status aggregator from check_suite events in internal/services/ci_status_aggregator.go
- [x] T110 [US4] Implement merge conflict detector via GitHub API in internal/services/merge_conflict_detector.go
- [x] T111 [US4] Create auto-merge service with merge API call in internal/services/auto_merge.go
- [x] T112 [US4] Implement webhook handler for status events (CI completion) in internal/webhooks/handlers/status.go
- [x] T113 [US4] Add merge condition re-evaluation on Codex approval comment in internal/webhooks/handlers/pr_review_comment.go
- [x] T114 [US4] Add merge condition re-evaluation on CI success in internal/webhooks/handlers/check_suite.go
- [x] T115 [US4] Implement merge failure handling and notification in internal/services/auto_merge.go
- [x] T116 [US4] Add GitHub status comment for merge success/failure in internal/services/github_notification.go
- [x] T117 [US4] Add Discord notification for merge events in internal/services/discord_notification.go

### Tests for User Story 4 (Test-First Development)

- [x] T118 [P] [US4] Write unit tests for MergeConditionChecker (DoD validation)
- [x] T119 [P] [US4] Write unit tests for AutoMergeService (GitHub merge API call)
- [x] T120 [US4] Write integration test for approve + CI success → auto-merge flow

**Checkpoint**: All user stories 1-4 should now be independently functional - complete automation from comment to merge

---

## Phase 7: User Story 5 - ブロック解除によるタスク再開 (Priority: P3)

**Goal**: 依存関係を持つタスクがブロックされている場合、完了によりブロックが解消されたタスクに自動着手する

**Independent Test**: 依存関係グラフでブロック解除時に次タスク実行が開始されること

### Implementation for User Story 5

- [x] T121 [P] [US5] Implement Issue dependency fetcher using GitHub API (GET /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocked_by and /blocking) in internal/services/issue_dependency_fetcher.go
- [x] T122 [P] [US5] Create directed graph data structure for dependencies in internal/utils/dependency_graph.go
- [ ] T123 [US5] Implement blocker graph builder from GitHub API dependency data in internal/services/blocker_graph.go
- [ ] T124 [US5] Create circular dependency detector with cycle detection algorithm in internal/services/circular_dependency_detector.go
- [ ] T125 [US5] Implement webhook handler for issues events (closed/reopened) in internal/webhooks/handlers/issues.go
- [ ] T126 [US5] Create blocked task resolver finding unblocked tasks in internal/services/blocked_task.go
- [ ] T127 [US5] Implement dependency validation preventing out-of-order execution in internal/services/dependency_validator.go
- [ ] T128 [US5] Implement automatic K8s Job trigger for unblocked tasks in internal/services/blocked_task.go
- [ ] T129 [US5] Add GitHub status comment for dependency violations in internal/services/github_notification.go
- [ ] T130 [US5] Add logging for blocker graph updates and task resumption in internal/services/blocked_task.go

### Tests for User Story 5 (Test-First Development)

- [ ] T131 [P] [US5] Write unit tests for IssueDependencyFetcher (GitHub API integration, error handling)
- [ ] T132 [P] [US5] Write unit tests for CircularDependencyDetector (cycle detection)
- [ ] T133 [US5] Write integration test for Issue close → unblock → auto-trigger flow

**Checkpoint**: All user stories should now be independently functional - complete system with dependency management

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T134 [P] Add comprehensive error messages and user-facing error codes in internal/utils/error_codes.go
- [ ] T135 [P] Implement rate limiting for GitHub API calls in internal/clients/github.go
- [ ] T136 [P] Add metrics collection for execution times and success rates in internal/services/metrics.go
- [ ] T137 [P] Create health check endpoint for webhook server in internal/webhooks/server.go
- [ ] T138 [P] Add environment variable validation on startup in internal/config/env.go
- [ ] T139 [P] Implement graceful shutdown handler in internal/webhooks/server.go
- [ ] T140 [P] Add/optimize SQL indexes via goose migrations in `migrations/*.sql`
- [ ] T141 [P] Create README.md with setup and deployment instructions
- [ ] T142 [P] Document API client configurations in docs/api-clients.md
- [ ] T143 [P] Add inline code documentation with Go doc comments
- [ ] T144 Run quickstart.md validation checklist
- [ ] T145 Perform security audit for secret handling and authorization
- [ ] T146 [P] Add performance optimization for blocker graph queries with SQL indexes (goose)
- [ ] T147 [P] Implement caching for GitHub API responses where appropriate

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
- T104, T105 can run in parallel

**Polish Phase (Phase 8):**
- Almost all tasks marked [P] can run in parallel (T081-T107, T110-T111)

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
- SC-003 (10min retry): US3 feedback loop + K8s Job re-creation (T113-T081)
- SC-004 (no duplicates): Idempotency middleware (T024)
- SC-005 (5min unblock): US5 blocker resolution (T126-T128)
- SC-006 (5min CI retry): US3 check_suite handler (T111-T112)
- SC-007 (2min merge): US4 auto-merge (T111)
- SC-008 (immediate notification): US3 failure notifications (T083-T084)
- SC-009 (DoD conditions): US4 merge checker (T108)

### Edge Cases Addressed

- Duplicate webhooks: T024 idempotency middleware
- Review response missing: T113 retry orchestrator with timeout
- Merge conflicts: T110 conflict detector blocks auto-merge
- Unlimited concurrency: No locking (運用側考慮 per FR-012)
- Circular dependencies: T124 cycle detector prevents deadlock
- Rate limits: T135 rate limiting for GitHub API
- Database queries: T140, T146 optimize with SQL indexes (goose)
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

**Total Tasks**: 147 tasks organized across 8 phases

**Breakdown**:
- Phase 1 (Setup): 7 tasks
- Phase 2 (Foundational): 58 tasks (includes agent-runner + Operator API + S3 session persistence)
  - Agent Runner: 24 tasks (T025-T048)
  - S3 Session Persistence: 17 tasks (T049_S3-T065_S3)
  - Operator API: 17 tasks (T025-T024, foundational services)
- Phase 3 (US1): 15 tasks (T066-T080)
- Phase 4 (US2): 9 tasks (T081-T089)
- Phase 5 (US3): 18 tasks (T090-T107)
- Phase 6 (US4): 13 tasks (T108-T120)
- Phase 7 (US5): 13 tasks (T121-T133)
- Phase 8 (Polish): 14 tasks (T134-T147)

**Critical Path**: Phase 1 → Phase 2 (with agent-runner + S3) → US1 → US2 → US3 → US4

**MVP Scope**: Phase 1 + Phase 2 + US1 + US2 = **89 tasks** for K8s Pod-based automation with session persistence

**Suggested First Delivery**:
1. Complete Phase 1-2 (Foundation with agent-runner + S3): 65 tasks
2. Add US1 (Trigger): 15 tasks
3. Add US2 (PR Creation): 9 tasks
4. **Stop and validate**: End-to-end flow from GitHub comment → K8s Pod → PR creation with session persistence

**Technology Stack**:
- Operator: Go 1.22 + Gin + GORM + client-go
- agent-runner: Go 1.22 + cobra CLI + AWS SDK Go v2 (S3) + Kubernetes Pod runtime
- Storage: MinIO (S3-compatible) for AI agent session persistence
- Database: MySQL 8.0+ with goose migrations
- Testing: Go testing + testify (unit + integration + contract tests)

---

## Maintenance: PAT 廃止対応（GitHub App 統一）

- [x] ドキュメントから `GITHUB_TOKEN` 記述を削除（`docs/deployment-manual.md`, `k8s/pod-template.yaml`, `specs/.../quickstart.md`, CI ワークフロー）
- [x] `agent-runner/main.go` から GitHubToken フィールドと参照を削除
- [x] `internal/webhooks/handlers/issue_comment.go` の `GITHUB_APP_TEST_ALLOW_TOKEN` 分岐を削除
- [x] テストを GitHub App モック前提に移行（PAT 依存除去）
