# ocm-bundle/build-pipeline Specification

## Purpose

Defines the observable behavior of the six-stage Makefile build pipeline that assembles, signs, and verifies the OCG OCM bundle — from preflight validation through Route B archive creation.

## Requirements

### Requirement: Build pipeline runs as six ordered, sequential stages

The Makefile SHALL implement six targets in dependency order: `preflight`, `build`, `package`, `inspect`, `sign`, `verify`. Each target SHALL depend on the previous. A failure in any stage SHALL stop the chain. No stage SHALL produce a shippable CTF without `sign` having run.

#### Scenario: All stages complete successfully

- **WHEN** `VERSION=1.0.0 make verify` runs with all preconditions met
- **THEN** each stage runs in order (preflight → build → package → inspect → sign → verify), each exits 0, and `bin/ocg-1.0.0.ctf.tgz` exists at the end

#### Scenario: A middle stage fails

- **WHEN** `inspect` exits non-zero (digest mismatch detected)
- **THEN** `sign` and `verify` do not run; Make exits with the stage's exit code; no signed CTF is produced

#### Scenario: Parallel make invocation attempted

- **WHEN** `make -j2 sign` is run (parallel jobs enabled)
- **THEN** the pipeline executes sequentially; `.NOTPARALLEL` or equivalent prevents concurrent stage execution; no `bin/ctf/` corruption occurs

### Requirement: ocm-build requires VERSION from caller; no git-tag or clean-tree guard

The `ocm-build` Makefile target SHALL require `VERSION` to be non-empty but SHALL NOT check git tags or working-tree cleanliness. Any `VERSION` value is accepted (including `0.0.0-dev.DATE-SHA`). This allows `ocm-build` to serve both the dev CI job (untagged HEAD) and the release workflow (where `ocm-release-preflight` already ran).

#### Scenario: ocm-build with dev version succeeds

- **WHEN** `VERSION=0.0.0-dev.20260118-abc1234 SIGN_KEY=/tmp/key.pem make ocm-build` runs on an untagged HEAD
- **THEN** `ocm-build` proceeds; `scripts/ocm-bundle.sh "0.0.0-dev.20260118-abc1234"` runs

#### Scenario: ocm-build without VERSION fails

- **WHEN** `make ocm-build` runs without `VERSION` set
- **THEN** exits 1 with "ERROR: VERSION is required"

### Requirement: Preflight validates VERSION, keys, working tree, and OCI tarballs

The `preflight` stage within `scripts/ocm-bundle.sh` SHALL fail fast before any build work. It SHALL verify: `ocm` CLI present and version `>= 0.22.1` (v2 codeline; v1 rejected); `VERSION` non-empty matching `^[0-9]+\.[0-9]+\.[0-9]+`; `.ocm/ocg.pub.pem` exists and non-zero; private key at `SIGN_KEY` exists and non-zero. Git working-tree and tag checks are NOT included — those are in `ocm-release-preflight`.

#### Scenario: Dirty working tree does NOT fail ocm-build preflight

- **WHEN** uncommitted files exist and `make ocm-build VERSION=0.0.0-dev.DATE-SHA` runs
- **THEN** `scripts/ocm-bundle.sh` preflight does not check the working tree; it proceeds past preflight

#### Scenario: Private key absent fails preflight

- **WHEN** the file at `SIGN_KEY` path does not exist when `make ocm-build` runs
- **THEN** preflight exits 1 naming the missing key path; no subsequent stage runs

#### Scenario: VERSION unset or empty fails preflight

- **WHEN** `VERSION` is empty
- **THEN** `ocm-build` exits 1 before calling the script: "ERROR: VERSION is required"

### Requirement: Preflight validates each service OCI tarball

The `preflight` stage SHALL verify each `bin/images/<service>.oci.tar` exists, is non-zero, and has exactly one entry in `index.json`. Four services: `data-ingestion`, `telemetry-store`, `inventory-store`, `inventory-connector`. Any missing or malformed tarball SHALL cause preflight to exit 1 naming the failing service.

#### Scenario: Missing image tarball fails preflight

