# ocg-inventory-connector

Reads raw NetBox REST API records and delivers them to the Operational Context Graph (OCG) Data Ingestion service via gRPC. The connector does no field mapping — each record is wrapped in an envelope and forwarded as-is.

## Run

```bash
CONNECTOR_NETBOX__BASE_URL=https://netbox.example.com \
CONNECTOR_NETBOX__TOKEN=your-token \
CONNECTOR_GRPC__ENDPOINT=ingestion.example.com:443 \
CONNECTOR_GRPC__CONNECTOR_ID=ocg-inventory-connector \
go run ./cmd/connector
```

## Configuration

All configuration is via environment variables with the `CONNECTOR_` prefix. Use `__` as the section separator.

| Env var | Description |
|---|---|
| `CONNECTOR_NETBOX__BASE_URL` | NetBox base URL (required) |
| `CONNECTOR_NETBOX__TOKEN` | NetBox API token |
| `CONNECTOR_NETBOX__PAGE_SIZE` | Records per page (default 1000) |
| `CONNECTOR_GRPC__ENDPOINT` | Data Ingestion gRPC endpoint (required) |
| `CONNECTOR_GRPC__CERT_FILE` | mTLS client certificate path (optional) |
| `CONNECTOR_GRPC__KEY_FILE` | mTLS client key path (optional) |
| `CONNECTOR_GRPC__CONNECTOR_ID` | Connector identity header (required) |
| `CONNECTOR_SCHEMA_VERSION` | Schema version tag on messages (required) |
| `CONNECTOR_ENDPOINTS` | Comma-separated list of NetBox endpoint paths to sync (required) |
| `CONNECTOR_ARTIFACT_TYPES` | JSON object overriding the default endpoint→artifact-type map |

`CONNECTOR_ARTIFACT_TYPES` example:

```bash
CONNECTOR_ARTIFACT_TYPES='{"dcim/devices/":"netbox.Device","ipam/prefixes/":"netbox.Prefix"}'
```

When `CONNECTOR_ARTIFACT_TYPES` is omitted the connector uses built-in defaults for all 65 standard NetBox endpoints. Unknown endpoints fall back to `netbox.Unknown`.

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
