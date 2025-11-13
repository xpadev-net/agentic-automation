# Multi-stage build for agent-runner
# Builds Go binary and installs Node.js runtime with AI agent CLIs

# ==============================================================================
# Stage 1: Go Build
# ==============================================================================
# Builds the agent-runner Go binary from source
FROM harbor-osk-k8s-pve.xpa.dev/docker/golang:1.24-alpine AS builder

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
# Runtime image using pre-built base image with Node.js, Go, and AI agent CLIs
# The base image is built separately and cached, reducing build time
FROM harbor-osk-k8s-pve.xpa.dev/xpadev-net/agentic-automation-runner-base:latest

# Copy compiled agent-runner binary from builder stage
# Note: COPY requires root, so we temporarily switch to root, but the binary is executable by agent user
USER root
COPY --from=builder /build/agent-runner /usr/local/bin/agent-runner

# Switch back to agent user for runtime
USER agent

# Set working directory for agent execution
# This matches the default WORKSPACE_DIR in main.go
# Note: WORKDIR is already set in the base image, but we set it again for clarity
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

