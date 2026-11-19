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

.PHONY: test test-unit test-integration test-benchmark coverage vet lint tidy vuln sbom

build: ## Build all Schema Registry commands
	echo "dummy build"

test: ## Run tests with race detection
	mkdir -p $(COVERAGE_DIR)
	go test $(TEST_PACKAGES) -race -coverprofile=$(COVERAGE_DIR)/$(NAME).out $(TEST_ARGS)

test-unit: ## Run package unit tests with race detection
	go test ./internal/... ./pkg/... -race $(TEST_ARGS)

test-integration: ## Run boundary integration tests with race detection
	go test ./test/integration/... -race $(TEST_ARGS)

test-benchmark: ## Run black-box benchmarks with allocation reporting
	go test ./test/benchmark -bench=. -benchmem $(TEST_ARGS)

test-coverage: ## Produce a coverage profile
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

clean: ## Clean all build, test, and static analysis artifacts
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf $(CURDIR)/sbom.spdx.json
	go clean -testcache
