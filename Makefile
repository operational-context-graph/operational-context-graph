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

.PHONY: help build run test lint vet tidy mocks \
	proto-lint proto-format proto-generate

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: proto-generate ## Build all services
	$(MAKE) -C $(SERVICE_DI) build

run: ## Run the data-ingestion service (dev)
	$(MAKE) -C $(SERVICE_DI) run

vet: ## Vet all services
	$(MAKE) -C $(SERVICE_DI) vet

test: ## Test all services
	$(MAKE) -C $(SERVICE_DI) COVERAGE_DIR=$(COVERAGE_DIR) test

lint: ## Lint all services
	$(MAKE) -C $(SERVICE_DI) lint

tidy: ## Tidy go.mod files
	$(MAKE) -C $(SERVICE_DI) tidy

mocks: ## Regenerate mocks
	$(MAKE) -C $(SERVICE_DI) mocks

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate
