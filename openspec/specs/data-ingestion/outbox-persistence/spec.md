# data-ingestion/outbox-persistence Specification

## Purpose
The outbox persistence layer is the durable write target of the synchronous ingest pipeline. It provides the `pgx`-backed `OutboxWriter`. The writer atomically writes a batch of canonical records to the PostgreSQL outbox table. It owns the outbox-table schema and its migrations. It also defines how the service connects to PostgreSQL and reports database health.

## Requirements

### Requirement: Atomic batch write to the outbox

The `pgx`-backed `OutboxWriter` SHALL implement `ingest.OutboxWriter`. It SHALL write all rows of one `Write(ctx, rows)` call in a single transaction. On a nil return every row SHALL be durable. On a non-nil return no row SHALL be visible to any reader. The implementation SHALL NOT expose a `pgx` transaction type across the `ingest.OutboxWriter` interface.

#### Scenario: Batch committed atomically

- **WHEN** `Write` is called with N rows and the transaction commits
- **THEN** all N rows are present in the outbox table
- **AND** `Write` returns a nil error

#### Scenario: Insert failure rolls back the whole batch

- **WHEN** `Write` is called with N rows and one insert fails (e.g., a constraint violation or a broken connection mid-batch)
- **THEN** the transaction is rolled back and no row from the batch is present in the outbox table
- **AND** `Write` returns a non-nil error

#### Scenario: Empty slice is a no-op

- **WHEN** `Write` is called with an empty (or nil) slice
- **THEN** no transaction is opened and no row is written
- **AND** `Write` returns a nil error

#### Scenario: Concurrent writers do not interleave batches

- **WHEN** two goroutines call `Write` simultaneously with separate batches against the same pool
- **THEN** each batch is committed as its own transaction with no rows interleaved between the two
- **AND** both calls complete without data races (verified under `-race`)

### Requirement: Database unavailability surfaces as an error

`Write` SHALL return a non-nil error whenever the database cannot durably accept the batch. This covers connection refused, pool exhausted, statement timeout, and context cancellation. It SHALL NOT silently drop rows or return nil on a failed commit. This lets the pipeline classify the failure as request-level rather than a record rejection.

#### Scenario: Connection refused returns an error

- **WHEN** the PostgreSQL instance is unreachable and `Write` is called
- **THEN** `Write` returns a non-nil error
- **AND** no rows are reported as written

#### Scenario: Context cancellation returns an error

- **WHEN** the caller's context is cancelled or its deadline exceeded during `Write`
- **THEN** `Write` returns a non-nil error wrapping the context error
- **AND** the transaction is rolled back

#### Scenario: Statement timeout returns an error

- **WHEN** the batch insert exceeds the configured statement timeout
- **THEN** `Write` returns a non-nil error
- **AND** no row from the batch is durable

### Requirement: Outbox table schema and migrations

The service SHALL own the outbox-table schema as versioned, embedded migrations. It SHALL apply them to the target database. The table SHALL carry the columns defined by the SDD: `id` (DB-generated monotonic primary key), `target_store`, `logical_artifact_type`, `schema_version`, `payload`, `content_type`, `batch_id` (nullable), and `created_at`. Migrations SHALL be idempotent across restarts and safe when several replicas start concurrently.

#### Scenario: Migrations create the outbox table on first start

- **WHEN** the service starts against a database without the outbox table
- **THEN** the migrations create the table with all SDD columns and the drain indexes
- **AND** startup proceeds only after migrations succeed

#### Scenario: Migrations are a no-op when already applied

- **WHEN** the service starts against a database already at the latest migration version
- **THEN** no schema change is made
- **AND** startup proceeds

#### Scenario: Concurrent replica startup does not corrupt the schema

- **WHEN** multiple replicas run migrations against the same database at the same time
- **THEN** a migration lock serializes them so the schema is applied exactly once
- **AND** no replica observes a partially applied migration

#### Scenario: Migration failure aborts startup

- **WHEN** a migration fails (e.g., the database rejects the DDL)
- **THEN** the service does not begin serving and exits with a non-nil error
- **AND** no partial schema is left marked as the current version

### Requirement: Canonical record persisted to the correct columns

`Write` SHALL map each `OutboxRecord` to a row so the drain can route and interpret it. `target_store`, `logical_artifact_type`, `schema_version`, `payload`, and `content_type` are written from the record. `id` and `created_at` are set by the database. `batch_id` is left NULL (pending).

#### Scenario: Row carries the resolved target store and canonical identity

- **WHEN** `Write` persists a record whose resolved target store is `telemetry` and whose canonical schema key is `(ocg.telemetry.log, 2.4.0)`
- **THEN** the row has `target_store = 'telemetry'`, `logical_artifact_type = 'ocg.telemetry.log'`, `schema_version = '2.4.0'`, and the canonical JSON in `payload`
- **AND** `batch_id` is NULL and `created_at` is set by the database

#### Scenario: Missing content type uses the default

- **WHEN** an `OutboxRecord` has no content type
- **THEN** the row stores `application/json` as the content type

### Requirement: Database connection configuration

The service SHALL load its PostgreSQL connection settings through the existing layered config (`OCG_DI_DATABASE__*`). The password SHALL be supplied only via environment/secret, never a plaintext config value. Required settings that are absent SHALL fail validation at startup.

#### Scenario: Valid configuration opens a pool

- **WHEN** host, port, database, user, and password are provided
- **THEN** the service opens a connection pool at startup
- **AND** configuration validation passes

#### Scenario: Missing required setting fails startup

- **WHEN** a required connection setting (e.g., host or database) is empty
- **THEN** configuration validation returns an error
- **AND** the service does not start

#### Scenario: Password is not read from a plaintext config field

- **WHEN** the configuration is loaded
- **THEN** the password is taken from `OCG_DI_DATABASE__PASSWORD` (sourced from a Kubernetes Secret), not from a committed config file field

### Requirement: Readiness reflects database connectivity

The service readiness probe SHALL report not-ready while the database is unreachable and ready once a connection can be acquired, so Kubernetes does not route ingest traffic to a replica that cannot persist to the outbox.

#### Scenario: Ready when the database is reachable

- **WHEN** the readiness probe runs and a pool connection can be acquired and pinged
- **THEN** the probe reports ready (HTTP 200)

#### Scenario: Not ready when the database is unreachable

- **WHEN** the readiness probe runs and no connection can be acquired within the probe timeout
- **THEN** the probe reports not-ready (non-200)
- **AND** liveness is unaffected (the process is not killed for a transient DB outage)
