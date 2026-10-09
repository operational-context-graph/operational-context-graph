# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

SERVICE_DI := services/data-ingestion
COVERAGE_DIR := $(CURDIR)/coverage

.PHONY: help build run test lint vet \
	di-build di-run di-test di-lint di-vet di-tidy di-mocks \
	proto-lint proto-format proto-generate

build: proto-generate di-build ## Build all services
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

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate
