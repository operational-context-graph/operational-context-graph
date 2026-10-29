# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

SERVICE_DI := services/data-ingestion
SERVICE_IS := services/inventory-store
SERVICE_TS := services/telemetry-store
CONNECTOR_INVENTORY := services/connectors/inventory
SERVICE_SR := services/schema-registry
COVERAGE_DIR := $(CURDIR)/coverage

.PHONY: build run test lint vet tidy mocks \
	proto-lint proto-format proto-generate go-work-init

go-work-init: ## Initialize local Go workspace (not committed; run once after checkout)
	@if [ ! -f go.work ]; then \
		go work init gen/go $(SERVICE_DI) $(SERVICE_IS) $(SERVICE_TS) $(CONNECTOR_INVENTORY); \
	fi

go-work-sync: ## Sync dependencies from the Go workspace's build list into each of the workspace modules
	go work sync

build: proto-generate ## Build all services
	$(MAKE) -C $(SERVICE_DI) build
	$(MAKE) -C $(SERVICE_IS) build
	$(MAKE) -C $(CONNECTOR_INVENTORY) build
	$(MAKE) -C $(SERVICE_TS) build
	$(MAKE) -C $(SERVICE_SR) build

run: ## Run the data-ingestion service (dev)
	$(MAKE) -C $(SERVICE_DI) run

vet: ## Vet all services and connectors
	$(MAKE) -C $(SERVICE_DI) vet
	$(MAKE) -C $(SERVICE_IS) vet
	$(MAKE) -C $(CONNECTOR_INVENTORY) vet
	$(MAKE) -C $(SERVICE_TS) vet
	$(MAKE) -C $(SERVICE_SR) vet

test: ## Test all services and connectors
	$(MAKE) -C $(SERVICE_DI) COVERAGE_DIR=$(COVERAGE_DIR) test
	$(MAKE) -C $(SERVICE_IS) COVERAGE_DIR=$(COVERAGE_DIR) test
	$(MAKE) -C $(CONNECTOR_INVENTORY) COVERAGE_DIR=$(COVERAGE_DIR) test
	$(MAKE) -C $(SERVICE_TS) COVERAGE_DIR=$(COVERAGE_DIR) test
	$(MAKE) -C $(SERVICE_SR) COVERAGE_DIR=$(COVERAGE_DIR) test

lint: ## Lint all services and connectors
	$(MAKE) -C $(SERVICE_DI) lint
	$(MAKE) -C $(CONNECTOR_INVENTORY) lint
	$(MAKE) -C $(SERVICE_TS) lint
	$(MAKE) -C $(SERVICE_SR) lint

tidy: ## Tidy go.mod files
	cd gen/go && go mod tidy
	$(MAKE) -C $(SERVICE_DI) tidy
	$(MAKE) -C $(CONNECTOR_INVENTORY) tidy
	$(MAKE) -C $(SERVICE_TS) tidy
	$(MAKE) -C $(SERVICE_SR) tidy

mocks: ## Regenerate mocks
	$(MAKE) -C $(SERVICE_DI) mocks

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate

