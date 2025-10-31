# GitHub Webhooks Contract

**Version**: 1.0.0
**Last Updated**: 2025-10-31

## Overview

This document defines the contract for GitHub webhook events consumed by the GitHub Agent Automation system. All webhooks are received at the `/webhooks/github` endpoint and verified using the GitHub App webhook secret.

## Webhook Endpoint

```
POST /webhooks/github
Content-Type: application/json
X-GitHub-Delivery: <unique-delivery-id>
X-GitHub-Event: <event-type>
X-Hub-Signature-256: sha256=<signature>
```

## Event Types

### 1. issue_comment (FR-001, FR-010)

Triggered when a comment is created on an Issue or PR.

**Purpose**: Detect `/run-agent` trigger and `@codex review` requests

**Webhook Payload** (relevant fields):

```json
{
  "action": "created",
  "issue": {
    "id": 123456,
    "number": 42,
    "title": "Add new feature",
    "body": "Description of the feature...",
    "state": "open",
    "labels": [
      { "name": "enhancement" }
    ],
    "user": {
      "login": "octocat"
    }
  },
  "comment": {
    "id": 789012,
    "body": "/run-agent",
    "user": {
      "login": "octocat"
    },
    "created_at": "2025-10-31T12:00:00Z"
  },
  "repository": {
    "full_name": "octocat/hello-world",
    "owner": {
      "login": "octocat"
    },
    "name": "hello-world"
  }
}
```

**Processing**:

1. Check if `comment.body` contains `/run-agent` (case-insensitive)
2. Verify `comment.user` has Collaborator+ permission (AuthorizationService)
3. Extract X-GitHub-Delivery header for idempotency
4. Create AgentRun with state=queued
5. Trigger AgentExecutionService
6. Log to AuditLog

**Edge Cases**:
- Comment edited: Ignore (only process `action: created`)
- Comment on closed Issue: Reject with GitHub comment
- Duplicate delivery ID: Return 200 OK, skip processing
- User lacks permission: Post GitHub comment with error

---

### 2. issues (FR-013)

Triggered when an Issue is opened, closed, or reopened.

**Purpose**: Detect dependency graph changes and trigger unblocked tasks

**Actions Subscribed**: `closed`, `reopened`

**Webhook Payload**:

```json
{
  "action": "closed",
  "issue": {
    "id": 123456,
    "number": 42,
    "title": "Task A",
    "body": "This blocks #43 and #44",
    "state": "closed"
  },
  "repository": {
    "full_name": "octocat/hello-world"
  }
}
```

**Processing**:

1. Parse Issue body/comments for dependency keywords:
   - "blocked by #N"
   - "depends on #N"
   - "blocking #N"
2. Update BlockerGraphEdges table
3. On `action: closed`:
   - Query BlockerGraphEdges for dependents
   - Check if all dependencies resolved
   - Trigger new AgentRun for unblocked Issues
4. Log dependency changes to AuditLog

**Dependency Parsing Regex**:

```regex
blocked by #(\d+)
depends on #(\d+)
blocking #(\d+)
```

---

### 3. pull_request

Triggered when a PR is opened, closed, or synchronized (new commits pushed).

**Purpose**: Track PR status and detect merge events

**Actions Subscribed**: `opened`, `synchronize`, `closed`

**Webhook Payload**:

```json
{
  "action": "opened",
  "number": 101,
  "pull_request": {
    "id": 987654,
    "number": 101,
    "title": "Fix bug in parser",
    "head": {
      "ref": "feature/issue-42"
    },
    "base": {
      "ref": "main"
    },
    "state": "open",
    "mergeable": true,
    "merged": false,
    "user": {
      "login": "github-agent-bot"
    }
  },
  "repository": {
    "full_name": "octocat/hello-world"
  }
}
```

**Processing**:

1. Upsert PullRequest record
2. Link to AgentRun (if created by agent)
3. On `action: synchronize` (new commits):
   - Reset CI status (CIStatus records)
   - Wait for check_suite webhook
4. On `action: closed` with `merged: true`:
   - Update AgentRun state to succeeded
   - Log merge event
5. Check merge conditions (MergeConditionChecker)

---

### 4. pull_request_review (FR-010, FR-015)

Triggered when a review is submitted on a PR.

**Purpose**: Detect Codex approval for auto-merge

**Webhook Payload**:

```json
{
  "action": "submitted",
  "review": {
    "id": 111222,
    "body": "Codex Review: Didn't find any major issues.",
    "user": {
      "login": "codex-bot"
    },
    "state": "approved"
  },
  "pull_request": {
    "number": 101
  },
  "repository": {
    "full_name": "octocat/hello-world"
  }
}
```

**Processing**:

1. Check if `review.user.login` is Codex bot
2. Parse `review.body` for approval pattern:
   - Regex: `/didn't find.*major issues/i`
   - Or exact: "Codex Review: Didn't find any major issues."
3. Create ReviewFeedback record with `approval_detected: true`
4. Re-evaluate merge conditions (MergeConditionChecker)
5. If all conditions met, call AutoMergeService
6. Log to AuditLog

---

### 5. pull_request_review_comment

Triggered when a comment is made on a PR review (inline code comments).

**Purpose**: Detect `@codex review` requests in PR comments

**Webhook Payload**:

```json
{
  "action": "created",
  "comment": {
    "id": 333444,
    "body": "@codex review",
    "user": {
      "login": "octocat"
    }
  },
  "pull_request": {
    "number": 101
  },
  "repository": {
    "full_name": "octocat/hello-world"
  }
}
```

**Processing**:

1. Check if `comment.body` contains `@codex review`
2. Verify user has permission
3. Call CodexReviewService to request review
4. Create ReviewFeedback record with status=requested
5. Log to AuditLog

