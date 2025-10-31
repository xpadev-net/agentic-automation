# quickstart.md: GitHub Agent Automation

**Last Updated**: 2025-10-31
**Prerequisites**: Node.js 22+, MySQL 8.0+, GitHub account with admin access to target repository

## Overview

GitHub Agent Automation responds to Issue comments (`/run-agent`), executes AI to implement changes, creates PRs, requests Codex reviews, auto-retries on failures (max 50), and auto-merges on approval.

## Required Permissions

### GitHub
- **Repository Admin** or **Collaborator** with write permission
- GitHub App installation with permissions:
  - Contents: Read & Write (for Git operations)
  - Issues: Read & Write (for comments and status updates)
  - Pull Requests: Read & Write (for PR creation/merge)
  - Checks: Read (for CI status)
  - Metadata: Read (for repository info)

### External Services
- **Codex**: API key for code review service
- **Discord**: Webhook URL for failure notifications
- **AI Agent**: API endpoint and key (e.g., Claude API)

---

## Quick Start (5 minutes)

### 1. Clone and Install

```bash
git clone https://github.com/your-org/agentic-automation.git
cd agentic-automation
git checkout 001-github-agent-automation

# Install dependencies
npm install
# Or with pnpm (recommended):
pnpm install
```

### 2. Setup Database

```bash
# Install MySQL 8.0+ if not already installed
# macOS:
brew install mysql

# Start MySQL server
brew services start mysql

# Create database
mysql -u root -p
```

```sql
CREATE DATABASE github_agent_automation CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'agent_user'@'localhost' IDENTIFIED BY 'secure_password';
GRANT ALL PRIVILEGES ON github_agent_automation.* TO 'agent_user'@'localhost';
FLUSH PRIVILEGES;
EXIT;
```

### 3. Configure Environment Variables

Create `.env` file in project root:

```bash
cp .env.example .env
```

Edit `.env` with your values:

```env
# Database
DATABASE_URL="mysql://agent_user:secure_password@localhost:3306/github_agent_automation"

# GitHub App Credentials
GITHUB_APP_ID=123456
GITHUB_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA...
...
-----END RSA PRIVATE KEY-----"
GITHUB_WEBHOOK_SECRET=your_webhook_secret_from_github_app

# External Services
CODEX_API_KEY=your_codex_api_key
CODEX_API_URL=https://codex.example.com/api
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123456/your_webhook_token

# AI Agent
AI_AGENT_API_KEY=your_ai_agent_key
AI_AGENT_API_URL=https://api.anthropic.com/v1/messages

# Server Configuration
PORT=3000
NODE_ENV=development
LOG_LEVEL=info
```

### 4. Run Database Migrations

```bash
# Generate Prisma Client from schema
npx prisma generate

# Run migrations to create tables
npx prisma migrate dev --name init

# Optional: Seed test data
npx prisma db seed
```

### 5. Start the Server

```bash
# Development mode with hot reload
npm run dev

# Or with pnpm:
pnpm dev

# Production mode
npm run build
npm start
```

Server should be running at `http://localhost:3000`

---

## Detailed Setup Instructions

### GitHub App Configuration

#### Step 1: Create GitHub App

1. Go to GitHub Settings → Developer settings → GitHub Apps
2. Click "New GitHub App"
3. Fill in:
   - **App name**: `GitHub Agent Automation`
   - **Homepage URL**: `https://your-domain.com`
   - **Webhook URL**: `https://your-domain.com/webhooks/github`
   - **Webhook secret**: Generate a random string (save for .env)

#### Step 2: Set Permissions

Under "Permissions", set:
- **Repository permissions**:
  - Contents: Read & Write
  - Issues: Read & Write
  - Pull requests: Read & Write
  - Checks: Read
  - Metadata: Read

#### Step 3: Subscribe to Events

