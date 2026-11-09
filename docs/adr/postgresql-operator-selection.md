# ADR: PostgreSQL Chart Selection (FOSS-EVAL-GOVERNANCE-02)

## Status

Accepted

## Context

The OCG OCM bundle includes a PostgreSQL Helm chart bundled as the `postgresql-chart` resource.
The chart provisions a PostgreSQL instance for the `data-ingestion` and `inventory-store` services in the demo environment.

Two candidate approaches were evaluated:

1. **Bitnami postgresql** — a standalone Helm chart that deploys PostgreSQL directly as a `StatefulSet`. No cluster-level prerequisites.
2. **CloudNativePG (CNPG)** — a Kubernetes operator that manages PostgreSQL through a `Cluster` custom resource. Requires the CNPG operator and its CRDs to be installed on the target cluster before the chart can be used.

The OCG bundle is a self-contained artefact. Its `deploy.sh` script runs `helm install` in a single step with no pre-install hooks or multi-stage cluster setup. Any chart that requires cluster-level prerequisites before `helm install` runs breaks this contract.

## Decision

Use the **Bitnami postgresql chart** (`oci://registry-1.docker.io/bitnamicharts/postgresql`).

## Options Evaluated

| Option | License | Governance | Prerequisites | Helm install complexity |
| --- | --- | --- | --- | --- |
| **Bitnami postgresql** | Apache-2.0 | VMware/Broadcom (single-vendor) | None | Single `helm install` step |
| CloudNativePG | Apache-2.0 | CNCF Sandbox | Operator + CRDs must be installed first | Multi-step: CRD install → operator install → `helm install` |
| Zalando postgres-operator | MIT | Zalando (single-vendor) | Operator + CRDs must be installed first | Multi-step |
| CrunchyData PGO | Apache-2.0 | CrunchyData (single-vendor) | Kustomize only, no Helm chart | Not compatible with Helm-only bundle |
| Percona Operator for PostgreSQL | Apache-2.0 | Percona (single-vendor) | Operator + CRDs must be installed first | Multi-step |
| StackGres | AGPLv3 | OnGres (single-vendor) | Operator + CRDs must be installed first | **License blocker** — AGPLv3 not permitted |

## Rationale

All operator-based options (CNPG, Zalando, Percona) require a CRD and operator pre-installation step that must complete before the PostgreSQL workload chart can be deployed. This is a hard operational constraint for the OCG bundle, which:

- ships as a single self-contained OCM component,
- installs via a single `helm install` invocation in `deploy.sh`, and
- does not support multi-stage cluster bootstrapping.

CNPG was the governance-preferred option in an earlier draft of this ADR. However, governance preference alone does not override a deployment feasibility constraint. CNPG's CRD prerequisite cannot be satisfied within the current bundle deployment model without significant rework (pre-install jobs, init containers, or a multi-phase deploy orchestrator — none of which are scoped for this iteration).

Bitnami postgresql satisfies all practical constraints:

- No cluster-level prerequisites.
- Installs as a standard StatefulSet subchart dependency.
- Apache-2.0 license — no enterprise restrictions.
- Chart already referenced in `helm/operational-context-graph/Chart.yaml` and tested in the local dev environment.

The single-vendor governance concern (FOSS-EVAL-GOVERNANCE-02) is accepted as a known limitation for this bundle version. The chart is used only in the demo/evaluation environment, not in a production-grade managed service.

## Consequences

- Bitnami postgresql chart (`oci://registry-1.docker.io/bitnamicharts/postgresql`) is declared as a subchart dependency in `helm/operational-context-graph/Chart.yaml`. It is fetched by `helm dependency update` and packaged into the umbrella chart tarball (`bin/ocg-<VERSION>.tgz`). No separate OCM resource for the postgresql chart is needed.
- The chart version is pinned in `Chart.yaml`. Version bumps are a code change — no separate Makefile variable is needed.
- If FOSS-EVAL-GOVERNANCE-02 must be fully satisfied in a future iteration, the bundle deployment model must first be extended to support multi-stage installs (e.g., a separate CRD bootstrap step or an OCM deployer that handles operator prerequisites). That work should produce a new ADR superseding this one.
- FOSS-EVAL-GOVERNANCE-02 non-compliance is documented here as a known gap for the current bundle version.

## References

- Bitnami postgresql chart: https://github.com/bitnami/charts/tree/main/bitnami/postgresql
- CloudNativePG CRD install docs: https://cloudnative-pg.io/documentation/current/installation_upgrade/
- CNCF CloudNativePG project: https://www.cncf.io/projects/cloudnativepg/
