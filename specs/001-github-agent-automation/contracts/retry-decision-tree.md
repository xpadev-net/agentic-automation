# Retry Decision Tree

**Version**: 1.0.0
**Last Updated**: 2025-11-01

## Overview

This document clarifies when to retry agent execution and how to count retry attempts. The system has two distinct retry mechanisms:

1. **AI Retry**: Counted against the 50-attempt limit (FR-014)
2. **API Retry**: Exponential backoff for transient failures (FR-016)

Note (Phased Implementation): In US2, failure handling covers recording and summarizing errors only; automatic AI retry orchestration (retry_count increment and Job re-creation) is introduced in US3.

---

## Decision Tree

```
┌─────────────────────────────────┐
│   Agent Execution Completed     │
└────────────┬────────────────────┘
             │
             ▼
    ┌────────────────┐
    │  Success/Fail? │
    └────┬───────┬───┘
         │       │
    [Success]  [Failure]
         │       │
         │       ▼
         │   ┌──────────────────────┐
         │   │  Identify Root Cause │
         │   └──────┬───────────────┘
         │          │
         │          ├─────────────────────────────┬─────────────────────┬────────────────────┐
         │          │                             │                     │                    │
         │     [Lint Failed]                 [CI Failed]        [Agent Error]       [GitHub API Error]
         │          │                             │                     │                    │
         │          ▼                             ▼                     ▼                    ▼
         │   ┌─────────────┐             ┌─────────────┐      ┌─────────────┐     ┌──────────────────┐
         │   │ AI RETRY    │             │ AI RETRY    │      │ AI RETRY    │     │ API RETRY        │
         │   │ +1 to count │             │ +1 to count │      │ +1 to count │     │ No count change  │
         │   └──────┬──────┘             └──────┬──────┘      └──────┬──────┘     └────────┬─────────┘
         │          │                            │                     │                     │
         │          ▼                            ▼                     ▼                     ▼
         │   ┌────────────────┐          ┌────────────────┐   ┌────────────────┐  ┌──────────────────┐
         │   │ Retry count    │          │ Retry count    │   │ Retry count    │  │ Exponential      │
         │   │ < 50?          │          │ < 50?          │   │ < 50?          │  │ backoff 3-5      │
         │   └───┬────────┬───┘          └───┬────────┬───┘   └───┬────────┬───┘  │ attempts         │
         │       │        │                  │        │           │        │       └────────┬─────────┘
         │      [Yes]   [No]                [Yes]   [No]         [Yes]   [No]              │
         │       │        │                  │        │           │        │               │
         │       │        │                  │        │           │        │           [Success]
         │       │        │                  │        │           │        │               │
         │       ▼        ▼                  ▼        ▼           ▼        ▼               ▼
         │   ┌────┐   ┌────┐            ┌────┐   ┌────┐      ┌────┐   ┌────┐        ┌────────┐
         │   │Re- │   │Send│            │Re- │   │Send│      │Re- │   │Send│        │Success │
         │   │try │   │Fail│            │try │   │Fail│      │try │   │Fail│        └────────┘
         │   │    │   │Noti│            │    │   │Noti│      │    │   │Noti│
         │   └────┘   └────┘            └────┘   └────┘      └────┘   └────┘
         │
         ▼
    ┌────────────┐
    │  Success   │
    │  Complete  │
    └────────────┘
```

---

## Retry Types

### 1. AI Retry (Counted)

**Trigger**: Agent execution produced incorrect code or failed validation

**Scenarios**:
- Lint failed (`npm run lint` exit code != 0)
- Type check failed (`npm run type-check` exit code != 0)
- CI tests failed (check_suite conclusion = failure)
- Codex review rejected (no approval comment)
- Agent returned error (agent CLI exit code != 0)
- No file changes detected (agent produced no output)

**Action**:
1. Increment `AgentRun.retry_count` by 1
2. Aggregate error context:
   - Lint/typecheck output
   - CI failure logs
   - Codex review comments
   - Agent stderr
3. Pass aggregated feedback to agent in next attempt
4. Create new Kubernetes Job with updated prompt

**Limit**: 50 attempts (FR-014)

**On Limit Exceeded**:
1. Set `AgentRun.state` = `failed`
2. Post GitHub Issue comment: "❌ Maximum retry limit (50) reached. Manual intervention required."
3. Send Discord notification (embed with error history)
4. Stop auto-retry loop

---

### 2. API Retry (Not Counted)

**Trigger**: Transient network/API failures

