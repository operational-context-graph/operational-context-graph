# Component Release PR Specification

## Purpose

Defines the behavior of `release-component-pr.yml`: the workflow that creates a release PR for a single component, generating the changelog so the component's release history is captured before the release branch is merged.

## Requirements

### Requirement: Component release PR contains only changelog changes

`release-component-pr.yml` SHALL create a release branch containing only the component's `CHANGELOG.md` update. It SHALL NOT modify any `helm/charts/*/values.yaml` files and SHALL NOT call any version resolution scripts.

#### Scenario: Component release PR created with changelog only
- **WHEN** `release-component-pr.yml` is dispatched for `services/data-ingestion` version `1.2.3`
- **THEN** the release branch contains only a changelog commit for `services/data-ingestion/CHANGELOG.md`
- **AND** no `helm/charts/*/values.yaml` file is modified

#### Scenario: Component path under gen/ is rejected
- **WHEN** `release-component-pr.yml` is dispatched with a component path under `gen/`
- **THEN** the workflow fails with a clear error before creating any branch
- **AND** no PR is opened

#### Scenario: Component path does not exist
- **WHEN** the supplied component path has no directory in the repository
- **THEN** the validation step fails with a clear error; no branch or PR is created

#### Scenario: Component has no recognizable module manifest
- **WHEN** the component directory exists but contains no `go.mod`, `package.json`, or `pyproject.toml`
- **THEN** the validation step fails with a clear error; no branch or PR is created

#### Scenario: Release tag already exists
- **WHEN** a git tag for `<component-path>/v<version>` already exists
- **THEN** the validation step fails with a clear error; no branch or PR is created

---

### Requirement: System release PR bumps subchart image tags and contains changelog changes

`release-system-pr.yml` SHALL accept optional per-component version override inputs. For each OCG-owned subchart, it SHALL resolve the version (from the override or the latest released git tag for that component) and update `helm/charts/<slug>/values.yaml` `.image.tag` before committing the root `CHANGELOG.md` update.

#### Scenario: System release PR bumps all subchart versions from overrides
- **WHEN** `release-system-pr.yml` is dispatched for version `1.0.0` with all version overrides supplied
- **THEN** each subchart `values.yaml` `.image.tag` is set to the supplied version
- **AND** the release branch contains a commit for the values changes followed by a changelog commit

#### Scenario: System release PR resolves latest released version when override is empty
- **WHEN** `release-system-pr.yml` is dispatched with an empty override for `data-ingestion`
- **THEN** the workflow resolves the latest `services/data-ingestion/v*` git tag by semver order
- **AND** sets `helm/charts/data-ingestion/values.yaml` `.image.tag` to that version

#### Scenario: No released version found and no override supplied
- **WHEN** `release-system-pr.yml` is dispatched with an empty override for a component that has no `<path>/v*` git tags
- **THEN** the bump step fails with a clear error identifying the component
- **AND** no commit is pushed

#### Scenario: Subchart chart directory does not exist
- **WHEN** the expected `helm/charts/<slug>/` directory is missing
- **THEN** the bump step fails with a clear error identifying the missing directory before committing

#### Scenario: values.yaml does not contain .image.tag
- **WHEN** `helm/charts/<slug>/values.yaml` exists but has no `.image.tag` field
- **THEN** the yq patch step fails with a non-zero exit code before any commit is pushed

#### Scenario: Subchart image tag already at target version
- **WHEN** the current `.image.tag` in `values.yaml` equals the resolved version
- **THEN** no change is made for that subchart; no commit entry is produced for it

## Non-Goals

- Validating that all components have been released before the system release PR is opened.
- Automated detection of which components have changed since the last release.
- Changing the PR template or review process.
- Bumping subchart image tags in the component release PR — that responsibility belongs to the system release PR.