- **WHEN** `bin/images/inventory-connector.oci.tar` does not exist and `make preflight` runs
- **THEN** preflight exits 1 naming `inventory-connector` and "missing or empty"; no subsequent stage runs

#### Scenario: Tarball with multiple index entries fails preflight

- **WHEN** `bin/images/data-ingestion.oci.tar` contains an `index.json` with two entries
- **THEN** preflight exits 1 naming `data-ingestion` and "expected exactly 1 index entry"

### Requirement: Package stage performs unconditional CTF directory reset

The `package` target SHALL unconditionally delete `bin/ctf/` before running `ocm add component-version`, regardless of whether `bin/ctf/` exists. The full invocation is:

```
VERSION=${VERSION} ocm add component-version \
  --working-directory . \
  --constructor .ocm/component-constructor.yaml \
  --repository ctf::./bin/ctf/
```

The local CTF is then referenced as `ctf::./bin/ctf//<component-name>:${VERSION}` in subsequent sign, verify, and publish commands.

#### Scenario: bin/ctf/ exists from a prior run

- **WHEN** `bin/ctf/` exists from a previous package invocation and `make package` runs
- **THEN** `bin/ctf/` is deleted before `ocm add component-version` runs; no stale blobs from the prior run remain

#### Scenario: bin/ctf/ does not exist

- **WHEN** `bin/ctf/` does not exist and `make package` runs
- **THEN** the delete step is a no-op (not an error); `ocm add component-version` proceeds normally

#### Scenario: Package stage is interrupted mid-run

- **WHEN** `ocm add component-version` is sent SIGINT mid-execution
- **THEN** the next `make package` invocation still deletes `bin/ctf/` unconditionally before retrying; no partial state persists

### Requirement: Build stage packages the Helm chart before OCM ingestion

The `build` target SHALL run `helm package helm/ -d bin/ --version ${VERSION}` to produce `bin/operational-context-graph-${VERSION}.tgz` before the `package` stage runs `ocm add component-version`. The chart tgz must exist at this path when OCM processes the `chart` resource.

#### Scenario: helm package produces chart tgz

- **WHEN** `VERSION=1.0.0 make build` runs and `helm/` chart is valid
- **THEN** `bin/operational-context-graph-1.0.0.tgz` exists and `ocm add component-version` can read it in the `package` stage

#### Scenario: helm package fails (missing dependency)

- **WHEN** `helm/charts/` dependencies are not updated and `helm package` runs
- **THEN** `make build` exits non-zero; the `package` stage does not run

### Requirement: Bundle manifest records version, commit, and per-service digest

The generated `bin/bundle-manifest.yaml` SHALL contain: `version` (the `${VERSION}` value), `sourceCommit` (the Git commit SHA at build time), and one `imageDigest` entry per service (service name → `sha256:<hex>`). Four services: `data-ingestion`, `telemetry-store`, `inventory-store`, `inventory-connector`.

#### Scenario: Bundle manifest generated with correct content

- **WHEN** `make build` completes for `VERSION=2.0.0` on commit `abc1234`
- **THEN** `bin/bundle-manifest.yaml` contains `version: "2.0.0"`, `sourceCommit: "abc1234..."`, and a digest entry for each of the four services

#### Scenario: Inspect stage detects a digest mismatch

- **WHEN** the digest in `bin/bundle-manifest.yaml` for `data-ingestion` does not match the digest of the corresponding resource in `bin/ctf/`
- **THEN** the inspect stage exits non-zero, prints a message naming `data-ingestion` as the mismatching resource, and the sign stage does not run

#### Scenario: Inspect stage passes with all digests matching

- **WHEN** every resource digest in `bin/bundle-manifest.yaml` matches the corresponding resource in the OCM descriptor
- **THEN** the inspect stage exits 0

### Requirement: image-values.yaml is generated with unexpanded IMAGE_REPOSITORY placeholder

`bin/image-values.yaml` SHALL be generated from the OCM descriptor's resource digests. The registry/repository prefix SHALL appear as the literal string `${IMAGE_REPOSITORY}` — not expanded from the environment.