**Scenarios**:
- GitHub API rate limit exceeded (HTTP 429)
- GitHub API server error (HTTP 5xx)
- GitHub API timeout (connection timeout)
- Network partition (connection refused)
- Operator API unreachable (agent-runner → Operator report call)
- S3 connection failure (session upload/download failures)

**Action**:
1. **Do NOT** increment `AgentRun.retry_count`
2. Apply exponential backoff with jitter:
   - Attempt 1: immediate
   - Attempt 2: 1s delay
   - Attempt 3: 2s delay
   - Attempt 4: 4s delay
   - Attempt 5: 8s delay
3. Maximum 3-5 attempts (depending on API)

**On All Retries Failed**:
- Log error to AuditLog
- Send Discord notification (infrastructure issue)
- For agent-runner → Operator API failure: Pod exits with code 1
- For GitHub API failure: Proceed with AI retry (+1 to count)
- For S3 failure (upload/download): Pod exits with code 1 → Operator creates new Job (API Retry)

---

## Detailed Scenarios

### Scenario A: Lint Failed

**Flow**:
```
1. agent-runner executes `npm run lint`
2. Exit code = 1 (failure)
3. agent-runner reports to Operator: status=failed, error_message="Lint failed: ..."
4. Operator increments AgentRun.retry_count (+1)
5. Operator checks: retry_count < 50? → Yes
6. Operator creates new K8s Job with feedback:
   - Previous attempt: "Lint failed at line 42: missing semicolon"
7. New agent-runner Pod starts with updated prompt
```

**Retry Type**: AI Retry (counted)

---

### Scenario B: GitHub API Rate Limit

**Flow**:
```
1. agent-runner calls GitHub API to create PR
2. Response: HTTP 429 Too Many Requests
3. agent-runner applies exponential backoff
4. Retry 1: Wait 1s → HTTP 429
5. Retry 2: Wait 2s → HTTP 429
6. Retry 3: Wait 4s → HTTP 200 OK (success)
7. agent-runner reports to Operator: status=succeeded, pr_number=101
8. AgentRun.retry_count remains unchanged
```

**Retry Type**: API Retry (not counted)

---

### Scenario C: CI Failed

**Flow**:
```
1. Agent execution succeeds, PR created
2. GitHub triggers CI via GitHub Actions
3. check_suite webhook: conclusion=failure
4. Operator parses CI logs: "Test failed: expected 'foo', got 'bar'"
5. Operator increments AgentRun.retry_count (+1)
6. Operator checks: retry_count < 50? → Yes
7. Operator creates new K8s Job with feedback:
   - "CI Failed: Test assertion error at parser_test.ts:42"
8. Agent re-executes with updated prompt
```

**Retry Type**: AI Retry (counted)

---

### Scenario D: Agent Execution Error

**Flow**:
```
1. agent-runner executes `claude-code --task "..."`
2. Claude Code returns error: "Unable to understand task requirements"
3. Exit code = 1
4. agent-runner reports to Operator: status=failed, error_message="Agent error: ..."
5. Operator increments AgentRun.retry_count (+1)
6. Operator re-triggers with clarified prompt
```

**Retry Type**: AI Retry (counted)

---

### Scenario E: Operator API Unreachable

**Flow**:
```
1. agent-runner completes all steps successfully
2. agent-runner attempts to report to Operator API
3. POST http://operator:3000/api/agent-runs/456/report → connection refused
4. agent-runner retries with exponential backoff (5 attempts)
5. All retries fail
6. agent-runner exits with code 1
7. Operator detects Job failure (Pod exit 1, no report received)
8. Operator increments AgentRun.retry_count (+1)
9. Operator creates new Job
```

**Retry Type**: Hybrid (API retry first, then AI retry if all fail)

---

### Scenario F: S3 Session Upload Failure

**Flow**:
```
1. agent-runner completes all steps successfully
2. agent-runner attempts to save session to S3
3. PUT s3://agent-sessions/sessions/123/session.tar.gz → connection timeout
4. agent-runner retries with exponential backoff (5 attempts: 1s, 2s, 4s, 8s, 16s)
5. All retries fail (S3/MinIO unavailable)
6. agent-runner logs error: "S3 session upload failed after 5 retries"
7. agent-runner exits with code 1 (Pod failure)
8. Operator detects Job failure (Pod exit 1, no report received)
9. Operator does NOT increment AgentRun.retry_count (infrastructure failure)
10. Operator creates new Job (API Retry)
11. New Pod attempts full workflow again (including S3 upload)
```

**Retry Type**: API Retry (not counted against 50 AI retries)

**Note**: If S3 remains unavailable, the cycle repeats. Manual intervention required to fix S3 infrastructure.

---

### Scenario G: S3 Session Download Failure (on Retry)

