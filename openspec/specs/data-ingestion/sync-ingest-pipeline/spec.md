# data-ingestion/sync-ingest-pipeline Specification

## Purpose
The synchronous ingest pipeline is the shared business-logic core of the Data Ingestion service. It validates each record in a batch against its registered schema. It normalizes conforming records into the canonical shape. It writes each normalized record to the persistent outbox. It returns per-batch rejection counts to the receiving layer so it can report partial success to connectors.

## Requirements

### Requirement: Schema validation per record

The pipeline SHALL validate each record's `payload` against the schema returned by `SchemaRegistryClient.Schema`. A record SHALL be rejected — not written, logged, and counted in `IngestResult.Rejected` — when its payload is non-conforming, or when `Schema` returns an error satisfying `errors.Is(err, ErrNotFound)` (unregistered schema). Any other error from `Schema` is a request-level failure. The pipeline SHALL abort the batch and return a non-nil error so the connector retries.

#### Scenario: Conforming record passes validation

- **WHEN** `SchemaRegistryClient.Schema` returns a schema and the record's `payload` conforms to it
- **THEN** the record proceeds to normalization and is eventually written to the outbox
- **AND** it is counted in `IngestResult.Accepted`

#### Scenario: Non-conforming payload is rejected

- **WHEN** a record's `payload` does not conform to the schema returned by `SchemaRegistryClient.Schema`
- **THEN** the record is not written to any store
- **AND** it is counted in `IngestResult.Rejected`
- **AND** `Ingest` does not return a non-nil error for that record alone

#### Scenario: Unregistered schema type is rejected

- **WHEN** `SchemaRegistryClient.Schema` returns an `ErrNotFound` error for a record's `SchemaKey`
- **THEN** the record is not written to any store
- **AND** it is counted in `IngestResult.Rejected`
- **AND** the pipeline logs the unregistered key
- **AND** `Ingest` does not return a non-nil error for that record alone

#### Scenario: Empty payload is rejected

- **WHEN** a record's `payload` field is empty (`len(payload) == 0`)
- **THEN** the record fails validation and is counted in `IngestResult.Rejected`

#### Scenario: Schema Registry unavailable aborts the batch for retry

- **WHEN** `SchemaRegistryClient.Schema` returns a non-`ErrNotFound` error (e.g., a timeout or connectivity failure) for a record
- **THEN** `Ingest` returns a non-nil error (request-level failure)
- **AND** no records from the batch are written to the outbox
- **AND** the receiving layer does not acknowledge the connector, so the batch is retried later
- **AND** the record is NOT counted as rejected

#### Scenario: Registry recovers on retry

- **WHEN** a batch first fails because the Schema Registry was unavailable, and the connector resends the identical batch after the registry recovers
- **THEN** the records are validated, normalized, and written to the outbox on the retry
- **AND** no record was dropped due to the earlier unavailability

### Requirement: Per-record failure isolation

The pipeline SHALL process every record in the batch independently. A validation or normalization failure for one record SHALL NOT stop processing of the remaining records.

#### Scenario: Mixed batch — some valid, some invalid

- **WHEN** a batch contains some records that pass validation and some that do not
- **THEN** the conforming records are normalized and written to the outbox
- **AND** the non-conforming records are dropped and counted in `IngestResult.Rejected`
- **AND** `IngestResult.Accepted + IngestResult.Rejected == len(batch)`

#### Scenario: All records rejected

- **WHEN** every record in a batch fails validation (all have unregistered schema types)
- **THEN** no records are written to the outbox
- **AND** `IngestResult.Rejected == len(batch)` and `IngestResult.Accepted == 0`
- **AND** `Ingest` returns a nil error

#### Scenario: All records accepted

- **WHEN** every record in a batch passes validation and normalization
- **THEN** all records are written to the outbox
- **AND** `IngestResult.Accepted == len(batch)` and `IngestResult.Rejected == 0`

### Requirement: Canonical normalization via source mapping

The pipeline SHALL normalize each validated record to its canonical shape. It fetches the source-mapping artifact for its `SchemaKey` from `SchemaRegistryClient.SourceMappings`. It then calls `SourceMappingExecutionEngine.Normalize` with that mapping and the record.

#### Scenario: Source mapping fetched and normalization called

- **WHEN** a record passes schema validation
- **THEN** the pipeline calls `SchemaRegistryClient.SourceMappings` with the record's `SchemaKey`
- **AND** calls `SourceMappingExecutionEngine.Normalize` with the returned mapping and the record

### Requirement: Normalized record shape and outbox write

`Normalize` SHALL return the normalized record as an `IngestRecord`. Its `Payload` SHALL be canonical JSON. Its `Schema` SHALL be the canonical `SchemaKey`. That normalized record SHALL be the value written to the outbox.

