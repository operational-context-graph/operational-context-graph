# OCG Helm Deployment — qa-de-1

Target cluster: `qa-de-1`  
Target namespace: `autonomous-operations`  
Registry: `keppel.eu-de-1.cloud.sap/ccloud/`

---

## What is in this folder

```
deploy/
  helm/
    Chart.yaml                           Helm chart metadata
    values-dev.yaml                      Dev/demo values (upstream images, 1 replica each)
    values-prod.yaml                     Production values (hardened images, HA replicas)
    values-secrets.example.yaml          Template for secrets file — copy, fill in, never commit
    templates/
      postgres-statefulset.yaml         PostgreSQL StatefulSet + headless Service
      inventory-store-deployment.yaml   Inventory Store Deployment + ClusterIP Service
      telemetry-store-deployment.yaml   Telemetry Store Deployment + ClusterIP Service
      data-ingestion-deployment.yaml    Data Ingestion Deployment
      doris-statefulset.yaml            Doris FE StatefulSet + BE StatefulSet + Services
  compose/
    dev.yml                             docker-compose for local development (upstream images)
```

**Note:** There are no Secret manifests in the chart. Secrets are injected at deploy time — see step 3 below.

---

## Before you deploy — required actions

### 1. Fill in image tags

Every image tag in `values-prod.yaml` is a placeholder:

```yaml
tag: "<git-sha>"
```

Replace `<git-sha>` with the exact git commit SHA of the build you want to deploy, e.g. `a1b2c3d`.

The CI pipeline builds and pushes images to `keppel.eu-de-1.cloud.sap/ccloud/` tagged with the commit SHA.

### 2. Fill in image digests (production only)

For production, images must also be digest-pinned. After the CI build completes, run:

```bash
docker pull keppel.eu-de-1.cloud.sap/ccloud/inventory-store:<git-sha>
docker inspect --format='{{index .RepoDigests 0}}' keppel.eu-de-1.cloud.sap/ccloud/inventory-store:<git-sha>
```

Copy the `sha256:...` digest into `values-prod.yaml`. Repeat for each image.

### 3. Set passwords (never commit these)

Copy the example secrets file and fill in real passwords:

```bash
cp deploy/helm/values-secrets.example.yaml deploy/helm/values-secrets.yaml
# edit values-secrets.yaml with real passwords
```

`values-secrets.yaml` is gitignored — it will never be committed.  
Pass it as an extra values file at deploy time:

```bash
helm install ocg ./deploy/helm \
  -n autonomous-operations \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml
```

The Secrets are created by the chart using values from this file. The templates read `.Values.postgres.password`, `.Values.doris.writerPassword`, and `.Values.doris.analystPassword`.

### 4. Confirm the namespace exists

```bash
kubectl get namespace autonomous-operations
```

If it does not exist, create it or ask cluster-admin to create it.

### 5. vm.max_map_count for Doris BE

The Doris BE init container runs `sysctl -w vm.max_map_count=2000000`. This requires the node to allow privileged init containers. Confirm with the cluster-admin that this is permitted on `qa-de-1`. If not, the node-level setting must be applied via a DaemonSet or node pool configuration.

---

## Deployment commands

### First install

```bash
# Ensure kubectl context points to qa-de-1
kubectl config current-context

# Dry run — validate templates without sending to cluster
helm install ocg-dry-run ./deploy/helm \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml \
  --dry-run --debug

# Actual install
helm install ocg ./deploy/helm \
  -n autonomous-operations \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml
```

### Upgrade (e.g. new image tag)

```bash
helm upgrade ocg ./deploy/helm \
  -n autonomous-operations \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml
```

### Check rollout status

```bash
kubectl rollout status deployment/ocg-inventory-store  -n autonomous-operations
kubectl rollout status deployment/ocg-telemetry-store   -n autonomous-operations
kubectl rollout status deployment/ocg-data-ingestion    -n autonomous-operations
kubectl rollout status statefulset/ocg-postgres         -n autonomous-operations
kubectl rollout status statefulset/ocg-doris-fe         -n autonomous-operations
kubectl rollout status statefulset/ocg-doris-be         -n autonomous-operations
```

### Rollback

```bash
helm rollback ocg -n autonomous-operations
```

---

## Local development (docker-compose)

```bash
cd operational-context-graph
docker compose -f deploy/compose/dev.yml up
```

**Before starting Doris BE**, set `vm.max_map_count` on the Docker host:

```bash
# macOS (Docker Desktop): add to ~/.docker/daemon.json then restart Docker Desktop
# Linux:
sudo sysctl -w vm.max_map_count=2000000
```

Services will be available at:

| Service           | Address                  |
|-------------------|--------------------------|
| Inventory Store   | http://localhost:8080    |
| Telemetry Store   | http://localhost:8081    |
| PostgreSQL        | localhost:5432           |
| Doris FE HTTP     | http://localhost:8030    |
| Doris FE MySQL    | localhost:9030 (MySQL client) |

---

## What still needs to happen (open items)

| # | What | Owner | Status |
|---|------|-------|--------|
| 1 | CI pipeline builds and pushes images to keppel | platform team | not started |
| 2 | Image tags and digests filled in values-prod.yaml | deployer | before each deploy |
| 3 | Real passwords injected (Secret or --set) | deployer | before first deploy |
| 4 | `vm.max_map_count` policy confirmed with cluster-admin | deployer | before first deploy |
| 5 | Health probe paths confirmed against actual application code (`/healthz`, `/readyz`) | developer | before first deploy |
| 6 | Doris FE/BE registered with each other (post-deploy step: `ADD BACKEND` SQL) | deployer | after first deploy |
| 7 | Postgres TLS certs added for `sslmode=verify-full` in production | platform team | before production |
| 8 | Hardened images built and pushed to keppel (see infrastructure/hardening.md) | platform team | before production |

---

## Notes on the chart structure

**Why namespace is hardcoded in templates:**  
All resources hardcode `namespace: autonomous-operations` because that is the only target namespace for this chart. If you ever need to deploy to a different namespace, replace the hardcoded value or add `{{ .Release.Namespace }}`.

**Why StatefulSets for Postgres and Doris:**  
Both require stable network identities and persistent volumes that survive pod restarts. A Deployment would lose data on reschedule.

**Why ClusterIP Services only:**  
None of the OCG components need to be reachable from outside the cluster directly. Inter-service communication goes through the internal DNS names (e.g. `ocg-inventory-store`, `ocg-doris-fe`). External access would go through an Ingress or the existing `autonomous-operations-api` gateway.

**Doris BE init container:**  
Doris Backend requires `vm.max_map_count >= 2000000` for its memory-mapped storage. The init container sets this at pod startup — without it BE will crash.
