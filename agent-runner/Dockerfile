# Multi-stage build for agent-runner
# Builds Go binary and installs Node.js runtime with AI agent CLIs

# ==============================================================================
# Stage 1: Go Build
# ==============================================================================
# Builds the agent-runner Go binary from source
FROM golang:1.24-alpine AS builder

# Set working directory for build
WORKDIR /build

# Copy dependency files first for better cache utilization
# This allows Docker to cache the dependency download layer
COPY go.mod go.sum ./

# Download dependencies (cached if go.mod/go.sum unchanged)
RUN go mod download

# Copy source code
COPY . .

# Build the agent-runner binary
RUN go build -o agent-runner main.go

# ==============================================================================
# Stage 2: Runtime Image
# ==============================================================================
# Runtime image with Node.js and AI agent CLIs (Claude Code, Cursor)
FROM node:22-alpine

# Install system tools required for agent execution
# - git: Repository clone, commit, and push operations
# - curl: Required for Cursor CLI installation script download
# - bash: Required for executing hook scripts from .agent-config.yaml
RUN apk add --no-cache git curl bash

# Install Claude Code CLI globally
# Command: claude-code --workspace /workspace --task "..." --non-interactive
RUN curl -fsSL https://claude.ai/install.sh | bash

# Install Cursor CLI using official installation script
# Command: cursor agent --cwd /workspace --prompt "..." --headless --no-confirm
# Flags: -f (fail on HTTP errors), -s (silent), -S (show errors), -L (follow redirects)
RUN curl -fsSL https://download.cursor.com/install.sh | sh

# Copy compiled agent-runner binary from builder stage
COPY --from=builder /build/agent-runner /usr/local/bin/agent-runner

# Set working directory for agent execution
# This matches the default WORKSPACE_DIR in main.go
WORKDIR /workspace

# Set entry point
# The binary accepts flags: --issue-id, --repo, --prompt, --previous-attempts, --ci-logs
ENTRYPOINT ["agent-runner"]

# Required Environment Variables (set by Kubernetes Pod):
# - KUBERNETES_NAMESPACE: Kubernetes namespace (auto-injected via Downward API)
# - OPERATOR_SERVICE_NAME: Operator service name
# - OPERATOR_SERVICE_PORT: Operator service port
#   Note: Operator API URL is auto-constructed from these three values
# - OPERATOR_API_TOKEN: Bearer token for API authentication
# - AGENT_RUN_ID: AgentRun database record ID
# - AGENT_TYPE: Agent to execute ("claude-code" or "cursor-agents")
# - GITHUB_TOKEN: GitHub Personal Access Token
# - ANTHROPIC_API_KEY: Claude API key (required if AGENT_TYPE=claude-code)
# - CURSOR_API_KEY: Cursor API key (required if AGENT_TYPE=cursor-agents)
# - RETRY_COUNT: Current retry count (default: 0)
# - WORKSPACE_DIR: Working directory (default: /workspace)

