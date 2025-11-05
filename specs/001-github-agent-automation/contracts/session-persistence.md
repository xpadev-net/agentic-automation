# AI Agent Session Persistence Contract

**Version**: 1.0.0
**Last Updated**: 2025-11-01

## Overview

This document specifies how AI agent session data (conversation history, context) is persisted to S3-compatible storage (MinIO) and restored across retry attempts. This enables agents to maintain context continuity when Pod execution fails and retries are required.

---

## Motivation

**Problem**: When an AI agent (claude-code or cursor-agent) fails and the Pod terminates, all session context is lost. The next retry attempt starts from scratch without knowledge of previous conversation history.

**Solution**: Before Pod termination, compress and upload session data to S3. On retry, download and restore the session state before agent execution.

**Benefits**:
- Context continuity across retries
- Faster convergence to solution (agent remembers previous mistakes)
- Better use of limited retry budget (max 50 attempts)

---

## Architecture

### High-Level Flow

```
┌─────────────────────────────────────────────────────────────┐
│ Initial Execution (retry_count = 0)                          │
├─────────────────────────────────────────────────────────────┤
│ 1. Pod starts                                                 │
│ 2. Clone repository                                           │
│ 3. Skip session restore (no previous session exists)         │
│ 4. Execute AI agent (fresh session)                          │
│ 5. Agent creates session files (~/.claude/, ~/.cursor/)      │
│ 6. Lint/typecheck validation                                 │
│ 7. Commit and push changes                                    │
│ 8. Create Pull Request                                        │
│ 9. Compress session data → tar.gz                            │
│ 10. Upload to S3: s3://bucket/sessions/{agent_run_id}/...    │
│ 11. Report success/failure to Operator API                   │
│ 12. Pod terminates                                            │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Retry Execution (retry_count > 0)                            │
├─────────────────────────────────────────────────────────────┤
│ 1. Pod starts                                                 │
│ 2. Clone repository                                           │
│ 3. Download session data from S3                             │
│ 4. Extract tar.gz → restore ~/.claude/, ~/.cursor/           │
│ 5. Execute AI agent (resume previous session)                │
│ 6. Agent sees previous conversation history                  │
│ 7. Lint/typecheck validation                                 │
│ 8. Commit and push changes                                    │
│ 9. Create Pull Request or update existing PR                 │
│ 10. Compress updated session data → tar.gz                   │
│ 11. Upload to S3 (overwrite previous session)                │
│ 12. Report success/failure to Operator API                   │
│ 13. Pod terminates                                            │
└─────────────────────────────────────────────────────────────┘
```

---

## Session Data to Persist

### For claude-code

**Global Session Directory**: `~/.claude/`
- `projects/{encoded-path}/{session-uuid}.jsonl` - Conversation history
- `projects/{encoded-path}/{summary-uuid}.jsonl` - Context summaries
- `settings.json` - Global preferences

**Project Directory**: `/workspace/.claude/`
- `settings.json` - Project-specific settings
- `settings.local.json` - Local overrides
- `commands/` - Custom slash commands

**Project Context**: `/workspace/CLAUDE.md`
- Project memory and guidelines

**Exclusions** (DO NOT persist):
- `~/.claude/.credentials.json` - Auth tokens (injected via K8s Secrets)
- `~/.claude/cache/` - Temporary cache files

### For cursor-agent

**Session Directory**: `~/.cursor/` (estimated location for headless mode)
- Session state files
- Agent configuration
- Conversation history

**Workspace**: `/workspace` (relevant project files only)

**Exclusions** (DO NOT persist):
- API keys and credentials
- Node modules (`/workspace/node_modules/`)
- Build artifacts (`/workspace/dist/`, `/workspace/build/`)

---

## S3 Storage Structure

### Bucket: `agent-sessions`

**Key Format**: `sessions/{agent_run_id}/session.tar.gz`

**Example**:
```
s3://agent-sessions/
  ├── sessions/
  │   ├── 123/
  │   │   └── session.tar.gz  (AgentRun ID 123)
  │   ├── 124/
  │   │   └── session.tar.gz  (AgentRun ID 124)
  │   └── 125/
  │       └── session.tar.gz  (AgentRun ID 125)
```

**Metadata File** (optional, for debugging): `sessions/{agent_run_id}/metadata.json`
```json
{
  "agent_run_id": 123,
  "agent_type": "claude-code",
  "retry_count": 3,
  "uploaded_at": "2025-11-01T10:30:00Z",
  "session_size_bytes": 524288,
  "files_included": [
    ".claude/projects/workspace/abc123.jsonl",
    ".claude/settings.json",
    "workspace/.claude/settings.json",
    "workspace/CLAUDE.md"
  ]
}
```

---