#### Scenario: Successful normalization

- **WHEN** `SchemaRegistryClient.SourceMappings` returns a source mapping and `SourceMappingExecutionEngine.Normalize` returns a normalized `IngestRecord` without error
- **THEN** that normalized record (canonical JSON payload and canonical `SchemaKey`) is written to the outbox
- **AND** the record is counted in `IngestResult.Accepted`

#### Scenario: Unregistered source mapping rejects the record

- **WHEN** `SchemaRegistryClient.SourceMappings` returns an `ErrNotFound` error for a validated record
- **THEN** that record is rejected and counted in `IngestResult.Rejected`
- **AND** the pipeline logs the error and continues with the remaining records

#### Scenario: Source mapping lookup unavailable aborts the batch for retry

- **WHEN** `SchemaRegistryClient.SourceMappings` returns a non-`ErrNotFound` error (registry unavailable) for a validated record
- **THEN** `Ingest` returns a non-nil error (request-level failure)
- **AND** no records from the batch are written to the outbox
- **AND** the batch is retried later

#### Scenario: Normalization engine returns error

- **WHEN** `SourceMappingExecutionEngine.Normalize` returns a non-nil error for a record
- **THEN** that record is counted in `IngestResult.Rejected` (not written to the outbox)
- **AND** the pipeline continues processing the remaining records

### Requirement: Target store resolution

The pipeline SHALL resolve the target store for each normalized record. It calls `SchemaRegistryClient.TargetStore` with the normalized record's canonical `SchemaKey`. It carries the resolved `StoreID` on the outbox row as `target_store`. The pipeline SHALL NOT hard-code routing decisions.

#### Scenario: Target store resolved for normalized record

- **WHEN** a record is successfully normalized
- **THEN** the pipeline calls `SchemaRegistryClient.TargetStore` with the normalized record's canonical `SchemaKey`
- **AND** the resolved `StoreID` is set on the outbox row's `target_store` field

### Requirement: Target store error handling

An `ErrNotFound` result from `TargetStore` MUST be treated as a record-level rejection. Any other error MUST be treated as a Schema Registry unavailability and a request-level failure.

#### Scenario: Target store resolved and persisted

- **WHEN** `SchemaRegistryClient.TargetStore` returns a `StoreID` for a record's `SchemaKey`
- **THEN** the outbox row for that record carries the resolved `StoreID` in its `target_store` field

#### Scenario: Unregistered target store rejects the record

- **WHEN** `SchemaRegistryClient.TargetStore` returns an `ErrNotFound` error for a normalized record
- **THEN** that record is rejected and counted in `IngestResult.Rejected`
- **AND** the pipeline logs the error and continues with the remaining records

#### Scenario: Target store lookup unavailable aborts the batch for retry

- **WHEN** `SchemaRegistryClient.TargetStore` returns a non-`ErrNotFound` error (registry unavailable) for a normalized record
- **THEN** `Ingest` returns a non-nil error (request-level failure)
- **AND** no records from the batch are written to the outbox
- **AND** the batch is retried later

### Requirement: Request-level failure propagation

`Ingest` SHALL return a non-nil error (request-level failure) when any registry lookup (`Schema`, `SourceMappings`, or `TargetStore`) returns a non-`ErrNotFound` error, or when the outbox write fails. On a request-level failure the receiving layer MUST NOT acknowledge the connector. It maps the error to `UNAVAILABLE` (registry or outbox) or `INTERNAL` (unexpected). Record-level rejections (non-conforming payload, `ErrNotFound`, normalization failure) SHALL NOT be request-level failures.

#### Scenario: Schema Registry unavailable is a request-level failure

- **WHEN** any of `SchemaRegistryClient.Schema`, `.SourceMappings`, or `.TargetStore` returns a non-`ErrNotFound` error
- **THEN** `Ingest` returns a non-nil error
- **AND** no records from the batch are written to the outbox
- **AND** the receiving layer does not acknowledge the connector

#### Scenario: Outbox write failure is a request-level failure

- **WHEN** `OutboxWriter.Write` returns a non-nil error
- **THEN** `Ingest` returns a non-nil error
- **AND** the receiving layer does not acknowledge the connector

#### Scenario: Record-level errors do not become request-level failures

- **WHEN** every record in a batch is rejected by a non-conforming payload or an `ErrNotFound` registry result, and the outbox write (if reached) succeeds
- **THEN** `Ingest` returns a nil error with `IngestResult.Rejected == len(batch)`
- **AND** the receiving layer acknowledges with a rejected count, not a gRPC error status

### Requirement: Outbox write before acknowledgement

