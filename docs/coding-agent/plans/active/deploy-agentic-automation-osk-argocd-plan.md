# Plan: Deploy agentic-automation to osk-cluster through Argo CD

- status: in_progress
- generated: 2026-09-30
- last_updated: 2026-09-30
- work_type: mixed

## Goal
- Add a secure, reproducible Argo CD-managed deployment for the agentic-automation Operator and its dynamic runner Jobs on `osk-cluster`, then validate the deployment without embedding credentials in Git.

## Definition of Done
- A dedicated `agentic-automation` namespace, Operator Service/Deployment, namespace-scoped RBAC, Vault-backed ExternalSecrets, database migration mechanism, and Argo CD Application are managed from `../infra`.
- Immutable Operator and runner image tags are available and deployed.
- The selected MySQL-compatible database, S3 bucket, Vault keys, and webhook route are configured.
- Argo CD reports the Application `Synced` and `Healthy`; migrations, rollout, and `/health` checks pass.
- A controlled webhook-to-runner smoke test completes or is explicitly user-waived with residual risk recorded.
- Codex runners can authenticate without an API-key environment variable by copying a Vault-backed read-only `auth.json` Secret input into a writable per-Job Codex home with mode `0600`.

## Planner-added requirements
- Use immutable `sha-*` image tags. Needed because: Argo CD cannot prove or roll back the exact release when mutable `latest` tags are used.
- Keep credentials in Vault via External Secrets and commit no secret values. Needed because: the deployment requires GitHub App, agent API, database, S3, and internal API credentials.
- Run database migrations before the Operator rollout. Needed because: the application does not migrate at startup and requires the schema to serve correctly.
- Use a dedicated namespace with namespace-scoped RBAC. Needed because: the Operator creates runner Jobs and reads Secrets in its own namespace.
- Require an end-to-end smoke test. Needed because: pod readiness alone does not verify GitHub authentication, Job creation, callbacks, and S3 persistence.
- Mount Codex OAuth credentials as read-only Secret input, initialize a restrictive writable per-Job copy, and never include it in session archives or logs. Needed because: `auth.json` contains access, identity, and refresh tokens.

## Scope / Non-goals
- Scope:
  - Runner support for Codex CLI OAuth `auth.json` authentication.
  - `../infra` workload IaC and osk Argo CD app-of-apps registration.
  - Release/image selection, ExternalSecret wiring, database migration, S3 configuration, RBAC, Service, and health validation.
  - Argo CD reconciliation and one controlled smoke test.
- Non-goals:
  - Redesigning application behavior.
  - Replacing shared cluster infrastructure beyond adding the database/bucket resources selected for this workload.
  - Changing the existing ARC runner scale set, which is separate from the deployed application.
  - Rotating unrelated credentials found elsewhere in `../infra`; exposed credentials discovered during research are reported separately.

## Design
- Chosen: GitOps workload manifests in `../infra/k8s/agentic-automation` plus an osk Argo CD child Application. Structure: one IaC owner for namespace, workload, RBAC, secrets, migrations, and release tags; ExternalSecrets cross the Vault trust boundary. Evolution: localized app directory and one app-of-apps entry make upgrades and deletion bounded. Verification: static renders and dry-runs are deterministic; the cross-system smoke test uses one controlled event, bounded waits, and sanitized status/log evidence. Operation: Argo self-heal/prune manages drift; immutable images make rollback observable. Human: cluster state maps directly to committed manifests. Safety: dedicated namespace, least-privilege RBAC, and no plaintext secrets.
- Alternative: apply the repository's existing template manifests directly with `kubectl`. Structure: fewer initial files but configuration is split between manual commands and templates. Evolution: drift and rollback are harder to audit. Verification: dry-run and rollout checks remain possible but desired state is not continuously reconciled. Operation: no automatic self-heal and mutable placeholders invite release ambiguity. Human: manual secret and namespace steps increase cognitive load. Safety: templates currently contain inconsistent namespaces and obsolete PAT-oriented configuration.
- Why chosen: the user explicitly identified the existing Argo CD IaC repository, and the cluster already uses an app-of-apps pattern backed by Vault External Secrets.

