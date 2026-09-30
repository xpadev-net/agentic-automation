# Lifecycle hardening rollout

Apply migration `000012_add_webhook_deliveries.sql` before rolling out the operator.
The operator fails closed with HTTP 503 if receipt persistence is unavailable.
The new table separates webhook receipt from actual AgentRun execution: ordinary
comments, permission denials, and deliveries coalesced with an active run do not
create queued executions. Receipt claims and Issue projection changes commit in
one transaction, so duplicate deliveries cannot overwrite a newer projection.

Webhook dispatch remains at-most-once, not exactly-once: a receipt is claimed
before handler side effects. If the operator dies after receipt commit but before
handling it, retry using a new delivery/trigger after inspecting the issue and
existing Jobs. Do not delete receipts to blindly replay external side effects.
Receipts are retained to cover delayed GitHub redeliveries.

Execution admission serializes on the parent Issue row after permission checks.
The focused tests cover separate SQLite connections; validate the migration and
concurrent transactions against the deployment's MySQL version before rollout.

Local and CI Go checks share `scripts/check-go-modules.sh` and cover the root
operator module and the nested `agent-runner` module. `deps` and `fmt` checks are
read-only checks; they report changes instead of silently editing source files.

## Runner/operator compatibility and reconciliation

Roll out the new runner image first. Drain already-running legacy retry Pods
(those with RETRY_COUNT > 0 and no job_name in their report) before upgrading the
operator. First-attempt legacy reports remain accepted for legacy Job names;
new stable `-attempt-N` Jobs require the injected JOB_NAME in both report types.
Unscoped old retry reports cannot safely be attributed and receive HTTP 409.
No deployment or migration is performed by these source changes.

The operator reconciles immediately at startup and every 30 seconds. Failed or
Complete Job conditions wait five minutes for reporter retries; missing known
dispatches wait ten minutes and a confirming lookup. Running/pending Jobs,
intermediate failed-Pod counters, API outages, and ambiguous legacy matches do
not create synthetic failures. Complete without a report is recorded as
`AgentReportMissing`, with an explicitly unknown result, never as success.
Reconciliation does not create/retry/delete Jobs and does not fetch Pod logs.
Failure evidence includes owned-container OOM/exit status when available.

Retries are admitted with an atomic attempt/count/timestamp/name reservation.
Job names are stable for repeated Create calls of one attempt, and old callbacks
cannot mutate or clean up a newer attempt. Failed plan attempts release only
their own linked review reservation. Intentional queued dependency waits without
a Job name remain pending; unexplained legacy queued/no-name rows require manual
inspection because absence alone cannot prove failed execution.

Known existing timing limitation: CI failure can arrive while its originating
runner is still executing or reporting. The failure is retained in CIStatus and
now returns `retry_deferred / agent_report_pending`, without incrementing retry
count or starting overlapping work. Automatic reevaluation after the report is
not implemented. Inspect the recorded CI failure and explicitly retrigger after
the original execution finishes. Receipt deduplication means replaying the same
delivery does not trigger a retry. The earlier implementation could also strand
this timing window behind its active-Job check.

## Logs and output contracts

Runner progress contains allowlisted event names and byte counts, not raw
assistant/tool output or stderr. Captured stdout stays separate for parsing.
Diagnostic/report boundaries redact known runtime secrets and common credential
formats; redaction is defense in depth, not proof that arbitrary unknown secrets
can be detected. This change does not claim an observed secret leak.

Codex JSONL requires a final completed agent_message and turn.completed. Cursor
stream JSON requires final assistant text and a successful result event. Tool-only,
malformed, truncated, and error streams fail closed. Plain-text parsing is selected
explicitly for the Claude CLI contract; there is no JSONL-to-raw fallback.

Unit/integration fixtures now inject fake Git push and S3 transports instead of
contacting placeholder external hosts. Real S3/cluster integration tests that the
repository already skips remain separate deployment-environment checks.