---

### 6. check_suite (FR-014)

Triggered when a CI check suite is completed.

**Purpose**: Detect CI pass/fail for auto-merge and AI retry

**Actions Subscribed**: `completed`

**Webhook Payload**:

```json
{
  "action": "completed",
  "check_suite": {
    "id": 555666,
    "status": "completed",
    "conclusion": "success",
    "head_branch": "feature/issue-42",
    "head_sha": "abc123def456",
    "pull_requests": [
      {
        "number": 101
      }
    ]
  },
  "repository": {
    "full_name": "octocat/hello-world"
  }
}
```

**Conclusion Values**:
- `success`: All checks passed
- `failure`: One or more checks failed
- `cancelled`: Checks were cancelled
- `skipped`: Checks were skipped
- `neutral`: Checks completed with neutral status

**Processing**:

1. Find PullRequest by number
2. Create/update CIStatus record
3. If `conclusion: failure`:
   - Fetch logs via GitHub API
   - Parse failure reason
   - Pass to RetryOrchestrator
   - Aggregate with ReviewFeedback
   - Trigger AI retry (increment AgentRun.retry_count)
4. If `conclusion: success`:
   - Re-evaluate merge conditions
   - If all conditions met, call AutoMergeService
5. Log to AuditLog

**Precedence & De-duplication with `status` events**:

- `check_suite` を優先シグナルとし、`status` はレガシー互換として使用する。
- 両方が発火した場合は `head_sha` が同一かつ最新のタイムスタンプのイベントを採用し二重処理を避ける。
- 既存の CIStatus レコード更新は idempotent に設計する（同一 `head_sha` の重複更新は無害）。

---

### 7. status

Triggered when commit status changes (legacy CI systems).

**Purpose**: Support legacy CI systems that don't use check suites

**Webhook Payload**:

```json
{
  "sha": "abc123def456",
  "state": "success",
  "description": "Build passed",
  "context": "ci/travis",
  "target_url": "https://travis-ci.org/...",
  "branches": [
    {
      "name": "feature/issue-42"
    }
  ],
  "repository": {
    "full_name": "octocat/hello-world"
  }
}
```

**State Values**: `pending`, `success`, `failure`, `error`

**Processing**: Similar to check_suite, but use `state` field instead of `conclusion`

---

## Security

### Signature Verification

All webhooks MUST be verified using HMAC-SHA256 (Go example):

```go
import (
  "crypto/hmac"
  "crypto/sha256"
  "encoding/hex"
)

func verifySignature(payload []byte, signature, secret string) bool {
  mac := hmac.New(sha256.New, []byte(secret))
  mac.Write(payload)
  expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
  return hmac.Equal([]byte(expected), []byte(signature))
}
```

**Library**: Use `github.com/google/go-github/v62/github` helpers if desired, or manual verification as above

**Rejection**: Return 401 Unauthorized for invalid signatures

### Rate Limiting

GitHub may send webhooks in bursts. Implement rate limiting:

- Accept up to 100 webhooks/second
- Queue excess for processing
- Return 202 Accepted immediately

### Idempotency

Use `X-GitHub-Delivery` header to prevent duplicate processing:

1. Extract delivery ID from header
2. Check `AgentRun.idempotency_key` for existing record
3. If exists, return 200 OK without processing
4. If new, create record and process

### Authorization

For `/run-agent` triggers:

1. Extract `comment.user.login`
2. Call GitHub API: `GET /repos/:owner/:repo/collaborators/:username`
3. If 404 or non-2xx, reject with GitHub comment
4. If 204, proceed with execution

## Error Handling

### Webhook Processing Errors

If processing fails:

1. Log error to AuditLog with full payload
2. Return 200 OK to GitHub (prevent retries)
3. Post error comment to Issue/PR
4. Send Discord notification
5. Increment error metric

**DO NOT** return 5xx errors (GitHub will retry indefinitely)

### GitHub API Errors

If GitHub API call fails during webhook processing:

1. Implement exponential backoff (max 3 retries)
2. If all retries fail, log and notify
3. Return 200 OK to prevent webhook retry

## Testing

### Webhook Payload Fixtures

Store test payloads in `tests/fixtures/webhooks/`:

- `issue_comment_trigger.json`
- `issue_closed.json`
- `pull_request_opened.json`
- `check_suite_success.json`
- `check_suite_failure.json`

### Contract Tests

Use Go testing to validate:

```go
func TestTriggerDetection(t *testing.T) {
  body := "/run-agent please"
  if !DetectTrigger(body) {
    t.Fatalf("expected trigger to be detected")
  }
}
```

### Local Testing

Use GitHub's webhook delivery redelivery feature:

1. Go to GitHub App settings → Advanced → Recent Deliveries
2. Click on a delivery → Redeliver
3. Monitor local server logs

Or use `ngrok` for local development (see quickstart.md)

---

## Monitoring

### Webhook Metrics

Track in MetricsService:

- `webhooks_received_total` (counter)
- `webhooks_processed_duration_ms` (histogram)
- `webhooks_failed_total` (counter)
- `webhooks_by_event_type` (counter with label)

### Alerts

Set up alerts for:

- Webhook processing time > 5 seconds (p95)
- Webhook failure rate > 5%
- Idempotency key collisions
- Signature verification failures

---

## References

- GitHub Webhooks Documentation: https://docs.github.com/webhooks
- go-github: https://github.com/google/go-github
- spec.md: Functional requirements (FR-001 through FR-018)
- data-model.md: Database schema for webhook data

---

**Contract Version**: 1.0.0
**Breaking Changes**: Any changes to event processing logic require review and approval before deployment
