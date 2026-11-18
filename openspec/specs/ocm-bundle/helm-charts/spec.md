# ocm-bundle/helm-charts Specification

## Purpose

Defines the structure and observable behavior of the per-service Helm charts and top-level umbrella chart used in the OCG OCM bundle, including digest-or-tag image references and dependency resolution order.

## Requirements

### Requirement: Per-service Helm charts expose repository, tag, and digest; digest takes precedence

Each service chart (`data-ingestion`, `inventory-store`, `telemetry-store`, `inventory-connector`) SHALL expose `image.repository`, `image.tag`, and `image.digest` values. When `image.digest` is non-empty, the deployment template SHALL render the image as `<repository>@<digest>`. When `image.digest` is empty, the template SHALL render `<repository>:<tag>`. `image.tag` has a development default; `image.digest` defaults to empty string.

**Note**: `schema-registry` is not bundled in this release; its chart is not required.

#### Scenario: Service chart renders image as repository@digest when digest is set

- **WHEN** `helm template` is run against a service chart with `image.repository=ghcr.io/ocg/data-ingestion`, `image.digest=sha256:abc123`, and `image.tag=v1.0.0`
- **THEN** the rendered Deployment contains `image: ghcr.io/ocg/data-ingestion@sha256:abc123`; the tag is silently ignored

#### Scenario: Service chart renders image as repository:tag when digest is empty

- **WHEN** `helm template` is run with `image.repository=ghcr.io/ocg/data-ingestion`, `image.tag=v1.0.0`, and `image.digest=""` (default)
- **THEN** the rendered Deployment contains `image: ghcr.io/ocg/data-ingestion:v1.0.0`

#### Scenario: Both digest and tag set — digest always wins

- **WHEN** `helm template` is run with both `image.tag=v1.0.0` and `image.digest=sha256:abc`
- **THEN** the rendered Deployment contains `image: <repo>@sha256:abc`; tag does not appear in the image reference

### Requirement: Per-service charts live at helm/charts/<name>/

Service charts live at `helm/charts/<name>/`. The umbrella chart at `helm/` declares them as local dependencies. `helm/charts/` is populated by `helm dependency update helm/`.

#### Scenario: helm dependency update resolves local dependencies

- **WHEN** `helm dependency update helm/` runs
- **THEN** all local chart dependencies resolve; `helm dependency update` exits 0

#### Scenario: A service chart directory is missing

- **WHEN** a service chart directory does not exist under `helm/charts/` and `helm dependency update helm/` runs
- **THEN** `helm dependency update` fails with an error naming the missing dependency

### Requirement: Chart versions are set consistently from VERSION at build time

The umbrella `helm/Chart.yaml` `version` and `appVersion` fields SHALL be updated to `${VERSION}` as part of the system release PR (via `release-system-pr.yml`). The Makefile `build` stage runs `helm package helm/ -d bin/ --version ${VERSION}` to produce `bin/operational-context-graph-${VERSION}.tgz`.

#### Scenario: Build stage produces correctly versioned chart tarball

- **WHEN** `VERSION=1.2.3 make ocm-build` runs
- **THEN** `bin/operational-context-graph-1.2.3.tgz` exists

### Requirement: image-values.yaml feeds image.digest into service charts at deploy time

`bin/image-values.yaml` SHALL be a valid Helm values file that sets `<service>.image.repository` and `<service>.image.digest` for each of the four services. At deploy time, applying this file SHALL override the chart defaults with the OCM-verified digest values. The `${IMAGE_REPOSITORY}` placeholder is expanded via `envsubst '${IMAGE_REPOSITORY}'` before `helm install` runs.

#### Scenario: image-values.yaml applied to umbrella chart

- **WHEN** `helm upgrade --install ocg helm/ -f bin/image-values.yaml` is run with `IMAGE_REPOSITORY` replaced
- **THEN** each service deployment uses the `image.digest` value from `bin/image-values.yaml`, not the chart default

#### Scenario: image-values.yaml contains IMAGE_REPOSITORY placeholder unexpanded

- **WHEN** `bin/image-values.yaml` is applied without substituting `${IMAGE_REPOSITORY}` first
- **THEN** Helm renders a malformed image reference containing the literal string `${IMAGE_REPOSITORY}`; the pre-install check fails before `helm install` runs

## Non-Goals

- Air-gap / offline Helm repository mirroring
- Third-party dependency charts (`postgresql`, `doris-operator`) inside `helm/` — operators are deployed separately (see ocm-bundle/dependency-bundles)
- `schema-registry` chart (deferred; no implementation)