## S3 Configuration (MinIO Compatible)

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `S3_ENDPOINT` | S3 API endpoint | `http://minio:9000` (MinIO) or `https://s3.amazonaws.com` (AWS) |
| `S3_REGION` | S3 region | `us-east-1` |
| `S3_BUCKET` | Bucket name | `agent-sessions` |
| `S3_ACCESS_KEY_ID` | Access key | `minioadmin` |
| `S3_SECRET_ACCESS_KEY` | Secret key | `minioadmin` |
| `S3_USE_PATH_STYLE` | Use path-style URLs (required for MinIO) | `true` |
| `S3_MAX_RETRIES` | Max retry attempts on S3 failure | `5` |
| `S3_RETRY_INITIAL_INTERVAL` | Initial backoff delay | `1s` |

### MinIO vs AWS S3 Differences

**MinIO** (default for this project):
- `S3_USE_PATH_STYLE=true` - Use `http://minio:9000/bucket/key` format
- `S3_ENDPOINT=http://minio:9000` - Local endpoint

**AWS S3**:
- `S3_USE_PATH_STYLE=false` - Use `https://bucket.s3.amazonaws.com/key` format
- `S3_ENDPOINT=https://s3.amazonaws.com` - AWS endpoint

---

## Implementation Details

### Session Save Process

**Trigger**: After agent execution completes (success or failure), before reporting to Operator

**Algorithm**:
```go
func SaveSession(agentRunID int, agentType string) error {
    // 1. Create temporary archive directory
    tmpDir := "/tmp/session-archive"

    // 2. Copy session files to archive directory
    if agentType == "claude-code" {
        CopyDir("~/.claude/", tmpDir + "/.claude/")
        CopyDir("/workspace/.claude/", tmpDir + "/workspace/.claude/")
        CopyFile("/workspace/CLAUDE.md", tmpDir + "/workspace/CLAUDE.md")
    } else if agentType == "cursor-agent" {
        CopyDir("~/.cursor/", tmpDir + "/.cursor/")
    }

    // 3. Create tar.gz archive
    tarPath := fmt.Sprintf("/tmp/session-%d.tar.gz", agentRunID)
    CreateTarGz(tmpDir, tarPath)

    // 4. Upload to S3 with retry
    s3Key := fmt.Sprintf("sessions/%d/session.tar.gz", agentRunID)
    err := S3UploadWithRetry(tarPath, s3Key)
    if err != nil {
        return fmt.Errorf("S3 upload failed after %d retries: %w", S3_MAX_RETRIES, err)
    }

    // 5. Cleanup temporary files
    os.RemoveAll(tmpDir)
    os.Remove(tarPath)

    return nil
}
```

**Error Handling**: If S3 upload fails after all retries, the agent-runner exits with code 1 (Job failure).

---

### Session Restore Process

**Trigger**: At Pod startup, after git clone, if `retry_count > 0`

**Algorithm**:
```go
func RestoreSession(agentRunID int, retryCount int) error {
    // 1. Skip if initial execution (no previous session)
    if retryCount == 0 {
        log.Info("Initial execution, no session to restore")
        return nil
    }

    // 2. Download session archive from S3 with retry
    s3Key := fmt.Sprintf("sessions/%d/session.tar.gz", agentRunID)
    tarPath := fmt.Sprintf("/tmp/session-%d.tar.gz", agentRunID)

    err := S3DownloadWithRetry(s3Key, tarPath)
    if err != nil {
        return fmt.Errorf("S3 download failed after %d retries: %w", S3_MAX_RETRIES, err)
    }

    // 3. Extract archive to root filesystem
    err = ExtractTarGz(tarPath, "/")
    if err != nil {
        return fmt.Errorf("tar extraction failed: %w", err)
    }

    // 4. Verify restored files exist
    if !FileExists("~/.claude/") && !FileExists("~/.cursor/") {
        return fmt.Errorf("session restore verification failed: no session files found")
    }

    // 5. Cleanup temporary archive
    os.Remove(tarPath)

    log.Info("Session restored successfully", "agent_run_id", agentRunID)
    return nil
}
```

**Error Handling**: If S3 download fails after all retries, the agent-runner exits with code 1 (Job failure).

---

## Retry Logic

### Exponential Backoff with Jitter

**Configuration**:
- Initial interval: 1s
- Max interval: 16s
- Max retries: 5
- Jitter: ±20% random variance

