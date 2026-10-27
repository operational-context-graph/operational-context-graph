# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

COVERAGE_DIR ?= $(CURDIR)/coverage

.PHONY: build test lint vet tidy

build: ## Build the inventory connector
	go build ./...

vet: ## Run go vet
	go vet ./...

test: ## Test with race detector and coverage
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/inventory.out

lint: ## Lint (golangci-lint if available, else go vet)
	if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found; running go vet"; go vet ./...; \
	fi

tidy: ## Tidy go.mod
	go mod tidy
