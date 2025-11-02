# quickstart.md: GitHub Agent Automation

**Last Updated**: 2025-10-31
**Prerequisites**: Go 1.22+, MySQL 8.0+, **Kubernetes cluster access (kubectl configured)**, GitHub account with admin access to target repository

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
go mod download
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
CODEX_BOT_USERNAME=codex-bot  # GitHub username of Codex bot for comment detection
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123456/your_webhook_token

# Agent Runner (Kubernetes Pod)
OPERATOR_API_URL=http://agent-operator.default.svc.cluster.local:3000  # Operator REST API URL for Pod → Operator communication
OPERATOR_API_TOKEN=your-secret-api-token  # Bearer token for agent-runner authentication
AGENT_RUNNER_IMAGE=ghcr.io/your-org/agent-runner:latest  # Docker image for agent-runner Pod
AI_AGENT_TIMEOUT_MINUTES=30  # Pod execution timeout
AI_AGENT_MAX_CONCURRENT_PODS=10  # Max concurrent Pod executions
AI_AGENT_DEFAULT_TYPE=claude-code  # claude-code | cursor-agents

# Kubernetes (optional, defaults to in-cluster config)
KUBE_CONFIG_PATH=/path/to/.kube/config

# Server Configuration
PORT=3000
ENV=development  # development | production
LOG_LEVEL=info
```

### 4. Run Database Migrations

```bash
# Install goose migration tool (if not already installed)
go install github.com/pressly/goose/v3/cmd/goose@latest

# Run migrations to create tables
goose -dir migrations mysql "agent_user:secure_password@tcp(localhost:3306)/github_agent_automation?charset=utf8mb4&parseTime=True&loc=Local" up

# Optional: Verify migration status
goose -dir migrations mysql "agent_user:secure_password@tcp(localhost:3306)/github_agent_automation?charset=utf8mb4&parseTime=True&loc=Local" status
```

### 5. Start the Server

```bash
# Development mode
go run cmd/operator/main.go

# Or build and run:
go build -o bin/operator cmd/operator/main.go
./bin/operator

# Production mode (with optimizations)
go build -ldflags="-s -w" -o bin/operator cmd/operator/main.go
./bin/operator
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
  - Pull request reviews: Read (review detection; may be covered under Pull Requests)
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

### Codex Integration Setup

**Note**: Codex is integrated via GitHub PR comments (no separate API required)

1. Ensure Codex GitHub App is installed on your repository
2. Verify Codex bot has access to your repository
3. Add Codex bot username to `.env` as `CODEX_BOT_USERNAME` (default: `codex-bot`)
4. Test integration:
   - Create a test PR
   - Comment `@codex review` on the PR
   - Verify Codex bot responds with review comment

### Discord Webhook Setup

1. Open Discord server settings
2. Go to "Integrations" → "Webhooks"
3. Click "New Webhook"
4. Name: "GitHub Agent Notifications"
5. Select target channel
6. Copy webhook URL
7. Add to `.env` as `DISCORD_WEBHOOK_URL`

### AI Agent Configuration

AI agent authentication is handled via Kubernetes Secrets, not environment variables in the Operator.

**Important**: The Operator does not directly call AI APIs. Instead, it creates Kubernetes Pods that execute the agents. API credentials are injected into these Pods at runtime as follows:

- **For Claude Code**: `ANTHROPIC_API_KEY` (from Kubernetes Secret)
- **For Cursor Agents**: `CURSOR_API_KEY` (from Kubernetes Secret)

To configure:

1. Get API key from https://console.anthropic.com (for Claude) or your AI provider
2. Create a Kubernetes Secret:
   ```bash
   kubectl create secret generic ai-agent-credentials \
     --from-literal=ANTHROPIC_API_KEY=sk-ant-... \
     --from-literal=CURSOR_API_KEY=your-cursor-key
   ```
3. The agent-runner Pod template (managed by the Operator) automatically injects these credentials

See [ai-agent-execution.md](contracts/ai-agent-execution.md) for Pod configuration details.

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
# Build CLI binary first
go build -o bin/operator cmd/operator/main.go

# Check execution status
./bin/operator status <agent-run-id>

# Manual retry (if automatic retries exhausted)
./bin/operator retry <agent-run-id>

# Manual trigger (for testing without webhook)
./bin/operator trigger <issue-id>

# View logs
tail -f logs/app.log
```

### Agent Runner Requirements

- The runner image uses go-github/v62 library for PR creation (no CLI dependencies).
- GitHub API token provided via GITHUB_TOKEN environment variable.
- Node.js runtime required for claude-code/cursor-agents execution.

### Repository Manifest Configuration (.agent-config.yaml)

Each target repository can customize pre-execution hooks, validation commands, and post-execution hooks by adding an `.agent-config.yaml` file to the repository root.

**File Location**: `.agent-config.yaml` (commit to repository root)

**Purpose**:
- Run pre-hooks before AI agent starts (e.g., `npm install`, `go mod download`)
- Run validations after AI agent completes (e.g., `npm run lint`, `go vet ./...`)
- Run post-hooks after PR is created (e.g., Discord notifications, trigger CI/CD)

**Example for Node.js/TypeScript Project**:

```yaml
version: "1.0"

hooks:
  pre:
    - name: "install-dependencies"
      command: "npm ci"
      description: "Install npm dependencies"
      timeout: "5m"
      required: true
  post:
    - name: "notify-discord"
      command: "curl -X POST $DISCORD_WEBHOOK_URL -H 'Content-Type: application/json' -d '{\"content\": \"PR created for issue #$ISSUE_NUMBER\"}'"
      description: "Send Discord notification"
      timeout: "30s"
      required: false

validation:
  - name: "lint"
    command: "npm run lint"
    description: "ESLint validation"
    timeout: "3m"
    required: true

  - name: "type-check"
    command: "npm run type-check"
    description: "TypeScript type checking"
    timeout: "2m"
    required: true

  - name: "tests"
    command: "npm run test"
    description: "Run test suite"
    timeout: "10m"
    required: false  # Optional - AI can fix test failures
```

**Example for Go Project**:

```yaml
version: "1.0"

hooks:
  pre:
    - name: "download-dependencies"
      command: "go mod download"
      description: "Download Go modules"
      timeout: "3m"
      required: true
  post: []

validation:
  - name: "fmt-check"
    command: "test -z $(gofmt -l .)"
    description: "Check Go formatting"
    timeout: "1m"
    required: true

  - name: "vet"
    command: "go vet ./..."
    description: "Go static analysis"
    timeout: "2m"
    required: true

  - name: "build"
    command: "go build ./..."
    description: "Compile packages"
    timeout: "5m"
    required: true
```

**Behavior**:
- If `.agent-config.yaml` does NOT exist → All hooks/validations are skipped
- If `.agent-config.yaml` exists → Commands are executed per manifest
- Pre-hook failure (required: true) → Abort workflow, report error
- Validation failure (required: true) → Retry AI agent with error feedback
- Post-hook failure → Log warning, continue (PR already created)

**Full Specification**: See [agent-manifest.md](contracts/agent-manifest.md) for complete schema and examples.

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
# Connect to MySQL database directly
mysql -u agent_user -p github_agent_automation

# Or use a GUI tool like MySQL Workbench, TablePlus, or DBeaver
# Connection: localhost:3306, user: agent_user, database: github_agent_automation
```

### Check Server Health

```bash
curl http://localhost:3000/health
# Expected: {"status":"ok","database":"connected"}
```

### View Logs

Logs are written to stdout (JSON format) and can be piped to file:

```bash
./bin/operator 2>&1 | tee -a logs/app.log
```

### Reset Database

```bash
# WARNING: Deletes all data
goose -dir migrations mysql "agent_user:secure_password@tcp(localhost:3306)/github_agent_automation?charset=utf8mb4&parseTime=True&loc=Local" down-to 0

# Re-run migrations
goose -dir migrations mysql "agent_user:secure_password@tcp(localhost:3306)/github_agent_automation?charset=utf8mb4&parseTime=True&loc=Local" up
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
1. Check Kubernetes Secret contains valid API keys (`ANTHROPIC_API_KEY` or `CURSOR_API_KEY`)
2. Verify API keys are not expired (test with direct API call)
3. Check rate limits not exceeded on AI provider side
4. Verify Pod has access to the Secret (check Pod environment variables)
5. Review agent-runner Pod logs: `kubectl logs <pod-name>`
6. Check Operator logs for Pod creation errors

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

### Agent Runner Docker Image

The `agent-runner` component runs as a Kubernetes Pod and requires a Docker image to be built and pushed to a container registry.

#### Automatic Build and Push (GitHub Actions)

When code is pushed to the `master` branch, GitHub Actions automatically builds and pushes the Docker image to GitHub Container Registry (ghcr.io):

- Image: `ghcr.io/<OWNER>/agent-runner:latest`
- Image (with SHA): `ghcr.io/<OWNER>/agent-runner:sha-<SHORT_SHA>`

The build job runs automatically on every push to master. No manual action required.

#### Manual Build and Push

To manually build and push the agent-runner Docker image:

```bash
# Build the image (creates both latest and sha-<SHA> tags)
make docker-build

