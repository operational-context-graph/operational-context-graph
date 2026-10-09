<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors

SPDX-License-Identifier: Apache-2.0
-->

# Data Ingestion service

The Data Ingestion service ingests operational data into the Operational
Context Graph. This directory currently holds the initial scaffold; the
ingestion pipeline from the software design document is implemented in later
changes.

## Layout

| Path | Purpose |
| --- | --- |
| `cmd/data-ingestion` | Process entrypoint (config load, server start, graceful shutdown). |
| `internal/config` | Configuration loading (koanf) and validation (go-playground/validator). |
| `internal/httpserver` | Gin engine construction and HTTP server lifecycle. |
| `internal/handlers` | Gin HTTP handlers. Health checks only, for now. |
| `internal/ingest` | Framework-agnostic domain logic. The real design lands here. |
| `mocks` | Generated testify mocks (optional, via mockery). |
| `configs` | Sample runtime configuration. |

## Framework choices

- Gin for the HTTP API.
- koanf for configuration loading.
- go-playground/validator for validation.
- stretchr/testify for assertions and mocks.
- vektra/mockery (optional) to generate testify-compatible mocks.

## Configuration

Configuration layers in this order: built-in defaults, an optional YAML file,
then environment variables prefixed with `OCG_DI_`.

| Setting | Env var | Default |
| --- | --- | --- |
| Server host | `OCG_DI_SERVER_HOST` | `0.0.0.0` |
| Server port | `OCG_DI_SERVER_PORT` | `8080` |
| Log level | `OCG_DI_LOGGING_LEVEL` | `info` |
| Config file path | `OCG_DI_CONFIG_FILE` | _(unset)_ |

See [`configs/config.example.yaml`](configs/config.example.yaml).

To run the application with a custom config file:

```bash
OCG_DI_CONFIG_FILE=configs/config.example.yaml make di-run
```

## Development

Run all commands from the repository root through `make`:

```bash
make di-build   # build
make di-vet     # go vet
make di-test    # race tests with coverage
make di-tidy    # tidy go.mod
make di-mocks   # regenerate mocks (requires mockery)
```

## Local deployment (minikube)

Deploy the service to a local minikube cluster using the umbrella Helm chart.
Run the commands from the repository root.

```bash
# 1. Build the image and load it into the minikube node.
docker build -t data-ingestion:dev services/data-ingestion
minikube image load data-ingestion:dev

# 2. Install the chart with the local override values.
helm install ocg helm/operational-context-graph \
  -f helm/operational-context-graph/values.local.yaml

# 3. Wait for the pod, then forward a local port to the service.
kubectl rollout status deployment/ocg-data-ingestion
kubectl port-forward svc/ocg-data-ingestion 8080:8080
```

Open <http://localhost:8080/healthz> in a browser.

The override file [`values.local.yaml`](../../helm/operational-context-graph/values.local.yaml) points the chart at the locally built `data-ingestion:dev` image and sets `pullPolicy: Never`, so Kubernetes uses the loaded image instead of pulling from the registry.

Remove the release with `helm uninstall ocg`.

## Endpoints

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness check. Returns `{"status":"ok"}`. |
| `GET` | `/readyz` | Readiness check. Returns `{"status":"ok"}`. |
