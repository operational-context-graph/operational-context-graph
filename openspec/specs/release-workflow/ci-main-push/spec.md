# CI Main Push Specification

## Purpose

Defines the behavior of `ci.yml` on every push to `main`: running the full test suite, building and pushing all artifacts tagged with a dev version, detecting whether the push is a release-branch merge, and pushing the release git tag once the dev artifacts are published. The pushed tag triggers the promotion workflows specified in `release-tagging`. `ci.yml` itself performs no aliasing.

## Requirements

### Requirement: Full CI suite runs on every main push

The CI pipeline SHALL run lint, build, unit tests, Helm lint, and workspace-sync checks on every push to `main`, regardless of whether the push originates from a release branch merge or a regular feature/fix merge.

#### Scenario: All checks pass on a regular main push
- **WHEN** a non-release branch is merged to `main` and all CI jobs succeed
- **THEN** the pipeline exits with success and the artifact publish jobs proceed

#### Scenario: Lint fails on main push
- **WHEN** `make lint-all` returns non-zero on a main push
- **THEN** the pipeline fails, no artifacts are published, and no release tag is pushed

#### Scenario: Unit tests fail on main push
- **WHEN** `make test-all` returns non-zero on a main push
- **THEN** the pipeline fails, no artifacts are published, and no release tag is pushed

#### Scenario: Helm lint fails on main push
- **WHEN** `ct lint` returns non-zero on a main push
- **THEN** the pipeline fails, no artifacts are published, and no release tag is pushed

---

### Requirement: Dev version tag derived from the commit committer date and SHA

The CI pipeline SHALL derive a dev version tag of the form `dev.<YYYYMMDD>-<sha7>` from the UTC date of the commit's committer timestamp and the first 7 characters of the commit SHA. The committer date, not the runner wall-clock date, is used so the dev tag matches the value the release promotion workflows recompute from the same commit.

#### Scenario: Dev tag derived correctly
- **WHEN** the CI pipeline runs on commit `abc1234` whose committer date is 2026-03-15 UTC
- **THEN** the dev version tag is `dev.20260315-abc1234`

#### Scenario: Committer date governs the dev tag across a wall-clock boundary
- **WHEN** the publish job runs on a later UTC calendar day than the commit's committer date
- **THEN** the dev tag uses the committer date, so it matches the tag the release promotion recomputes from the same commit

#### Scenario: Commit SHA is truncated to 7 characters
- **WHEN** `GITHUB_SHA` is a standard 40-character git SHA
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
- **THEN** the CI pipeline fails; the Helm publish and `tag-release` jobs do not run; no release tag is pushed, so the tag-triggered promotion workflows never fire

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

### Requirement: OCM bundle publish is a placeholder pending implementation

The CI pipeline SHALL include an OCM bundle publish job wired into the artifact dependency graph, so release promotion does not need a second change when OCM support lands. Until implemented, the job SHALL be a no-op placeholder that packages and pushes nothing and emits an empty digest.

#### Scenario: OCM placeholder runs without publishing a bundle
- **WHEN** CI succeeds on a main push
- **THEN** the OCM publish job completes successfully, pushes no OCI artifact, and emits an empty digest output

#### Scenario: OCM placeholder keeps the dependency graph intact
- **WHEN** a downstream job depends on the OCM publish job
- **THEN** it observes the placeholder's successful completion and is not blocked by the missing implementation

---

### Requirement: Release merge is detected from the squash-merged PR source branch

On a push to `main`, the CI pipeline SHALL determine whether the push is a release-branch merge. It SHALL read the authoritative PR number from the squash commit subject `(#N)` (first line only), resolve the PR head ref via a targeted GitHub API lookup, and parse that ref with `scripts/parse-release-branch.sh` to produce `release_type`, `slug`, `version`, and `tag`. The commit subject is untrusted input and SHALL only be expanded as a shell variable, never interpolated into a script body.

#### Scenario: Component release merge detected
- **WHEN** the squash-merged PR head ref is `release/data-ingestion/1.2.3`
- **THEN** detection yields `release_type` `component`, `slug` `data-ingestion`, `version` `1.2.3`, and `tag` `services/data-ingestion/v1.2.3`

