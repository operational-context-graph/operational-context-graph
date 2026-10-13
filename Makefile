# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

SERVICE_DI := services/data-ingestion
COVERAGE_DIR := $(CURDIR)/coverage

.PHONY: build run test lint vet tidy mocks \
	proto-lint proto-format proto-generate

build: proto-generate ## Build all services
	go build github.com/operational-context-graph/operational-context-graph/services/data-ingestion/...
	go build github.com/operational-context-graph/telemetry-store/...
	go build github.com/operational-context-graph/inventory-store/...

run: ## Run the data-ingestion service (dev)
	$(MAKE) -C $(SERVICE_DI) run

vet: ## Vet all services
	go vet github.com/operational-context-graph/operational-context-graph/services/data-ingestion/...
	go vet github.com/operational-context-graph/telemetry-store/...
	go vet github.com/operational-context-graph/inventory-store/...

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

