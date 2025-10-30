# Tasks: GitHub Agent Automation

**Input**: Design documents from `/specs/001-github-agent-automation/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, quickstart.md

**Tests**: Tests are NOT explicitly requested in the feature specification, so test tasks are omitted.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Single project**: `src/`, `tests/` at repository root
- Paths assume single project structure based on plan.md

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure

- [x] T001 Create project structure with src/, tests/, config/, prisma/ directories
- [x] T002 Initialize Node.js/TypeScript project with package.json and tsconfig.json
- [x] T003 [P] Install core dependencies (express, @octokit/webhooks, @octokit/rest, axios, prisma, @prisma/client)
- [x] T004 [P] Configure TypeScript compiler options in tsconfig.json
- [x] T005 [P] Setup ESLint and Prettier configuration files
- [x] T006 [P] Create .env.example with required environment variables (GITHUB_TOKEN, GITHUB_WEBHOOK_SECRET, CODEX_API_KEY, DISCORD_WEBHOOK_URL)
- [x] T007 [P] Setup .gitignore for node_modules, .env, dist/

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Database & ORM Setup (Prisma)

- [ ] T008 Create Prisma schema in prisma/schema.prisma with 5 models (Issue, PullRequest, AgentRun, ReviewFeedback, BlockerGraphEdges)
- [ ] T009 Initialize Prisma Client singleton in src/lib/prisma.ts
- [ ] T010 Generate Prisma Client and run initial migration with `prisma migrate dev`

### Repository Layer (Prisma-based)

- [ ] T011 [P] Implement IssueRepository using Prisma Client in src/repositories/IssueRepository.ts
- [ ] T012 [P] Implement PullRequestRepository using Prisma Client in src/repositories/PullRequestRepository.ts
- [ ] T013 [P] Implement AgentRunRepository with idempotency check using Prisma Client in src/repositories/AgentRunRepository.ts
- [ ] T014 [P] Implement ReviewFeedbackRepository using Prisma Client in src/repositories/ReviewFeedbackRepository.ts
- [ ] T015 [P] Implement BlockerGraphRepository using Prisma Client in src/repositories/BlockerGraphRepository.ts

### Infrastructure & Services

- [ ] T016 Setup GitHub webhook server with signature verification in src/webhooks/server.ts
- [ ] T017 [P] Implement GitHub API client wrapper in src/lib/github-client.ts
- [ ] T018 [P] Implement Codex API client in src/lib/codex-client.ts
- [ ] T019 [P] Implement Discord webhook client in src/lib/discord-client.ts
- [ ] T020 Create global error handler middleware in src/middleware/error-handler.ts
- [ ] T021 [P] Setup structured logging with Winston in src/lib/logger.ts
- [ ] T022 [P] Implement retry utility with exponential backoff and jitter in src/utils/retry.ts
- [ ] T023 [P] Create environment configuration loader in src/config/env.ts
- [ ] T024 Implement idempotency middleware using X-GitHub-Delivery header in src/middleware/idempotency.ts

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: User Story 1 - コメント起動で自動実行 (Priority: P1) 🎯 MVP

**Goal**: Issue に所定のトリガ文字列 "/run-agent" を含むコメントが投稿されると、システムが当該 Issue 内容を取得し、AI エージェント実行を開始する

**Independent Test**: テストリポジトリでトリガコメント投稿→エージェント実行開始イベントが記録されること

### Implementation for User Story 1

- [ ] T025 [P] [US1] Create webhook event types definition in src/types/webhook-events.ts
- [ ] T026 [P] [US1] Implement trigger detection service in src/services/TriggerDetectionService.ts
- [ ] T027 [US1] Implement comment parser to detect "/run-agent" in src/utils/comment-parser.ts
- [ ] T028 [US1] Implement GitHub user permission checker (Collaborator+) in src/services/AuthorizationService.ts
- [ ] T029 [US1] Create webhook handler for issue_comment events in src/webhooks/handlers/issue-comment-handler.ts
- [ ] T030 [US1] Implement AgentRun state machine (queued→started→succeeded/failed) in src/services/AgentRunStateMachine.ts
- [ ] T031 [US1] Create queue service to enqueue agent runs in src/services/QueueService.ts
- [ ] T032 [US1] Implement Issue context collector (body, comments, labels) in src/services/IssueContextCollector.ts
- [ ] T033 [US1] Add logging for trigger detection and authorization failures in src/webhooks/handlers/issue-comment-handler.ts
- [ ] T034 [US1] Implement GitHub status comment poster for execution start in src/services/GitHubNotificationService.ts

**Checkpoint**: At this point, User Story 1 should be fully functional and testable independently - webhook triggers agent execution

---

## Phase 4: User Story 2 - AI処理とPR作成 (Priority: P1)

**Goal**: AI エージェントが Issue 内容を元に変更を作成し、コミット・プッシュ・PR 作成までを自動化する

**Independent Test**: 変更対象がない最小リポジトリで、ブランチ作成→コミット→PR作成が確認できること

### Implementation for User Story 2

- [ ] T035 [P] [US2] Create AI agent client interface in src/lib/ai-agent-client.ts
- [ ] T036 [P] [US2] Implement Git operations wrapper (branch, commit, push) in src/lib/git-operations.ts
- [ ] T037 [US2] Implement branch name generator (issue-based) in src/utils/branch-name-generator.ts
- [ ] T038 [US2] Create AgentExecutionService orchestrating AI + Git flow in src/services/AgentExecutionService.ts
- [ ] T039 [US2] Implement code generation handler calling AI agent in src/services/CodeGenerationService.ts
- [ ] T040 [US2] Implement commit message generator from AI output in src/utils/commit-message-generator.ts
- [ ] T041 [US2] Implement PR creation service with GitHub API in src/services/PullRequestCreationService.ts
- [ ] T042 [US2] Add PR link storage to AgentRun record in src/services/AgentExecutionService.ts
- [ ] T043 [US2] Implement error handling for Git operation failures in src/services/AgentExecutionService.ts
- [ ] T044 [US2] Add GitHub status comment for PR creation success/failure in src/services/GitHubNotificationService.ts
- [ ] T045 [US2] Implement Discord notification for agent execution results in src/services/DiscordNotificationService.ts

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently - full automation from comment to PR

---

## Phase 5: User Story 3 - Codexレビューと再試行 (Priority: P2)

**Goal**: 作成した PR を Codex にレビュー依頼し、戻り結果を AI にフィードバックして修正を再試行する（最大50回）

**Independent Test**: ダミーのレビュー応答を与えた場合、再実行が行われ新しいコミットが積み上がること

### Implementation for User Story 3

- [ ] T046 [P] [US3] Implement webhook handler for pull_request_review events in src/webhooks/handlers/pr-review-handler.ts
- [ ] T047 [P] [US3] Implement Codex review request service in src/services/CodexReviewService.ts
- [ ] T048 [US3] Implement "@codex review" comment detection in src/utils/comment-parser.ts
- [ ] T049 [US3] Create Codex approval detector ("Codex Review: Didn't find any major issues.") in src/services/CodexApprovalDetector.ts
- [ ] T050 [US3] Implement webhook handler for check_suite events (CI results) in src/webhooks/handlers/check-suite-handler.ts
- [ ] T051 [US3] Create CI failure analyzer parsing check logs in src/services/CIFailureAnalyzer.ts
- [ ] T052 [US3] Implement retry orchestrator managing retry_count (max 50) in src/services/RetryOrchestrator.ts
- [ ] T053 [US3] Create feedback aggregator combining review + CI results in src/services/FeedbackAggregator.ts
- [ ] T054 [US3] Implement AI re-execution with feedback context in src/services/CodeGenerationService.ts
- [ ] T055 [US3] Add retry count validation and failure threshold (50) in src/services/RetryOrchestrator.ts
- [ ] T056 [US3] Implement failure notification to GitHub Issue on max retries in src/services/GitHubNotificationService.ts
- [ ] T057 [US3] Implement failure notification to Discord webhook on max retries in src/services/DiscordNotificationService.ts
- [ ] T058 [US3] Add GitHub status comment updates for retry progress in src/services/GitHubNotificationService.ts
- [ ] T059 [US3] Store ReviewFeedback records for each review cycle in src/services/CodexReviewService.ts

**Checkpoint**: All user stories 1-3 should now be independently functional - quality loop with automatic improvements

---

## Phase 6: User Story 4 - Approveと自動マージ (Priority: P2)

**Goal**: PR が承認状態 (CI success + Codex approve + no conflicts) になったら自動でマージする

**Independent Test**: PR に Approve を付与すると自動でマージされること

### Implementation for User Story 4

- [ ] T060 [P] [US4] Implement merge condition checker (CI + Codex + conflicts) in src/services/MergeConditionChecker.ts
- [ ] T061 [P] [US4] Create CI status aggregator from check_suite events in src/services/CIStatusAggregator.ts
- [ ] T062 [US4] Implement merge conflict detector via GitHub API in src/services/MergeConflictDetector.ts
- [ ] T063 [US4] Create auto-merge service with merge API call in src/services/AutoMergeService.ts
- [ ] T064 [US4] Implement webhook handler for status events (CI completion) in src/webhooks/handlers/status-handler.ts
- [ ] T065 [US4] Add merge condition re-evaluation on Codex approval comment in src/webhooks/handlers/pr-review-handler.ts
- [ ] T066 [US4] Add merge condition re-evaluation on CI success in src/webhooks/handlers/check-suite-handler.ts
- [ ] T067 [US4] Implement merge failure handling and notification in src/services/AutoMergeService.ts
- [ ] T068 [US4] Add GitHub status comment for merge success/failure in src/services/GitHubNotificationService.ts
- [ ] T069 [US4] Add Discord notification for merge events in src/services/DiscordNotificationService.ts

**Checkpoint**: All user stories 1-4 should now be independently functional - complete automation from comment to merge

---

## Phase 7: User Story 5 - ブロック解除によるタスク再開 (Priority: P3)

**Goal**: 依存関係を持つタスクがブロックされている場合、完了によりブロックが解消されたタスクに自動着手する

**Independent Test**: 依存関係グラフでブロック解除時に次タスク実行が開始されること

### Implementation for User Story 5

- [ ] T070 [P] [US5] Implement Issue blocker parser ("blocked by" / "blocking" syntax) in src/services/IssueBlockerParser.ts
- [ ] T071 [P] [US5] Create directed graph data structure for dependencies in src/lib/dependency-graph.ts
- [ ] T072 [US5] Implement blocker graph builder from Issue metadata in src/services/BlockerGraphBuilder.ts
- [ ] T073 [US5] Create circular dependency detector with cycle detection algorithm in src/services/CircularDependencyDetector.ts
- [ ] T074 [US5] Implement webhook handler for issues events (closed/reopened) in src/webhooks/handlers/issues-handler.ts
- [ ] T075 [US5] Create blocked task resolver finding unblocked tasks in src/services/BlockedTaskResolver.ts
- [ ] T076 [US5] Implement dependency validation preventing out-of-order execution in src/services/DependencyValidator.ts
- [ ] T077 [US5] Add blocked task queue management in src/services/QueueService.ts
- [ ] T078 [US5] Implement automatic trigger for unblocked tasks in src/services/BlockedTaskResolver.ts
- [ ] T079 [US5] Add GitHub status comment for dependency violations in src/services/GitHubNotificationService.ts
- [ ] T080 [US5] Add logging for blocker graph updates and task resumption in src/services/BlockedTaskResolver.ts

**Checkpoint**: All user stories should now be independently functional - complete system with dependency management

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T081 [P] Add comprehensive error messages and user-facing error codes in src/utils/error-codes.ts
- [ ] T082 [P] Implement rate limiting for GitHub API calls in src/lib/github-client.ts
- [ ] T083 [P] Add metrics collection for execution times and success rates in src/services/MetricsService.ts
- [ ] T084 [P] Create health check endpoint for webhook server in src/webhooks/server.ts
- [ ] T085 [P] Add environment variable validation on startup in src/config/env.ts
- [ ] T086 [P] Implement graceful shutdown handler in src/webhooks/server.ts
- [ ] T087 [P] Optimize Prisma Client queries with proper indexes in prisma/schema.prisma
- [ ] T088 [P] Create README.md with setup and deployment instructions
- [ ] T089 [P] Document API client configurations in docs/api-clients.md
- [ ] T090 [P] Add inline code documentation with JSDoc comments
- [ ] T091 Run quickstart.md validation checklist
- [ ] T092 Perform security audit for secret handling and authorization
- [ ] T093 [P] Add performance optimization for blocker graph queries with Prisma
- [ ] T094 [P] Implement caching for GitHub API responses where appropriate

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
- T008-T010 must run sequentially (Prisma schema → Client init → Migration)
- T011-T015 (all repositories) can run in parallel after Prisma setup
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
# Launch API clients in parallel (after Prisma setup):
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
2. Complete Phase 2: Foundational (17 tasks) - CRITICAL foundation with Prisma
3. Complete Phase 3: User Story 1 (10 tasks)
4. Complete Phase 4: User Story 2 (11 tasks)
5. **STOP and VALIDATE**: Test end-to-end flow from comment to PR
6. Deploy to staging environment

**MVP Value**: Automated PR creation from Issue comments - immediate productivity gain

### Incremental Delivery

1. **Foundation** (Phase 1+2): 24 tasks → Prisma ORM, webhooks, core infrastructure ready
2. **MVP** (+US1+US2): 21 tasks → Comment triggers AI agent, creates PR automatically
3. **Quality Loop** (+US3): 14 tasks → Automatic improvements via Codex review + CI retry (max 50)
4. **Full Automation** (+US4): 10 tasks → Auto-merge on approval - zero human intervention
5. **Dependency Management** (+US5): 11 tasks → Sequential task execution with blocking
6. **Production Ready** (+Polish): 14 tasks → Monitoring, docs, security hardening

Total: 94 tasks (reduced from 99 by using Prisma ORM)

### Parallel Team Strategy

With multiple developers after Foundational phase:

1. **Team completes Phase 1+2 together** (24 tasks, foundational with Prisma)
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
- **Prisma ORM**: Type-safe database access with auto-generated client (T008-T010)
- **Idempotency**: Every webhook handler uses X-GitHub-Delivery for deduplication (T024)
- **Retry logic**: All external API calls use exponential backoff utility (T022)
- **Max 50 retries**: Enforced in RetryOrchestrator (T052, T055)
- **Notifications**: Dual channel (GitHub + Discord) for all failures (FR-008, FR-014)
- **Authorization**: Collaborator+ check in every webhook handler (FR-018, T028)
- **DoD**: CI success + Codex approve + no conflicts (SC-009)

### Success Criteria Mapping

- SC-001 (2min to start): US1 webhook processing (T029)
- SC-002 (15min to PR): US2 agent execution speed (T038-T041)
- SC-003 (10min retry): US3 feedback loop (T052-T054)
- SC-004 (no duplicates): Idempotency middleware (T024)
- SC-005 (5min unblock): US5 blocker resolution (T075-T078)
- SC-006 (5min CI retry): US3 check_suite handler (T050-T051)
- SC-007 (2min merge): US4 auto-merge (T063)
- SC-008 (immediate notification): US3 failure notifications (T056-T057)
- SC-009 (DoD conditions): US4 merge checker (T060)

### Edge Cases Addressed

- Duplicate webhooks: T024 idempotency middleware
- Review response missing: T052 retry orchestrator with timeout
- Merge conflicts: T062 conflict detector blocks auto-merge
- Unlimited concurrency: No locking (運用側考慮 per FR-012)
- Circular dependencies: T073 cycle detector prevents deadlock
- Rate limits: T082 rate limiting for GitHub API
- Database queries: T087, T093 Prisma optimization with indexes

### Prisma Schema Highlights

```prisma
model AgentRun {
  idempotencyKey  String   @unique  // X-GitHub-Delivery
  retryCount      Int      @default(0)  // Max 50
  state           String   // queued/started/succeeded/failed
  // ... other fields
}

model BlockerGraphEdges {
  @@id([taskId, dependsOnTaskId])  // Composite key
  // Bidirectional relations for dependency graph
}
```

---

**Total Tasks**: 94 tasks organized across 8 phases (5 tasks reduced by using Prisma)

**Critical Path**: Phase 1 → Phase 2 → US1 → US2 → US3 → US4 (MVP with quality loop)

**MVP Scope**: Phase 1 + Phase 2 + US1 + US2 = 45 tasks for basic automation

**Suggested First Delivery**: Complete through US2 for immediate value, then iterate with US3/US4

**Prisma Benefits**: Type safety, automatic migrations, reduced boilerplate code, better maintainability
