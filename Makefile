# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

SERVICE_DI := services/data-ingestion
SERVICE_IS := services/inventory-store
SERVICE_TS := services/telemetry-store
CONNECTOR_INVENTORY := services/connectors/inventory
COVERAGE_DIR := $(CURDIR)/coverage

.PHONY: build run test lint vet tidy mocks \
	proto-lint proto-format proto-generate go-work-init

go-work-init: ## Initialize local Go workspace (not committed; run once after checkout)
	@if [ ! -f go.work ]; then \
		go work init gen/go $(SERVICE_DI) $(SERVICE_IS) $(SERVICE_TS) $(CONNECTOR_INVENTORY); \
	fi
	go work sync

build: proto-generate ## Build all services
	$(MAKE) -C $(SERVICE_DI) build
	$(MAKE) -C $(SERVICE_IS) build
	$(MAKE) -C $(CONNECTOR_INVENTORY) build
	go build ./$(SERVICE_TS)/...

run: ## Run the data-ingestion service (dev)
	$(MAKE) -C $(SERVICE_DI) run

vet: ## Vet all services and connectors
	$(MAKE) -C $(SERVICE_DI) vet
	$(MAKE) -C $(SERVICE_IS) vet
	$(MAKE) -C $(CONNECTOR_INVENTORY) vet
	go vet ./$(SERVICE_TS)/...

test: ## Test all services and connectors
	$(MAKE) -C $(SERVICE_DI) COVERAGE_DIR=$(COVERAGE_DIR) test
	$(MAKE) -C $(CONNECTOR_INVENTORY) COVERAGE_DIR=$(COVERAGE_DIR) test
	cd $(SERVICE_TS) && go test ./...

lint: ## Lint all services and connectors
	$(MAKE) -C $(SERVICE_DI) lint
	$(MAKE) -C $(CONNECTOR_INVENTORY) lint

tidy: ## Tidy go.mod files
	cd gen/go && go mod tidy
	$(MAKE) -C $(SERVICE_DI) tidy
	$(MAKE) -C $(CONNECTOR_INVENTORY) tidy
	cd $(SERVICE_TS) && go mod tidy

mocks: ## Regenerate mocks
	$(MAKE) -C $(SERVICE_DI) mocks

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate

