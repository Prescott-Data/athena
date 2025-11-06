SHELL=/bin/bash

.PHONY: help build run test clean lint lint-fmt lint-vet lint-lint lint-tidy docker-build docker-run install-tools generate

# Default target
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Build targets
build: ## Build the memory-server binary
	go build -o memory-server ./cmd/memory-server/main.go

run: ## Run the memory-server (requires .env file)
	./memory-server

# Testing targets
test: ## Run all tests
	go test -v ./...

test-e2e: ## Run end-to-end tests with environment variables from .env.dev
	set -a; source .env.dev; set +a; go test -v ./...

debug-test-e2e: ## Run end-to-end tests with debug output
	@echo "--- Environment variables ---"
	@set -a; source .env.dev; set +a; env
	@echo "--- Running tests ---"
	@set -a; source .env.dev; set +a; go test -v ./...

test-short: ## Run tests with short flag
	go test -short -v ./...

test-race: ## Run tests with race detector
	go test -race -v ./...

test-cover: ## Run tests with coverage
	go test -cover -v ./...

# Linting targets
lint: ## Run all linting checks
	./scripts/lint.sh all

lint-fmt: ## Run go fmt
	./scripts/lint.sh fmt

lint-vet: ## Run go vet
	./scripts/lint.sh vet

lint-lint: ## Run golangci-lint
	./scripts/lint.sh lint

lint-tidy: ## Run go mod tidy
	./scripts/lint.sh tidy

# Docker targets
docker-build: ## Build Docker image
	docker build -t memory-os:latest .

docker-run: ## Run Docker container (requires .env file)
	docker run --env-file .env -p 8080:8080 memory-os:latest

docker-clean: ## Clean up Docker images and containers
	docker system prune -f
	docker image rm memory-os:latest || true

# Development tools
install-tools: ## Install development tools
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install golang.org/x/tools/cmd/goimports@latest

generate: ## Generate protobuf code
	./scripts/generate.sh

# Cleanup
clean: ## Clean build artifacts
	rm -f memory-server
	go clean ./...

# CI/CD targets
ci: ## Run CI pipeline locally
	make lint
	make test
	make build

# Pre-commit hook
pre-commit: ## Run pre-commit checks
	make lint-fmt
	make lint-vet
	make lint-tidy
	git add -A

# Database targets (for local development)
db-migrate: ## Run database migrations (if any)
	@echo "No migrations to run"

db-seed: ## Seed database with test data (if applicable)
	@echo "No seeding required"

# Documentation
docs: ## Generate documentation
	go doc -all ./... > docs/api.md

# Development server
dev: ## Run in development mode with hot reload
	@echo "Development server not configured. Use 'make run' for basic execution."
