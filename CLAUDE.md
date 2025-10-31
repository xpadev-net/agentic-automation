# agentic-automation Development Guidelines

Auto-generated from all feature plans. Last updated: 2025-11-01

## Active Technologies

- Go 1.22+ with Gin, GORM, goose migrations (001-github-agent-automation)

## Project Structure

```text
cmd/                    # Main applications
internal/              # Private application code
pkg/                   # Public libraries
tests/                 # Test files
migrations/            # Database migrations (goose)
```

## Commands

go test ./... && go vet ./...

## Code Style

Go 1.22+: Follow standard Go conventions (gofmt, golint)

## Recent Changes

- 001-github-agent-automation: Migrated to Go 1.22+ with Gin, GORM, goose

<!-- MANUAL ADDITIONS START -->
<!-- MANUAL ADDITIONS END -->