**Implementation** (using `github.com/cenkalti/backoff/v4`):
```go
func S3UploadWithRetry(localPath, s3Key string) error {
    backoffConfig := backoff.NewExponentialBackOff()
    backoffConfig.InitialInterval = 1 * time.Second
    backoffConfig.MaxInterval = 16 * time.Second
    backoffConfig.MaxElapsedTime = 0 // Use max retries instead

    retryCount := 0
    operation := func() error {
        retryCount++
        log.Info("Uploading to S3", "attempt", retryCount, "key", s3Key)

        err := s3Client.Upload(context.Background(), s3Key, localPath)
        if err != nil {
            // Check if error is retryable
            if IsRetryableError(err) {
                log.Warn("S3 upload failed, retrying...", "error", err, "attempt", retryCount)
                return err // Trigger retry
            } else {
                // Non-retryable error (e.g., 403 Forbidden)
                log.Error("S3 upload failed with non-retryable error", "error", err)
                return backoff.Permanent(err) // Stop retrying
            }
        }

        log.Info("S3 upload succeeded", "key", s3Key, "attempt", retryCount)
        return nil
    }

    err := backoff.Retry(operation, backoff.WithMaxRetries(backoffConfig, 5))
    return err
}
```

### Retryable vs Non-Retryable Errors

**Retryable** (exponential backoff):
- Network timeout
- Connection refused
- HTTP 500 (Internal Server Error)
- HTTP 503 (Service Unavailable)
- Transient network errors

**Non-Retryable** (immediate failure):
- HTTP 403 (Forbidden) - Invalid credentials
- HTTP 404 (Not Found) - Bucket does not exist
- HTTP 400 (Bad Request) - Invalid request format
- Authentication errors

---

## Error Handling Strategy

### S3 Upload Failure

**Scenario**: Agent execution succeeded, but S3 upload fails after 5 retries

**Action**:
1. Log error: "S3 session upload failed after 5 retries: {error}"
2. Exit agent-runner with code 1 (Job failure)
3. Operator detects Pod failure (exit 1, no success report)
4. Operator increments `AgentRun.retry_count` (API Retry, not counted against 50 AI retries)
5. Operator creates new Kubernetes Job
6. New Pod re-attempts full workflow (including S3 upload)

**Rationale**: S3 storage is critical infrastructure. If unavailable, the system should not proceed.

---

### S3 Download Failure (on Retry)

**Scenario**: Pod starts for retry (retry_count > 0), but S3 download fails after 5 retries

**Action**:
1. Log error: "S3 session download failed after 5 retries: {error}"
2. Exit agent-runner with code 1 (Job failure)
3. Operator detects Pod failure
4. Operator increments `AgentRun.retry_count` (API Retry)
5. Operator creates new Kubernetes Job
6. New Pod re-attempts S3 download

**Rationale**: Without previous session context, retry is less effective. Ensure S3 is accessible before proceeding.

---

### Session Corruption

**Scenario**: Downloaded tar.gz is corrupted or incomplete

**Detection**:
- `tar -xzf` fails with extraction error
- Extracted files have 0 bytes
- Session directory structure is incomplete

**Action**:
1. Log error: "Session restore failed: corrupted archive"
2. Delete corrupted session from S3 (optional)
3. Exit agent-runner with code 1
4. Operator triggers retry (which will fail S3 download and escalate)

**Fallback**: Manual intervention required - reset `retry_count` to 0 via CLI

---

## Security Considerations

### Credentials Exclusion

**Critical**: DO NOT persist credentials to S3

**Excluded Files**:
- `~/.claude/.credentials.json` - Claude API key
- `~/.cursor/.credentials.json` - Cursor API key
- `/workspace/.env` - Environment variables
- Any file matching `*.pem`, `*.key`, `*_key`

**Verification**:
```go
func ShouldExclude(filePath string) bool {
    excludePatterns := []string{
        ".credentials.json",
        ".env",
        "*.pem",
        "*.key",
        "*_key",
        "node_modules/",
        ".git/",
    }

    for _, pattern := range excludePatterns {
        if matched, _ := filepath.Match(pattern, filepath.Base(filePath)); matched {
            return true
        }
    }
    return false
}
```

### S3 Bucket Access Control

