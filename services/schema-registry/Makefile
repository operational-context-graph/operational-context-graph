SHELL               := /usr/bin/env bash
NAME                := $(notdir $(CURDIR))

PROJECT_ROOT        ?= ../..
BUILD_DIR           ?= $(CURDIR)/bin
SCRIPTS_DIR         := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))../../scripts)
COVERAGE_DIR        ?= $(CURDIR)/coverage
COVERAGE_THRESHOLD  ?= 90
TEST_PACKAGES       ?= ./...
TEST_ARGS           ?=

GOVULNCHECK_VERSION ?= v1.10.0

.PHONY: build build-schema-introspection-api run run-schema-introspection-api \
	test test-unit test-integration test-benchmark race-test coverage vet lint tidy vuln sbom migrate-up migrate-down

build: build-schema-introspection-api ## Build all Schema Registry commands

build-schema-introspection-api: ## Build the Schema Introspection API
	go build -o $(BUILD_DIR)/schema-introspection-api ./cmd/schema-introspection-api

run: run-schema-introspection-api ## Run the Schema Introspection API

run-schema-introspection-api: ## Run the Schema Introspection API from source
	go run ./cmd/schema-introspection-api

test: ## Run tests with race detection and produce a coverage profile
	mkdir -p $(COVERAGE_DIR)
	go test $(TEST_PACKAGES) -race -coverprofile=$(COVERAGE_DIR)/$(NAME).out -coverpkg=./... $(TEST_ARGS)

test-unit: ## Run package unit tests with race detection
	go test ./internal/... ./pkg/... -race $(TEST_ARGS)

test-integration: ## Run boundary integration tests with race detection
	go test ./test/integration/... -race $(TEST_ARGS)

test-benchmark: ## Run black-box benchmarks with allocation reporting
	go test ./test/benchmark -bench=. -benchmem $(TEST_ARGS)

test-coverage: ## Check test coverage
	$(SCRIPTS_DIR)/check-coverage.sh $(COVERAGE_DIR)/$(NAME).out $(COVERAGE_THRESHOLD)

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run

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
	go run github.com/pressly/goose/v3/cmd/goose -dir migrations postgres "$(DATABASE_URL)" up

migrate-down: ## Roll back the latest Goose migration using DATABASE_URL
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required"; exit 1)
	go run github.com/pressly/goose/v3/cmd/goose -dir migrations postgres "$(DATABASE_URL)" down

clean: ## Clean all build, test, and static analysis artifacts
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf $(CURDIR)/sbom.spdx.json
	go clean -testcache
