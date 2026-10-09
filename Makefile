# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

# Root orchestrator. Build, lint, and test run through make and are scoped to
# the sub-component being changed. Aggregate targets (build/test/lint/vet) fan
# out to every service and grow as services are added.

SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

SERVICE_DI := services/data-ingestion
COVERAGE_DIR := $(CURDIR)/coverage

.PHONY: help build run test lint vet \
	di-build di-run di-test di-lint di-vet di-tidy di-mocks

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: di-build ## Build all services
run: di-run ## Run the data-ingestion service (dev)
test: di-test ## Test all services
lint: di-lint ## Lint all services
vet: di-vet ## Vet all services

di-build: ## Build the data-ingestion service
	cd $(SERVICE_DI) && go build ./...

di-run: ## Run the data-ingestion service from source
	cd $(SERVICE_DI) && go run ./cmd/data-ingestion

di-vet: ## Run go vet on the data-ingestion service
	cd $(SERVICE_DI) && go vet ./...

di-test: ## Test data-ingestion with race detector and coverage
	mkdir -p $(COVERAGE_DIR)
	cd $(SERVICE_DI) && go test ./... -race -coverprofile=$(COVERAGE_DIR)/data-ingestion.out

di-lint: ## Lint data-ingestion (golangci-lint if available, else go vet)
	cd $(SERVICE_DI) && if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found; running go vet"; go vet ./...; \
	fi

di-tidy: ## Tidy the data-ingestion go.mod
	cd $(SERVICE_DI) && go mod tidy

di-mocks: ## Generate mocks for data-ingestion (requires mockery)
	cd $(SERVICE_DI) && mockery
