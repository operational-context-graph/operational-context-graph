# Operational Context Graph — Helm Chart

Umbrella chart that deploys the full Operational Context Graph stack into a Kubernetes namespace.

## Prerequisites

Complete these steps before running `helm install` or `helm upgrade`.

### 1. Set vm.max_map_count

Doris requires `vm.max_map_count >= 2000000` on the node. You may need to repeat this after every node restart depending on your local Kubernetes setup:

```sh
docker run --privileged --pid=host --rm alpine \
  nsenter -t 1 -m -u -n -i sh -c "sysctl -w vm.max_map_count=2000000"
```

### 2. Patch the Doris BE init container (if swapoff fails)

If the BE pod is stuck because `swapoff -a` exits non-zero (swap is already disabled at the OS level), patch the init container and restart the pod:

```sh
kubectl patch statefulset ocg-doris-be -n autonomous-operations \
  --type='json' -p='[{
    "op": "replace",
    "path": "/spec/template/spec/initContainers/0/args/1",
    "value": "sysctl -w vm.max_map_count=2000000 && swapoff -a || true"
  }]'
kubectl delete pod ocg-doris-be-0 -n autonomous-operations
```

### 3. Transfer doris-operator ownership (upgrade from standalone install only)

If `doris-operator` was previously installed as a standalone Helm release, transfer ownership annotations before upgrading to avoid conflicts:

```sh
for resource in \
  "serviceaccount/doris-operator" \
  "role/leader-election-role" \
  "rolebinding/leader-election-rolebinding" \
  "deployment/doris-operator" \
  "service/doris-service" \
  "secret/doris-operator-secret-cert"; do
  kubectl annotate $resource -n autonomous-operations \
    meta.helm.sh/release-name=ocg --overwrite
done
kubectl annotate clusterrole/doris-operator \
  meta.helm.sh/release-name=ocg --overwrite
kubectl annotate clusterrolebinding/doris-operator-rolebinding \
  meta.helm.sh/release-name=ocg --overwrite
```

### 4. Set the Doris root password

After Doris FE is `Running` and BE shows `Alive=true`:

```sh
kubectl exec -n autonomous-operations ocg-doris-fe-0 \
  -- mysql -h 127.0.0.1 -P 9030 -u root \
  -e "SET PASSWORD FOR 'root'@'%' = PASSWORD('<your-doris-password>');"
```

### 5. Create secrets

```sh
kubectl create secret generic postgres-secret \
  --from-literal=password=<your-postgres-password> \
  -n autonomous-operations --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic netbox-token \
  --from-literal=token=<your-netbox-token> \
  -n autonomous-operations --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic telemetry-store-doris-secret \
  --from-literal=DORIS_STREAM_LOAD_URL="http://ocg-doris-fe-service:8030" \
  --from-literal=DORIS_STREAM_LOAD_USER="root" \
  --from-literal=DORIS_STREAM_LOAD_PASSWORD="<your-doris-password>" \
  -n autonomous-operations --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic telemetry-store-doris-read-secret \
  --from-literal=DORIS_DSN="root:<your-doris-password>@tcp(ocg-doris-fe-service:9030)/" \
  -n autonomous-operations --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic telemetry-store-doris-ddl-secret \
  --from-literal=DORIS_MIGRATE_DSN="root:<your-doris-password>@tcp(ocg-doris-fe-service:9030)/" \
  -n autonomous-operations --dry-run=client -o yaml | kubectl apply -f -
```
