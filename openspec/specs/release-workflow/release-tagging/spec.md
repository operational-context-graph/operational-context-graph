# Release Tagging Specification

## Purpose

Defines the behavior of `release-component.yml` and `release-system.yml`: two separate workflows that fire after CI succeeds on a push to `main`. Each handles one release type only, with permissions scoped to that type's needs. `release.yml` is deleted.

## Requirements

### Requirement: Each release workflow is scoped to one release type with minimal permissions

`release-component.yml` SHALL declare only `contents: read` at the workflow level (individual jobs acquire `contents: write` via the shared `push-release-tag.yml` reusable workflow and `packages: write` for image aliasing). `release-system.yml` SHALL declare `contents: read` and `packages: read` at the workflow level; the `alias-release-artifacts` job acquires `packages: write`. Neither workflow SHALL declare permissions beyond what its release type requires at the workflow level.

#### Scenario: Component release workflow has no workflow-level registry write permissions
- **WHEN** a code reviewer inspects `release-component.yml`
- **THEN** `packages: write` does not appear at the workflow level; only the `alias-component-image` job declares it

#### Scenario: System release workflow has no workflow-level registry write permissions
- **WHEN** a code reviewer inspects `release-system.yml`
- **THEN** `packages: write` does not appear at the workflow level; only the `alias-release-artifacts` job declares it

---

### Requirement: Release type is detected from the merge source branch

Both `release-component.yml` and `release-system.yml` SHALL detect the release type by resolving the source branch of the commit that triggered the CI workflow and parsing it with `scripts/parse-release-branch.sh`. Each workflow skips all downstream jobs when the detected type does not match its own type.

#### Scenario: Component release workflow fires on non-release main push
- **WHEN** a non-release branch is merged to `main`
- **THEN** `release-component.yml` detects `release_type` as empty and all jobs are skipped; the workflow exits successfully

#### Scenario: System release workflow fires on component release merge
- **WHEN** a `release/<slug>/<version>` branch is merged to `main`
- **THEN** `release-system.yml` detects `release_type` as `component`; all its jobs are skipped; the workflow exits successfully

#### Scenario: Component release workflow fires on component release merge
- **WHEN** the merged source branch is `release/<slug>/<version>`
- **THEN** `release-component.yml` detects `release_type` as `component` and proceeds

#### Scenario: System release workflow fires on system release merge
- **WHEN** the merged source branch is `release/<version>`
- **THEN** `release-system.yml` detects `release_type` as `system` and proceeds

#### Scenario: GitHub API call to resolve source branch fails
- **WHEN** the `gh api` call to resolve the PR for the commit SHA returns an error or empty result
- **THEN** the detect job fails with a logged error; no tags are pushed

#### Scenario: Merge commit has no associated PR
- **WHEN** the commit has no associated pull request in the GitHub API response
- **THEN** source branch resolves to empty; `release_type` is empty; all jobs in both workflows are skipped

---

### Requirement: Component release pushes a git tag and aliases the dev image to the release version

On a component release, `release-component.yml` SHALL push an annotated git tag of the form `<component-path>/v<version>`, then alias the dev image in GHCR to `<version>` and `<version>-<sha7>` using `crane tag` — without rebuilding the image. It SHALL NOT create a GitHub Release.

#### Scenario: Git tag pushed and image aliased for component release
- **WHEN** `release_type` is `component` for `services/data-ingestion` version `1.2.3`, dev tag is `dev.20260315-abc1234`
- **THEN** annotated tag `services/data-ingestion/v1.2.3` is pushed to the repository
- **AND** `ghcr.io/operational-context-graph/data-ingestion:1.2.3` and `ghcr.io/operational-context-graph/data-ingestion:1.2.3-abc1234` are pushed as aliases
- **AND** no GitHub Release is created

#### Scenario: Component has no Dockerfile — tag pushed, no alias
- **WHEN** `release_type` is `component` and `has_dockerfile` is `false`
- **THEN** the git tag is pushed
- **AND** the `alias-component-image` job is skipped; no image tags are created

#### Scenario: Git tag already exists for component release
- **WHEN** tag `services/data-ingestion/v1.2.3` already exists before the tag job runs
- **THEN** the tag push fails with a clear error; no alias tags are pushed; no GitHub Release is created

#### Scenario: GitHub App token generation fails for tag push
- **WHEN** `actions/create-github-app-token` returns an error
- **THEN** the tag push job fails; no tag is pushed; no alias runs; no GitHub Release is created

---

### Requirement: System release pushes a git tag, aliases artifacts, creates a GitHub Release, and attaches the versioned Helm chart

On a system release, `release-system.yml` SHALL push an annotated git tag `v<version>`, alias the dev Helm chart OCI artifact to `<version>` using `crane tag`, pull the versioned Helm chart, and create a GitHub Release with the Helm chart `.tgz` attached.

#### Scenario: System release creates GitHub Release with versioned Helm chart
- **WHEN** `release_type` is `system` for version `1.0.0`, dev tag is `dev.20260315-abc1234`
- **THEN** annotated tag `v1.0.0` is pushed to the repository
- **AND** `ghcr.io/operational-context-graph/operational-context-graph:1.0.0` is aliased from the dev tag via `crane tag`
- **AND** the Helm chart is pulled from `ghcr.io/operational-context-graph/operational-context-graph:1.0.0`
- **AND** a GitHub Release titled `system v1.0.0` is created with the `.tgz` artifact attached

#### Scenario: System release GitHub Release already exists
- **WHEN** a GitHub Release for tag `v1.0.0` already exists
- **THEN** the release creation step fails with a clear error; the git tag is not pushed again

#### Scenario: Helm chart OCI alias fails
- **WHEN** `crane tag` for the Helm chart returns non-zero (e.g. dev tag does not exist)
- **THEN** the `alias-release-artifacts` job fails; `pull-helm-chart` is blocked; the GitHub Release is not created

#### Scenario: Helm chart pull succeeds but `.tgz` export fails
- **WHEN** `helm pull` exits non-zero or produces no `.tgz` file
- **THEN** the Helm pull step fails; the GitHub Release creation step is skipped

#### Scenario: GitHub Release creation fails (API error)
- **WHEN** the GitHub API returns an error on release creation
- **THEN** the step fails; the git tag is already pushed; re-running the workflow MUST not attempt to re-push the tag (the tag push step will fail gracefully on the re-run)

#### Scenario: System git tag already exists
- **WHEN** tag `v1.0.0` already exists before the tag job runs
- **THEN** the tag push fails with a clear error; the alias and GitHub Release creation steps are blocked

---

### Requirement: No artifacts are built during release

Neither `release-component.yml` nor `release-system.yml` SHALL build container images, compile code, or package Helm charts.

#### Scenario: Release workflows contain no build steps
- **WHEN** a code reviewer inspects `release-component.yml` and `release-system.yml`
- **THEN** no `docker build`, `go build`, `helm package`, or equivalent build commands appear in either file

---

### Requirement: Component releases do not create GitHub Releases

`release-component.yml` SHALL NOT create a GitHub Release.

#### Scenario: Component release produces no GitHub Release
- **WHEN** `release_type` is `component`
- **THEN** no `gh release create` or equivalent step runs in `release-component.yml`
- **AND** the GitHub Releases page shows no new entry after the workflow completes

## Non-Goals

- OCM bundle creation or attachment — handled by a separate workflow (placeholder exists).
- SBOM generation or attestation at release time — generated at build time.
- Automated rollback of git tags on downstream failure — git tags are permanent once pushed.
- Eliminating the two skipped workflow runs on non-release main pushes — accepted trade-off for permission isolation.