## Compatibility stance
- surface: GitHub webhook endpoint, GitHub App credentials, and runner callback configuration.
- stance: ask-user
- justification: these are boundary-crossing interfaces; the desired public hostname, GitHub App installation, agent provider, release, and Vault paths cannot be inferred safely from current repository state.
- Task_3 database-schema stance: migrate; the user selected the existing TiDB service, and all 11 repository migrations plus their rollback limitations must be validated before reconciliation.

## Context (workspace)
- Related files/areas:
  - `specs/001-github-agent-automation/k8s/operator/service.yaml`
  - `k8s/rbac.yaml`
  - `internal/clients/kubernetes.go`
  - `internal/config/database.go`
  - `.github/workflows/docker-build.yml`
  - `../infra/k8s/argocd/apps/osk/kustomization.yaml`
  - `../infra/k8s/osk-vault-secretstore/clustersecretstore.yml`
- Existing patterns or references:
  - osk uses `argocd-root-app` with child Applications, automated sync, and Vault-backed ExternalSecrets through `vault-backend`.
  - Current `master` is `139bd1e9e6ea1f0a4b25a3d3a6dea63e166d3087`; its latest image workflow run failed, while older runs succeeded.
  - The application supports MySQL only; the cluster's shared PostgreSQL service is incompatible.
- Design record consulted and deviations from its acceptance:
  - None found.

## Open Questions (max 3)
- None. User approved `agentic-automation.xpa.dev`, Vault prefix `agentic-automation/`, default agent `codex`, and repairing/rebuilding current `master` on 2026-09-30.

## Assumptions
- A1: The target namespace will be `agentic-automation` — source: user intent plus existing namespace isolation convention; confirm before execution.
- A2: SeaweedFS will provide the S3-compatible session store unless the user selects another backend — source: existing osk infrastructure; confirm in Task_1.
- A3: `../infra` remains the canonical desired-state repository and changes will be committed/pushed through its normal Git workflow — source: `../infra/k8s/argocd/root/osk/application.yml`.
- A4: Use the existing TiDB `basic-tidb.tidb-cluster.svc:4000` as the MySQL-compatible database — source: user decision on 2026-09-30 and live TidbCluster Ready status.
- A5: The user explicitly authorized copying the local `~/.codex/auth.json` into Vault and using it in runner Pods — source: user decision on 2026-09-30.

## Tasks

### Task_1: Confirm deployment dependencies and release artifacts
- type: research
- owns:
  - docs/coding-agent/plans/active/deploy-agentic-automation-osk-argocd-plan.md
  - read-only Kubernetes resources in namespaces argocd, tidb-cluster, seaweedfs, and agentic-automation
  - read-only GitHub Actions and container registry metadata for xpadev-net/agentic-automation
- depends_on: []
- description: |
  Resolve the three open questions, define and verify the required Vault key names without reading values, select the database/S3 endpoints, and verify the GitHub Actions and registry prerequisites needed to publish a later exact-SHA image pair.
- acceptance:
  - Target namespace, database endpoint/database/user, S3 endpoint/bucket, Vault key names, webhook hostname, default agent, and base source revision are recorded.
  - No credential values are printed or committed.
  - The Actions runner, Harbor connectivity, credentials, and workflow configuration required by Task_4 are checked; final release SHA and image pullability are deferred to Task_4 after OAuth support exists.
- validation:
  - kind: decision
    required: true
    owner: user
    detail: "Select webhook hostname, Vault path prefix, default agent, and current-master repair versus an older successful release; these decisions block Task_2 and later work."
  - kind: cluster-read
    required: true
    owner: orchestrator
    detail: "Read-only kubectl checks confirm Argo CD, database, SeaweedFS, External Secrets, storage, and namespace prerequisites."
  - kind: release-prerequisite-read
    required: true
    owner: orchestrator
    detail: "gh, runner, network, and registry evidence establish whether the exact-SHA build can run; final image evidence is owned by Task_4."

### Task_2: Provision external prerequisites securely
- type: chore
- owns:
  - Vault path selected for agentic-automation secrets
  - Cloudflare Tunnel public-hostname route selected for the Operator Service
  - TiDB database and application user on basic-tidb.tidb-cluster.svc:4000
  - pre-migration restore point or approved empty-database rollback procedure for the dedicated TiDB database
  - SeaweedFS session bucket selected for agentic-automation