# Push to registry (requires authentication)
make docker-push

# Or do both at once
make docker-build-push
```

**Authentication for ghcr.io:**

```bash
# Login to GitHub Container Registry
docker login ghcr.io -u <YOUR_GITHUB_USERNAME> -p <GITHUB_TOKEN>
```

To get a GitHub token:
1. Go to GitHub Settings → Developer settings → Personal access tokens → Tokens (classic)
2. Generate a new token with `write:packages` permission
3. Use the token as the password when logging in

**Customizing the image location:**

You can override the default registry and owner using environment variables:

```bash
# Use custom registry and owner
DOCKER_REGISTRY=ghcr.io DOCKER_OWNER=myorg make docker-build-push

# Or set in your environment
export DOCKER_REGISTRY=ghcr.io
export DOCKER_OWNER=myorg
make docker-build-push
```

**Using the image in Kubernetes:**

Update your Kubernetes Pod template or Job to reference the image:

```yaml
containers:
  - name: agent-runner
    image: ghcr.io/<OWNER>/agent-runner:latest
    # Or use a specific SHA tag:
    # image: ghcr.io/<OWNER>/agent-runner:sha-abc1234
```

### Production Checklist

- [ ] Set `ENV=production` in `.env`
- [ ] Use strong database password
- [ ] Enable HTTPS (reverse proxy with nginx/Caddy)
- [ ] Setup log aggregation (e.g., Loki, ELK)
- [ ] Configure backup for MySQL database
- [ ] Setup monitoring (Prometheus, Grafana)
- [ ] Enable rate limiting at nginx level
- [ ] Review security headers (Gin middleware)
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
│  Gin Server      │────▶│  GORM DB    │
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

- **Webhook Server** (Gin): Receives GitHub events
- **Services Layer**: Business logic (TDD with Go testing + testify)
- **Repositories Layer**: Database access (GORM ORM)
- **External Clients**: GitHub, Codex, Discord, AI Agent
- **CLI Commands**: Manual operations for testing (cobra)

---

## Performance Tuning

### Database Optimization

```sql
-- Add custom indexes if needed
CREATE INDEX idx_agent_runs_active ON agent_runs(state, created_at DESC) WHERE state IN ('queued', 'started');
```

### GORM Connection Pooling

Edit `internal/config/database.go`:

```go
db.SetMaxOpenConns(25) // Maximum open connections
db.SetMaxIdleConns(10) // Maximum idle connections
db.SetConnMaxLifetime(time.Hour) // Maximum connection lifetime
```

Or via `DATABASE_URL` query parameters:
```
mysql://user:pass@host/db?parseTime=true&maxOpenConns=25&maxIdleConns=10
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

The system verifies GitHub webhook signatures using HMAC-SHA256 (Go `crypto/hmac`). Requests with invalid signatures are rejected (401).

### Authorization

Only users with Collaborator+ permission on the repository can trigger agent execution. This is checked via GitHub API before starting execution.

### Dependency Scanning

Run regular security audits:

```bash
# Check for known vulnerabilities
go list -json -deps | nancy sleuth

# Or use govulncheck (Go's official vulnerability checker)
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# Verify module checksums
go mod verify
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

All logs are JSON format (zap):

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

**A**: Edit `internal/services/trigger_detection.go` and change the regex pattern. Note: This requires code changes and redeployment.

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
2. Run tests: `go test ./...` and `go vet ./...`
3. Follow project structure in plan.md
4. Add integration tests for new features
5. Format code: `gofmt -w .` and `golint ./...`

---

**Setup Complete**: You should now have a working GitHub Agent Automation system. Test with a simple Issue in a test repository first before deploying to production.
