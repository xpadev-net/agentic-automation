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

### Discord Webhook Client (Go)

**Location**: `internal/clients/discord.go`

**Example**:
```go
package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/your-org/agentic-automation/internal/models"
)

type DiscordClient struct {
	webhookURL string
	httpClient *http.Client
}

func NewDiscordClient(webhookURL string) *DiscordClient {
	return &DiscordClient{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type DiscordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Color       int                 `json:"color"`
	Fields      []DiscordEmbedField `json:"fields"`
	Timestamp   string              `json:"timestamp"`
	Footer      DiscordEmbedFooter  `json:"footer"`
}

type DiscordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type DiscordEmbedFooter struct {
	Text string `json:"text"`
}

type DiscordPayload struct {
	Embeds []DiscordEmbed `json:"embeds"`
}

func (d *DiscordClient) SendFailureNotification(agentRun *models.AgentRun, issue *models.Issue) error {
	payload := DiscordPayload{
		Embeds: []DiscordEmbed{
			{
				Title:       "🚨 Agent Execution Failed",
				Description: fmt.Sprintf("**Issue #%d**: %s", issue.Number, issue.Title),
				Color:       15158332, // Red
				Fields: []DiscordEmbedField{
					{Name: "Repository", Value: issue.Repo, Inline: true},
					{Name: "Agent Type", Value: agentRun.AgentType, Inline: true},
					{Name: "Retry Count", Value: fmt.Sprintf("%d/50", agentRun.RetryCount), Inline: true},
					{Name: "Failure Reason", Value: getErrorMessage(agentRun.ErrorMessage), Inline: false},
					{Name: "Issue URL", Value: fmt.Sprintf("[View Issue](https://github.com/%s/issues/%d)", issue.Repo, issue.Number), Inline: false},
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Footer:    DiscordEmbedFooter{Text: "GitHub Agent Automation"},
			},
		},
	}

	return d.send(payload)
}

func (d *DiscordClient) SendMaxRetriesNotification(agentRun *models.AgentRun, issue *models.Issue) error {
	payload := DiscordPayload{
		Embeds: []DiscordEmbed{
			{
				Title:       "❌ Max Retries Exceeded",
				Description: fmt.Sprintf("**Issue #%d**: %s", issue.Number, issue.Title),
				Color:       10038562, // Dark Red
				Fields: []DiscordEmbedField{
					{Name: "Repository", Value: issue.Repo, Inline: true},
					{Name: "Agent Type", Value: agentRun.AgentType, Inline: true},
					{Name: "Total Attempts", Value: "50", Inline: true},
					{Name: "Last Error", Value: getErrorMessage(agentRun.ErrorMessage), Inline: false},
					{Name: "Action Required", Value: "Manual intervention needed. Check Issue comments for details.", Inline: false},
					{Name: "Issue URL", Value: fmt.Sprintf("[View Issue](https://github.com/%s/issues/%d)", issue.Repo, issue.Number), Inline: false},
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Footer:    DiscordEmbedFooter{Text: "GitHub Agent Automation | Manual Review Required"},
			},
		},
	}

	return d.send(payload)
}

func (d *DiscordClient) send(payload DiscordPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", d.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		// Log error but do not throw - notification failure should not block main flow
		fmt.Printf("Failed to send Discord notification: %v\n", err)
		return nil // Return nil to not block execution
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		fmt.Printf("Discord webhook returned error status: %d\n", resp.StatusCode)
	}

	return nil
}

func getErrorMessage(msg *string) string {
	if msg == nil || *msg == "" {
		return "Unknown error"
	}
	return *msg
}
```

---

## Error Handling

### Webhook Failures

**Retry**: 3 attempts with 1s delay

**Failure behavior**: Log error but do not throw exception (notifications are non-critical)

**Rate Limiting**: Discord webhooks have rate limit of 30 requests per minute. Implement throttling:

```go
import (
	"sync"
	"time"
)

type RateLimitedDiscordClient struct {
	*DiscordClient
	lastSent      time.Time
	minInterval   time.Duration
	mu            sync.Mutex
}

func NewRateLimitedDiscordClient(webhookURL string) *RateLimitedDiscordClient {
	return &RateLimitedDiscordClient{
		DiscordClient: NewDiscordClient(webhookURL),
		minInterval:   2 * time.Second, // Max 30/min
	}
}

func (d *RateLimitedDiscordClient) send(payload DiscordPayload) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Throttle to max 30/min
	now := time.Now()
	elapsed := now.Sub(d.lastSent)
	if elapsed < d.minInterval {
		time.Sleep(d.minInterval - elapsed)
	}

	err := d.DiscordClient.send(payload)
	if err == nil {
		d.lastSent = time.Now()
	}

	return err
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

**Location**: `tests/unit/clients/discord_client_test.go`

**Tests**:
- Payload format validation
- Retry logic on webhook failures
- Rate limiting throttle
- Color code correctness

### Integration Tests

**Location**: `tests/integration/discord_webhook_test.go`

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
```go
import "strings"

func sanitize(text string) string {
	text = strings.ReplaceAll(text, "@everyone", "@ everyone")
	text = strings.ReplaceAll(text, "@here", "@ here")
	text = strings.ReplaceAll(text, "<@", "< @")
	return text
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
