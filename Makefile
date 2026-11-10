SHELL       := /usr/bin/env bash
BIN_DIR     := bin
COVERAGE_DIR := $(CURDIR)/coverage
COVERAGE_THRESHOLD := 25

# Go modules discovery. Connectors carry the subdirectory prefix.
# TARGET_DIR = services/$(TARGET) resolves correctly for every target.
SERVICES   := $(patsubst services/%/go.mod,%,$(wildcard services/*/go.mod))
CONNECTORS := $(patsubst services/connectors/%/go.mod,connectors/%,$(wildcard services/connectors/*/go.mod))
TARGETS    := $(SERVICES) $(CONNECTORS)

# Resolved path for a single target. Used when TARGET is set.
TARGET_DIR = services/$(TARGET)

.PHONY: init build build-all run \
        vet vet-all test test-all lint lint-all tidy tidy-all tidy-check-all mocks \
        go-modules-json workspace-sync-check \
        proto-lint proto-format proto-generate \
        docker-build docker-build-all \
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

# =============================================================================
# Development
# =============================================================================

# Build one service. Requires TARGET=<name>, e.g. make build TARGET=data-ingestion
build: require-target
	$(MAKE) -C $(TARGET_DIR) build

# Build every service and connector.
build-all: require-targets
	@failed=0; \
	for s in $(TARGETS); do \
		echo "==> build $$s"; \
		$(MAKE) --no-print-directory build TARGET=$$s || failed=1; \
	done; \
	exit $$failed

# Run one service locally. Requires TARGET=<name>.
run: require-target
	$(MAKE) -C $(TARGET_DIR) run

# Vet one service. Requires TARGET=<name>.
vet: require-target
	$(MAKE) -C $(TARGET_DIR) vet

# Vet every service and connector.
vet-all: require-targets
	@failed=0; \
	for s in $(TARGETS); do \
		echo "==> vet $$s"; \
		$(MAKE) --no-print-directory vet TARGET=$$s || failed=1; \
	done; \
	exit $$failed

# Test one service. Requires TARGET=<name>.
test: require-target
	$(MAKE) -C $(TARGET_DIR) COVERAGE_DIR=$(COVERAGE_DIR) COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD) test

# Test every service and connector. Coverage profiles land in $(COVERAGE_DIR).
test-all: require-targets
	@failed=0; \
	for s in $(TARGETS); do \
		echo "==> test $$s"; \
		$(MAKE) --no-print-directory test TARGET=$$s || failed=1; \
	done; \
	exit $$failed

# Lint one service. Requires TARGET=<name>.
lint: require-target
	$(MAKE) -C $(TARGET_DIR) lint

# Lint every service and connector.
lint-all: require-targets
	@failed=0; \
	for s in $(TARGETS); do \
		echo "==> lint $$s"; \
		$(MAKE) --no-print-directory lint TARGET=$$s || failed=1; \
	done; \
	exit $$failed

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

# Build one service image. Requires TARGET=<bake-target>, e.g. make docker-build TARGET=data-ingestion
docker-build: require-target
	docker buildx bake --progress=plain $(TARGET)

# Build all service images.
docker-build-all:
	docker buildx bake --progress=plain

# =============================================================================
# CI
# =============================================================================

# Emit discovered Go module paths as a JSON array. Used by CI to drive matrix jobs.
# Output example: ["services/data-ingestion","services/connectors/inventory"]
go-modules-json: require-targets
	@echo $(addprefix services/,$(TARGETS)) | tr ' ' '\n' | jq -R . | jq -cs .

# Check that go.mod and go.sum are tidy (no changes after running go mod tidy).
# Fails if any module is out of sync; run make tidy-all to fix.
tidy-check-all: require-targets
	cd gen/go && go mod tidy && git diff --exit-code go.mod go.sum
	@for s in $(TARGETS); do \
		echo "==> tidy-check $$s"; \
		cd $(CURDIR)/services/$$s && go mod tidy && git diff --exit-code go.mod go.sum; \
	done

# Check that the Go workspace is in sync with all module go.mod files.
# Fails if go work sync would change go.work, go.mod, or go.sum in any module.
# Run "go work sync" locally and commit the resulting changes to fix.
workspace-sync-check: require-targets
	go work sync
	git diff --exit-code go.work
	@for s in $(TARGETS); do \
		echo "==> workspace-sync-check $$s"; \
		git diff --exit-code services/$$s/go.mod services/$$s/go.sum; \
	done
	git diff --exit-code gen/go/go.mod gen/go/go.sum

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
