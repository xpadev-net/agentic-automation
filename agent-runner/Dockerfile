# Multi-stage build for agent-runner
# Builds Go binary and installs Node.js runtime with AI agent CLIs

# ==============================================================================
# Stage 1: Go Build
# ==============================================================================
# Builds the agent-runner Go binary from source
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

# Build the agent-runner binary with build metadata
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X agent-runner/pkg/version.Commit=${GIT_SHA} -X agent-runner/pkg/version.BuiltAt=${BUILT_AT}" -o agent-runner main.go

# ==============================================================================
# Stage 2: Runtime Image
# ==============================================================================
# Runtime image with Node.js and AI agent CLIs (Claude Code, Cursor)
FROM node:22-slim

# Add Go toolchain to runtime image (allows running go build/test inside Pod)
ARG GO_VERSION=1.24.9

# Install system tools required for agent execution
# - git: Repository clone, commit, and push operations
# - curl: Required for Cursor CLI installation script download
# - bash: Required for executing hook scripts from .agent-config.yaml
RUN apt-get update && apt-get install -y \
  git \
  curl \
  bash \
  build-essential \
  pkg-config \
  ca-certificates && \
  rm -rf /var/lib/apt/lists/*

# Install Go from official tarball with checksum verification
RUN curl -fsSLo /tmp/go.tgz https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz && \
  curl -fsSLo /tmp/go.tgz.sha256 https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz.sha256 && \
  sha256sum -c /tmp/go.tgz.sha256 && \
  tar -C /usr/local -xzf /tmp/go.tgz && \
  rm -f /tmp/go.tgz /tmp/go.tgz.sha256

# Configure Go paths for both root (during build) and agent user (runtime)
ENV GOPATH=/home/agent/go
RUN mkdir -p /home/agent/go && chown -R agent:agent /home/agent/go

# Create non-root user for security
# --home option creates the home directory for the system user
RUN addgroup --system agent && adduser --system --home /home/agent --group agent

# Create workspace directory and set ownership
RUN mkdir -p /workspace && chown -R agent:agent /workspace

# Switch to agent user for CLI installations
# This is done before copying the binary to maximize cache efficiency
USER agent

# Set HOME environment variable for agent user
ENV HOME=/home/agent

# Install Claude Code CLI as agent user
# Command: claude-code --workspace /workspace --task "..." --non-interactive
# This layer is cached independently of the binary, improving build performance
RUN curl -fsSL https://claude.ai/install.sh | bash

# Install Cursor CLI as agent user using official installation script
# Command: cursor-agent -p "..."
# Flags: -f (fail on HTTP errors), -s (silent), -S (show errors), -L (follow redirects)
# This layer is cached independently of the binary, improving build performance
RUN curl https://cursor.com/install -fsS | bash

# Add Cursor CLI to PATH
# Cursor CLI installs cursor-agent to $HOME/.local/bin
# According to https://cursor.com/ja/docs/cli/installation, PATH must include $HOME/.local/bin
ENV PATH="/usr/local/go/bin:$HOME/.local/bin:${GOPATH}/bin:$PATH"

# Copy compiled agent-runner binary from builder stage
# This is done after CLI installation to maximize cache efficiency
# Binary changes won't invalidate the CLI installation cache
# Note: COPY requires root, so we temporarily switch back, but the binary is executable by agent user
USER root
COPY --from=builder /build/agent-runner /usr/local/bin/agent-runner

# Switch back to agent user for runtime
USER agent

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
# - AGENT_TYPE: Agent to execute ("claude-code" or "cursor-agent")
# - GITHUB_APP_ID: GitHub App ID (App auth is the default; PAT unsupported)
# - GITHUB_PRIVATE_KEY: GitHub App private key (PEM, multi-line)
# - ANTHROPIC_API_KEY: Claude API key (required if AGENT_TYPE=claude-code)
# - CURSOR_API_KEY: Cursor API key (required if AGENT_TYPE=cursor-agent)
# - RETRY_COUNT: Current retry count (default: 0)
# - WORKSPACE_DIR: Working directory (default: /workspace)