Under "Subscribe to events", enable:
- `issue_comment` (for trigger detection)
- `pull_request` (for PR tracking)
- `pull_request_review` (for Codex reviews)
- `pull_request_review_comment` (for review comments)
- `check_suite` (for CI results)
- `status` (for CI status updates)
- `issues` (for dependency tracking - close/reopen)

#### Step 4: Generate Private Key

1. Scroll to bottom → "Generate a private key"
2. Save the downloaded `.pem` file
3. Convert to single-line string for .env:
   ```bash
   awk 'NF {sub(/\r/, ""); printf "%s\\n",$0;}' github-app.pem | pbcopy
   # Paste into .env as GITHUB_PRIVATE_KEY value
   ```

#### Step 5: Install App

1. Click "Install App" (left sidebar)
2. Select target repository/organization
3. Grant permissions
4. Save installation ID (visible in URL after installation)

### Codex API Setup

**Note**: Replace with actual Codex API documentation when available

1. Sign up for Codex account at `https://codex.example.com`
2. Generate API key from dashboard
3. Add to `.env` as `CODEX_API_KEY`
4. Test API connection:
   ```bash
   curl -H "Authorization: Bearer $CODEX_API_KEY" \
        https://codex.example.com/api/health
   ```

### Discord Webhook Setup

1. Open Discord server settings
2. Go to "Integrations" → "Webhooks"
3. Click "New Webhook"
4. Name: "GitHub Agent Notifications"
5. Select target channel
6. Copy webhook URL
7. Add to `.env` as `DISCORD_WEBHOOK_URL`

### AI Agent Configuration

For Claude API:

1. Get API key from https://console.anthropic.com
2. Add to `.env`:
   ```env
   AI_AGENT_API_KEY=sk-ant-...
   AI_AGENT_API_URL=https://api.anthropic.com/v1/messages
   ```

For other AI providers (OpenAI, etc.), adjust `AI_AGENT_API_URL` accordingly.

---

## Usage

### Basic Workflow

1. **Create Issue** in target repository describing the feature/bug
2. **Comment on Issue** with `/run-agent` to trigger automation
3. **Monitor Progress** via GitHub comments:
   - "🤖 Agent execution started..."
   - "📝 PR created: #123"
   - "🔍 Codex review requested"
   - "✅ CI passed, auto-merging..."
4. **Failures Notified** to Discord and GitHub Issue comments

### Managing Dependencies

To create task dependencies, include in Issue body or comment:

```markdown
This issue depends on #123 and #456
```

Or:

```markdown
blocked by #123
```

The system parses these and won't start execution until dependencies are closed.

### Manual Operations (CLI)

```bash
# Check execution status
npm run cli status <agent-run-id>

# Manual retry (if automatic retries exhausted)
npm run cli retry <agent-run-id>

# Manual trigger (for testing without webhook)
npm run cli trigger <issue-id>

# View logs
npm run cli logs --follow
```

### Testing Locally

Use ngrok to expose local webhook server:

```bash
# Install ngrok
brew install ngrok

# Expose port 3000
ngrok http 3000

# Update GitHub App webhook URL to ngrok URL
# e.g., https://abc123.ngrok.io/webhooks/github
```

---

## Common Operations

### View Database Records

```bash
# Open Prisma Studio (GUI for database)
npx prisma studio
# Opens at http://localhost:5555
```

### Check Server Health

```bash
curl http://localhost:3000/health
# Expected: {"status":"ok","database":"connected"}
```

### View Logs

Logs are written to stdout (JSON format) and can be piped to file:

```bash
npm start | tee -a logs/app.log
```

### Reset Database

```bash
# WARNING: Deletes all data
npx prisma migrate reset

# Re-run migrations
npx prisma migrate dev
```

---

## Troubleshooting

### Issue: Webhook not receiving events

**Solution**:
1. Check GitHub App webhook URL is correct and accessible
2. Verify webhook secret matches `.env` value
3. Check firewall/network settings
4. Test with ngrok for local development
5. View webhook deliveries in GitHub App settings