The pipeline SHALL write all successfully normalized records to the outbox via a single `OutboxWriter.Write(ctx, rows)` call before returning. The write is atomic: on nil all rows are durable; on error none are visible. A non-nil error from `OutboxWriter.Write` SHALL cause `Ingest` to return a non-nil error. The receiving layer MUST NOT acknowledge the connector in that case.

#### Scenario: All normalized records written in one atomic call before return

- **WHEN** normalization of all conforming records succeeds and `OutboxWriter.Write` returns nil
- **THEN** `OutboxWriter.Write` is called exactly once with all accepted rows
- **AND** all rows are durable in the outbox before `Ingest` returns
- **AND** `Ingest` returns a nil error

#### Scenario: Outbox write failure causes request failure

- **WHEN** `OutboxWriter.Write` returns a non-nil error
- **THEN** `Ingest` returns a non-nil error
- **AND** the receiving layer does not acknowledge the connector
- **AND** the pipeline treats the batch as not durable (no partial-success count is reported)

#### Scenario: No outbox call when no records are accepted

- **WHEN** every record in the batch is rejected before the outbox write
- **THEN** `OutboxWriter.Write` is not called
- **AND** `Ingest` returns a nil error with `IngestResult.Accepted == 0`

#### Scenario: Concurrent outbox writes do not interleave results

- **WHEN** two goroutines call `Ingestor.Ingest` simultaneously with separate batches
- **THEN** each batch's normalized records are written as a single atomic unit without mixing rows from the two batches in one transaction
- **AND** `Ingest` returns the correct `IngestResult` to each goroutine

### Requirement: Rejection count reporting

`Ingest` SHALL return an `IngestResult` where `Accepted + Rejected == len(batch)` for every non-error return. The receiving layer uses `Rejected` to populate `IngestResponse.partial_success.rejected_messages`.

#### Scenario: Count invariant holds on mixed batch

- **WHEN** `Ingest` returns a nil error for a batch of N records where M pass validation
- **THEN** `IngestResult.Accepted == M` and `IngestResult.Rejected == N - M`
- **AND** `IngestResult.Accepted + IngestResult.Rejected == N`

#### Scenario: Count invariant holds on all-accepted batch

- **WHEN** all N records pass validation and are written
- **THEN** `IngestResult.Accepted == N` and `IngestResult.Rejected == 0`

#### Scenario: Count invariant holds on all-rejected batch

- **WHEN** all N records fail validation
- **THEN** `IngestResult.Rejected == N` and `IngestResult.Accepted == 0`

### Requirement: Empty and boundary batch handling

The pipeline SHALL handle an empty batch (`len(batch) == 0`) without error, returning `IngestResult{0, 0}`. The pipeline SHALL process a single-record batch correctly.

> **Note (boundary)**: The protocol rejects an empty request with `INVALID_ARGUMENT` at the receiving layer, so the pipeline should not receive an empty batch in production. The empty-batch behavior here is a defensive no-op, not a substitute for the receiving layer's `INVALID_ARGUMENT` check.

#### Scenario: Empty batch returns zero counts

- **WHEN** `Ingest` is called with an empty batch (`[]IngestRecord{}`)
- **THEN** `Ingest` returns `IngestResult{Accepted: 0, Rejected: 0}` and a nil error
- **AND** no calls are made to `SchemaRegistryClient` or `OutboxWriter`

#### Scenario: Nil batch is treated as empty

- **WHEN** `Ingest` is called with a nil batch
- **THEN** `Ingest` returns `IngestResult{Accepted: 0, Rejected: 0}` and a nil error

#### Scenario: Single-record batch is processed correctly

- **WHEN** the batch contains exactly one conforming record
- **THEN** `IngestResult.Accepted == 1` and `IngestResult.Rejected == 0`

#### Scenario: Single-record batch with invalid record

- **WHEN** the batch contains exactly one non-conforming record
- **THEN** `IngestResult.Rejected == 1` and `IngestResult.Accepted == 0`
- **AND** `Ingest` returns a nil error

### Requirement: Concurrent call safety

`Ingestor.Ingest` SHALL be safe for concurrent use. The pipeline implementation SHALL hold no mutable state of its own; all shared state lives in the injected `SchemaRegistryClient`, `SourceMappingExecutionEngine`, and `OutboxWriter`, which are required to be concurrency-safe by their callers.

#### Scenario: Two concurrent batches complete without data races

- **WHEN** two goroutines call `Ingestor.Ingest` simultaneously with disjoint batches
- **THEN** both calls complete without data races (verified under `-race`)
- **AND** each returns an `IngestResult` consistent only with its own input batch

#### Scenario: Injected dependencies are called concurrently without pipeline-level locks

- **WHEN** `Ingestor.Ingest` is called by N goroutines simultaneously
- **THEN** the pipeline does not introduce additional synchronization beyond what the injected dependencies provide
