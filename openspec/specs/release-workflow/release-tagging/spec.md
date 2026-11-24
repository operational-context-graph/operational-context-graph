# Release Tagging Specification

## Purpose

Defines the behavior of `release-component.yml` and `release-system.yml`: two separate promotion workflows that fire on a release git tag push. The tag itself is pushed by the `tag-release` job in `ci.yml` (see `ci-main-push`) after the dev artifacts are published. Each workflow handles one release type only, reads its intent directly from the tag ref, and has permissions scoped to that type's needs. Neither builds artifacts; each aliases the already-published dev artifact to the release version.

## Requirements

### Requirement: Each release workflow is scoped to one release type with minimal permissions

`release-component.yml` SHALL declare only `contents: read` at the workflow level (individual jobs acquire `packages: write` as needed). `release-system.yml` SHALL declare `contents: read` and `packages: read` at the workflow level. Neither workflow SHALL declare permissions beyond what its release type requires at the workflow level.

#### Scenario: Component release workflow has no workflow-level registry write permissions
- **WHEN** a code reviewer inspects `release-component.yml`
- **THEN** `packages: write` does not appear at the workflow level; only the `retag-component-image` job declares it

#### Scenario: System release workflow has no workflow-level registry write permissions
- **WHEN** a code reviewer inspects `release-system.yml`
- **THEN** `packages: write` does not appear at the workflow level; only the `retag-release-artifacts` job declares it

---

### Requirement: Local reusable workflow references use the $/ self-repository prefix at the top level

Local reusable workflow references SHALL use the `$/` self-repository prefix (e.g., `$/.github/workflows/push-release-tag.yml`). The `$/` form is pinned and passes zizmor's `self-repository` audit; `./` is flagged. Referenced files MUST live directly under `.github/workflows/` — GitHub refuses reusable workflows in subdirectories.

#### Scenario: Local reusable workflow reference uses $/ prefix and top-level path
- **WHEN** a code reviewer inspects `ci.yml` or `release-system.yml`
- **THEN** every `uses:` reference to a workflow in the same repository starts with `$/.github/workflows/`
- **AND** no such reference points into a subdirectory of `.github/workflows/`

---

### Requirement: Release workflows trigger on the pushed release tag and detect type from it

`release-component.yml` SHALL trigger on tag pushes matching `services/**/v*.*.*` and `release-system.yml` SHALL trigger on tag pushes matching `v*.*.*`. Each workflow SHALL read the release type directly from the pushed tag ref using `scripts/parse-release-tag.sh`, with no PR or source-branch reverse lookup, and SHALL skip all downstream jobs when the detected type does not match its own.

#### Scenario: Component tag detected by the component workflow
- **WHEN** tag `services/data-ingestion/v1.2.3` is pushed
- **THEN** `release-component.yml` parses `release_type` `component` and proceeds

#### Scenario: System tag detected by the system workflow
- **WHEN** tag `v1.0.0` is pushed
- **THEN** `release-system.yml` parses `release_type` `system` and proceeds

#### Scenario: Component workflow ignores a non-matching tag type
- **WHEN** a tag reaches `release-component.yml` whose parsed `release_type` is not `component`
- **THEN** all downstream jobs are skipped and the workflow exits successfully

#### Scenario: Tag ref is not a valid release tag
- **WHEN** the pushed tag does not parse to a known release type
- **THEN** `parse-release-tag.sh` yields an empty `release_type` and all downstream jobs are skipped

---

### Requirement: Component release aliases the dev image to the release version

On a component release, `release-component.yml` SHALL alias the dev image in GHCR to `<version>` and `<version>-<sha7>` using `crane tag`, without rebuilding the image. It SHALL NOT push a git tag (the triggering tag already exists) and SHALL NOT create a GitHub Release.

#### Scenario: Dev image aliased for component release
- **WHEN** tag `services/data-ingestion/v1.2.3` triggers the workflow and the dev tag for the tagged commit is `dev.20260315-abc1234`
- **THEN** `ghcr.io/operational-context-graph/data-ingestion:1.2.3` and `ghcr.io/operational-context-graph/data-ingestion:1.2.3-abc1234` are pushed as aliases of the dev image
- **AND** no image layer is built and no GitHub Release is created

#### Scenario: Component has no Dockerfile — no alias
- **WHEN** the parsed tag has `has_dockerfile` `false`
- **THEN** the `retag-component-image` job is skipped and no image tags are created

#### Scenario: Dev image not present when the alias runs
- **WHEN** `crane tag` cannot find the dev image for the tagged commit
- **THEN** the `retag-component-image` job fails with a clear error and no alias tags are pushed

---

### Requirement: System release aliases the dev chart, attaches it, and creates a GitHub Release

