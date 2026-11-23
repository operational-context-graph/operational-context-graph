# ocm-bundle/dependency-bundles Specification

## Purpose

Defines the governance and sourcing requirements for third-party operator charts used by the OCG stack.

## Requirements

### Requirement: GOVERNANCE-02 ADRs required before production

Two Architecture Decision Records SHALL be created and merged before the OCG bundle reaches production: `docs/adr/postgresql-operator-selection.md` (postgresql operator evaluation and approved choice) and a doris-operator ADR (apache/doris-operator governance rationale). These ADRs document FOSS-EVAL-GOVERNANCE-02 compliance and are a process gate, not a CI check.

#### Scenario: ADRs present before production deploy

- **WHEN** the OCG system release is being approved for production
- **THEN** the release checklist includes verification that both ADRs are merged in the architecture documentation repository

### Requirement: Operator charts are not bundled in the OCM CTF

The OCM CTF SHALL NOT include `postgresql-chart` or `doris-operator-chart` resources. Operators are cluster-level concerns with their own release cadence and MUST be deployed separately via cluster bootstrap tooling. CRDs must be installed before the application chart; a single `helm install` cannot guarantee that order.

#### Scenario: Operator chart bundling not attempted

- **WHEN** `ocm get resources ctf::./bin/ctf//` lists resources in the OCG CTF
- **THEN** no `postgresql-chart` or `doris-operator-chart` resource appears; the CTF contains exactly seven resources (four images, chart, bundle-manifest, image-values)

## Non-Goals

- Embedding operator chart tarballs inside the OCM CTF (deferred)
- Automated upstream chart version upgrades (must be explicit, pinned, and reviewed)
- Separate OCM wrapper components per dependency (deferred)
- Framework distribution component (deferred)
- `fetch-deps` Makefile step or `bin/deps/` directory (not needed)
