# ocm-bundle/ci-integration Specification

## Purpose

Defines the observable behavior of the CI/CD pipeline extension that runs the OCM build pipeline, gates publishing on verification, and ensures the deployment step checks resource integrity before applying the bundle.

## Requirements

### Requirement: Dev CI builds OCM bundle via make ocm-build

The dev CI workflow SHALL build and sign the OCM bundle using `VERSION="${VERSION}" SIGN_KEY=/tmp/ocg-signing-key.pem make ocm-build` with a dev version string (`0.0.0-dev.YYYYMMDD-SHA`). The `ocm-build` target does NOT enforce git-tag or clean-tree guards — these are reserved for `ocm-release-preflight`. The dev build runs on every push to `main`.

#### Scenario: Dev CI builds and publishes bundle on push to main

- **WHEN** a push to `main` triggers the CI pipeline and all prior jobs succeed
- **THEN** `publish-ocm-bundle` runs; `make ocm-build` builds and signs the CTF at version `0.0.0-dev.YYYYMMDD-SHA`; `ocm transfer` pushes to `oci::${REGISTRY}/ocm`

#### Scenario: All six stages succeed end to end

- **WHEN** the CI pipeline runs with a valid `VERSION`, all four service images built (`data-ingestion`, `telemetry-store`, `inventory-store`, `inventory-connector`), and the signing key secret populated
- **THEN** all six stages complete with exit 0 and `bin/ocg-<version>.ctf.tgz` is produced as the pipeline artifact

#### Scenario: Package stage fails (corrupt OCI tarball)

- **WHEN** `bin/images/telemetry-store.oci.tar` is corrupt and the package stage detects it
- **THEN** the CI pipeline fails at the package stage; inspect, sign, and verify do not run; no CTF artifact is uploaded

### Requirement: Release workflow re-versions dev CTF via ocm-reversion.sh

The release workflow SHALL NOT rebuild the OCM bundle from source. Instead it SHALL:

1. Resolve the dev bundle version from the merge commit SHA: `0.0.0-dev.YYYYMMDD-SHA`
2. Run `make ocm-release-preflight` to verify HEAD is tagged `v${VERSION}` and the working tree is clean
3. Run `scripts/ocm-reversion.sh "${DEV_VERSION}" "${VERSION}" "${REGISTRY}"` which fetches the dev CTF, verifies its signature, extracts resources, builds a new CTF at `${VERSION}` preserving resource versions, signs, and verifies
4. Push the release CTF to `oci::${REGISTRY}/operational-context-graph`

Only the component version changes. Resource versions (image tags) are preserved from the dev bundle.

#### Scenario: Release re-versions dev CTF successfully

- **WHEN** `scripts/ocm-reversion.sh "0.0.0-dev.20260118-abc1234" "1.2.3" "ghcr.io/my-org"` runs with the dev CTF present in GHCR
- **THEN** all six stages complete; `bin/ctf/` contains component version `1.2.3`; resource versions match those in the dev descriptor; `bin/ocg-1.2.3.ctf.tgz` is produced

#### Scenario: Release preflight fails on untagged HEAD

- **WHEN** `make ocm-release-preflight VERSION=1.2.3` runs and HEAD is not tagged `v1.2.3`
- **THEN** `ocm-release-preflight` exits 1 naming the missing tag; `ocm-reversion.sh` does not run

#### Scenario: Dev CTF signature verification fails

- **WHEN** the dev CTF has a tampered signature or was not signed
- **THEN** `ocm-reversion.sh` exits non-zero at the verify-source stage; no release CTF is produced

### Requirement: ocm-release-preflight target gates release-only checks

A dedicated `ocm-release-preflight` Makefile target SHALL enforce release-specific guards separately from `ocm-build`:

1. `VERSION` is set and non-empty
2. HEAD is tagged exactly `v${VERSION}` (strict match)
3. Git working tree is clean

`ocm-build` SHALL NOT contain these guards — it SHALL accept any `VERSION` from the caller.

#### Scenario: ocm-release-preflight passes on tagged clean HEAD

- **WHEN** HEAD is tagged `v1.2.3`, working tree is clean, and `VERSION=1.2.3 make ocm-release-preflight` runs
- **THEN** exits 0 with message `ocm-release-preflight: OK (VERSION=1.2.3, HEAD tagged v1.2.3, tree clean)`

#### Scenario: ocm-build accepts dev version without git tag

- **WHEN** `VERSION=0.0.0-dev.20260118-abc1234 SIGN_KEY=/tmp/key.pem make ocm-build` runs on an untagged HEAD
- **THEN** `ocm-build` proceeds without a tag check; `scripts/ocm-bundle.sh` runs with the dev version

### Requirement: CI writes private key from secret to a path outside the checkout

The CI workflow SHALL write the OCM signing private key from a CI secret to a filesystem path that is NOT inside the repository checkout directory. The path SHALL be passed to the `sign` stage as a Make variable or environment variable.

#### Scenario: Private key injected from CI secret

- **WHEN** the CI secret `OCM_SIGNING_KEY` is set and the workflow writes it to `/tmp/ocg-signing-key.pem`
- **THEN** preflight succeeds; the sign stage proceeds; the key does not appear in any committed file, log, or artifact