On a system release, `release-system.yml` SHALL alias the dev Helm chart OCI artifact to `<version>` using `crane tag`, pull the versioned chart, and create a GitHub Release titled `system v<version>` with the chart `.tgz` attached. It SHALL NOT push a git tag (the triggering tag already exists) and SHALL NOT repackage or rebuild the chart.

#### Scenario: System release creates GitHub Release with versioned Helm chart
- **WHEN** tag `v1.0.0` triggers the workflow and the dev tag for the tagged commit is `dev.20260315-abc1234`
- **THEN** `ghcr.io/operational-context-graph/operational-context-graph:1.0.0` is aliased from the dev chart via `crane tag`
- **AND** the chart is pulled from `ghcr.io/operational-context-graph/operational-context-graph:1.0.0`
- **AND** a GitHub Release titled `system v1.0.0` is created with the `.tgz` attached

#### Scenario: Helm chart OCI alias fails
- **WHEN** `crane tag` for the Helm chart returns non-zero (e.g. dev tag not found)
- **THEN** the `retag-release-artifacts` job fails, `pull-helm-chart` is blocked, and no GitHub Release is created

#### Scenario: Helm chart pull produces no .tgz
- **WHEN** `helm pull` exits non-zero or produces no `.tgz` file
- **THEN** the pull step fails and GitHub Release creation is skipped

#### Scenario: System GitHub Release already exists
- **WHEN** a GitHub Release for tag `v1.0.0` already exists
- **THEN** the release creation step fails with a clear error

#### Scenario: GitHub Release creation API error
- **WHEN** the GitHub API returns an error on release creation
- **THEN** the step fails; the git tag already exists and is not re-pushed by this workflow; re-running retries only the promotion steps

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

---

### Requirement: System release notes are extracted from CHANGELOG.md by a script

`release-system.yml` SHALL extract the GitHub Release body from the root `CHANGELOG.md` by invoking `scripts/extract-release-notes.sh <version> CHANGELOG.md`. The script exits non-zero with a GitHub Actions error annotation when no matching section is found.

#### Scenario: Release notes extracted successfully
- **WHEN** `release_type` is `system` for version `1.0.0` and `CHANGELOG.md` contains `## [1.0.0] - 2026-03-15`
- **THEN** `scripts/extract-release-notes.sh 1.0.0 CHANGELOG.md` prints the section body and exits 0
- **AND** the GitHub Release description contains the extracted markdown

#### Scenario: CHANGELOG section missing for requested version
- **WHEN** `scripts/extract-release-notes.sh` finds no heading matching the supplied version
- **THEN** the script exits non-zero with `::error::No release notes found for version <version>`
- **AND** the `generate-system-release-notes` job fails; the GitHub Release is not created

---

### Requirement: extract-release-notes.sh supports both CHANGELOG heading formats

`scripts/extract-release-notes.sh` SHALL match headings in either the git-cliff format (`## v<version> (<YYYY-MM-DD>)`) or the system format (`## [<version>] - <YYYY-MM-DD>`). The match is on the version string regardless of brackets or leading `v`. The section body is everything between the matched heading (exclusive) and the next `##` heading (exclusive), with leading and trailing blank lines stripped.

#### Scenario: git-cliff format heading matched
- **WHEN** `CHANGELOG.md` contains `## v1.2.3 (2026-03-15)` and version `1.2.3` is requested
- **THEN** the script extracts the body of that section

#### Scenario: System format heading matched
- **WHEN** `CHANGELOG.md` contains `## [1.0.0] - 2026-03-15` and version `1.0.0` is requested
- **THEN** the script extracts the body of that section

## Non-Goals

- Pushing the release git tag — the tag is pushed by the `tag-release` job in `ci.yml`; these workflows consume it.
- OCM bundle aliasing — pending `publish-ocm-bundle` implementation; the system workflow carries a placeholder.
- SBOM generation or attestation at release time — generated at build time.
- Automated rollback of git tags on downstream failure — git tags are permanent once pushed.

## Implementation Notes

- **Alias mechanism**: `crane tag` re-tags artifacts without rebuild. Crane is installed at runtime from the GitHub releases page at a pinned version (v0.22.1, SHA256 `0ab7a1d6932a213aed964ce97666c3077fe691c8606413674a8b3e0b9ec4cda0`); the installer verifies the checksum before use.
- **Dev tag resolution**: the dev tag consumed by the alias step is recomputed from the tagged commit's committer date via `gh api repos/{repo}/commits/{sha}`, matching the value `ci.yml` used when it published the artifact.
- **GitHub Release identity**: `release-system.yml` creates the GitHub Release through the `create-github-release.yml` reusable workflow, which pushes as the release GitHub App.
