.PHONY: build test vet migrate-up migrate-down migrate-status run clean deps help docker-build docker-push docker-build-push

# Default target
.DEFAULT_GOAL := help

# Variables
BIN_DIR := bin
OPERATOR_BIN := $(BIN_DIR)/operator
AGENT_RUNNER_BIN := $(BIN_DIR)/agent-runner
MIGRATIONS_DIR := migrations

# Docker variables
DOCKER_REGISTRY ?= ghcr.io
DOCKER_OWNER ?= $(shell echo '$(shell git config user.name)' | tr '[:upper:]' '[:lower:]')
IMAGE_NAME ?= agent-runner
GIT_SHA ?= $(shell git rev-parse --short HEAD)
FULL_IMAGE_LATEST ?= $(DOCKER_REGISTRY)/$(DOCKER_OWNER)/$(IMAGE_NAME):latest
FULL_IMAGE_SHA ?= $(DOCKER_REGISTRY)/$(DOCKER_OWNER)/$(IMAGE_NAME):sha-$(GIT_SHA)

# Load environment variables from .env if it exists
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

# Database URL - required for migrations
# Format: mysql://user:password@tcp(host:port)/database?charset=utf8mb4&parseTime=True&loc=Local
DATABASE_URL ?= mysql://agent_user:secure_password@tcp(localhost:3306)/github_agent_automation?charset=utf8mb4&parseTime=True&loc=Local

# Strip quotes from DATABASE_URL if present (handles both quoted and unquoted values)
# This allows .env files to use DATABASE_URL="mysql://..." or DATABASE_URL=mysql://...
DATABASE_URL_STRIPPED := $(shell echo '$(DATABASE_URL)' | sed 's/^"//;s/"$$//')


help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deps: ## Download and install dependencies
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

build: ## Build all binaries
	@echo "Building binaries..."
	@mkdir -p $(BIN_DIR)
	go build -o $(OPERATOR_BIN) ./cmd/operator
	go build -o $(AGENT_RUNNER_BIN) ./cmd/agent-runner

test: ## Run all tests
	@echo "Running tests..."
	go test ./... -v

vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

migrate-up: ## Run database migrations up
	@echo "Running migrations up..."
	@if [ -z "$(DATABASE_URL_STRIPPED)" ]; then \
		echo "Error: DATABASE_URL environment variable is not set"; \
		exit 1; \
	fi
	@GOOSE_DSN=$$(echo "$(DATABASE_URL_STRIPPED)" | sed -E 's|mysql://||'); \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(MIGRATIONS_DIR) mysql "$$GOOSE_DSN" up

migrate-down: ## Run database migrations down
	@echo "Running migrations down..."
	@if [ -z "$(DATABASE_URL_STRIPPED)" ]; then \
		echo "Error: DATABASE_URL environment variable is not set"; \
		exit 1; \
	fi
	@GOOSE_DSN=$$(echo "$(DATABASE_URL_STRIPPED)" | sed -E 's|mysql://||'); \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(MIGRATIONS_DIR) mysql "$$GOOSE_DSN" down

migrate-status: ## Show migration status
	@echo "Checking migration status..."
	@if [ -z "$(DATABASE_URL_STRIPPED)" ]; then \
		echo "Error: DATABASE_URL environment variable is not set"; \
		exit 1; \
	fi
	@GOOSE_DSN=$$(echo "$(DATABASE_URL_STRIPPED)" | sed -E 's|mysql://||'); \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(MIGRATIONS_DIR) mysql "$$GOOSE_DSN" status

run: ## Run the operator server (development)
	@echo "Starting operator server..."
	go run ./cmd/operator

clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf $(BIN_DIR)
	go clean

docker-build: ## Build Docker image for agent-runner
	@echo "Building Docker image..."
	@echo "Image: $(FULL_IMAGE_LATEST)"
	@echo "Image: $(FULL_IMAGE_SHA)"
	cd agent-runner && docker build -t $(FULL_IMAGE_LATEST) -t $(FULL_IMAGE_SHA) .

docker-push: ## Push Docker image to registry
	@echo "Pushing Docker images..."
	@echo "Pushing: $(FULL_IMAGE_LATEST)"
	docker push $(FULL_IMAGE_LATEST)
	@echo "Pushing: $(FULL_IMAGE_SHA)"
	docker push $(FULL_IMAGE_SHA)

docker-build-push: docker-build docker-push ## Build and push Docker image