#### Scenario: image-values.yaml contains literal placeholder

- **WHEN** `make build` runs with `IMAGE_REPOSITORY` set in the environment
- **THEN** `bin/image-values.yaml` contains the literal text `${IMAGE_REPOSITORY}`, not the value of the environment variable

#### Scenario: image-values.yaml contains correct digests

- **WHEN** `make build` completes
- **THEN** each service entry in `bin/image-values.yaml` contains the same `sha256:` digest as recorded in `bin/bundle-manifest.yaml` for that service

### Requirement: Sign stage invokes ocm sign with --signature flag; OCM config auto-discovered

The `sign` target SHALL invoke `ocm sign component-version --signature ocg-sig <CTF_REF>`. OCM v2 CLI auto-discovers `.ocm/config` from the working directory. No `--config`, `--key`, or `--public-key` flags are passed — all credentials are declared in `.ocm/config`. Preflight has already validated the working tree, key paths, and public key.

#### Scenario: Sign exits 0 on valid CTF

- **WHEN** preflight passed, `bin/ctf/` contains the OCG component, and `SIGN_KEY` is valid
- **THEN** `ocm sign component-version --signature ocg-sig ctf::./bin/ctf//<component>:<version>` exits 0; the CTF contains a signature named `ocg-sig`

### Requirement: Verify stage invokes ocm verify with --signature flag; OCM config auto-discovered

The `verify` target SHALL invoke `ocm verify component-version --signature ocg-sig <CTF_REF>`. Same `.ocm/config` auto-discovery as sign. No `--config` or `--public-key` flags.

#### Scenario: ocm verify passes on a correctly signed CTF

- **WHEN** the CTF is signed with `ocg-sig` and `.ocm/ocg.pub.pem` is valid
- **THEN** `ocm verify` exits 0

#### Scenario: ocm verify fails on a tampered CTF

- **WHEN** a blob in `bin/ctf/` is modified after signing
- **THEN** `ocm verify` exits non-zero; the verify stage propagates the exit code

### Requirement: OCM config is a single committed file at `.ocm/config`

`.ocm/config` SHALL be a single YAML file committed to the repository. It SHALL contain signing config (`signing.config.ocm.software/v1alpha1` with nested `signer:` block, RSASSA-PSS), credentials config (`credentials.config.ocm.software` consumer with `type: RSA/v1alpha1`), and transfer config (`transfer.config.ocm.software/v1alpha1` with `recursive: -1`). OCM v2 auto-discovers `.ocm/config` from `$PWD`.

#### Scenario: .ocm/config is tracked by git

- **WHEN** `git ls-files .ocm/config` runs
- **THEN** the file is listed; it is committed to the repository

### Requirement: OCM config contains no key material

`.ocm/config` SHALL reference credentials by PATH only. The private key field SHALL contain the path to the key file (e.g. `private_key_pem_file: /tmp/...`), not the PEM key material itself. The file SHALL be safe to commit to the repository.

#### Scenario: .ocm/config committed and contains no key material

- **WHEN** `git show HEAD:.ocm/config` is run
- **THEN** the file contains `private_key_pem_file: /tmp/...` (a path), not PEM key material; `grep -E 'BEGIN.*KEY' .ocm/config` returns no match
### Requirement: Route B archive structure

The verify stage SHALL produce `bin/ocg-<version>.ctf.tgz` by archiving the CTF directory. The archive root SHALL contain the CTF directory name (e.g. `bin/ocg-1.0.0.ctf.tgz` extracts to a named subdirectory with `artifact-index.json` inside). OCM SHALL be able to consume the archive with `ocm get component-version --file bin/ocg-<version>.ctf.tgz`.

#### Scenario: Archive is readable by OCM

- **WHEN** `ocm get component-version --file bin/ocg-1.0.0.ctf.tgz` runs
- **THEN** the command exits 0 and reports the component version

## Non-Goals

- Multi-platform build matrix beyond linux/amd64 and linux/arm64
- Image vulnerability scanning as a pipeline stage
- GitOps / reconciliation-loop deployment
- Key rotation or key versioning scheme
