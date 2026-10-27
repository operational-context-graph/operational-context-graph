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
	go build ./...

run: ## Run the data-ingestion service (dev)
	$(MAKE) -C $(SERVICE_DI) run

vet: ## Vet the whole module
	go vet ./...

test: ## Test the whole module with race detector and coverage
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/coverage.out

lint: ## Lint all services and connectors
	$(MAKE) -C $(SERVICE_DI) lint
	$(MAKE) -C $(CONNECTOR_INVENTORY) lint
	$(MAKE) -C $(SERVICE_TS) lint

tidy: ## Tidy the root go.mod
	go mod tidy

mocks: ## Regenerate mocks
	$(MAKE) -C $(SERVICE_DI) mocks

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate

