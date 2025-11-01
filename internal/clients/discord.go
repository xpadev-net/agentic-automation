package clients

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Color constants for Discord embeds
const (
	ColorSuccess  = 3066993  // Green #2ECC71
	ColorWarning  = 16776960 // Yellow #FFFF00
	ColorError    = 15158332 // Red #E74C3C
	ColorCritical = 10038562 // Dark Red #99004A
	ColorInfo     = 3447003  // Blue #3498DB
	ColorAlert    = 15105570 // Orange #E67E22
)

// DiscordEmbedFooter represents the footer of a Discord embed
type DiscordEmbedFooter struct {
	Text string `json:"text"`
}

// DiscordEmbedField represents a field in a Discord embed
type DiscordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// DiscordEmbed represents a Discord embed object
type DiscordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Fields      []DiscordEmbedField `json:"fields,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
	Footer      *DiscordEmbedFooter `json:"footer,omitempty"`
}

// DiscordPayload represents the request payload for Discord webhook
type DiscordPayload struct {
	Embeds []DiscordEmbed `json:"embeds"`
}

// DiscordClient represents a Discord webhook client
type DiscordClient struct {
	webhookURL string
	httpClient *http.Client
	logger     *zap.Logger
	lastSent   time.Time  // For future rate limiting
	mu         sync.Mutex // For future rate limiting
}

// NewDiscordClient creates a new Discord webhook client
// If webhookURL is empty, it attempts to load from DISCORD_WEBHOOK_URL environment variable
// Returns nil if webhook URL is not available (non-blocking, logs warning)
func NewDiscordClient(webhookURL string, logger *zap.Logger) *DiscordClient {
	// If webhookURL is not provided, try to get from environment
	if webhookURL == "" {
		webhookURL = config.GetEnv("DISCORD_WEBHOOK_URL", "")
	}

	// If still empty, log warning and return nil
	if webhookURL == "" {
		if logger != nil {
			logger.Warn("Discord webhook URL is not configured. Discord notifications will be disabled.")
		}
		return nil
	}

	return &DiscordClient{
		webhookURL: webhookURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// send sends a Discord payload to the webhook URL
// This is a non-blocking method - errors are logged but not returned
func (c *DiscordClient) send(ctx context.Context, payload DiscordPayload) error {
	if c == nil || c.webhookURL == "" {
		return nil // Client is disabled, silently skip
	}

	// Marshal payload to JSON
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		c.logger.Error("Failed to marshal Discord payload",
			zap.Error(err),
			zap.String("notification_type", "unknown"))
		return nil // Non-blocking
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.webhookURL, bytes.NewReader(payloadBytes))
	if err != nil {
		c.logger.Error("Failed to create Discord webhook request",
			zap.Error(err))
		return nil // Non-blocking
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to send Discord notification",
			zap.Error(err),
			zap.String("webhook_url", c.maskWebhookURL()))
		return nil // Non-blocking
	}
	defer resp.Body.Close()

	// Read response body for error logging
	respBody, _ := io.ReadAll(resp.Body)

	// Check status code
	if resp.StatusCode >= 400 {
		c.logger.Error("Discord webhook returned error status",
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(respBody)),
			zap.String("webhook_url", c.maskWebhookURL()))
		return nil // Non-blocking
	}

	// Success
	c.logger.Info("Discord notification sent successfully",
		zap.Int("status_code", resp.StatusCode),
		zap.String("webhook_url", c.maskWebhookURL()))
	return nil
}

// maskWebhookURL returns a masked version of the webhook URL for logging
// Shows only the last few characters of the token
func (c *DiscordClient) maskWebhookURL() string {
	if c.webhookURL == "" {
		return ""
	}

	// Format: https://discord.com/api/webhooks/{id}/{token}
	parts := strings.Split(c.webhookURL, "/")
	if len(parts) < 2 {
		return "***"
	}

	token := parts[len(parts)-1]
	if len(token) <= 4 {
		return "***"
	}

	masked := "***" + token[len(token)-4:]
	return strings.Join(parts[:len(parts)-1], "/") + "/" + masked
}

// sanitizeMessage sanitizes text to prevent Discord mention injection
func sanitizeMessage(text string) string {
	text = strings.ReplaceAll(text, "@everyone", "@ everyone")
	text = strings.ReplaceAll(text, "@here", "@ here")
	text = strings.ReplaceAll(text, "<@", "< @")
	return text
}

// getErrorMessage returns the error message, or "Unknown error" if nil/empty
func getErrorMessage(msg *string) string {
	if msg == nil || *msg == "" {
		return "Unknown error"
	}
	return *msg
}

// formatGitHubURL formats a GitHub URL for issues or PRs
func formatGitHubURL(repo string, number int, isPR bool) string {
	if isPR {
		return fmt.Sprintf("https://github.com/%s/pull/%d", repo, number)
	}
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, number)
}

// SendFailureNotification sends an agent execution failure notification
func (c *DiscordClient) SendFailureNotification(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue) error {
	if c == nil {
		return nil // Client is disabled
	}

	embed := DiscordEmbed{
		Title:       sanitizeMessage("🚨 Agent Execution Failed"),
		Description: sanitizeMessage(fmt.Sprintf("**Issue #%d**: %s", issue.Number, issue.Title)),
		Color:       ColorError,
		Fields: []DiscordEmbedField{
			{
				Name:   "Repository",
				Value:  sanitizeMessage(issue.Repo),
				Inline: true,
			},
			{
				Name:   "Agent Type",
				Value:  sanitizeMessage(agentRun.AgentType),
				Inline: true,
			},
			{
				Name:   "Retry Count",
				Value:  fmt.Sprintf("%d/50", agentRun.RetryCount),
				Inline: true,
			},
			{
				Name:   "Failure Reason",
				Value:  sanitizeMessage(getErrorMessage(agentRun.ErrorMessage)),
				Inline: false,
			},
			{
				Name:   "Issue URL",
				Value:  fmt.Sprintf("[View Issue](%s)", formatGitHubURL(issue.Repo, issue.Number, false)),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Footer: &DiscordEmbedFooter{
			Text: "GitHub Agent Automation",
		},
	}

	payload := DiscordPayload{
		Embeds: []DiscordEmbed{embed},
	}

	c.logger.Info("Sending Discord failure notification",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("issue_number", issue.Number))

	return c.send(ctx, payload)
}

// SendMaxRetriesNotification sends a max retries exceeded notification
func (c *DiscordClient) SendMaxRetriesNotification(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue) error {
	if c == nil {
		return nil // Client is disabled
	}

	embed := DiscordEmbed{
		Title:       sanitizeMessage("❌ Max Retries Exceeded"),
		Description: sanitizeMessage(fmt.Sprintf("**Issue #%d**: %s", issue.Number, issue.Title)),
		Color:       ColorCritical,
		Fields: []DiscordEmbedField{
			{
				Name:   "Repository",
				Value:  sanitizeMessage(issue.Repo),
				Inline: true,
			},
			{
				Name:   "Agent Type",
				Value:  sanitizeMessage(agentRun.AgentType),
				Inline: true,
			},
			{
				Name:   "Total Attempts",
				Value:  fmt.Sprintf("%d", agentRun.RetryCount),
				Inline: true,
			},
			{
				Name:   "Last Error",
				Value:  sanitizeMessage(getErrorMessage(agentRun.ErrorMessage)),
				Inline: false,
			},
			{
				Name:   "Action Required",
				Value:  "Manual intervention needed. Check Issue comments for details.",
				Inline: false,
			},
			{
				Name:   "Issue URL",
				Value:  fmt.Sprintf("[View Issue](%s)", formatGitHubURL(issue.Repo, issue.Number, false)),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Footer: &DiscordEmbedFooter{
			Text: "GitHub Agent Automation | Manual Review Required",
		},
	}

	payload := DiscordPayload{
		Embeds: []DiscordEmbed{embed},
	}

	c.logger.Info("Sending Discord max retries notification",
		zap.Int("agent_run_id", agentRun.ID),
		zap.Int("issue_number", issue.Number))

	return c.send(ctx, payload)
}

// SendCIFailureNotification sends a CI failure notification
// Only sends if retryCount is a multiple of 10 (to prevent spam)
func (c *DiscordClient) SendCIFailureNotification(ctx context.Context, pr *models.PullRequest, retryCount int, ciError string) error {
	if c == nil {
		return nil // Client is disabled
	}

	// Only send if retryCount is a multiple of 10
	if retryCount%10 != 0 {
		c.logger.Debug("Skipping CI failure notification (not a multiple of 10)",
			zap.Int("retry_count", retryCount))
		return nil
	}

	embed := DiscordEmbed{
		Title:       sanitizeMessage("⚠️ CI Failed - Retry Triggered"),
		Description: sanitizeMessage(fmt.Sprintf("**PR #%d**: %s", pr.Number, pr.Branch)),
		Color:       ColorWarning,
		Fields: []DiscordEmbedField{
			{
				Name:   "Repository",
				Value:  sanitizeMessage(pr.Repo),
				Inline: true,
			},
			{
				Name:   "Retry Count",
				Value:  fmt.Sprintf("%d/50", retryCount),
				Inline: true,
			},
			{
				Name:   "CI Error",
				Value:  sanitizeMessage(fmt.Sprintf("```\n%s\n```", ciError)),
				Inline: false,
			},
			{
				Name:   "PR URL",
				Value:  fmt.Sprintf("[View PR](%s)", formatGitHubURL(pr.Repo, pr.Number, true)),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Footer: &DiscordEmbedFooter{
			Text: "GitHub Agent Automation",
		},
	}

	payload := DiscordPayload{
		Embeds: []DiscordEmbed{embed},
	}

	c.logger.Info("Sending Discord CI failure notification",
		zap.Int("pr_number", pr.Number),
		zap.Int("retry_count", retryCount))

	return c.send(ctx, payload)
}

// SendPRMergedNotification sends a PR auto-merged success notification
// Only sends if DISCORD_NOTIFY_SUCCESS is set to "true"
func (c *DiscordClient) SendPRMergedNotification(ctx context.Context, pr *models.PullRequest, issue *models.Issue, retryCount int) error {
	if c == nil {
		return nil // Client is disabled
	}

	// Check if success notifications are enabled
	notifySuccess := config.GetEnv("DISCORD_NOTIFY_SUCCESS", "false")
	if notifySuccess != "true" {
		c.logger.Debug("Skipping PR merged notification (DISCORD_NOTIFY_SUCCESS is not enabled)")
		return nil
	}

	var issueDescription string
	if issue != nil {
		issueDescription = sanitizeMessage(fmt.Sprintf("**Issue #%d**: %s", issue.Number, issue.Title))
	} else {
		issueDescription = sanitizeMessage(fmt.Sprintf("**PR #%d**: %s", pr.Number, pr.Branch))
	}

	embed := DiscordEmbed{
		Title:       sanitizeMessage("✅ PR Auto-Merged"),
		Description: issueDescription,
		Color:       ColorSuccess,
		Fields: []DiscordEmbedField{
			{
				Name:   "Repository",
				Value:  sanitizeMessage(pr.Repo),
				Inline: true,
			},
			{
				Name:   "PR Number",
				Value:  fmt.Sprintf("#%d", pr.Number),
				Inline: true,
			},
			{
				Name:   "Retry Count",
				Value:  fmt.Sprintf("%d", retryCount),
				Inline: true,
			},
			{
				Name:   "PR URL",
				Value:  fmt.Sprintf("[View PR](%s)", formatGitHubURL(pr.Repo, pr.Number, true)),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Footer: &DiscordEmbedFooter{
			Text: "GitHub Agent Automation",
		},
	}

	payload := DiscordPayload{
		Embeds: []DiscordEmbed{embed},
	}

	c.logger.Info("Sending Discord PR merged notification",
		zap.Int("pr_number", pr.Number))

	return c.send(ctx, payload)
}

// SendOperatorAPIErrorNotification sends an operator API error notification
func (c *DiscordClient) SendOperatorAPIErrorNotification(ctx context.Context, agentRunID int, podName, errorMsg string) error {
	if c == nil {
		return nil // Client is disabled
	}

	embed := DiscordEmbed{
		Title:       sanitizeMessage("🔌 Operator API Unreachable"),
		Description: sanitizeMessage(fmt.Sprintf("**AgentRun #%d** failed to report result", agentRunID)),
		Color:       ColorAlert,
		Fields: []DiscordEmbedField{
			{
				Name:   "Agent Run ID",
				Value:  fmt.Sprintf("%d", agentRunID),
				Inline: true,
			},
			{
				Name:   "Pod Name",
				Value:  sanitizeMessage(podName),
				Inline: true,
			},
			{
				Name:   "Error",
				Value:  sanitizeMessage(errorMsg),
				Inline: false,
			},
			{
				Name:   "Action Required",
				Value:  fmt.Sprintf("Check Operator health and Pod logs:\n```\nkubectl logs %s\n```", sanitizeMessage(podName)),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Footer: &DiscordEmbedFooter{
			Text: "GitHub Agent Automation | Infrastructure Issue",
		},
	}

	payload := DiscordPayload{
		Embeds: []DiscordEmbed{embed},
	}

	c.logger.Info("Sending Discord operator API error notification",
		zap.Int("agent_run_id", agentRunID),
		zap.String("pod_name", podName))

	return c.send(ctx, payload)
}