#### Scenario: System release merge detected
- **WHEN** the squash-merged PR head ref is `release/1.0.0`
- **THEN** detection yields `release_type` `system`, `version` `1.0.0`, and `tag` `v1.0.0`

#### Scenario: Non-release merge yields empty release type
- **WHEN** the squash-merged PR head ref is a non-release branch
- **THEN** `release_type` is empty and no release tag is pushed

#### Scenario: Commit subject carries no PR number
- **WHEN** the squash commit subject has no trailing `(#N)`
- **THEN** detection skips, `release_type` is empty, and no release tag is pushed

#### Scenario: PR head ref lookup fails
- **WHEN** the GitHub API lookup for the resolved PR number returns an error or empty ref
- **THEN** the detect job fails with a logged error and no release tag is pushed

---

### Requirement: Release git tag is pushed only after the dev artifacts are published

When a push to `main` is a release-branch merge, the CI pipeline SHALL push the annotated release git tag (`<component-path>/v<version>` for a component, `v<version>` for a system) using the release GitHub App, and SHALL do so only after the `publish-dev` and `publish-helm` jobs succeed. The tag push SHALL run only on `push` to the protected default branch, never on pull requests, so a fork cannot reach the App private key. The pushed tag is what triggers the promotion workflows in `release-tagging`.

#### Scenario: Tag pushed after artifacts exist on a release merge
- **WHEN** a `release/data-ingestion/1.2.3` merge passes CI and `publish-dev` and `publish-helm` succeed
- **THEN** the annotated tag `services/data-ingestion/v1.2.3` is pushed by the release App
- **AND** because the tag is pushed only after the dev image and chart are published, the tag-triggered promotion cannot run before its source artifact exists

#### Scenario: No tag pushed when a dev artifact fails to publish
- **WHEN** a release merge passes the test suite but `publish-dev` or `publish-helm` fails
- **THEN** the `tag-release` job does not run and no release tag is pushed, so no release tag can point at a commit whose artifacts were never published

#### Scenario: No tag pushed on a non-release main push
- **WHEN** a non-release branch is merged to `main`
- **THEN** detection yields an empty `release_type`, the `tag-release` job is skipped, and no tag is pushed

#### Scenario: Tag push path does not run on pull requests
- **WHEN** the event is a pull request rather than a push to `main`
- **THEN** the detect and `tag-release` jobs do not run, so the App private key is never in scope for a fork

#### Scenario: Release git tag already exists
- **WHEN** the computed release tag already exists in the repository
- **THEN** the tag push fails with a clear error and no new tag is created to trigger a promotion workflow

#### Scenario: GitHub App token generation fails for the tag push
- **WHEN** `actions/create-github-app-token` returns an error in the tag push workflow
- **THEN** the `tag-release` job fails, no tag is pushed, and no promotion workflow runs

## Non-Goals

- Aliasing dev artifacts to the release version — handled by the promotion workflows in `release-tagging`, triggered by the tag this pipeline pushes.
- Multi-architecture builds (`linux/arm64`) — all images target `linux/amd64` only.
- Integration or end-to-end tests as part of the CI suite — only unit tests and static analysis.
- Automated component discovery from workspace manifests — the bake targets and component slugs are enumerated statically.
- Changing the CI behavior for pull requests — PR CI runs lint, build, and test only; no artifact push and no tag push.

## Implementation Notes

- **Dev tag date source**: `ci.yml` derives the date from the commit committer date via `git show -s --format=%cI` on the checked-out commit. The promotion workflows recompute the same date from `gh api repos/{repo}/commits/{sha}`. Both read the committer date of the same commit, so the dev tag matches on both sides.
- **Tag push identity**: the `tag-release` job calls the `push-release-tag.yml` reusable workflow, which mints a GitHub App token and pushes the annotated tag as the release App.
- **Helm OCI target path**: `ghcr.io/<repo-owner>/operational-context-graph`.
- **OCM bundle**: the OCM publish job is a placeholder pending implementation.
