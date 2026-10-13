# ocg-inventory-connector

Reads raw NetBox REST API records and delivers them to OCG Data Ingestion via gRPC IngestionService. No field mapping — the connector is a dumb mediator. Schema mapping is handled by the OCG Semantic Network Builder at ingestion time.

## Run

```bash
CONNECTOR_NETBOX__BASE_URL=https://netbox.example.com \
CONNECTOR_NETBOX__TOKEN=your-token \
CONNECTOR_GRPC__ENDPOINT=ingestion.example.com:443 \
CONNECTOR_GRPC__CERT_FILE=/path/to/client.crt \
CONNECTOR_GRPC__KEY_FILE=/path/to/client.key \
CONNECTOR_GRPC__CONNECTOR_ID=ocg-inventory-connector \
go run ./cmd/connector
```

Or with a config file:

```bash
CONNECTOR_CONFIG=config.yaml go run ./cmd/connector
```

## Configuration

See `configs/config.example.yaml` for defaults. All keys are overridable via env vars using `CONNECTOR_` prefix and `__` as section separator:

| Env var | Config key | Description |
|---|---|---|
| `CONNECTOR_NETBOX__BASE_URL` | `netbox.base_url` | NetBox base URL |
| `CONNECTOR_NETBOX__TOKEN` | `netbox.token` | NetBox API token |
| `CONNECTOR_NETBOX__PAGE_SIZE` | `netbox.page_size` | Records per page (default 1000) |
| `CONNECTOR_GRPC__ENDPOINT` | `grpc.endpoint` | Data Ingestion gRPC endpoint |
| `CONNECTOR_GRPC__CERT_FILE` | `grpc.cert_file` | mTLS client certificate (optional) |
| `CONNECTOR_GRPC__KEY_FILE` | `grpc.key_file` | mTLS client key (optional) |
| `CONNECTOR_GRPC__CONNECTOR_ID` | `grpc.connector_id` | Connector identity header |
| `CONNECTOR_CONNECTOR__SCHEMA_VERSION` | `connector.schema_version` | Schema version tag on messages |
| `CONNECTOR_CONNECTOR__LOGICAL_ARTIFACT_ID` | `connector.logical_artifact_id` | Map of endpoint path → artifact ID |

## Tests

```bash
# Unit tests
go test ./...

# Integration tests (requires mockreplay on localhost:8081)
go test -tags=integration -run TestIntegration_CoreEndpoints -timeout 60s ./integration/

# Large endpoints (devices, interfaces, ip-addresses)
SLOW=1 go test -tags=integration -run TestIntegration_SlowEndpoints -timeout 600s ./integration/
```

## Docs

- [System Context](docs/diagrams/overview.md#system-context)
- [Internal Components](docs/diagrams/overview.md#internal-components)
- [Run Sequence](docs/diagrams/overview.md#run-sequence)
- [ADRs](docs/adr/)

## Package layout

| Package | Responsibility |
|---|---|
| `cmd/connector` | Entry point — load config, run, exit |
| `internal/runner` | Orchestrates the run: paginate → build → publish per endpoint |
| `internal/netbox` | NetBox HTTP client with pagination and retry |
| `internal/envelope` | Wraps raw JSON records into `Message` with partition and watermark |
| `internal/publisher` | `Publisher` interface, `GRPCPublisher` (conn.go, batch.go), `StubPublisher` |
| `internal/config` | koanf-based config loader with env var overrides |