- depends_on: [Task_1]
- description: |
  Populate the required credentials through a user-controlled secure channel, create the dedicated TiDB database/user and S3 bucket, and establish the public webhook route without exposing secret values to logs or Git.
- acceptance:
  - Vault contains the agreed key names for database URL, GitHub App ID/private key/webhook secret, Operator API token, Codex `auth.json`, S3 credentials, and optional Discord webhook.
  - The opaque contents of the authorized local `~/.codex/auth.json` are copied byte-for-byte to the agreed Vault key without printing, parsing, transforming, or logging token fields.
  - The TiDB database/user can authenticate from the target namespace and the S3 bucket is reachable.
  - Before migrations, either a restorable TiDB backup identifier is recorded or the dedicated database is proven empty and an approved rollback procedure records that it may be dropped and recreated if migration fails before accepting traffic.
  - The selected public hostname routes to the planned `agent-operator` Service endpoint, or the user records it as a preconfigured external prerequisite before reconciliation.
  - No secret values appear in Git, task output, shell history evidence, or Kubernetes manifests.
- validation:
  - kind: secure-input
    required: true
    owner: user
    detail: "Authorize the local ~/.codex/auth.json source and destination Vault key; copy it as one opaque payload, then validate only Vault metadata. ExternalSecret readiness is deferred to Task_5."
  - kind: prerequisite-readiness
    required: true
    owner: orchestrator
    detail: "Validate TiDB authentication/schema target, SeaweedFS bucket access, Vault key presence by metadata/readiness only, and public route reachability without printing secrets."
  - kind: restore-readiness
    required: true
    owner: orchestrator
    detail: "Establish and verify a TiDB backup/restore point, or prove the new database is empty and record the drop-and-recreate rollback procedure before any migration runs."

### Task_3: Support Codex OAuth auth-file authentication
- type: impl
- owns:
  - agent-runner/pkg/agent/executor.go
  - agent-runner/pkg/agent/executor_test.go
  - agent-runner/pkg/utils/pr_parser.go
  - agent-runner/pkg/utils/pr_parser_test.go
  - agent-runner/pkg/parser/plan.go
  - agent-runner/pkg/parser/plan_test.go
  - internal/clients/kubernetes.go
  - internal/clients/kubernetes_test.go
  - agent-runner/README.md
- depends_on: [Task_1]
- description: |
  Permit Codex execution when a valid auth file exists. Mount the Vault-backed Secret read-only as input, copy it into a restrictive writable per-Job Codex home before startup so Codex can refresh it during the run, and preserve API-key compatibility.
- acceptance:
  - Codex execution accepts either `CODEX_API_KEY`/`OPENAI_API_KEY` or an existing non-empty `~/.codex/auth.json`.
  - Dynamically created Codex Jobs mount only the `auth.json` Secret key as read-only input and initialize a writable per-Job `~/.codex/auth.json` with mode `0600`; non-Codex Jobs are unchanged.
  - Missing API-key and missing/empty auth file still fail before invoking Codex with a credential-safe error.
  - Existing session archive exclusion for `auth.json` remains effective and documented.
  - Rotation is documented as manual re-copy of the current local auth file to Vault followed by ExternalSecret refresh; a runner authentication failure must not retry indefinitely with stale credentials.
- validation:
  - kind: command
    required: true
    owner: worker
    detail: "rtk go test ./agent-runner/pkg/agent ./internal/clients with positive API-key, positive auth-file, and missing-credential cases executed."
  - kind: security
    required: true
    owner: reviewer
    detail: "Review verifies restrictive mount permissions, no token logging/archive path, API-key backward compatibility, and no credential values in tests or fixtures."

### Task_4: Build and publish the OAuth-capable release
- type: chore
- owns:
  - agentic-automation feature branch and OAuth-support commit
  - GitHub Actions Docker Build and Push run for that commit
  - immutable Operator and runner images for that exact commit SHA
  - .github/workflows/docker-build.yml
  - .github/workflows/docker-build-base.yml
  - Dockerfile
  - agent-runner/Dockerfile
  - agent-runner/Dockerfile.base
