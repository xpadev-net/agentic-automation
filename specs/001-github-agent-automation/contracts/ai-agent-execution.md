# AI Agent Execution Contract (Kubernetes Pod + Push型通知)

**Version**: 1.0.0
**Last Updated**: 2025-10-31

## Overview

AI agents (claude-code/cursor-agent) are executed in Kubernetes Pods via `agent-runner` wrapper (Go binary). The Pod performs the entire workflow (agent execution → lint/typecheck → git commit/push → PR creation) and **pushes** the result to the Operator via REST API.

**Architecture**: Push-type notification (Pod → Operator)

```
[Operator]
  ↓ 1. Create K8s Job with Pod
[Pod起動] agent-runner binary
  ↓ 2. Execute agent + lint + git operations
[Task完了]
  ↓ 3. POST /api/agent-runs/{id}/report (with retry)
[Operator API]
  ↓ 4. Update AgentRun state
[Pod exit 0]
```

---

## Agent Runner Specification

### Entry Point

```bash
agent-runner \
  --issue-id=123 \
  --repo=owner/repo \
  --prompt="Fix bug in parser" \
  --previous-attempts="..." \
  --ci-logs="..."
```

### Required Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `KUBERNETES_NAMESPACE` | Kubernetes namespace (auto-injected via Downward API) | `default` |
| `OPERATOR_SERVICE_NAME` | Operator service name | `agent-operator` |
| `OPERATOR_SERVICE_PORT` | Operator service port | `3000` |
| `OPERATOR_API_TOKEN` | Bearer token for API authentication | `sk-secret-token-abc123` |
| `AGENT_RUN_ID` | AgentRun database record ID | `456` |
| `AGENT_TYPE` | Agent to execute | `claude-code` or `cursor-agent` |
| `WORKSPACE_DIR` | Working directory | `/workspace` (default) |
| `GITHUB_APP_ID` | GitHub App ID | `123456` |
| `GITHUB_PRIVATE_KEY` | GitHub App RSA private key (PEM, multi-line) | `-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n` |

**Note**: The Operator API URL is automatically constructed from `KUBERNETES_NAMESPACE`, `OPERATOR_SERVICE_NAME`, and `OPERATOR_SERVICE_PORT` as: `http://{OPERATOR_SERVICE_NAME}.{KUBERNETES_NAMESPACE}.svc.cluster.local:{OPERATOR_SERVICE_PORT}`

### Execution Flow

```
1. Parse command-line arguments (Issue context)
2. Clone repository to WORKSPACE_DIR
   └─ Obtain GitHub App installation token, then
      git clone https://x-access-token:${INSTALLATION_TOKEN}@github.com/${repo}.git
3. Checkout new branch (auto-generated name: feature/issue-{id})
4. Execute selected agent (claude-code or cursor-agent)
   └─ Pass Issue context + previous attempts + CI logs as prompt
5. Run lint/typecheck validation
   ├─ npm run lint (biome/eslint)
   └─ npm run type-check (tsc --noEmit)
6. Check for file changes
   └─ git diff --exit-code (exit 1 if no changes)
7. Commit changes
   └─ git commit -m "feat: implement issue #{id}"
8. Push to remote
   └─ Obtain fresh installation token (if expired) and `git push origin feature/issue-{id}`
9. Create Pull Request (via GitHub API using go-github/v62 with installation token)
   └─ github.CreatePullRequest(repo, base, head, title, body)
10. Report result to Operator API (with retry)
    └─ POST /api/agent-runs/{id}/report
11. Exit 0 (success) or Exit 1 (failure)
```

---

## Operator API Contract

### Endpoint

```
POST /api/agent-runs/{id}/report
```

### Authentication

**Type**: Bearer Token
**Header**: `Authorization: Bearer {OPERATOR_API_TOKEN}`

