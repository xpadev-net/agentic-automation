# Multi-stage build for operator service
# Builds Go binary for the webhook server and orchestration service

# ==============================================================================
# Stage 1: Go Build
# ==============================================================================
# Builds the operator Go binary from source
FROM golang:1.24-alpine AS builder

# Build arguments for embedding build metadata
ARG GIT_SHA="unknown"
ARG BUILT_AT="unknown"

# Set working directory for build
WORKDIR /build

# Copy dependency files first for better cache utilization
# This allows Docker to cache the dependency download layer
COPY go.mod go.sum ./

# Download dependencies (cached if go.mod/go.sum unchanged)
RUN go mod download

# Copy source code
COPY . .

# Build the operator binary
# CGO_ENABLED=0 for static binary (no C dependencies)
# -ldflags="-s -w" to strip debug info and reduce binary size
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X agentic-automation/internal/version.Commit=${GIT_SHA} -X agentic-automation/internal/version.BuiltAt=${BUILT_AT}" -o operator ./cmd/operator

# ==============================================================================
# Stage 2: Runtime Image
# ==============================================================================
# Minimal runtime image for the operator service
FROM alpine:latest

# Install CA certificates for HTTPS requests
# Required for GitHub API, Discord webhooks, and S3 connections
RUN apk add --no-cache ca-certificates

# Create non-root user for security
RUN addgroup -g 1000 operator && \
    adduser -D -u 1000 -G operator operator

# Copy compiled operator binary from builder stage
COPY --from=builder /build/operator /usr/local/bin/operator

# Change ownership of binary
RUN chown operator:operator /usr/local/bin/operator

# Switch to non-root user
USER operator

# Set working directory
WORKDIR /app

# Expose default HTTP port
EXPOSE 3000

# Health check endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:3000/health || exit 1

# Set entry point
ENTRYPOINT ["operator"]

# Required Environment Variables:
# - DATABASE_URL: MySQL connection string (mysql://user:pass@tcp(host:port)/db?...)
# - GITHUB_APP_ID: GitHub App ID
# - GITHUB_PRIVATE_KEY: GitHub App RSA private key (PEM format, multi-line supported)
# - GITHUB_WEBHOOK_SECRET: Webhook signature verification secret
# - OPERATOR_API_TOKEN: Bearer token for agent-runner authentication
# - AGENT_RUNNER_IMAGE: Docker image for agent-runner pods
# - S3_ENDPOINT: S3 API endpoint
# - S3_REGION: S3 region
# - S3_BUCKET: S3 bucket name
# - S3_ACCESS_KEY_ID: S3 access key
# - S3_SECRET_ACCESS_KEY: S3 secret key
#
# Optional Environment Variables:
# - PORT: HTTP server port (default: 3000)
# - ENV: Environment (development/production, default: production)
# - LOG_LEVEL: Log level (debug/info/warn/error, default: info)
# - CODEX_BOT_USERNAME: Codex bot GitHub username (default: codex-bot)
# - DISCORD_WEBHOOK_URL: Discord webhook for failure notifications
# - AI_AGENT_TIMEOUT_MINUTES: Pod execution timeout (default: 30)
# - AI_AGENT_MAX_CONCURRENT_PODS: Max concurrent executions (default: 10)
# - AI_AGENT_DEFAULT_TYPE: Default agent type (claude-code/cursor-agents)
#
# Note: GitHub authentication uses GitHub App installation tokens by default.
#       Personal Access Tokens (PAT) are not supported.
# - OPERATOR_SERVICE_NAME: Operator service name (default: agent-operator)
# - OPERATOR_SERVICE_PORT: Operator service port (default: 3000)
# - S3_USE_PATH_STYLE: Use path-style URLs (true for MinIO, false for AWS S3)
# - S3_MAX_RETRIES: Retry attempts (default: 5)
# - S3_RETRY_INITIAL_INTERVAL: Initial backoff interval (default: 1s)
