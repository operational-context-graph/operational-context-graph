# ocm-bundle/component-constructor Specification

## Purpose

Defines the structure and observable behavior of `.ocm/component-constructor.yaml`, the hand-authored OCM input file that declares the OCG component version's resources, versions, and access types.

## Requirements

### Requirement: Component constructor declares four image resources and three non-image resources

`.ocm/component-constructor.yaml` SHALL declare a component named `github.com/operational-context-graph/operational-context-graph`. It SHALL contain exactly four `ociImage` resources (`data-ingestion`, `telemetry-store`, `inventory-store`, `inventory-connector`) and three non-image resources: `chart` (type `helmChart`), `bundle-manifest` (type `blob`), and `image-values` (type `blob`). Every version field SHALL use `${VERSION}`.

**Note**: `schema-registry` was removed (no implementation). `postgresql-chart`/`doris-operator-chart` are not bundled (see dependency-bundles spec).

#### Scenario: Constructor rejected when VERSION is unset

- **WHEN** `VERSION` is not set in the environment and `ocm add component-version` runs
- **THEN** the `ocm-build` target has already exited 1 with "VERSION is unset or empty" before this command is reached

#### Scenario: Missing image tarball causes package stage to fail

- **WHEN** `bin/images/inventory-connector.oci.tar` does not exist and `ocm add component-version` runs
- **THEN** OCM exits non-zero naming the missing file; the package stage fails with an error identifying `inventory-connector`

#### Scenario: Preflight checks exactly four service tarballs

- **WHEN** `make preflight` runs
- **THEN** it checks for `bin/images/{data-ingestion,telemetry-store,inventory-store,inventory-connector}.oci.tar` and exits 1 naming any missing service; it does not check for `bin/images/schema-registry.oci.tar`

### Requirement: ocm add component-version runs from repository root with correct flags

`ocm add component-version` SHALL run with `--working-directory . --constructor .ocm/component-constructor.yaml --repository ctf::./bin/ctf/` from the repository root so that all paths resolve correctly. All image resources SHALL use `File/v1` input type with `mediaType: application/vnd.ocm.software.oci.layout.v1+tar`, reading pre-built OCI layout tarballs from `./bin/images/<service>.oci.tar`. The resulting local CTF reference is `ctf::./bin/ctf//<component-name>:${VERSION}`.

#### Scenario: Constructor accepted with all four image resources

- **WHEN** `ocm add component-version .ocm/component-constructor.yaml --working-directory .` runs with `VERSION=1.0.0` and all four `bin/images/<service>.oci.tar` files present
- **THEN** `ocm add component-version` exits 0 and the generated CTF contains seven resources (four images, `chart`, `bundle-manifest`, `image-values`)

### Requirement: Image resource input type is File/v1 with OCI layout media type

Each `ociImage` resource SHALL use input type `File/v1` with `mediaType: application/vnd.ocm.software.oci.layout.v1+tar`. This embeds the pre-built OCI layout tarball directly into the CTF as a `LocalBlob/v1` access entry.

#### Scenario: Image resource appears as LocalBlob/v1 in generated descriptor

- **WHEN** `ocm add component-version` processes `data-ingestion` with a valid OCI layout tarball
- **THEN** the generated component descriptor contains `access.type: LocalBlob/v1` and `access.localReference: sha256:<digest>` for `data-ingestion`; no external registry reference appears

#### Scenario: OCI layout tarball with more than one index entry is rejected

- **WHEN** `bin/images/data-ingestion.oci.tar` contains an `index.json` with two entries
- **THEN** the preflight stage exits 1 naming `data-ingestion` and "expected exactly 1 index entry, found 2"

### Requirement: chart resource uses Helm/v1 input from a pre-packaged chart tarball

The `chart` resource SHALL use input type `Helm/v1` with `path: ./bin/operational-context-graph-${VERSION}.tgz`. The Makefile `build` stage SHALL package the top-level chart with `helm package helm/ -d bin/ --version ${VERSION}` before the `package` stage runs `ocm add component-version`.

#### Scenario: chart resource packaged from pre-built tgz

- **WHEN** `bin/operational-context-graph-1.0.0.tgz` exists and `ocm add component-version` processes the `chart` resource
- **THEN** the CTF contains a `helmChart` resource with a `LocalBlob/v1` access entry; `ocm get resources` reports `chart` with type `helmChart`

#### Scenario: chart tgz absent causes package stage to fail

- **WHEN** `bin/operational-context-graph-1.0.0.tgz` does not exist when `ocm add component-version` runs
- **THEN** OCM exits non-zero; the build stage has already failed at `helm package` before this is reached

### Requirement: bundle-manifest and image-values are blob resources with File/v1 input

`bundle-manifest` SHALL have type `blob` with `mediaType: application/yaml` and `File/v1` input reading `./bin/bundle-manifest.yaml`. `image-values` SHALL have type `blob` with `mediaType: application/yaml` and `File/v1` input reading `./bin/image-values.yaml`.

#### Scenario: bundle-manifest appears as blob in descriptor

- **WHEN** `ocm add component-version` processes `bundle-manifest`
- **THEN** the descriptor records `type: blob` and `access.type: LocalBlob/v1` for `bundle-manifest`; `ocm get resources` does not report it as `ociImage` or `helmChart`

### Requirement: Constructor uses ${VERSION} in every version field, no hardcoded version

Every `version:` field in `.ocm/component-constructor.yaml` SHALL contain `${VERSION}` and nothing else. No version string SHALL be hardcoded.

#### Scenario: All version fields use ${VERSION}

- **WHEN** `grep -c '\${VERSION}' .ocm/component-constructor.yaml` runs
- **THEN** the count equals the number of version fields in the file (one per resource plus the component-level version); no `grep '[0-9]\+\.[0-9]\+\.[0-9]\+'` match appears in the file

## Non-Goals

- Hand-editing the generated component descriptor in `bin/ctf/`
- Multiple component versions in one constructor file
- External (non-local) resource access types in the constructor
