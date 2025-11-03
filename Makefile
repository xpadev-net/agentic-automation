.PHONY: build test vet migrate-up migrate-down migrate-status run clean deps help docker-build docker-push docker-build-push docker-build-operator docker-push-operator docker-build-push-operator deploy-infra deploy-operator deploy-all k8s-secrets

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
IMAGE_NAME ?= agentic-automation-runner
OPERATOR_IMAGE_NAME ?= agentic-automation-operator
GIT_SHA ?= $(shell git rev-parse --short HEAD)
FULL_IMAGE_LATEST ?= $(DOCKER_REGISTRY)/$(DOCKER_OWNER)/$(IMAGE_NAME):latest
FULL_IMAGE_SHA ?= $(DOCKER_REGISTRY)/$(DOCKER_OWNER)/$(IMAGE_NAME):sha-$(GIT_SHA)
OPERATOR_IMAGE_LATEST ?= $(DOCKER_REGISTRY)/$(DOCKER_OWNER)/$(OPERATOR_IMAGE_NAME):latest
OPERATOR_IMAGE_SHA ?= $(DOCKER_REGISTRY)/$(DOCKER_OWNER)/$(OPERATOR_IMAGE_NAME):sha-$(GIT_SHA)

# Kubernetes namespace
K8S_NAMESPACE ?= default

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
	CGO_ENABLED=1 go test ./... -v

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

docker-build-operator: ## Build Docker image for operator
	@echo "Building operator Docker image..."
	@echo "Image: $(OPERATOR_IMAGE_LATEST)"
	@echo "Image: $(OPERATOR_IMAGE_SHA)"
	docker build -t $(OPERATOR_IMAGE_LATEST) -t $(OPERATOR_IMAGE_SHA) .

docker-push-operator: ## Push operator Docker image to registry
	@echo "Pushing operator Docker images..."
	@echo "Pushing: $(OPERATOR_IMAGE_LATEST)"
	docker push $(OPERATOR_IMAGE_LATEST)
	@echo "Pushing: $(OPERATOR_IMAGE_SHA)"
	docker push $(OPERATOR_IMAGE_SHA)

docker-build-push-operator: docker-build-operator docker-push-operator ## Build and push operator Docker image

k8s-secrets: ## Display commands to create Kubernetes secrets (does not execute)
	@echo "=========================================="
	@echo "Kubernetes Secrets Creation Commands"
	@echo "=========================================="
	@echo ""
	@echo "1. Create operator-secrets:"
	@echo "kubectl create secret generic operator-secrets \\"
	@echo "  --namespace=$(K8S_NAMESPACE) \\"
	@echo "  --from-literal=database-url=\"\$$DATABASE_URL\" \\"
	@echo "  --from-literal=github-app-id=\"\$$GITHUB_APP_ID\" \\"
	@echo "  --from-file=github-private-key=./github-app.pem \\"
	@echo "  --from-literal=github-webhook-secret=\"\$$GITHUB_WEBHOOK_SECRET\" \\"
	@echo "  --from-literal=discord-webhook-url=\"\$$DISCORD_WEBHOOK_URL\" \\"
	@echo "  --from-literal=operator-api-token=\"\$$OPERATOR_API_TOKEN\""
	@echo ""
	@echo "2. Create github-token:"
	@echo "kubectl create secret generic github-token \\"
	@echo "  --namespace=$(K8S_NAMESPACE) \\"
	@echo "  --from-literal=token=\"\$$GITHUB_TOKEN\""
	@echo ""
	@echo "3. Create anthropic-api-key:"
	@echo "kubectl create secret generic anthropic-api-key \\"
	@echo "  --namespace=$(K8S_NAMESPACE) \\"
	@echo "  --from-literal=api-key=\"\$$ANTHROPIC_API_KEY\""
	@echo ""
	@echo "4. Create cursor-api-key:"
	@echo "kubectl create secret generic cursor-api-key \\"
	@echo "  --namespace=$(K8S_NAMESPACE) \\"
	@echo "  --from-literal=api-key=\"\$$CURSOR_API_KEY\""
	@echo ""
	@echo "5. Create s3-credentials:"
	@echo "kubectl create secret generic s3-credentials \\"
	@echo "  --namespace=$(K8S_NAMESPACE) \\"
	@echo "  --from-literal=access-key-id=\"\$$S3_ACCESS_KEY_ID\" \\"
	@echo "  --from-literal=secret-access-key=\"\$$S3_SECRET_ACCESS_KEY\""
	@echo ""
	@echo "=========================================="

deploy-infra: ## Deploy MySQL and MinIO for development/testing
	@echo "Deploying infrastructure (MySQL + MinIO)..."
	kubectl apply -f k8s/mysql-deployment.yaml --namespace=$(K8S_NAMESPACE)
	kubectl apply -f k8s/minio-deployment.yaml --namespace=$(K8S_NAMESPACE)
	@echo ""
	@echo "Waiting for MySQL to be ready..."
	kubectl wait --for=condition=ready pod -l app=mysql --timeout=120s --namespace=$(K8S_NAMESPACE) || true
	@echo ""
	@echo "Waiting for MinIO to be ready..."
	kubectl wait --for=condition=ready pod -l app=minio --timeout=120s --namespace=$(K8S_NAMESPACE) || true
	@echo ""
	@echo "Infrastructure deployed successfully!"
	@echo ""
	@echo "Next steps:"
	@echo "1. Port-forward MinIO: kubectl port-forward svc/minio 9000:9000 --namespace=$(K8S_NAMESPACE)"
	@echo "2. Create S3 bucket: ./scripts/setup-minio.sh"
	@echo "3. Run migrations: make migrate-up"

deploy-operator: ## Deploy operator service to Kubernetes
	@echo "Deploying operator service..."
	kubectl apply -f k8s/rbac.yaml --namespace=$(K8S_NAMESPACE)
	kubectl apply -f specs/001-github-agent-automation/k8s/operator/service.yaml --namespace=$(K8S_NAMESPACE)
	@echo ""
	@echo "Waiting for operator to be ready..."
	kubectl wait --for=condition=ready pod -l app=agent-operator --timeout=120s --namespace=$(K8S_NAMESPACE) || true
	@echo ""
	@echo "Operator deployed successfully!"
	@echo ""
	@echo "Check status:"
	@echo "  kubectl get pods -l app=agent-operator --namespace=$(K8S_NAMESPACE)"
	@echo "  kubectl logs -l app=agent-operator --namespace=$(K8S_NAMESPACE)"

deploy-all: deploy-infra deploy-operator ## Deploy all components (infra + operator)
	@echo ""
	@echo "=========================================="
	@echo "All components deployed successfully!"
	@echo "=========================================="