**MinIO Bucket Policy**:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": ["arn:aws:iam:::user/agent-runner"]
      },
      "Action": [
        "s3:PutObject",
        "s3:GetObject",
        "s3:DeleteObject"
      ],
      "Resource": ["arn:aws:s3:::agent-sessions/*"]
    }
  ]
}
```

**Encryption**: Use server-side encryption (SSE-S3) for session data at rest

---

## Performance Optimization

### Compression

**Algorithm**: gzip with level 6 (balanced compression/speed)

**Expected Sizes**:
- Initial session (1-2 MB): Compressed to ~200-400 KB
- Retry session (5-10 MB): Compressed to ~1-2 MB

**Benchmark**:
- Compression time: ~100-500 ms for 5 MB session
- Upload time (10 Mbps network): ~1-2 seconds for 1 MB file
- Download time: ~1-2 seconds
- Extraction time: ~100-300 ms

**Total Overhead**: ~3-5 seconds per Pod execution (save + restore)

---

### Selective Archiving

**Optimization**: Only include changed files on retry

**Algorithm**:
```go
func IncrementalArchive(agentRunID int, retryCount int) error {
    if retryCount == 0 {
        // Initial: Save full session
        return FullArchive(agentRunID)
    } else {
        // Retry: Only save modified files since last execution
        // Note: Requires tracking file mtimes - implement if performance becomes issue
        return FullArchive(agentRunID) // Fallback to full archive for now
    }
}
```

**Future Enhancement**: Implement delta compression using rsync-style algorithm

---

## Monitoring & Observability

### Metrics to Track

**S3 Operation Metrics**:
- `s3_upload_duration_seconds` (histogram) - Upload latency
- `s3_download_duration_seconds` (histogram) - Download latency
- `s3_upload_retries_total` (counter) - Retry attempts
- `s3_upload_failures_total` (counter) - Permanent failures
- `session_size_bytes` (histogram) - Archive size distribution

**Session Restore Metrics**:
- `session_restore_success_total` (counter) - Successful restores
- `session_restore_failure_total` (counter) - Failed restores
- `session_files_restored_total` (gauge) - Number of files per session

### Log Events

**Critical Events** (log level: INFO):
- Session save started/completed
- Session restore started/completed
- S3 upload/download success

**Warning Events** (log level: WARN):
- S3 retry attempt (with backoff duration)
- Large session size (> 10 MB)

**Error Events** (log level: ERROR):
- S3 upload/download failure after all retries
- Session corruption detected
- Credentials found in session archive (security violation)

---

## Testing Strategy

### Unit Tests

**S3 Client Tests** (`agent-runner/pkg/storage/s3_test.go`):
- Mock S3 API responses
- Test retry logic with injected failures
- Verify path-style URL construction for MinIO
- Test credential handling

**Session Archive Tests** (`agent-runner/pkg/storage/session_test.go`):
- Test tar.gz compression/extraction
- Verify file exclusion patterns (credentials, node_modules)
- Test archive integrity verification
- Test incremental vs full archive

### Integration Tests

**End-to-End Test**:
```go
func TestSessionPersistenceFlow(t *testing.T) {
    // 1. Start MinIO test server
    minioServer := StartTestMinIO()
    defer minioServer.Stop()

    // 2. Execute agent-runner (initial)
    runID := 123
    retryCount := 0
    ExecuteAgentRunner(runID, retryCount)

    // 3. Verify S3 upload
    sessionKey := fmt.Sprintf("sessions/%d/session.tar.gz", runID)
    assert.True(t, S3ObjectExists(sessionKey))

    // 4. Execute agent-runner (retry)
    retryCount = 1
    ExecuteAgentRunner(runID, retryCount)

    // 5. Verify session was restored
    assert.True(t, FileExists("~/.claude/projects/..."))

    // 6. Verify updated session uploaded
    assert.True(t, S3ObjectExists(sessionKey))
}
```

**Failure Scenario Tests**:
- S3 unavailable on upload → Pod exits with code 1
- S3 unavailable on download → Pod exits with code 1
- Corrupted archive → Pod exits with code 1
- Network timeout → Exponential backoff retry succeeds

---

## Deployment Checklist

### MinIO Setup

- [ ] Deploy MinIO server (k8s StatefulSet or external service)
- [ ] Create `agent-sessions` bucket
- [ ] Configure bucket policy (restrict to agent-runner user)
- [ ] Generate S3 access credentials
- [ ] Store credentials in Kubernetes Secret

### Kubernetes Configuration

- [ ] Add S3 environment variables to Pod template (k8s/pod-template.yaml)
- [ ] Mount S3 credentials as Secret volumes (optional, or use env vars)
- [ ] Configure ServiceAccount with S3 access permissions (if using IRSA/Workload Identity)

### Testing

- [ ] Run integration test suite with MinIO test server
- [ ] Deploy to staging environment
- [ ] Trigger manual retry with session restore
- [ ] Verify session continuity (agent remembers previous context)
- [ ] Test S3 failure scenarios (disconnect MinIO)

---

## References

- [agent-runner-detail.md](./agent-runner-detail.md) - Agent runner implementation guide
- [retry-decision-tree.md](./retry-decision-tree.md) - Retry logic and error handling
- [AWS SDK for Go v2](https://aws.github.io/aws-sdk-go-v2/docs/) - S3 client library
- [MinIO Go Client SDK](https://min.io/docs/minio/linux/developers/go/minio-go.html) - Alternative S3 client

---

**Version**: 1.0.0
**Last Updated**: 2025-11-01
