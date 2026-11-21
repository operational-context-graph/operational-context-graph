# ocm-bundle/dependency-bundles Specification

## Purpose

Defines the governance and sourcing requirements for third-party operator charts used by the OCG stack.

## Requirements

### Requirement: GOVERNANCE-02 ADRs required before production

Two Architecture Decision Records SHALL be created and merged before the OCG bundle reaches production:

1. **postgresql ADR** (`docs/adr/postgresql-operator-selection.md`): records the operator evaluation and approved choice for the postgresql dependency
2. **doris-operator ADR**: records the `apache/doris-operator` source evaluation and the governance rationale

These ADRs document FOSS-EVAL-GOVERNANCE-02 compliance. They do not gate the CI pipeline directly — the constraint is process, not a CI check.

**Sources evaluated**:
- `postgresql`: Bitnami postgresql (Helm chart dependency, single-vendor VMware/Broadcom concern documented in ADR), CloudNativePG (CNCF Sandbox, Apache-2.0), Zalando, CrunchyData PGO, Percona, StackGres (AGPLv3 — license blocker)
- `doris-operator`: `apache/doris-operator` (ASF-governed, Apache-2.0, only viable option)

**Rationale for not bundling operator charts**: CRDs must be installed before the application chart, but bundled Helm charts are installed by a single `helm install`. Operators are cluster-level concerns with their own release cadence. The recommended approach is to deploy operators separately via cluster bootstrap tooling.

#### Scenario: ADRs present before production deploy

- **WHEN** the OCG system release is being approved for production
- **THEN** the release checklist includes verification that both ADRs are merged in the architecture documentation repository

#### Scenario: Operator chart bundling not attempted

- **WHEN** `ocm get resources ctf::./bin/ctf//` lists resources in the OCG CTF
- **THEN** no `postgresql-chart` or `doris-operator-chart` resource appears; the CTF contains exactly seven resources (four images, chart, bundle-manifest, image-values)

## Non-Goals

- Embedding operator chart tarballs inside the OCM CTF (deferred)
- Automated upstream chart version upgrades (must be explicit, pinned, and reviewed)
- Separate OCM wrapper components per dependency (deferred)
- Framework distribution component (deferred)
- `fetch-deps` Makefile step or `bin/deps/` directory (not needed)
