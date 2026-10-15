# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL := /usr/bin/env bash

SERVICE_DI := services/data-ingestion
CONNECTOR_INVENTORY := services/connectors/inventory
COVERAGE_DIR := $(CURDIR)/coverage

.PHONY: build run test lint vet tidy mocks \
	proto-lint proto-format proto-generate

build: proto-generate ## Build all services and connectors
	$(MAKE) -C $(SERVICE_DI) build
	$(MAKE) -C $(CONNECTOR_INVENTORY) build

run: ## Run the data-ingestion service (dev)
	$(MAKE) -C $(SERVICE_DI) run

vet: ## Vet all services and connectors
	$(MAKE) -C $(SERVICE_DI) vet
	$(MAKE) -C $(CONNECTOR_INVENTORY) vet

test: ## Test all services and connectors
	$(MAKE) -C $(SERVICE_DI) COVERAGE_DIR=$(COVERAGE_DIR) test
	$(MAKE) -C $(CONNECTOR_INVENTORY) COVERAGE_DIR=$(COVERAGE_DIR) test

lint: ## Lint all services and connectors
	$(MAKE) -C $(SERVICE_DI) lint
	$(MAKE) -C $(CONNECTOR_INVENTORY) lint

tidy: ## Tidy go.mod files
	$(MAKE) -C $(SERVICE_DI) tidy
	$(MAKE) -C $(CONNECTOR_INVENTORY) tidy

mocks: ## Regenerate mocks
	$(MAKE) -C $(SERVICE_DI) mocks

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate
