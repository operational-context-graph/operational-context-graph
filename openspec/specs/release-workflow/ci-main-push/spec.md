# CI Main Push Specification

## Purpose

Defines the behavior of the CI pipeline on every push to `main`: running the full test suite, building and pushing all artifacts tagged with a dev version, and conditionally aliasing artifacts to a release version when the merge source was a release branch.

## Requirements

### Requirement: Full CI suite runs on every main push

The CI pipeline SHALL run lint, build, unit tests, Helm lint, and workspace-sync checks on every push to `main`, regardless of whether the push originates from a release branch merge or a regular feature/fix merge.

#### Scenario: All checks pass on a regular main push
- **WHEN** a non-release branch is merged to `main` and all CI jobs succeed
- **THEN** the pipeline exits with success and artifact jobs proceed

#### Scenario: Lint fails on main push
- **WHEN** `make lint-all` returns non-zero on a main push
- **THEN** the pipeline fails, no artifacts are built or pushed, and no tags are applied

#### Scenario: Unit tests fail on main push
- **WHEN** `make test-all` returns non-zero on a main push
- **THEN** the pipeline fails, no artifacts are built or pushed, and no tags are applied

#### Scenario: Helm lint fails on main push
- **WHEN** `ct lint` returns non-zero on a main push
- **THEN** the pipeline fails, no artifacts are built or pushed, and no tags are applied

---

### Requirement: Dev version tag derived from push date and commit SHA

The CI pipeline SHALL derive a dev version tag of the form `dev.<YYYYMMDD>-<sha7>` from the UTC date of the push and the first 7 characters of the commit SHA.

#### Scenario: Dev tag derived correctly
- **WHEN** the CI pipeline runs on commit `abc1234` on 2026-03-15 UTC
- **THEN** the dev version tag is `dev.20260315-abc1234`

#### Scenario: Commit SHA shorter than 7 characters is not possible in practice, but tag format is stable
- **WHEN** `GITHUB_SHA` is exactly 40 characters (standard git SHA)
- **THEN** only the first 7 characters are used in the dev tag

---

### Requirement: All container images are built and pushed on every main push

The CI pipeline SHALL build all container images defined in `docker-bake.hcl` (targets: `inventory-connector`, `inventory-store`, `telemetry-store`, `data-ingestion`) and push them to GHCR tagged with the dev version tag.

#### Scenario: All images built and pushed with dev tag
- **WHEN** CI succeeds on a main push with dev tag `dev.20260315-abc1234`
- **THEN** images are pushed to `ghcr.io/operational-context-graph/<slug>:dev.20260315-abc1234` for each bake target

#### Scenario: SBOM and build-provenance attestation generated at build time
- **WHEN** images are pushed to GHCR
- **THEN** SBOM (SPDX-JSON) and build-provenance attestations are generated and pushed to the registry alongside each image

#### Scenario: GHCR push fails for one image
- **WHEN** the GHCR registry returns an error during push of one image target
- **THEN** the CI pipeline fails, the GitHub Release workflow does not run (its `workflow_run` prerequisite does not reach `success`)

#### Scenario: Docker Buildx unavailable on runner
- **WHEN** `docker/setup-buildx-action` fails to initialize
- **THEN** the build step fails and no images are pushed

---

### Requirement: Helm chart is packaged from the tree as-is and pushed to OCI on every main push

The CI pipeline SHALL package the umbrella Helm chart from the repository tree without any runtime patching of subchart values, and push it to GHCR as an OCI artifact tagged with the dev version tag. Subchart image tags in `values.yaml` files are maintained by component release PRs; CI does not modify them.

#### Scenario: Helm chart packaged and pushed with dev tag
- **WHEN** CI succeeds on a main push with dev tag `dev.20260315-abc1234`
- **THEN** the Helm chart is pushed to `ghcr.io/operational-context-graph/operational-context-graph:dev.20260315-abc1234` as an OCI artifact

#### Scenario: Helm dependency update fails
- **WHEN** `helm dependency update` fails (e.g., chart repository unreachable)
- **THEN** the Helm push job fails and the pipeline fails; no chart is pushed

#### Scenario: OCI push of Helm chart fails
- **WHEN** `helm push` returns non-zero
- **THEN** the Helm push job fails and the pipeline fails

---

### Requirement: OCM bundle is packaged and pushed to OCI on every main push

The CI pipeline SHALL package an OCM component bundle and push it to GHCR as an OCI artifact tagged with the dev version tag.

#### Scenario: OCM bundle packaged and pushed with dev tag
- **WHEN** CI succeeds on a main push with dev tag `dev.20260315-abc1234`
- **THEN** the OCM bundle is pushed to `ghcr.io/operational-context-graph/ocg-ocm:dev.20260315-abc1234` as an OCI artifact

#### Scenario: OCM CLI tool unavailable on runner
- **WHEN** the OCM CLI is not installed or returns non-zero during bundle creation
- **THEN** the OCM push job fails and the pipeline fails; no bundle is pushed

---

### Requirement: Released component image is aliased to release version on component release merge

When the source branch of a main push is `release/<slug>/<version>`, the CI pipeline SHALL alias the released component's GHCR image from its `dev.<date>-<sha>` digest to `<version>` and `<version>-<sha>` — without rebuilding the image.

#### Scenario: Component release merge aliases image to release version
- **WHEN** branch `release/data-ingestion/1.2.3` is merged to `main`, CI succeeds, dev tag is `dev.20260315-abc1234`
- **THEN** `ghcr.io/operational-context-graph/data-ingestion:1.2.3` and `ghcr.io/operational-context-graph/data-ingestion:1.2.3-abc1234` are pushed as aliases pointing to the same digest as `:dev.20260315-abc1234`
- **AND** no new image layer is built

#### Scenario: Only the released component receives the version alias
- **WHEN** branch `release/data-ingestion/1.2.3` is merged to `main`
- **THEN** only `data-ingestion` receives the `1.2.3` and `1.2.3-abc1234` tags; `inventory-store`, `telemetry-store`, and `inventory-connector` receive only their `dev.<date>-<sha>` tags

#### Scenario: Source branch name does not match a known component slug
- **WHEN** the source branch is `release/unknown-service/1.0.0` and no component with that slug exists in `docker-bake.hcl`
- **THEN** the alias step fails with a clear error message identifying the unknown slug; no alias tags are pushed

#### Scenario: Dev image not yet present when alias is attempted
- **WHEN** the alias job runs before the image build job completes (dependency ordering violated)
- **THEN** the alias job MUST be declared as depending on the image build job so that this scenario cannot occur in a correctly configured workflow

#### Scenario: Non-Dockerfile component release merge
- **WHEN** branch `release/gen-go/1.0.0` is merged to `main` and `gen/go` has no `Dockerfile`
- **THEN** no image alias step runs for that component; other artifact jobs proceed normally

---

### Requirement: Helm chart and OCM bundle are aliased to system release version on system release merge

When the source branch of a main push is `release/<version>`, the CI pipeline SHALL alias the Helm chart OCI artifact and the OCM bundle OCI artifact from their `dev.<date>-<sha>` tags to `<version>` — without repackaging.

#### Scenario: System release merge aliases Helm chart and OCM bundle to release version
- **WHEN** branch `release/1.0.0` is merged to `main`, CI succeeds, dev tag is `dev.20260315-abc1234`
- **THEN** `ghcr.io/operational-context-graph/operational-context-graph:1.0.0` points to the same digest as `:dev.20260315-abc1234`
- **AND** `ghcr.io/operational-context-graph/ocg-ocm:1.0.0` points to the same digest as `:dev.20260315-abc1234`
- **AND** no repackaging or rebuild occurs

#### Scenario: Helm chart OCI push failure during alias
- **WHEN** the alias tag push for the Helm chart fails (registry error)
- **THEN** the alias job fails; the system release tag job in `release-system.yml` does not run (depends on CI success)

## Non-Goals

- Multi-architecture builds (`linux/arm64`) — all images target `linux/amd64` only.
- Integration or end-to-end tests as part of the CI suite — only unit tests and static analysis.
- Automated component discovery from workspace manifests — the bake targets and component slugs are enumerated statically.
- Changing the CI behavior for pull requests — PR CI continues to run lint, build, and test only; no artifact push.

## Open Questions

- **OCM bundle tool**: which CLI (`ocm`, `ctf`, other?) is used to package the OCM bundle? The spec assumes `ocm` CLI. Implementor must confirm availability on `ubuntu-latest` runners.
- **Helm OCI target path**: spec assumes `ghcr.io/operational-context-graph/operational-context-graph`. Confirm the target image name and namespace before implementing.
- **Alias mechanism**: spec assumes `crane tag` for re-tagging without rebuild. Confirm `crane` is available on `ubuntu-latest` runners or identify the alternative.