- depends_on: [Task_3]
- description: |
  Commit the reviewed OAuth support, publish both images for the exact commit through the repository workflow, and verify that the immutable tags are pullable before IaC references them.
- acceptance:
  - The OAuth-support code is committed on a non-default feature branch without rewriting history.
  - Operator and runner builds succeed for the same full commit, with immutable tags derived from that SHA.
  - Pullability is verified from the osk cluster path before workload reconciliation.
- validation:
  - kind: command
    required: true
    owner: orchestrator
    detail: "Run the repository Go test suite and vet before push; record exact commands and results."
  - kind: release
    required: true
    owner: orchestrator
    detail: "gh evidence shows both image jobs succeeded for the commit and an osk-cluster pull probe succeeds for both immutable tags."
  - kind: review
    required: true
    owner: reviewer
    detail: "Independent code/security review approves the OAuth-support diff and validation evidence before release use."

### Task_5: Add agentic-automation workload IaC
- type: impl
- owns:
  - ../infra/k8s/agentic-automation/**
- depends_on: [Task_4]
- description: |
  Add the namespace-scoped workload, configuration, RBAC, ExternalSecrets, probes, resources, and immutable image references. The initial schema was applied manually before deployment under the user's run-first validation ruling.
- acceptance:
  - The Operator, runner Job permissions, referenced Secrets, image pull configuration, and namespace are internally consistent.
  - The Deployment targets the already migrated TiDB schema at version 11; future migration automation is recorded as follow-up rather than blocking this initial run.
  - No plaintext credentials are committed.
  - The manifests use the selected MySQL-compatible database and S3-compatible session store.
- validation:
  - kind: render
    required: true
    owner: worker
    detail: "rtk kubectl kustomize ../infra/k8s/agentic-automation succeeds."
  - kind: schema
    required: true
    owner: worker
    detail: "rtk kubectl apply --dry-run=client -k ../infra/k8s/agentic-automation succeeds."
  - kind: security
    required: true
    owner: reviewer
    detail: "Review confirms no secret values, least-privilege RBAC, safe pod security settings, and consistent Secret references."
  - kind: migration-preflight
    required: true
    owner: reviewer
    detail: "Review all 11 migrations for TiDB v8.5 compatibility and idempotent execution; confirm either a pre-migration backup/restore point or the approved verified-empty database drop/recreate path, and document that image rollback does not roll back schema."

### Task_6: Register the osk Argo CD Application
- type: impl
- owns:
  - ../infra/k8s/argocd/apps/osk/agentic-automation.yml
  - ../infra/k8s/argocd/apps/osk/kustomization.yaml
- depends_on: [Task_5]
- description: |
  Register the workload in the existing osk app-of-apps hierarchy with automated reconciliation.
- acceptance:
  - The Application points to `k8s/agentic-automation` in `xpadev-net/infra` and targets the dedicated namespace.
  - Automated sync, prune, self-heal, ServerSideApply, and CreateNamespace are explicit.
  - The osk app-of-apps Kustomization renders with the new Application.
- validation:
  - kind: render
    required: true
    owner: worker
    detail: "rtk kubectl kustomize ../infra/k8s/argocd/apps/osk succeeds."
  - kind: server-dry-run
    required: true
    owner: orchestrator
    detail: "Server-side dry-run against osk-cluster succeeds for the workload and Argo Application."

### Task_7: Integrate, publish, and reconcile GitOps changes
- type: chore
- owns:
  - ../infra Git branch and deployment-scoped commit
  - xpadev-net/infra pull request for the deployment branch
  - argocd/argocd-root-app and argocd/agentic-automation Applications
  - agentic-automation namespace runtime resources
- depends_on: [Task_6]
- description: |
  Review the integrated diff, publish the infra change without rewriting history, merge or otherwise land it with user-authorized GitHub workflow, and observe Argo reconciliation.
- acceptance:
  - The infra change is reviewable and contains only deployment-scoped edits.
  - The landed revision is observed by `argocd-root-app` and the child Application becomes Synced/Healthy.
  - Migration Job succeeds and Operator Deployment becomes Available.
- validation:
  - kind: authorization
    required: true
    owner: user
    detail: "Authorize push/PR creation and, separately, landing the infra change so Argo CD may reconcile production resources; plan approval alone does not authorize merge."
  - kind: git-review
    required: true
    owner: reviewer
    detail: "Independent review approves the integrated IaC diff and required validation evidence."
  - kind: rollout
    required: true
    owner: orchestrator
    detail: "Argo status, migration Job, Deployment rollout, pods, events, logs, Service, ExternalSecrets, and /health are checked."
  - kind: migration-safety
    required: true
    owner: orchestrator
    detail: "Capture the pre-migration backup/restore identifier or verified-empty database rollback evidence, observe migration completion, and stop reconciliation on migration failure before accepting Operator health."
  - kind: auth-runtime
    required: true
    owner: reviewer
    detail: "After Argo creates the ExternalSecret and workload resources, use the published runner image and actual Secret-input to writable-Codex-home path to verify `codex login status` and one bounded `codex exec` succeed; confirm the writable copy can change without mutating the Kubernetes Secret, and record manual Vault rotation/revocation as the accepted operational limitation."

### Task_8: Run a controlled end-to-end smoke test
- type: test
- owns:
  - one explicitly identified test issue/event in xpadev-net/agentic-automation
  - runner Job created for that event in namespace agentic-automation
  - sanitized status/log evidence for that event
- depends_on: [Task_9]
- description: |
  Exercise the deployed webhook-to-runner path and capture operational evidence without exposing credentials.
- acceptance:
  - A signed GitHub event reaches the Operator and creates an AgentRun and runner Job.
  - The runner authenticates to GitHub, reports back to the Operator, and persists session state to S3.
  - Temporary test resources are removed or explicitly retained by user choice.
- validation:
  - kind: authorization
    required: true
    owner: user
    detail: "Authorize the identified GitHub test event before it is emitted."
  - kind: end-to-end
    required: true
    owner: reviewer
    detail: "Independently verify webhook receipt, database record, Job completion, callback, and S3 persistence from sanitized logs/status."

### Task_9: Enable unrestricted Codex execution and streaming progress
- type: code
- owns:
  - agent-runner/pkg/agent/executor.go
  - agent-runner/pkg/agent/executor_test.go
  - agent-runner/main.go
  - internal/clients/kubernetes.go
  - internal/clients/kubernetes_test.go
  - Codex runtime documentation directly describing flags and defaults
  - ../infra/k8s/agentic-automation/deployment.yaml
- depends_on: [Task_7]
- description: |
  Correct the failed smoke test by using Codex's unrestricted mode only for write-enabled execution, preserve read-only plan creation, emit JSONL progress as it arrives, and make `gpt-5.6-luna` the explicit default model.
- acceptance:
  - Write-enabled Codex execution uses `--dangerously-bypass-approvals-and-sandbox` without a simultaneous `--sandbox` argument.
  - Read-only/plan creation continues to use `--sandbox read-only` and never uses the dangerous bypass flag.
  - Codex runs with `--json`; JSONL is retained for final parsing and streamed to runner logs without losing stderr or deadlocking.
  - An unset model resolves to `gpt-5.6-luna`, and the GitOps Deployment explicitly supplies the same value.
- validation:
  - kind: unit
    required: true
    owner: worker
    detail: "Run focused agent-runner and Kubernetes client tests covering exact Codex arguments, safe read-only behavior, streaming output, and model propagation."
  - kind: integration
    required: true
    owner: orchestrator
    detail: "Build/publish immutable images, reconcile through Argo CD, and rerun Issue #304 through the webhook-to-PR path."
  - kind: security-review
    required: true
    owner: reviewer
    detail: "Confirm dangerous mode is bounded to write-enabled execution and no additional credentials or Kubernetes permissions are introduced."

## Task Waves (explicit parallel dispatch sets)

- Wave 1 (parallel): [Task_1]
- Wave 2 (parallel): [Task_2]
- Wave 3 (parallel): [Task_3]
- Wave 4 (parallel): [Task_4]
- Wave 5 (parallel): [Task_5]
- Wave 6 (parallel): [Task_6]
- Wave 7 (parallel): [Task_7]
- Wave 8 (parallel): [Task_9]
- Wave 9 (parallel): [Task_8]

## Rollback / Safety
- Pin both images to an immutable prior successful SHA and revert the infra commit to roll back application changes.
- Pause automated sync before emergency manual intervention; do not mutate long-lived resources outside Git while self-heal remains enabled.
- Database migrations must be forward-safe or have an explicit restore/rollback procedure before execution.
- Do not print Secret data. Validate only Secret/ExternalSecret names, keys, readiness, and workload references.
- The plaintext GitHub PAT discovered in the existing ARC manifest must be revoked/rotated separately and must not be copied into this deployment.

## Progress Log (append-only)

- 2026-09-30 01:10 Wave 0 completed: [research]
  - Summary: Confirmed osk Argo CD app-of-apps, Vault External Secrets, shared PostgreSQL incompatibility, existing SeaweedFS, and failed latest image build.
  - Validation evidence: Read-only kubectl and gh queries; repository and infra worktrees are clean on `master`.
  - Notes: No cluster or Git state was mutated.

- 2026-09-30 01:15 User decision recorded: existing TiDB selected.
  - Summary: Live TidbCluster `basic` is Ready; internal endpoint is `basic-tidb.tidb-cluster.svc:4000`.
  - Validation evidence: Three TiDB members and three TiKV stores report healthy/Up.
  - Notes: The checked-in cluster manifest explicitly warns it is not production-suitable; user selection is recorded with that residual risk.

- 2026-09-30 02:05 Wave 3 Worker completed: [Task_3]
  - Summary: Added API-key-or-auth-file admission, Codex-only Secret input and ephemeral auth-home initialization, tests, and operations documentation.
  - Validation evidence: `rtk go test ./internal/clients` 31 tests; `rtk go test ./pkg/agent` 33 tests; `rtk go test ./pkg/storage` 203 tests; targeted vet, gofmt, and diff check passed.
  - Notes: Worker report schema/ownership/evidence reconciled with no blockers. Orchestrator identified emptyDir non-root ownership as a focused Reviewer risk before integration approval.

- 2026-09-30 03:25 Waves 4-7 completed: [Task_4, Task_5, Task_6, Task_7]
  - Summary: Published immutable GHCR images at `sha-e617dc4`, merged osk GitOps IaC, reconciled both Argo Applications, and rolled out the Operator.
  - Validation evidence: image workflow 36603437723 succeeded; both GHCR manifests inspected; `argocd-root-app` and `agentic-automation` are Synced/Healthy at infra revision `106ec4d`; Deployment is 1/1; all five ExternalSecrets are Ready; `/health` reports database connected.
  - Notes: A live Pod using the released runner reproduced the Job auth initialization path, verified mode 0600, and reported `Logged in using ChatGPT`; the temporary Pod was deleted.

- 2026-09-30 03:25 Task_8 pending external prerequisites.
  - Summary: End-to-end webhook-to-runner testing cannot start because GitHub App ID/private key and the public Cloudflare route are not provisioned.
  - Validation evidence: Operator logs explicitly report missing GitHub App credentials while continuing to serve health checks.
  - Notes: No synthetic GitHub event was emitted.

- 2026-09-30 03:58 Wave 8 Worker completed: [Task_9]
  - Summary: Added write-only Codex dangerous bypass, preserved read-only plan execution, added JSONL progress streaming, and set `gpt-5.6-luna` as the code and GitOps default.
  - Validation evidence: Codex-focused tests, `pkg/agent` race tests (32), `internal/clients` tests (14), full agent-runner Go tests, gofmt, and diff checks passed.
  - Notes: No Worker blockers. Independent security/correctness review and live release/E2E validation remain pending.

## Decision Log (append-only; re-plans and major discoveries)

- 2026-09-30 01:10 Decision: Use GitOps through `../infra` rather than direct kubectl apply.
  - Trigger / new insight: osk already has a healthy Argo CD root Application and Vault-backed secret convention.
  - Plan delta (what changed): Added infra workload path, Argo child Application, immutable release, migration, and E2E tasks.
  - Tradeoffs considered: Direct apply is faster initially but creates drift and weak rollback provenance.
  - User approval: no
  - Record proposed: none; deployment-specific choice

- 2026-09-30 01:15 Decision: Use the existing TiDB cluster for application persistence.
  - Trigger / new insight: User explicitly selected TiDB; live cluster reports Ready with three healthy TiDB and TiKV members.
  - Plan delta (what changed): Removed the database backend choice from open questions and fixed Task_1 to validate/create a dedicated database and user on `basic-tidb.tidb-cluster.svc:4000`.
  - Tradeoffs considered: A dedicated MySQL deployment would isolate workload risk but add another stateful system; existing TiDB reduces operational footprint but carries the manifest's explicit non-production warning.
  - User approval: yes
  - Record proposed: none; environment-specific deployment choice

- 2026-09-30 01:20 Decision: Approve execution with proposed deployment defaults.
  - Trigger / new insight: User approved the reviewed plan and all remaining default choices.
  - Plan delta (what changed): Status moved to in_progress; hostname, Vault prefix, Codex default, and current-master release are fixed.
  - Tradeoffs considered: Current master requires repairing or successfully rerunning its failed build instead of deploying a stale successful image.
  - User approval: yes
  - Record proposed: none; execution authorization

- 2026-09-30 01:25 Discovery: Local Codex authentication is ChatGPT OAuth, not an API key.
  - Trigger / new insight: `~/.codex/auth.json` has `auth_mode=chatgpt`, a null `OPENAI_API_KEY`, and OAuth token fields.
  - Plan delta (what changed): Do not store short-lived access/refresh tokens as `CODEX_API_KEY`; Task_2 requires a durable OpenAI/Codex API key supplied through a secure channel.
  - Tradeoffs considered: Copying OAuth tokens would be immediately convenient but creates expiry, refresh, and credential-exposure risks and is incompatible with the runner's explicit API-key contract.
  - User approval: no; durable API key input required
  - Record proposed: none; credential prerequisite

- 2026-09-30 01:30 Decision: Support and deploy the local Codex OAuth auth file.
  - Trigger / new insight: User explicitly requested pushing `~/.codex/auth.json` to Vault and using it in runner Pods.
  - Plan delta (what changed): Added Task_3 for auth-file support and shifted workload/Argo/reconciliation/smoke-test tasks to Task_4–Task_7.
  - Tradeoffs considered: A durable API key is operationally simpler; OAuth preserves the user's existing login but stores a refresh token and requires file mounting.
  - User approval: yes
  - Record proposed: none; application-specific credential contract

- 2026-09-30 01:35 Decision: Use a writable per-Job auth copy with manual Vault rotation.
  - Trigger / new insight: Kubernetes Secret volumes are read-only while Codex may need to update its local auth storage; official OpenAI guidance recommends purpose-built access tokens for repeatable automation, but the user explicitly selected the existing auth file.
  - Plan delta (what changed): Added writable emptyDir initialization, runtime auth validation, manual re-copy/revocation procedure, and a post-code-change exact-SHA image build task.
  - Tradeoffs considered: A Codex access token or workload identity has a cleaner automation lifecycle; the selected personal OAuth file avoids new credential issuance but may require manual rotation and carries the user's identity.
  - User approval: yes for auth-file use; manual rotation recorded as residual operational limitation
  - Record proposed: none; deployment credential operation

- 2026-09-30 01:45 Progress: Codex auth file stored in Vault.
  - Trigger / new insight: User supplied a Vault token through local `.env` after the cluster's External Secrets role proved read-only.
  - Plan delta (what changed): `secret/agentic-automation/codex` now contains opaque property `auth_json` at version 1; Task_3 no longer waits for unrelated TiDB/S3 prerequisites.
  - Tradeoffs considered: Code support can proceed safely while the remaining Task_2 prerequisites are unresolved.
  - User approval: yes
  - Record proposed: none; execution progress

- 2026-09-30 02:15 Decision: Prefer initial runtime evidence over completing every automation layer first.
  - Trigger / new insight: User explicitly approved running the deployment first and correcting observed failures.
  - Plan delta (what changed): Applied all 11 migrations manually to a verified-empty dedicated TiDB database; Task_5 may deploy against schema version 11 without first adding an Argo migration Job. Future migration automation remains follow-up work.
  - Tradeoffs considered: This reduces time to first runtime evidence but leaves future schema rollout automation incomplete.
  - User approval: yes
  - Record proposed: none; time-bounded operational choice

- 2026-09-30 02:35 Discovery: Harbor is unreachable from the osk Actions runner.
  - Trigger / new insight: The master Docker workflow failed at Harbor login with `no route to host` before either image build/push step.
  - Plan delta (what changed): Task_4 now owns a focused workflow fix to publish the exact-SHA release to reachable GHCR; Task_5 image references will use GHCR.
  - Tradeoffs considered: Fixing Harbor networking is broader infrastructure work; GHCR is already authenticated by the workflow and is sufficient for the initial deployment.
  - User approval: covered by the explicit run-first/fix-on-failure ruling
  - Record proposed: none; release transport adjustment

- 2026-09-30 02:55 Discovery: Runtime Dockerfiles and the base-image workflow still depend on Harbor.
  - Trigger / new insight: The GHCR-only release workflow reached Buildx successfully, then failed while resolving Harbor-hosted builder/runtime base images. The equivalent private GHCR runner base already exists and is pullable with registry credentials.
  - Plan delta (what changed): Task_4 now owns all three Dockerfiles plus the runner-base workflow; public language/runtime bases move to Docker Hub and the runner runtime base moves to GHCR. The base workflow becomes GHCR-only for future refreshes.
  - Tradeoffs considered: Rebuilding the full runner toolchain in every release avoids a private base dependency but materially increases build time; retaining the prebuilt base on GHCR preserves the current layering model.
  - User approval: yes; user explicitly directed use of GHCR because Harbor is destroyed
  - Record proposed: none; registry migration detail

- 2026-09-30 03:15 Discovery: The pre-existing GHCR runner base predates Codex CLI installation.
  - Trigger / new insight: A live smoke Pod using the released runner image and Vault-synchronized auth file failed with `codex: not found`; the release had consumed the old mutable `runner-base:latest`.
  - Plan delta (what changed): Rebuild the runner base from the current Dockerfile on GHCR, then pin `agent-runner/Dockerfile` to that immutable base tag and publish a new exact-SHA runner/operator release before updating GitOps.
  - Tradeoffs considered: Overwriting the existing application SHA tag after refreshing `latest` would break immutability; a new commit and release tag preserves provenance.
  - User approval: covered by the run-first/fix-on-failure ruling
  - Record proposed: none; release provenance correction

- 2026-09-30 03:45 Decision: Use unrestricted Codex execution for write-enabled runs and stream JSONL progress.
  - Trigger / new insight: Issue #304 reached Codex but produced no changes because the agent reported an environment permission constraint; the user explicitly selected yolo-style execution, `gpt-5.6-luna`, and streaming progress.
  - Plan delta (what changed): Added Task_9 before the E2E retry. Write-enabled runs use `--dangerously-bypass-approvals-and-sandbox`; plan creation remains `--sandbox read-only`; all runs add `--json`; the model default is explicit in code and GitOps.
  - Tradeoffs considered: `workspace-write` is safer but reproduced the failure; unrestricted execution relies on the Pod/container as the external sandbox and increases the impact of credentials already mounted in runner Jobs.
  - User approval: yes
  - Record proposed: none; deployment runtime policy

## Notes
- Risks:
  - High: secret handling, external GitHub webhook trust boundary, database migration, and production cluster mutation.
  - The existing ARC manifest in `../infra` contains a plaintext GitHub PAT; treat it as exposed and rotate it outside this plan unless scope is expanded.
  - The latest image workflow failed, so current `master` does not yet have proven deployable images.
  - The existing TiDB resource states it is not production-suitable; selecting it requires explicit acceptance.
  - The user accepted the existing TiDB choice; compatibility with all 11 migrations and the configured collation remains a required pre-deploy check.
- Edge cases:
  - The Operator image does not include Goose or migration SQL; Task_2 must use a purpose-built migration mechanism rather than assuming startup migration.
  - Namespace-local image pull credentials must also be available to dynamically created runner Jobs.
  - Multiple Operator replicas may duplicate coordination work; start with one replica unless concurrency safety is demonstrated.