Example:
```bash
curl -X POST \
  -H "Authorization: Bearer sk-secret-token-abc123" \
  -H "Content-Type: application/json" \
  -d '{"status":"succeeded","agent_type":"claude-code","pr_number":101}' \
  http://operator:3000/api/agent-runs/456/report
```

### Request Body

```json
{
  "status": "succeeded",           // "succeeded" | "failed"
  "agent_type": "claude-code",     // "claude-code" | "cursor-agent"
  "pr_number": 101,                // Optional: PR number if succeeded
  "branch": "feature/issue-42",    // Optional: Git branch name
  "commit_sha": "abc123def456",    // Optional: Git commit SHA
  "error_message": "Lint failed",  // Optional: Error details if failed
  "logs": "Agent output:\n..."     // Optional: Agent execution logs
}
```

**Required fields**:
- `status`
- `agent_type`

**Conditional fields**:
- If `status=succeeded`: `pr_number`, `branch`, `commit_sha` should be provided
- If `status=failed`: `error_message`, `logs` should be provided

### Response

**Success (200 OK)**:
```json
{
  "message": "Report received, AgentRun #456 updated to succeeded",
  "agent_run_id": 456
}
```

**Error (401 Unauthorized)**:
```json
{
  "error": "INVALID_TOKEN",
  "message": "Invalid or missing Bearer token"
}
```

**Error (404 Not Found)**:
```json
{
  "error": "AGENT_RUN_NOT_FOUND",
  "message": "AgentRun with ID 456 not found"
}
```

### Retry Policy

If API call fails, agent-runner retries with **exponential backoff**:

| Attempt | Delay | Total Time |
|---------|-------|------------|
| 1 | 0s | 0s |
| 2 | 1s | 1s |
| 3 | 2s | 3s |
| 4 | 4s | 7s |
| 5 | 8s | 15s |
| **Max** | **5 attempts** | **~15s total** |

If all 5 attempts fail, Pod exits with code 1 (failure).

---

## Exit Codes

Agent-runner returns the following exit codes:

| Code | Meaning | Operator Action |
|------|---------|-----------------|
| **0** | Success (report sent) | AgentRun state updated to `succeeded` |
| **1** | Failure (any step failed) | AgentRun state updated to `failed`, retry count +1 |

**Note**: Operator does **NOT** monitor Pod exit codes. Exit codes are only for Kubernetes Job status tracking.

---

## Supported Agents

### 1. claude-code

**Command**:
```bash
claude --prompt "{issue_context}"
```

**Installation** (in Dockerfile):
```dockerfile
RUN npm install -g @anthropic/claude-code
```

**Environment**:
- `ANTHROPIC_API_KEY`: Claude API key (injected by Pod template)

### 2. cursor-agent

**Command**:
```bash
cursor-agent -p "{issue_context}"
```

**Installation** (in Dockerfile):
```dockerfile
RUN curl https://cursor.com/install -fsS | bash
```

**Environment**:
- `CURSOR_API_KEY`: Cursor API key (injected by Pod template)

---

## Agent Selection

Agent type is determined by:

1. **Issue Label** (highest priority)
   - Label `agent:claude-code` → Use claude-code
   - Label `agent:cursor-agent` → Use cursor-agent

2. **Environment Variable** (fallback)
   - `AI_AGENT_DEFAULT_TYPE` in Operator config

3. **Default** (if neither specified)
   - `claude-code`

---

## Issue Context Format

The Issue context passed to agent includes:

```json
{
  "issue_number": 42,
  "issue_title": "Fix parser bug",
  "issue_body": "The parser fails when...",
  "issue_labels": ["bug", "priority:high"],
  "previous_attempts": [
    {
      "retry_count": 1,
      "error": "Lint failed: missing semicolon",
      "ci_logs": "Error: Expected ';' at line 42"
    }
  ],
  "dependencies": [
    {"issue_number": 41, "status": "closed"}
  ]
}
```

