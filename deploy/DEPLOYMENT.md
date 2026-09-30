# OCG Helm Deployment

Target cluster: `<cluster>`  
Target namespace: `<namespace>`  
Registry: `<registry>/`

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

The CI pipeline builds and pushes images to `<registry>/` tagged with the commit SHA.

### 2. Fill in image digests (production only)

For production, images must also be digest-pinned. After the CI build completes, run:

```bash
docker pull <registry>/inventory-store:<git-sha>
docker inspect --format='{{index .RepoDigests 0}}' <registry>/inventory-store:<git-sha>
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
  -n <namespace> \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml
```

The Secrets are created by the chart using values from this file. The templates read `.Values.postgres.password`, `.Values.doris.writerPassword`, and `.Values.doris.analystPassword`.

### 4. Confirm the namespace exists

```bash
kubectl get namespace <namespace>
```

If it does not exist, create it or ask cluster-admin to create it.

### 5. vm.max_map_count for Doris BE

The Doris BE init container runs `sysctl -w vm.max_map_count=2000000`. This requires the node to allow privileged init containers. Confirm with the cluster-admin that this is permitted on `<cluster>`. If not, the node-level setting must be applied via a DaemonSet or node pool configuration.

---

## Deployment commands

### First install

```bash
# Ensure kubectl context points to the target cluster
kubectl config current-context

# Dry run — validate templates without sending to cluster
helm install ocg-dry-run ./deploy/helm \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml \
  --dry-run --debug

# Actual install
helm install ocg ./deploy/helm \
  -n <namespace> \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml
```

### Upgrade (e.g. new image tag)

```bash
helm upgrade ocg ./deploy/helm \
  -n <namespace> \
  -f deploy/helm/values-prod.yaml \
  -f deploy/helm/values-secrets.yaml
```

### Check rollout status

```bash
kubectl rollout status deployment/ocg-inventory-store  -n <namespace>
kubectl rollout status deployment/ocg-telemetry-store   -n <namespace>
kubectl rollout status deployment/ocg-data-ingestion    -n <namespace>
kubectl rollout status statefulset/ocg-postgres         -n <namespace>
kubectl rollout status statefulset/ocg-doris-fe         -n <namespace>
kubectl rollout status statefulset/ocg-doris-be         -n <namespace>
```

### Rollback

```bash
helm rollback ocg -n <namespace>
```

---

## Local development (docker-compose)

> **Note:** The application images (`inventory-store`, `telemetry-store`, `data-ingestion`) do not exist yet. Until they are built and published, only the infrastructure services (Postgres, Doris) can be started locally.

### Start infrastructure only

From the repo root:

```bash
docker compose -f deploy/compose/dev.yml up sysctl-init postgres doris-fe doris-be
```

`vm.max_map_count` is set automatically by the `sysctl-init` service — no manual host configuration needed.

### Start full stack (once application images exist)

```bash
docker compose -f deploy/compose/dev.yml up
```

### Services

| Service           | Address                       |
|-------------------|-------------------------------|
| Inventory Store   | http://localhost:8080         |
| Telemetry Store   | http://localhost:8081         |
| PostgreSQL        | localhost:5432                |
| Doris FE HTTP     | http://localhost:8030         |
| Doris FE MySQL    | localhost:9030 (MySQL client) |

### Tear down

```bash
docker compose -f deploy/compose/dev.yml down      # stop, keep data volumes
docker compose -f deploy/compose/dev.yml down -v   # stop and delete all data
```

---

## What still needs to happen (open items)

| # | What | Owner | Status |
|---|------|-------|--------|
| 1 | Application services implemented and Dockerfiles added (inventory-store, telemetry-store, data-ingestion) | developer | not started |
| 2 | CI pipeline builds and pushes images to the registry | platform team | not started |
| 3 | Image tags and digests filled in values-prod.yaml | deployer | before each deploy |
| 4 | Real passwords injected (Secret or --set) | deployer | before first deploy |
| 5 | `vm.max_map_count` policy confirmed with cluster-admin | deployer | before first deploy |
| 6 | Health probe paths confirmed against actual application code (`/healthz`, `/readyz`) | developer | before first deploy |
| 7 | Doris FE/BE registered with each other (local dev: automatic via `BE_ADDR`; Kubernetes: post-deploy `ADD BACKEND` SQL) | deployer | after first deploy |
| 8 | Postgres TLS certs added for `sslmode=verify-full` in production | platform team | before production |
| 9 | Hardened images built and pushed to registry (see `infrastructure/` Dockerfiles) | platform team | before production |

---

## Notes on the chart structure

**Why namespace is a value:**  
All resources use `namespace: {{ .Values.namespace }}`. Set the `namespace` key in your values file or pass `--set namespace=<your-namespace>` at deploy time.

**Why StatefulSets for Postgres and Doris:**  
Both require stable network identities and persistent volumes that survive pod restarts. A Deployment would lose data on reschedule.

**Why ClusterIP Services only:**  
None of the OCG components need to be reachable from outside the cluster directly. Inter-service communication goes through the internal DNS names (e.g. `ocg-inventory-store`, `ocg-doris-fe`). External access would go through an Ingress or an API gateway.

**Doris BE init container:**  
Doris Backend requires `vm.max_map_count >= 2000000` for its memory-mapped storage. The init container sets this at pod startup — without it BE will crash.
