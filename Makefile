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

# =============================================================================
# OCM Bundle Pipeline
# =============================================================================
# Six ordered stages: preflight → build → package → inspect → sign → verify.
# Each stage depends on the previous. No stage produces a shippable CTF without
# sign having run. Run make -j without concern: .NOTPARALLEL serialises all
# targets so concurrent invocations cannot corrupt bin/ctf/.
#
# Required env:
#   VERSION      - semver string (derived from git tag if not set)
#   SIGN_KEY     - path to RSA private key PEM (default /tmp/ocg-signing-key.pem)
#
# RELEASE ORDER for helm dependency update:
#   All subcharts live under helm/charts/. helm dependency update helm/ resolves
#   all file:// dependencies in a single pass.

.NOTPARALLEL:

OCM_VERSION     := 0.22.1
SIGN_KEY        ?= /tmp/ocg-signing-key.pem
OCM_COMPONENT   := github.com/operational-context-graph/operational-context-graph
OCM_CTF         := ctf::./bin/ctf/
OCM_SERVICES    := data-ingestion telemetry-store inventory-store inventory-connector

.PHONY: ocm-preflight ocm-build ocm-package ocm-inspect ocm-sign ocm-verify \
        preflight package inspect sign verify

# Short aliases (build conflicts with Go build target; use ocm-build instead)
preflight: ocm-preflight
package:   ocm-package
inspect:   ocm-inspect
sign:      ocm-sign
verify:    ocm-verify

ocm-preflight:
	@# (a) ocm on PATH
	@command -v ocm >/dev/null 2>&1 || { echo "ERROR: ocm: not found — install OCM v2 CLI >= $(OCM_VERSION)"; exit 1; }
	@# (b) OCM CLI version >= 0.22.1; reject v1 (0.16–0.53)
	@set -euo pipefail; \
	raw=$$(ocm version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ -z "$$raw" ]; then echo "ERROR: cannot determine OCM CLI version"; exit 1; fi; \
	ocm version 2>/dev/null | grep -q 'Open Component Model v2' || { echo "ERROR: OCM v1 not supported, use v2 >= $(OCM_VERSION) (found $$raw)"; exit 1; }; \
	major=$$(echo "$$raw" | cut -d. -f1); \
	minor=$$(echo "$$raw" | cut -d. -f2); \
	patch=$$(echo "$$raw" | cut -d. -f3); \
	req_minor=22; req_patch=1; \
	if [ "$$major" -lt 1 ] && { [ "$$minor" -lt "$$req_minor" ] || { [ "$$minor" -eq "$$req_minor" ] && [ "$$patch" -lt "$$req_patch" ]; }; }; then \
	  echo "ERROR: OCM CLI $$raw < $(OCM_VERSION) required"; exit 1; \
	fi
	@# (c) Derive VERSION if not set
	$(eval VERSION ?= $(shell git describe --tags --exact-match HEAD 2>/dev/null | sed 's/^v//' || true))
	@test -n "$(VERSION)" || { echo "ERROR: VERSION is not set and HEAD is not tagged with a vX.Y.Z tag"; exit 1; }
	@# (d) VERSION is valid semver (no pre-release)
	@echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "ERROR: VERSION '$(VERSION)' is not valid semver (X.Y.Z)"; exit 1; }
	@# (e) Git working tree is clean
	@test -z "$$(git status --porcelain)" || { echo "ERROR: working tree is dirty:"; git status --porcelain; exit 1; }
	@# (f) Public key exists and is non-zero
	@test -s .ocm/ocg.pub.pem || { echo "ERROR: .ocm/ocg.pub.pem is missing or empty"; exit 1; }
	@# (g) Private key exists and is non-zero
	@test -f "$(SIGN_KEY)" || { echo "ERROR: SIGN_KEY=$(SIGN_KEY) does not exist"; exit 1; }
	@test -s "$(SIGN_KEY)" || { echo "ERROR: key file is empty: $(SIGN_KEY)"; exit 1; }
	@# (h) OCI tarballs exist, are non-zero, have exactly one index.json entry
	@for svc in $(OCM_SERVICES); do \
	  tar=bin/images/$$svc.oci.tar; \
	  test -f "$$tar" || { echo "ERROR: $$tar: missing service tarball"; exit 1; }; \
	  test -s "$$tar" || { echo "ERROR: $$svc: tarball is empty"; exit 1; }; \
	  count=$$(tar -xOf "$$tar" index.json 2>/dev/null | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d["manifests"]))' 2>/dev/null || echo "ERR"); \
	  if [ "$$count" = "ERR" ]; then echo "ERROR: $$svc: tarball is empty or index.json unreadable"; exit 1; fi; \
	  if [ "$$count" != "1" ]; then echo "ERROR: $$svc: expected exactly 1 index entry, found $$count"; exit 1; fi; \
	done
	@echo "preflight: OK (VERSION=$(VERSION))"

ocm-build: ocm-preflight
	@# Pull in all external chart deps (postgresql, doris-operator) and resolve
	@# local file:// subcharts under helm/charts/ in a single pass.
	helm dependency update helm/
	@# Package the umbrella chart
	helm package helm/ -d bin/ --version $(VERSION)
	@# Bundle manifest
	@{ \
	  echo "version: \"$(VERSION)\""; \
	  echo "sourceCommit: \"$$(git rev-parse HEAD)\""; \
	  echo "imageDigests:"; \
	  for svc in $(OCM_SERVICES); do \
	    digest=$$(tar -xOf bin/images/$$svc.oci.tar index.json 2>/dev/null | python3 -c 'import sys,json; m=json.load(sys.stdin)["manifests"][0]; print(m["digest"])' 2>/dev/null); \
	    echo "  $$svc: \"$$digest\""; \
	  done; \
	} > bin/bundle-manifest.yaml
	@yq . bin/bundle-manifest.yaml >/dev/null && echo "build: bundle-manifest.yaml is valid YAML"
	@# image-values.yaml — IMAGE_REPOSITORY must NOT be expanded
	@{ \
	  printf 'imageDefaults:\n  repository: $${IMAGE_REPOSITORY}\nimages:\n'; \
	  for svc in $(OCM_SERVICES); do \
	    digest=$$(tar -xOf bin/images/$$svc.oci.tar index.json 2>/dev/null | python3 -c 'import sys,json; m=json.load(sys.stdin)["manifests"][0]; print(m["digest"])' 2>/dev/null); \
	    printf '  %s:\n    digest: "%s"\n' "$$svc" "$$digest"; \
	  done; \
	} > bin/image-values.yaml
	@grep '\$${IMAGE_REPOSITORY}' bin/image-values.yaml >/dev/null || { echo "ERROR: IMAGE_REPOSITORY placeholder missing from image-values.yaml"; exit 1; }
	@echo "build: OK (VERSION=$(VERSION))"

ocm-package: ocm-build
	@rm -rf bin/ctf/
	VERSION=$(VERSION) ocm add component-version \
	  --working-directory . \
	  --constructor .ocm/component-constructor.yaml \
	  --repository ctf::./bin/ctf/
	@echo "package: OK"

ocm-inspect: ocm-package
	@echo "inspect: checking resource digests..."
	@for svc in $(OCM_SERVICES); do \
	  expected=$$(yq ".imageDigests.$$svc" bin/bundle-manifest.yaml 2>/dev/null | tr -d '"'); \
	  actual=$$(tar -xOf bin/images/$$svc.oci.tar index.json 2>/dev/null | python3 -c 'import sys,json; m=json.load(sys.stdin)["manifests"][0]; print(m["digest"])' 2>/dev/null); \
	  if [ "$$expected" != "$$actual" ]; then \
	    echo "ERROR: $$svc digest mismatch: expected $$expected, actual $$actual"; exit 1; \
	  fi; \
	done
	@echo "inspect: OK"

ocm-sign: ocm-inspect
	ocm sign component-version \
	  --signature ocg-sig \
	  $(OCM_CTF)$(OCM_COMPONENT):$(VERSION)
	@echo "sign: OK"

ocm-verify: ocm-sign
	ocm verify component-version \
	  --signature ocg-sig \
	  $(OCM_CTF)$(OCM_COMPONENT):$(VERSION)
	@# Route B archive
	tar -czf bin/ocg-$(VERSION).ctf.tgz -C bin ctf/
	@echo "verify: OK — bin/ocg-$(VERSION).ctf.tgz created"