#### Scenario: CI secret is not set (secret absent)

- **WHEN** the CI secret `OCM_SIGNING_KEY` is unset or empty
- **THEN** the key file written to `/tmp/` is empty (zero bytes); the sign stage detects an empty key file and fails with exit code 1 before invoking `ocm sign`

#### Scenario: Key file written inside checkout by mistake

- **WHEN** the workflow accidentally writes the key to a path inside the repository directory
- **THEN** this is a misconfiguration; the spec requires the path to be outside the checkout

### Requirement: CI publish gate re-verifies component version in registry after transfer

After `ocm transfer` pushes the CTF to the registry, the CI workflow SHALL run `ocm verify component-version --signature ocg-sig <registry-ref>` against the registry copy. If verify exits non-zero, the publish step fails.

#### Scenario: Transfer succeeds and registry verify passes

- **WHEN** `ocm transfer` exits 0 and the registry copy is intact
- **THEN** `ocm verify component-version --signature ocg-sig <registry-ref>` exits 0; the publish step succeeds

#### Scenario: Transfer produces corrupt registry copy

- **WHEN** the registry copy has a mismatching digest after transfer
- **THEN** `ocm verify` exits non-zero; the CI publish step fails with the verify error

### Requirement: Deployment pre-install check validates access.localReference per resource

Before running `helm install` or `helm upgrade`, the deployment step SHALL check every resource in the OCM bundle: `access.localReference` MUST equal `sha256:` concatenated with the resource's `digest.value`. Any mismatch SHALL fail the deployment before Helm runs.

#### Scenario: All resources pass localReference check

- **WHEN** every OCM resource has `access.localReference == "sha256:" + digest.value`
- **THEN** the pre-install check exits 0 and `helm install` runs

#### Scenario: One resource has a mismatching localReference

- **WHEN** the `data-ingestion` resource has `access.localReference: sha256:aaa` but `digest.value: bbb`
- **THEN** the pre-install check exits non-zero, names `data-ingestion` as the mismatching resource with both values, and `helm install` does not run

#### Scenario: image-values.yaml still contains unexpanded IMAGE_REPOSITORY placeholder

- **WHEN** `${IMAGE_REPOSITORY}` was not replaced before the pre-install check runs
- **THEN** the check fails with a message indicating the placeholder was not substituted; `helm install` does not run

### Requirement: CI installs OCM CLI v2 at the required version

The CI workflow SHALL install `ocm` CLI from the `open-component-model/open-component-model` repository (OCM v2) at version `>= 0.22.1` before running any OCM stage. Both `publish-ocm-bundle` and `ocm-bundle` jobs SHALL install the CLI independently with the same pinned version and SHA256.

**Note**: OCM v1 (`open-component-model/ocm`, versions 0.16–0.53) MUST NOT be used.

#### Scenario: OCM CLI v2 installed at correct version

- **WHEN** the install step downloads and verifies OCM CLI v2 at `>= 0.22.1`
- **THEN** `ocm version` exits 0 and reports a v2 version `>= 0.22.1`; the preflight stage can proceed

#### Scenario: OCM CLI download fails

- **WHEN** the binary download URL returns a non-200 status or checksum verification fails
- **THEN** the install step fails with exit code 1; no subsequent OCM stage runs

### Requirement: ocm-reversion.sh stages: fetch → verify-source → extract → build → sign → verify

`scripts/ocm-reversion.sh` SHALL run six sequential stages and fail fast at any error. Only the component version changes; resource versions are preserved from the dev descriptor.

#### Scenario: ocm-reversion.sh labels carried forward

- **WHEN** the dev descriptor has non-empty `component.labels`
- **THEN** the script carries them forward to the release CTF via `yq -i '.components[0].labels = (strenv(LABELS_JSON) | fromjson)'`

### Requirement: OCM bundle published to ocm OCI base path

Both dev CI and release jobs SHALL publish the OCM CTF to `oci::${REGISTRY}/ocm`. The OCM CLI automatically appends `/component-descriptors/<component-name>:<version>` under that base. The Helm chart publishes separately to `oci://${REGISTRY}/charts`, landing at `ghcr.io/<org>/charts/operational-context-graph:<version>`.

#### Scenario: OCM bundle push path

- **WHEN** `ocm transfer component-version ctf::./bin/ctf// "oci::${REGISTRY}/ocm"` runs
- **THEN** the CTF is accessible at `ghcr.io/<org>/ocm/component-descriptors/github.com/operational-context-graph/operational-context-graph:<version>` in GHCR

#### Scenario: Helm chart push path is distinct

- **WHEN** `helm push operational-context-graph-${VERSION}.tgz oci://${REGISTRY}/charts` runs
- **THEN** the chart is accessible at `ghcr.io/<org>/charts/operational-context-graph:<version>`; it does not collide with the OCM bundle path

## Non-Goals

- Automated key rotation or key re-signing of existing CTF archives
- Multi-registry publishing in a single pipeline run
- GitOps / pull-request-based deployment reconciliation
