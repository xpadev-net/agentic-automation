# Discord Notifications Contract

**Version**: 1.0.0
**Last Updated**: 2025-10-31

## Overview

Discord notifications are sent via Discord Webhook for critical events (failures, max retries exceeded). All notifications use Discord's **embed** format for rich formatting.

**Webhook URL**: Stored in environment variable `DISCORD_WEBHOOK_URL`

---

## Notification Types

### 1. Agent Execution Failed

**Trigger**: AgentRun state transitions to `failed` after max retries (50)

**Payload**:
```json
{
  "embeds": [
    {
      "title": "🚨 Agent Execution Failed",
      "description": "**Issue #42**: Fix parser bug",
      "color": 15158332,
      "fields": [
        {
          "name": "Repository",
          "value": "owner/repo",
          "inline": true
        },
        {
          "name": "Agent Type",
          "value": "claude-code",
          "inline": true
        },
        {
          "name": "Retry Count",
          "value": "50/50",
          "inline": true
        },
        {
          "name": "Failure Reason",
          "value": "Lint failed: 5 errors found",
          "inline": false
        },
        {
          "name": "Issue URL",
          "value": "[View Issue](https://github.com/owner/repo/issues/42)",
          "inline": false
        }
      ],
      "timestamp": "2025-10-31T12:00:00.000Z",
      "footer": {
        "text": "GitHub Agent Automation"
      }
    }
  ]
}
```