Formatted as command-line prompt:
```
Issue #42: Fix parser bug

Description:
The parser fails when...

Previous Attempts (1):
- Attempt 1 failed: Lint failed: missing semicolon
  CI Logs: Error: Expected ';' at line 42

Please fix the issue and ensure lint/typecheck passes.
```

---

## Validation Steps

### 1. Lint Validation

**Command**: `npm run lint`

**Expected**: Exit code 0

**Failure handling**:
- Capture lint output (stdout/stderr)
- Include in error_message when reporting to Operator
- Operator increments retry_count and re-triggers agent

### 2. TypeCheck Validation

**Command**: `npm run type-check`

**Expected**: Exit code 0

**Failure handling**:
- Capture typecheck output
- Include in error_message
- Operator retries with feedback

### 3. File Changes Validation

**Command**: `git diff --exit-code`

**Expected**: Exit code 1 (changes exist)

**Failure handling** (no changes):
- Report as failure: "No file changes detected"
- Operator retries with feedback

---

## Pod Template Example

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: agent-runner-{{ .AgentRunID }}
spec:
  restartPolicy: Never
  serviceAccountName: agent-automation
  containers:
  - name: agent-runner
    image: ghcr.io/your-org/agent-runner:latest
    command: ["agent-runner"]
    args:
      - "--issue-id={{ .IssueID }}"
      - "--repo={{ .Repo }}"
      - "--prompt={{ .Prompt }}"
      - "--previous-attempts={{ .PreviousAttempts }}"
    env:
    # Operator API URL construction (auto-generated from Kubernetes service info)
    - name: KUBERNETES_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
    - name: OPERATOR_SERVICE_NAME
      value: "agent-operator"
    - name: OPERATOR_SERVICE_PORT
      value: "3000"
    - name: OPERATOR_API_TOKEN
      valueFrom:
        secretKeyRef:
          name: agent-runner-secret
          key: api-token
    - name: AGENT_RUN_ID
      value: "{{ .AgentRunID }}"
    - name: AGENT_TYPE
      value: "{{ .AgentType }}"
    - name: GITHUB_APP_ID
      valueFrom:
        secretKeyRef:
          name: operator-secrets
          key: github-app-id
    - name: GITHUB_PRIVATE_KEY
      valueFrom:
        secretKeyRef:
          name: operator-secrets
          key: github-private-key
    - name: ANTHROPIC_API_KEY
      valueFrom:
        secretKeyRef:
          name: anthropic-api-key
          key: token
    resources:
      requests:
        memory: "512Mi"
        cpu: "500m"
      limits:
        memory: "2Gi"
        cpu: "2000m"
```

---

## Timeout & Concurrency

### Timeout

**Default**: 30 minutes (configurable via `AI_AGENT_TIMEOUT_MINUTES`)

**Implementation**:
- Kubernetes Job `activeDeadlineSeconds: 1800`
- If timeout, Pod is killed by K8s
- Operator detects Job failure (no report received)
- Operator increments retry_count and re-triggers

### Concurrency

**Default**: Max 10 concurrent Pods (configurable via `AI_AGENT_MAX_CONCURRENT_PODS`)

**Implementation**:
- Operator tracks active AgentRuns with state=`started`
- Before creating new Job, check active count
- If at limit, queue request until slot available

---

## Error Scenarios

### 1. Agent Execution Failed

**Cause**: Agent binary error, OOM, etc.

**agent-runner action**:
```go
reporter.ReportFailure("Agent execution failed: exit code 1", agentLogs, agentType)
```

**Operator action**:
- Update AgentRun state=`failed`
- Increment retry_count
- If retry_count < 50, re-trigger with error feedback
- If retry_count >= 50, notify (GitHub + Discord)

### 2. Lint/TypeCheck Failed

**Cause**: Generated code has syntax errors

**agent-runner action**:
```go
reporter.ReportFailure("Lint failed: 5 errors found", lintOutput, agentType)
```

**Operator action**:
- Retry with lint errors as context (+1)

### 3. No File Changes

**Cause**: Agent did not modify any files

**agent-runner action**:
```go
reporter.ReportFailure("No file changes detected", "", agentType)
```

**Operator action**:
- Retry with "no changes" feedback (+1)

### 4. Git Push Failed

**Cause**: Network issue, authentication failure

**agent-runner action**:
```go
reporter.ReportFailure("Git push failed: connection timeout", gitError, agentType)
```

**Operator action**:
- Retry after delay (transient network issue)

### 5. PR Creation Failed

**Cause**: GitHub API error, rate limit

**agent-runner action**:
```go
reporter.ReportFailure("PR creation failed: rate limit exceeded", ghError, agentType)
```

**Operator action**:
- Retry after backoff

### 6. Operator API Unreachable

**Cause**: Network partition, Operator down

**agent-runner action**:
- Retry 5 times with exponential backoff
- If all fail, exit 1 (failure)

**Operator recovery**:
- Manual intervention: check Pod logs, extract result
- Re-create AgentRun if needed

---

## Security Considerations

### 1. API Token Management

**Storage**: Kubernetes Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: agent-runner-secret
type: Opaque
stringData:
  api-token: sk-secret-token-abc123
```

