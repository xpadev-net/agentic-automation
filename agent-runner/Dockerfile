# Multi-stage build
# Stage 1: Go build
FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY . .
RUN go build -o agent-runner main.go

# Stage 2: Runtime image with Node.js and agent CLIs
FROM node:22-alpine

# Install git (required for git operations)
RUN apk add --no-cache git

# Install Claude Code CLI
RUN npm install -g @anthropic/claude-code

# Install Cursor CLI
RUN curl -fsSL https://download.cursor.com/install.sh | sh

# Copy agent-runner binary from builder stage
COPY --from=builder /build/agent-runner /usr/local/bin/agent-runner

# Set working directory for agent execution
WORKDIR /workspace

# Entry point
ENTRYPOINT ["agent-runner"]

