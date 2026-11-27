# ADR: Doris Operator Selection (FOSS-EVAL-GOVERNANCE-02)

## Status

Proposed — pending review before bundle reaches production

## Context

The OCG OCM bundle includes an Apache Doris operator Helm chart as a bundled resource (`doris-operator-chart`). The operator is required to provision and manage an Apache Doris cluster for the `telemetry-store` service in the demo environment.

An operator must be selected and evaluated for FOSS-EVAL-GOVERNANCE-02 (open governance) compliance before the bundle reaches production.

## Decision

**`apache/doris-operator`** (`github.com/apache/doris-operator`) — the only viable option.

## Options Evaluated

| Option | License | Governance | Helm Chart | Notes |
| --- | --- | --- | --- | --- |
| **`apache/doris-operator`** | Apache-2.0 | ASF (Apache Software Foundation) | GitHub + ArtifactHub | SelectDB contributors merged upstream; ASF governance removes single-vendor concern; **chosen** |
| `zncdatadev/doris-operator` (Kubedoop) | Apache-2.0 | zncdatadev (unknown) | GitHub only | 3 stars, no releases — not production-viable |

## Rationale

`apache/doris-operator` is the only viable option:
- ASF governance satisfies FOSS-EVAL-GOVERNANCE-02 (no single-vendor concern)
- Apache-2.0 license (permissive, no enterprise restrictions)
- Helm chart available at ArtifactHub and in the GitHub repository
- SelectDB contributors merged their upstream fork into the ASF project — no separate SelectDB repo exists
- Tracks Apache Doris release cadence

`zncdatadev/doris-operator` has no stable releases and insufficient community adoption for production use.

## Consequences

- `apache/doris-operator` is a **declared but not bundled** Helm dependency. It is listed in `helm/operational-context-graph/Chart.yaml` (fetched from `https://charts.selectdb.com` at deploy time via `helm dependency update`) and is not included as a resource in the OCM component.
- Operators require cluster-level CRD pre-installation; embedding them in the OCM bundle does not add value and would require a multi-stage deploy orchestrator outside current scope.
- Deployers must install the `apache/doris-operator` chart and its CRDs on the target cluster before deploying the OCG umbrella chart with `doris.enabled: true`.
- The Helm chart version is pinned in `helm/operational-context-graph/Chart.yaml`. Version bumps are a code change — no separate Makefile variable needed.
- This ADR must be merged to `autonomous-operations/autonomous-operations` (or equivalent canonical location) before the OCM bundle reaches production.

## References

- apache/doris-operator GitHub: https://github.com/apache/doris-operator
- ArtifactHub: https://artifacthub.io/packages/helm/apache-doris/doris-operator
- Apache Doris project: https://doris.apache.org/