**Color**: Red (#E74C3C / 15158332)

---

### 2. CI Failure (Retry Triggered)

**Trigger**: CI check suite fails, retry triggered (retry_count < 50)

**Payload**:
```json
{
  "embeds": [
    {
      "title": "⚠️ CI Failed - Retry Triggered",
      "description": "**PR #101**: Fix parser bug",
      "color": 16776960,
      "fields": [
        {
          "name": "Repository",
          "value": "owner/repo",
          "inline": true
        },
        {
          "name": "Retry Count",
          "value": "3/50",
          "inline": true
        },
        {
          "name": "CI Error",
          "value": "```\nTests failed: 5 tests\n  - parser_test.ts:42 Expected 'foo' but got 'bar'\n```",
          "inline": false
        },
        {
          "name": "PR URL",
          "value": "[View PR](https://github.com/owner/repo/pull/101)",
          "inline": false
        }
      ],
      "timestamp": "2025-10-31T12:00:00.000Z",
      "footer": {
        "text": "GitHub Agent Automation"
      }
    }
  ]
}
```

**Color**: Yellow (#FFFF00 / 16776960)

**Note**: Only send if retry_count is multiple of 10 (to avoid spam)

---

### 3. Max Retries Exceeded

**Trigger**: retry_count reaches 50, execution stops

**Payload**:
```json
{
  "embeds": [
    {
      "title": "❌ Max Retries Exceeded",
      "description": "**Issue #42**: Fix parser bug",
      "color": 10038562,
      "fields": [
        {
          "name": "Repository",
          "value": "owner/repo",
          "inline": true
        },
        {
          "name": "Agent Type",
          "value": "claude-code",
          "inline": true
        },
        {
          "name": "Total Attempts",
          "value": "50",
          "inline": true
        },
        {
          "name": "Last Error",
          "value": "Lint failed: missing semicolon at line 42",
          "inline": false
        },
        {
          "name": "Action Required",
          "value": "Manual intervention needed. Check Issue comments for details.",
          "inline": false
        },
        {
          "name": "Issue URL",
          "value": "[View Issue](https://github.com/owner/repo/issues/42)",
          "inline": false
        }
      ],
      "timestamp": "2025-10-31T12:00:00.000Z",
      "footer": {
        "text": "GitHub Agent Automation | Manual Review Required"
      }
    }
  ]
}
```

**Color**: Dark Red (#99004A / 10038562)

**Note**: This triggers both GitHub Issue comment AND Discord notification (dual channel)

---

### 4. PR Auto-Merged

**Trigger**: PR successfully auto-merged (optional, success notification)

**Payload**:
```json
{
  "embeds": [
    {
      "title": "✅ PR Auto-Merged",
      "description": "**Issue #42**: Fix parser bug",
      "color": 3066993,
      "fields": [
        {
          "name": "Repository",
          "value": "owner/repo",
          "inline": true
        },
        {
          "name": "PR Number",
          "value": "#101",
          "inline": true
        },
        {
          "name": "Retry Count",
          "value": "2",
          "inline": true
        },
        {
          "name": "PR URL",
          "value": "[View PR](https://github.com/owner/repo/pull/101)",
          "inline": false
        }
      ],
      "timestamp": "2025-10-31T12:00:00.000Z",
      "footer": {
        "text": "GitHub Agent Automation"
      }
    }
  ]
}
```

**Color**: Green (#2ECC71 / 3066993)

**Note**: Optional notification, can be disabled via config

---

### 5. Operator API Error

**Trigger**: agent-runner fails to report result to Operator API (all retries exhausted)

**Payload**:
```json
{
  "embeds": [
    {
      "title": "🔌 Operator API Unreachable",
      "description": "**AgentRun #456** failed to report result",
      "color": 15105570,
      "fields": [
        {
          "name": "Agent Run ID",
          "value": "456",
          "inline": true
        },
        {
          "name": "Pod Name",
          "value": "agent-runner-456",
          "inline": true
        },
        {
          "name": "Error",
          "value": "Failed to send report after 5 attempts: connection refused",
          "inline": false
        },
        {
          "name": "Action Required",
          "value": "Check Operator health and Pod logs:\n```\nkubectl logs agent-runner-456\n```",
          "inline": false
        }
      ],
      "timestamp": "2025-10-31T12:00:00.000Z",
      "footer": {
        "text": "GitHub Agent Automation | Infrastructure Issue"
      }
    }
  ]
}
```

**Color**: Orange (#E67E22 / 15105570)

---

## Implementation

### Discord Webhook Client

**Location**: `src/lib/discord-client.ts`

**Example**:
```typescript
import axios from 'axios';

export class DiscordNotificationService {
  private webhookUrl: string;

  constructor() {
    this.webhookUrl = process.env.DISCORD_WEBHOOK_URL!;
  }

  async sendFailureNotification(agentRun: AgentRun, issue: Issue) {
    const payload = {
      embeds: [
        {
          title: '🚨 Agent Execution Failed',
          description: `**Issue #${issue.number}**: ${issue.title}`,
          color: 15158332, // Red
          fields: [
            {
              name: 'Repository',
              value: issue.repo,
              inline: true,
            },
            {
              name: 'Agent Type',
              value: agentRun.agent_type,
              inline: true,
            },
            {
              name: 'Retry Count',
              value: `${agentRun.retry_count}/50`,
              inline: true,
            },
            {
              name: 'Failure Reason',
              value: agentRun.error_message || 'Unknown error',
              inline: false,
            },
            {
              name: 'Issue URL',
              value: `[View Issue](https://github.com/${issue.repo}/issues/${issue.number})`,
              inline: false,
            },
          ],
          timestamp: new Date().toISOString(),
          footer: {
            text: 'GitHub Agent Automation',
          },
        },
      ],
    };

    await this.send(payload);
  }

  async sendMaxRetriesNotification(agentRun: AgentRun, issue: Issue) {
    const payload = {
      embeds: [
        {
          title: '❌ Max Retries Exceeded',
          description: `**Issue #${issue.number}**: ${issue.title}`,
          color: 10038562, // Dark Red
          fields: [
            {
              name: 'Repository',
              value: issue.repo,
              inline: true,
            },
            {
              name: 'Agent Type',
              value: agentRun.agent_type,
              inline: true,
            },
            {
              name: 'Total Attempts',
              value: '50',
              inline: true,
            },
            {
              name: 'Last Error',
              value: agentRun.error_message || 'Unknown error',
              inline: false,
            },
            {
              name: 'Action Required',
              value: 'Manual intervention needed. Check Issue comments for details.',
              inline: false,
            },
            {
              name: 'Issue URL',
              value: `[View Issue](https://github.com/${issue.repo}/issues/${issue.number})`,
              inline: false,
            },
          ],
          timestamp: new Date().toISOString(),
          footer: {
            text: 'GitHub Agent Automation | Manual Review Required',
          },
        },
      ],
    };

    await this.send(payload);
  }

  private async send(payload: any) {
    try {
      await axios.post(this.webhookUrl, payload, {
        headers: { 'Content-Type': 'application/json' },
      });
    } catch (error) {
      console.error('Failed to send Discord notification:', error);
      // Do not throw - notification failure should not block main flow
    }
  }
}
```

---

## Error Handling

### Webhook Failures

**Retry**: 3 attempts with 1s delay

**Failure behavior**: Log error but do not throw exception (notifications are non-critical)

**Rate Limiting**: Discord webhooks have rate limit of 30 requests per minute. Implement throttling:

```typescript
private lastSent: number = 0;
private MIN_INTERVAL_MS = 2000; // 2 seconds

