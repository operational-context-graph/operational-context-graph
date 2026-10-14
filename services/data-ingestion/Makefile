# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

COVERAGE_DIR ?= $(CURDIR)/coverage

.PHONY: build run test lint vet tidy vendor mocks

build: ## Build the data-ingestion service
	GOWORK=off go build ./...

run: ## Run the data-ingestion service from source
	GOWORK=off go run ./cmd/data-ingestion

vet: ## Run go vet
	GOWORK=off go vet ./...

test: ## Test with race detector and coverage
	mkdir -p $(COVERAGE_DIR)
	GOWORK=off go test ./... -race -coverprofile=$(COVERAGE_DIR)/data-ingestion.out

lint: ## Lint (golangci-lint if available, else go vet)
	if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found; running go vet"; go vet ./...; \
	fi

tidy: ## Tidy go.mod
	go mod tidy

vendor: ## Sync vendor directory (run after buf generate)
	go mod vendor

mocks: ## Generate mocks (requires mockery)
	mockery
