# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL       := /usr/bin/env bash
BIN_DIR     := bin
COVERAGE_DIR := $(CURDIR)/coverage

# Go modules discovery. Connectors carry the subdirectory prefix.
# TARGET_DIR = services/$(TARGET) resolves correctly for every target.
SERVICES   := $(patsubst services/%/go.mod,%,$(wildcard services/*/go.mod))
CONNECTORS := $(patsubst services/connectors/%/go.mod,connectors/%,$(wildcard services/connectors/*/go.mod))
TARGETS    := $(SERVICES) $(CONNECTORS)

# Resolved path for a single target. Used when TARGET is set.
TARGET_DIR = services/$(TARGET)

.PHONY: init build build-all run \
        vet vet-all test test-all lint lint-all tidy tidy-all mocks \
        proto-lint proto-format proto-generate \
        require-target require-targets

# =============================================================================
# Workspace
# =============================================================================

# Set up the local Go workspace file. Not committed; run once after checkout.
# go.work is ignored by .gitignore so the workspace is local only.
# Always re-runs go work use to handle partial or outdated workspace files.
init:
	@if [ ! -f go.work ]; then \
		go work init; \
	fi
	go work use gen/go $(addprefix services/,$(TARGETS))
	go work sync

# =============================================================================
# Development
# =============================================================================

# Build one service. Requires TARGET=<name>, e.g. make build TARGET=data-ingestion
build: require-target
	$(MAKE) -C $(TARGET_DIR) build

# Build every service and connector.
build-all: require-targets
	@for s in $(TARGETS); do \
		echo "==> build $$s"; \
		$(MAKE) --no-print-directory build TARGET=$$s; \
	done

# Run one service locally. Requires TARGET=<name>.
run: require-target
	$(MAKE) -C $(TARGET_DIR) run

# Vet one service. Requires TARGET=<name>.
vet: require-target
	$(MAKE) -C $(TARGET_DIR) vet

# Vet every service and connector.
vet-all: require-targets
	@for s in $(TARGETS); do \
		echo "==> vet $$s"; \
		$(MAKE) --no-print-directory vet TARGET=$$s; \
	done

# Test one service. Requires TARGET=<name>.
test: require-target
	$(MAKE) -C $(TARGET_DIR) COVERAGE_DIR=$(COVERAGE_DIR) test

# Test every service and connector. Coverage profiles land in $(COVERAGE_DIR).
test-all: require-targets
	@for s in $(TARGETS); do \
		echo "==> test $$s"; \
		$(MAKE) --no-print-directory test TARGET=$$s; \
	done

# Lint one service. Requires TARGET=<name>.
lint: require-target
	$(MAKE) -C $(TARGET_DIR) lint

# Lint every service and connector.
lint-all: require-targets
	@for s in $(TARGETS); do \
		echo "==> lint $$s"; \
		$(MAKE) --no-print-directory lint TARGET=$$s; \
	done

# Tidy go.mod for one service. Requires TARGET=<name>.
tidy: require-target
	$(MAKE) -C $(TARGET_DIR) tidy

# Tidy go.mod for every module, including the shared gen/go stubs.
tidy-all: require-targets
	cd gen/go && go mod tidy
	@for s in $(TARGETS); do \
		echo "==> tidy $$s"; \
		$(MAKE) --no-print-directory tidy TARGET=$$s; \
	done

# Regenerate mocks for data-ingestion (requires mockery).
mocks:
	$(MAKE) -C services/data-ingestion mocks

# =============================================================================
# Protobuf
# =============================================================================

proto-lint:
	buf lint

proto-format:
	buf format -w

# Regenerate Go stubs under gen/go from proto sources.
proto-generate:
	buf generate

# =============================================================================
# Guards
# =============================================================================

require-target:
	@test -n "$(TARGET)" || { \
		echo "ERROR: TARGET is required. Example: make $(MAKECMDGOALS) TARGET=telemetry-store"; \
		echo "       Available targets: $(TARGETS)"; \
		exit 1; \
	}

require-targets:
	@test -n "$(TARGETS)" || { \
		echo "ERROR: no targets found under services/*/go.mod"; \
		echo "       Run make from the repo root and ensure each service has a go.mod"; \
		exit 1; \
	}