private async send(payload: any) {
  // Throttle to max 30/min
  const now = Date.now();
  const elapsed = now - this.lastSent;
  if (elapsed < this.MIN_INTERVAL_MS) {
    await new Promise(resolve => setTimeout(resolve, this.MIN_INTERVAL_MS - elapsed));
  }

  try {
    await axios.post(this.webhookUrl, payload);
    this.lastSent = Date.now();
  } catch (error) {
    console.error('Discord notification failed:', error);
  }
}
```

---

## Color Codes

| Event Type | Color Name | Hex Code | Decimal |
|------------|------------|----------|---------|
| Success | Green | #2ECC71 | 3066993 |
| Warning | Yellow | #FFFF00 | 16776960 |
| Error | Red | #E74C3C | 15158332 |
| Critical | Dark Red | #99004A | 10038562 |
| Info | Blue | #3498DB | 3447003 |
| Alert | Orange | #E67E22 | 15105570 |

---

## Testing

### Unit Tests

**Location**: `tests/unit/services/discord-notification.test.ts`

**Tests**:
- Payload format validation
- Retry logic on webhook failures
- Rate limiting throttle
- Color code correctness

### Integration Tests

**Location**: `tests/integration/discord-webhook.test.ts`

**Tests**:
- Send notification to test webhook URL
- Verify embed rendering in Discord
- Test error handling (invalid webhook URL)

---

## Configuration

### Environment Variables

```env
# Discord Webhook URL
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123456/your_webhook_token

# Optional: Disable success notifications (reduce noise)
DISCORD_NOTIFY_SUCCESS=false

# Optional: Notify only on specific retry counts
DISCORD_NOTIFY_RETRY_INTERVAL=10  # Notify every 10 retries
```

### Notification Filters

**Default behavior**:
- ✅ Agent execution failed (always)
- ✅ Max retries exceeded (always)
- ✅ Operator API error (always)
- ⚠️ CI failure (every 10 retries)
- ❌ PR auto-merged (disabled by default)

**Customization**: Modify `src/services/DiscordNotificationService.ts` to add filters

---

## Discord Channel Setup

### 1. Create Webhook

1. Open Discord server settings
2. Go to "Integrations" → "Webhooks"
3. Click "New Webhook"
4. **Name**: GitHub Agent Notifications
5. **Channel**: Select target channel (e.g., `#automation-alerts`)
6. Copy webhook URL

### 2. Test Webhook

```bash
curl -X POST \
  -H "Content-Type: application/json" \
  -d '{
    "embeds": [{
      "title": "🧪 Test Notification",
      "description": "GitHub Agent Automation is online",
      "color": 3447003,
      "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%S.000Z)'"
    }]
  }' \
  "${DISCORD_WEBHOOK_URL}"
```

Expected: Embed message appears in Discord channel

---

## Security Considerations

### 1. Webhook URL Protection

**DO NOT** commit webhook URL to git:
- Store in `.env` file (gitignored)
- Use Kubernetes Secret in production

**Rotation**: If webhook URL is leaked, delete and recreate in Discord settings

### 2. Message Content Sanitization

**Risk**: Malicious Issue titles/bodies could inject mentions or links

**Mitigation**: Escape Discord markdown:
```typescript
function sanitize(text: string): string {
  return text
    .replace(/@everyone/g, '@ everyone')
    .replace(/@here/g, '@ here')
    .replace(/<@/g, '< @');
}
```

### 3. Rate Limit Compliance

**Discord Limits**:
- 30 requests per minute per webhook
- Message size: 2000 characters
- Embed size: 6000 characters total

**Enforcement**: Implement throttling as shown above

---

## References

- **Discord Webhook API**: https://discord.com/developers/docs/resources/webhook
- **Embed Object**: https://discord.com/developers/docs/resources/channel#embed-object
- **Operator Notification Service**: `src/services/DiscordNotificationService.ts`
- **Functional Requirements**: [../spec.md](../spec.md) (FR-008, FR-014)

---

**Contract Version**: 1.0.0
**Last Updated**: 2025-10-31