**Flow**:
```
1. Operator creates new Job for retry (AgentRun.retry_count = 3)
2. Pod starts, passes RETRY_COUNT=3 environment variable
3. agent-runner clones repository
4. agent-runner attempts to restore session from S3
5. GET s3://agent-sessions/sessions/123/session.tar.gz → HTTP 503 Service Unavailable
6. agent-runner retries with exponential backoff (5 attempts)
7. All retries fail
8. agent-runner logs error: "S3 session download failed after 5 retries"
9. agent-runner exits with code 1 (Pod failure)
10. Operator detects Job failure
11. Operator does NOT increment AgentRun.retry_count (API Retry)
12. Operator creates new Job
13. Cycle repeats until S3 is available
```

**Retry Type**: API Retry (not counted)

**Rationale**: Without session context, AI retry would be less effective. Ensure S3 is accessible before proceeding with work.

---

## Counting Rules

### Rule 1: One Increment Per Failed Attempt

Each agent execution attempt that fails for **any AI-related reason** increments `retry_count` by exactly 1.

**Example**:
```
Attempt 1: Lint failed → retry_count = 1
Attempt 2: Lint passed, CI failed → retry_count = 2
Attempt 3: All passed → retry_count = 2 (no increment, success)
```

---

### Rule 2: API Retries Are Transparent

Transient API failures are retried automatically without incrementing `retry_count`.

**Example**:
```
Attempt 1: GitHub API timeout → retry with backoff (3 times) → success
           retry_count = 0 (no increment)
```

---

### Rule 3: Multiple Failures in One Attempt

If multiple validations fail in a single attempt, only increment by 1.

**Example**:
```
Attempt 1:
  - Lint failed: 5 errors
  - Type check failed: 2 errors
  → increment retry_count by 1 (not 2)
```

---

### Rule 4: agent-runner Retry Budget

agent-runner has its own retry budget (5 attempts) for reporting to Operator API. This is **separate** from the 50 AI retries.

**Example**:
```
AgentRun retry_count = 10
agent-runner reports to Operator (5 API retry attempts)
If all fail: retry_count becomes 11 (AI retry triggered)
```

---

## Implementation

### RetryOrchestrator Service (internal/services/retry_orchestrator.go)

```go
package services

type RetryOrchestrator struct {
	agentRunRepo AgentRunRepository
	jobService   KubernetesJobService
}

func (r *RetryOrchestrator) ShouldRetry(agentRun *AgentRun) bool {
	return agentRun.RetryCount < 50
}

func (r *RetryOrchestrator) TriggerRetry(agentRun *AgentRun, feedback string) error {
	// Increment retry count
	agentRun.RetryCount++
	if err := r.agentRunRepo.Update(agentRun); err != nil {
		return err
	}

	// Check limit
	if agentRun.RetryCount >= 50 {
		return r.handleMaxRetriesExceeded(agentRun)
	}

	// Aggregate feedback and create new Job
	prompt := r.buildRetryPrompt(agentRun, feedback)
	return r.jobService.CreateJob(agentRun, prompt)
}

func (r *RetryOrchestrator) handleMaxRetriesExceeded(agentRun *AgentRun) error {
	agentRun.State = "failed"
	// Post GitHub comment
	// Send Discord notification
	return r.agentRunRepo.Update(agentRun)
}
```

---

## Edge Cases

### 1. Same Error Repeated 3 Times

**Problem**: Agent stuck in infinite loop with same error

**Detection**:
- Hash of (error_message + ci_logs + review_comments)
- If hash matches previous 3 consecutive attempts → pause

**Action**:
- Post GitHub comment: "⚠️ Same error detected 3 times. Pausing auto-retry. Please review."
- Do NOT increment retry_count
- Require manual reset via CLI: `operator retry <run-id> --reset-count`

---

### 2. Partial Success (Lint Passed, CI Failed Later)

**Problem**: Should retry count include partial successes?

**Answer**: Yes, any failure triggers +1 increment

**Reason**: The goal is to reach fully working code, not partial progress

---

### 3. Codex Never Responds

**Problem**: PR created, but Codex bot never posts review

**Action**:
- Spec requires **manual intervention** (FR-010 clarification)
- System does NOT poll for Codex response
- User must manually trigger re-review: comment `@codex review` again

---

## References

- [spec.md](../spec.md) - FR-014 (AI Retry), FR-016 (API Retry)
- [data-model.md](../data-model.md) - AgentRun.retry_count field
- [agent-runner-detail.md](./agent-runner-detail.md) - agent-runner implementation

---

**Version**: 1.0.0
**Last Updated**: 2025-11-01