**Rotation**: Rotate token periodically (30-90 days)

### 2. GitHub App Permissions & Secrets

**Required repository permissions (App)**:
- Contents: Read & Write
- Issues: Read & Write
- Pull Requests: Read & Write
- Metadata: Read-only

**Secrets injection (Operator Namespace)**:
```yaml
env:
- name: GITHUB_APP_ID
  valueFrom:
    secretKeyRef:
      name: operator-secrets
      key: github-app-id
- name: GITHUB_PRIVATE_KEY
  valueFrom:
    secretKeyRef:
      name: operator-secrets
      key: github-private-key
- name: GITHUB_WEBHOOK_SECRET
  valueFrom:
    secretKeyRef:
      name: operator-secrets
      key: github-webhook-secret
```

### 3. Network Policies

**Restrict Pod egress**:
- Allow: GitHub API (api.github.com)
- Allow: Operator API (agent-operator.default.svc.cluster.local)
- Allow: AI Agent APIs (api.anthropic.com, etc.)
- Deny: All other external traffic

---

## Monitoring & Observability

### Metrics (Operator side)

- `agent_reports_received_total` (counter)
- `agent_reports_succeeded_total` (counter)
- `agent_reports_failed_total` (counter)
- `agent_report_processing_duration_ms` (histogram)

### Logs (agent-runner side)

All logs to stdout in JSON format:

```json
{
  "timestamp": "2025-10-31T12:00:00Z",
  "level": "INFO",
  "agent_run_id": "456",
  "agent_type": "claude-code",
  "step": "agent_execution",
  "message": "Agent execution started"
}
```

**Log levels**: DEBUG, INFO, WARN, ERROR

---

## Testing

### Unit Tests (agent-runner)

- `pkg/reporter/client_test.go` - API client retry logic
- `pkg/agent/executor_test.go` - Agent execution mocking
- `pkg/lint/runner_test.go` - Lint command parsing

### Integration Tests (operator)

- `tests/integration/agent-report.test.ts` - Full Pod → Operator flow
- Mock K8s cluster with test Pods
- Verify AgentRun state transitions

### Contract Tests

- `tests/contract/agent-report-api.test.ts` - API request/response validation
- Validate JSON schema for POST /api/agent-runs/{id}/report

---

## References

- **Operator Internal API**: [internal-api.yaml](./internal-api.yaml)
- **GitHub Webhooks**: [github-webhooks.md](./github-webhooks.md)
- **Data Model**: [../data-model.md](../data-model.md)
- **Functional Requirements**: [../spec.md](../spec.md) (FR-003, FR-005, FR-014)

---

**Contract Version**: 1.0.0
**Breaking Changes**: Any changes to API endpoint, request/response schema require version bump and migration plan.
