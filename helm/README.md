# Operational Context Graph (OCG) Helm Chart

Umbrella chart that deploys the full Operational Context Graph stack into a Kubernetes namespace.

## Cluster setup (once per cluster)

### 1. Install the Doris Operator

The Doris Operator is a cluster-scoped controller with its own lifecycle. It is not bundled in this chart. Install it once per cluster before deploying OCG:

```sh
helm install doris-operator \
  oci://registry-1.docker.io/selectdb/doris-operator \
  --version 25.8.0 \
  --namespace doris-operator \
  --create-namespace
```

If you previously installed the operator as a standalone release into the OCG namespace, transfer ownership annotations before upgrading to avoid conflicts:

```sh
for resource in \
  "serviceaccount/doris-operator" \
  "role/leader-election-role" \
  "rolebinding/leader-election-rolebinding" \
  "deployment/doris-operator" \
  "service/doris-service" \
  "secret/doris-operator-secret-cert"; do
  kubectl annotate $resource -n <namespace> \
    meta.helm.sh/release-name=ocg --overwrite
done
kubectl annotate clusterrole/doris-operator \
  meta.helm.sh/release-name=ocg --overwrite
kubectl annotate clusterrolebinding/doris-operator-rolebinding \
  meta.helm.sh/release-name=ocg --overwrite
```

---

## Namespace setup (per environment)

### 1. Create secrets

```sh
# PostgreSQL password (used by data-ingestion and inventory-store)
kubectl create secret generic postgres-secret \
  --from-literal=password=<your-postgres-password> \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -

# NetBox API token (used by inventory-connector)
kubectl create secret generic netbox-token \
  --from-literal=token=<your-netbox-token> \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -
```

### 2. Deploy OCG (first pass, without telemetry-store)

Install the chart with Doris enabled but `telemetry-store` disabled. The Doris root password is not set yet, so the telemetry-store secrets cannot be created at this point.

```sh
helm upgrade --install ocg helm/ \
  --namespace <namespace> \
  --create-namespace \
  --set telemetry-store.enabled=false
```

Wait until Doris FE is `Running` and BE shows `Alive=true`:

```sh
kubectl get doriscluster -n <namespace>
```

### 3. Set the Doris root password and create telemetry-store secrets

Once the cluster is healthy, set the root password and create the telemetry-store secrets using the same value:

```sh
kubectl exec -n <namespace> ocg-doris-fe-0 \
  -- mysql -h 127.0.0.1 -P 9030 -u root \
  -e "SET PASSWORD FOR 'root'@'%' = PASSWORD('<your-doris-password>');"

# Doris write credentials (used by telemetry-store for stream load)
kubectl create secret generic telemetry-store-doris-secret \
  --from-literal=DORIS_STREAM_LOAD_URL="http://ocg-doris-fe-service:8030" \
  --from-literal=DORIS_STREAM_LOAD_USER="root" \
  --from-literal=DORIS_STREAM_LOAD_PASSWORD="<your-doris-password>" \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -

# Doris read credentials (used by telemetry-store for queries)
kubectl create secret generic telemetry-store-doris-read-secret \
  --from-literal=DORIS_DSN="root:<your-doris-password>@tcp(ocg-doris-fe-service:9030)/" \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -

# Doris DDL credentials (used by the telemetry-store migration init container)
kubectl create secret generic telemetry-store-doris-ddl-secret \
  --from-literal=DORIS_MIGRATE_DSN="root:<your-doris-password>@tcp(ocg-doris-fe-service:9030)/" \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -
```

### 4. Enable telemetry-store and upgrade

```sh
helm upgrade --install ocg helm/ \
  --namespace <namespace>
```
