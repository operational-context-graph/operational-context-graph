# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

COVERAGE_DIR ?= $(CURDIR)/coverage
BIN_DIR      := $(CURDIR)/bin

.PHONY: build run test lint vet tidy

build: ## Build all commands into bin/
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/telemetry-store ./cmd/telemetry-store
	go build -o $(BIN_DIR)/telemetry-store-migrator ./cmd/migrate

run: ## Run the telemetry-store service from source
	go run ./cmd/telemetry-store

vet: ## Run go vet
	go vet ./...

test: ## Test with race detector and coverage
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/telemetry-store.out

lint: ## Lint (golangci-lint if available, else go vet)
	if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found; running go vet"; go vet ./...; \
	fi

tidy: ## Tidy go.mod
	go mod tidy