### Issue: Database connection failed

**Solution**:
1. Verify MySQL is running: `mysql -u root -p`
2. Check `DATABASE_URL` in `.env` is correct
3. Ensure database exists: `SHOW DATABASES;`
4. Check user permissions: `SHOW GRANTS FOR 'agent_user'@'localhost';`

### Issue: AI Agent execution fails

**Solution**:
1. Check API key is valid and not expired
2. Verify rate limits not exceeded
3. Check `AI_AGENT_API_URL` is correct
4. Test API directly with curl
5. Review logs for detailed error messages

### Issue: Codex review not detected

**Solution**:
1. Verify Codex bot has commented on PR
2. Check comment matches approval pattern (see data-model.md)
3. Ensure `ReviewFeedback.approval_detected` is being set
4. Review logs for regex matching errors

### Issue: PR not auto-merging

**Solution**:
1. Check all merge conditions (see data-model.md):
   - CI passed (all CIStatus.conclusion = 'success')
   - Codex approved (ReviewFeedback.approval_detected = true)
   - No conflicts (PullRequest.mergeable = true)
2. Verify GitHub App has write permission on PRs
3. Check branch protection rules don't block auto-merge
4. Review audit logs: `SELECT * FROM audit_logs WHERE event_type = 'merge.attempted'`

---

## Deployment

### Docker Deployment

```bash
# Build image
docker build -t github-agent-automation:latest .

# Run container
docker run -d \
  --name agent-automation \
  --env-file .env \
  -p 3000:3000 \
  github-agent-automation:latest
```

### Docker Compose

```yaml
# docker-compose.yml
version: '3.8'
services:
  app:
    build: .
    ports:
      - "3000:3000"
    env_file:
      - .env
    depends_on:
      - mysql
    restart: unless-stopped

  mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: root_password
      MYSQL_DATABASE: github_agent_automation
      MYSQL_USER: agent_user
      MYSQL_PASSWORD: secure_password
    volumes:
      - mysql_data:/var/lib/mysql
    ports:
      - "3306:3306"

volumes:
  mysql_data:
```

Run with:
```bash
docker-compose up -d
```

### Production Checklist

- [ ] Set `NODE_ENV=production` in `.env`
- [ ] Use strong database password
- [ ] Enable HTTPS (reverse proxy with nginx/Caddy)
- [ ] Setup log aggregation (e.g., Loki, ELK)
- [ ] Configure backup for MySQL database
- [ ] Setup monitoring (Prometheus, Grafana)
- [ ] Enable rate limiting at nginx level
- [ ] Review security headers (helmet.js)
- [ ] Setup alerting for failures (Discord + PagerDuty)
- [ ] Document rollback procedure
- [ ] Test disaster recovery plan

---

## Architecture Overview

```
┌─────────────┐
│   GitHub    │
│  (Webhooks) │
└──────┬──────┘
       │
       ▼
┌──────────────────┐     ┌─────────────┐
│  Hono Server     │────▶│  Prisma DB  │
│  (Port 3000)     │     │  (MySQL)    │
└────────┬─────────┘     └─────────────┘
         │
         ├─────▶ AI Agent (Claude API)
         │
         ├─────▶ Codex API (Reviews)
         │
         └─────▶ Discord (Notifications)
```

### Core Components

- **Webhook Server** (Hono): Receives GitHub events
- **Services Layer**: Business logic (TDD with Vitest)
- **Repositories Layer**: Database access (Prisma ORM)
- **External Clients**: GitHub, Codex, Discord, AI Agent
- **CLI Commands**: Manual operations for testing

---

## Performance Tuning

### Database Optimization

```sql
-- Add custom indexes if needed
CREATE INDEX idx_agent_runs_active ON agent_runs(state, created_at DESC) WHERE state IN ('queued', 'started');
```

### Prisma Connection Pooling

Edit `prisma/schema.prisma`:

```prisma
datasource db {
  provider = "mysql"
  url      = env("DATABASE_URL")
  relationMode = "prisma"
  // Adjust pool size for production
  // ?connection_limit=10&pool_timeout=20
}
```

### Rate Limiting

Configure nginx reverse proxy:

```nginx
limit_req_zone $binary_remote_addr zone=webhooks:10m rate=10r/s;

server {
    location /webhooks/github {
        limit_req zone=webhooks burst=20 nodelay;
        proxy_pass http://localhost:3000;
    }
}
```

---

## Security Considerations

### Secrets Management

**DO NOT** commit `.env` to git. Add to `.gitignore`:
```gitignore
.env
.env.local
.env.*.local
*.pem
```

For production, use secret managers:
- AWS Secrets Manager
- HashiCorp Vault
- Doppler
- 1Password

### Webhook Signature Verification

The system automatically verifies GitHub webhook signatures using `@octokit/webhooks`. Requests with invalid signatures are rejected (401).

### Authorization

Only users with Collaborator+ permission on the repository can trigger agent execution. This is checked via GitHub API before starting execution.

### Dependency Scanning

Run regular security audits:

```bash
# npm
npm audit
npm audit fix

# Or with pnpm
pnpm audit

# Use Snyk for continuous monitoring
npx snyk test
```

---

## Monitoring & Observability

### Metrics Endpoint

```bash
curl http://localhost:3000/metrics
```

Returns JSON:
```json
{
  "webhooks_received": 1234,
  "agent_runs_started": 567,
  "agent_runs_succeeded": 450,
  "agent_runs_failed": 17,
  "prs_created": 550,
  "prs_merged": 440,
  "avg_execution_time_ms": 45000,
  "github_api_calls_last_hour": 234
}
```

### Structured Logging

All logs are JSON format (Pino):

```json
{
  "level": 30,
  "time": 1698765432000,
  "msg": "Webhook received",
  "event": "issue_comment",
  "delivery_id": "abc-123",
  "repo": "owner/repo",
  "issue": 42
}
```

### Audit Trail

Query audit logs:

```sql
-- Recent webhook events
SELECT * FROM audit_logs
WHERE event_type = 'webhook.received'
ORDER BY created_at DESC
LIMIT 100;

-- Failed executions
SELECT * FROM audit_logs
WHERE event_type = 'agent.failed'
AND created_at > NOW() - INTERVAL 24 HOUR;
```

---

## FAQ

### Q: How do I customize the trigger phrase?

**A**: Edit `src/services/TriggerDetectionService.ts` and change the regex pattern. Note: This requires code changes and redeployment.

### Q: Can I run multiple agents in parallel?

**A**: Yes, the system supports unlimited concurrency (per FR-012). However, be mindful of GitHub API rate limits (5000 req/hour).

### Q: What happens if 50 retries are exhausted?

**A**: The AgentRun state transitions to `failed`, and notifications are sent to both GitHub Issue (comment) and Discord webhook. Manual intervention required.

### Q: How do I pause all agent executions?

**A**: Set an environment flag or stop the server. There's no built-in pause mechanism. Consider implementing a feature flag in Phase 8.

### Q: Can I use this with GitHub Enterprise?

**A**: Yes, set `GITHUB_API_URL` in `.env` to your Enterprise URL:
```env
GITHUB_API_URL=https://github.company.com/api/v3
```

---

## Support & Contribution

- **Issues**: https://github.com/your-org/agentic-automation/issues
- **Docs**: See `specs/001-github-agent-automation/` directory
- **Constitution**: See `.specify/memory/constitution.md` for development principles

Before contributing:
1. Read constitution.md (TDD required)
2. Run tests: `npm test`
3. Follow project structure in plan.md
4. Add integration tests for new features

---

**Setup Complete**: You should now have a working GitHub Agent Automation system. Test with a simple Issue in a test repository first before deploying to production.
