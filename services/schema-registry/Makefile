# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

PROJECT_ROOT ?= ../..
BUILD_DIR ?= $(CURDIR)/bin
COVERAGE_DIR ?= $(CURDIR)/coverage
GOVULNCHECK_VERSION ?= v1.9.0
GOOSE_VERSION ?= v3.26.0
TEST_PACKAGES ?= ./...
TEST_ARGS ?=

.PHONY: build build-schema-introspection-api run run-schema-introspection-api \
	test race-test coverage vet lint tidy vuln sbom migrate-up migrate-down

build: build-schema-introspection-api ## Build all Schema Registry commands

build-schema-introspection-api: ## Build the Schema Introspection API
	go build -o $(BUILD_DIR)/schema-introspection-api ./cmd/schema-introspection-api

run: run-schema-introspection-api ## Run the Schema Introspection API

run-schema-introspection-api: ## Run the Schema Introspection API from source
	go run ./cmd/schema-introspection-api

test: ## Run tests with race detection
	go test $(TEST_PACKAGES) -race $(TEST_ARGS)

race-test: test ## Run race-enabled tests

coverage: ## Produce a coverage profile
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/schema-registry.out

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint when available, otherwise go vet
	if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		go vet ./...; \
	fi

tidy: ## Tidy module dependencies
	go mod tidy

vuln: ## Check reachable Go vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

sbom: ## Generate a module software bill of materials
	@if ! command -v syft >/dev/null 2>&1; then \
		echo "syft is required for sbom"; exit 1; \
	fi
	syft dir:. -o spdx-json=$(CURDIR)/sbom.spdx.json -c $(PROJECT_ROOT)/.syft.yml

oss-license-check: ## Perform OSS license compliance check
	grant check sbom.spdx.json -c $(PROJECT_ROOT)/.grant.yml

migrate-up: ## Apply Goose migrations using DATABASE_URL
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir migrations postgres "$(DATABASE_URL)" up

migrate-down: ## Roll back the latest Goose migration using DATABASE_URL
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir migrations postgres "$(DATABASE_URL)" down

clean: ## Clean all build, test, and static analysis artifacts
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf $(CURDIR)/sbom.spdx.json
