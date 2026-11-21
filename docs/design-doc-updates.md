# OCM Deployment Bundle — Design Document Update

This document records deviations from the OCG design document at
`autonomous-operations/autonomous-operations` (architecture/software-design/operational-context-graph/ocm-deployment-bundle).
Apply these changes to the design document before the OCM bundle change merges to `main`.

## 17.1 OCM CLI Version

**Change**: Update pinned version from `v0.16.x` (v1 legacy, `open-component-model/ocm`) to `>= 0.22.1` (v2, `open-component-model/open-component-model`).

**Detail**:
- v1 and v2 are incompatible codelines from different GitHub repositories
- v1 repo: `https://github.com/open-component-model/ocm` (versions 0.16–0.53, legacy)
- v2 repo: `https://github.com/open-component-model/open-component-model` (versions >= 0.22.1, current)
- Any `go install` or download URL pointing to the v1 repo MUST be updated to the v2 repo
- Pinned version for CI: `v0.22.1`, asset `ocm-linux-amd64`, SHA256 `15532daec120686519dc3d122e078820114f2bd090b1ed3091e9bea6704339ca`
- OCM v1 (`open-component-model/ocm`) MUST NOT be used — preflight rejects v1 versions

## 17.2 OCM Config Path and Sign/Verify Flags

**Change**: Update config path from `ocm/config` (no leading dot) to `.ocm/config` (dotdir). Remove `--config`, `--key`, and `--public-key` flags from sign/verify command examples.

**Detail**:
- OCM v2 auto-discovers `.ocm/config` from `$PWD` — no `--config` flag needed
- `ocm/` (no leading dot) is NOT auto-discovered; using it would require `--config ocm/config` on every command
- Sign command: `ocm sign component-version --signature ocg-sig <CTF_REF>` (no `--config`, `--key`)
- Verify command: `ocm verify component-version --signature ocg-sig <CTF_REF>` (no `--config`, `--public-key`)
- All credential and config resolution happens through `.ocm/config` automatically

## 17.3 OCM Directory Layout

**Change**: (a) Change `ocm/component-constructor.yaml` to `.ocm/component-constructor.yaml`. (b) Remove the separate `ocm/` (no-dot) directory entirely. (c) Update `.gitignore` examples to add only `.ocm/ocg.key` — no negative exclusions needed.

**Detail**:
- All OCM artifacts live under `.ocm/`: `config`, `ocg.pub.pem`, `component-constructor.yaml`, `README.md`, `ocg.key` (gitignored)
- Same convention as aos-demo, which places the constructor at `$(SAT_DIR)/.ocm/component-constructor.yaml`
- No separate `ocm/` (no-dot) directory — it was redundant since the gitignored private key is the only artifact that would have gone there, and it is now covered by `.ocm/ocg.key`
- Note: `*.pem` and `*.key` are globally gitignored in this repository's `.gitignore`. The `.ocm/ocg.pub.pem` (committed) requires a negative exclusion `!.ocm/ocg.pub.pem`

## 17.4 Third-Party Dependency Bundling

**Change**: Replace the separate-wrapper-component model with bundled dep charts inside the OCG component.

**Original design**: Separate OCM components `github.com/operational-context-graph/postgresql` and `github.com/operational-context-graph/doris-operator`, combined via a framework distribution component.

**Revised approach**: PostgreSQL (`postgresql-chart`) and Doris operator (`doris-operator-chart`) Helm charts are embedded directly as resources inside the OCG component. No separate components. No framework distribution component.

**Rationale for deviation**:
- Upstream operators (CloudNativePG, `apache/doris-operator`) do not publish OCM components — referencing them "at source" is not possible
- Separate wrapper components add pipeline complexity (two extra `ocm add` + `ocm sign` invocations, two extra ADRs, a framework distribution component) without a lifecycle benefit at current scale
- A dep upgrade still requires a human action regardless of bundling strategy
- For the demo environment, a single deployable CTF is simpler and sufficient

**Original rationale preserved**: Separate components decouple dep patch cadence from OCG release cadence and limit re-signing scope to the changed component. Migrate to separate components when the OCM pipeline matures and dep upgrades become frequent.

**Consequence**: OCG component contains all ten resources (five images + two dep charts + chart + bundle-manifest + image-values). A dep upgrade requires a new OCG release and re-sign. Two FOSS-EVAL-GOVERNANCE-02 ADRs required before production (see `docs/adr/`).
