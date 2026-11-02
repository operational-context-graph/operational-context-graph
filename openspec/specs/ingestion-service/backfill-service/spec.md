# backfill-service Specification

## Purpose

The backfill-service capability implements the gRPC `BackfillService` receiving layer. It exposes `GetWatermark`, `StartGap`, `CloseGap`, and a backfill-channel `Ingest` RPC. It delegates watermark and gap state management to the `Backfiller` interface and delegates record processing to the `Ingestor` pipeline. It enforces the same request-level limits as `IngestionService`.

## Requirements

### Requirement: GetWatermark returns committed watermark or empty

`BackfillService.GetWatermark` SHALL return the last committed watermark for the `(connector, partition)` pair when one exists. When no watermark has been recorded, it SHALL return an empty `GetWatermarkResponse` (watermark field unset) — not `codes.NotFound`. Connector identity MUST be present; absent identity returns `UNAUTHENTICATED`.

#### Scenario: Watermark exists — returned in response
- **WHEN** `GetWatermark` is called by an authenticated connector for a partition with a committed watermark
- **THEN** `GetWatermarkResponse.Watermark` contains the last committed watermark and the call returns `nil`

#### Scenario: No watermark recorded — empty response
- **WHEN** `GetWatermark` is called for a `(connector, partition)` pair with no recorded watermark
- **THEN** `GetWatermarkResponse.Watermark` is unset (nil); no error is returned; `codes.NotFound` is NOT returned

#### Scenario: Absent connector identity — UNAUTHENTICATED
- **WHEN** `GetWatermark` is called with no mTLS certificate CN and no `connector-id` header
- **THEN** the method returns `codes.Unauthenticated`

#### Scenario: Backfiller storage error — UNAVAILABLE with RetryInfo
- **WHEN** `GetWatermark` is called and `Backfiller.GetWatermark` returns a non-nil error
- **THEN** the method returns `codes.Unavailable` with a `google.rpc.RetryInfo` detail; `codes.Internal` is NOT returned

---

### Requirement: StartGap opens a gap for the partition

`BackfillService.StartGap` SHALL call `Backfiller.StartGap` for the `(connector, partition)` pair. The `partition` field in the request is required; absent or nil partition returns `INVALID_ARGUMENT`. Connector identity MUST be present; absent identity returns `UNAUTHENTICATED`.

#### Scenario: Gap started successfully
- **WHEN** `StartGap` is called with a valid partition and an authenticated connector
- **THEN** `Backfiller.StartGap` is called and `StartGapResponse{}` is returned with `nil` error

#### Scenario: Nil or absent partition — INVALID_ARGUMENT
- **WHEN** `StartGap` is called with a nil or absent `partition` field
- **THEN** the method returns `codes.InvalidArgument`; `Backfiller.StartGap` is NOT called

#### Scenario: Absent connector identity — UNAUTHENTICATED
- **WHEN** `StartGap` is called with no connector identity
- **THEN** the method returns `codes.Unauthenticated`

#### Scenario: Backfiller storage error — UNAVAILABLE with RetryInfo
- **WHEN** `Backfiller.StartGap` returns a non-nil error
- **THEN** the method returns `codes.Unavailable` with `RetryInfo`

#### Scenario: Idempotent: gap already open — no error
- **WHEN** `StartGap` is called for a pair where a gap is already open
- **THEN** `BackfillService.StartGap` returns `nil` (BackfillLogic is idempotent)

---

### Requirement: CloseGap closes the gap for the partition

`BackfillService.CloseGap` SHALL call `Backfiller.CloseGap` for the `(connector, partition)` pair. The `partition` field is required; absent or nil partition returns `INVALID_ARGUMENT`. Connector identity MUST be present.

#### Scenario: Gap closed successfully
- **WHEN** `CloseGap` is called with a valid partition and an authenticated connector
- **THEN** `Backfiller.CloseGap` is called and `CloseGapResponse{}` is returned with `nil` error

#### Scenario: Nil or absent partition — INVALID_ARGUMENT
- **WHEN** `CloseGap` is called with a nil or absent `partition` field
- **THEN** the method returns `codes.InvalidArgument`; `Backfiller.CloseGap` is NOT called

#### Scenario: Absent connector identity — UNAUTHENTICATED
- **WHEN** `CloseGap` is called with no connector identity
- **THEN** the method returns `codes.Unauthenticated`

#### Scenario: Backfiller storage error — UNAVAILABLE with RetryInfo
- **WHEN** `Backfiller.CloseGap` returns a non-nil error
- **THEN** the method returns `codes.Unavailable` with `RetryInfo`

#### Scenario: No-op when gap is not open — no error
- **WHEN** `CloseGap` is called for a pair where no gap is open
- **THEN** `BackfillService.CloseGap` returns `nil` (BackfillLogic is idempotent)

---

### Requirement: BackfillService.Ingest writes gap-filtered messages to the outbox

`BackfillService.Ingest` SHALL apply the same request-level validations as `IngestionService.Ingest`. For each message, it SHALL check whether a gap is open for the `(connector, partition)` pair. Messages for a closed gap SHALL be silently dropped and NOT counted as rejected. Messages for an open gap go through the ingestion pipeline. After a successful pipeline write, `Backfiller.AdvanceBackfill` SHALL be called per accepted message with a non-nil watermark.

#### Scenario: All messages pass gap filter — written to outbox
- **WHEN** `BackfillService.Ingest` is called and all messages belong to partitions with open gaps
- **THEN** all messages are processed by the pipeline; `IngestResponse.partial_success.rejected_messages` reflects only schema/validation rejections

#### Scenario: No gaps open — entire batch silently dropped
- **WHEN** `BackfillService.Ingest` is called and no partition in the batch has an open gap
- **THEN** `IngestResponse` is returned with `rejected_messages = 0` and no outbox write occurs

#### Scenario: Mixed batch — some partitions have gaps, some do not
- **WHEN** `BackfillService.Ingest` is called with messages for partitions A (gap open) and B (gap closed)
- **THEN** messages for partition A go through the pipeline; messages for partition B are silently dropped; `rejected_messages` counts only schema/validation failures, not gap-filtered messages

#### Scenario: Empty messages — INVALID_ARGUMENT
- **WHEN** `BackfillService.Ingest` is called with zero messages
- **THEN** the method returns `codes.InvalidArgument`

#### Scenario: Batch exceeds MaxMessages — RESOURCE_EXHAUSTED
- **WHEN** the number of messages in the request exceeds `LimitsConfig.MaxMessages`
- **THEN** the method returns `codes.ResourceExhausted`

#### Scenario: Absent connector identity — UNAUTHENTICATED
- **WHEN** `BackfillService.Ingest` is called with no connector identity
- **THEN** the method returns `codes.Unauthenticated`

#### Scenario: IsGapOpen returns error — UNAVAILABLE
- **WHEN** `Backfiller.IsGapOpen` returns a non-nil error for any message's partition
- **THEN** the entire request fails with `codes.Unavailable` and `RetryInfo`; no outbox write occurs

#### Scenario: Pipeline returns ErrUnavailable — UNAVAILABLE
- **WHEN** the ingestion pipeline returns `ingest.ErrUnavailable` for a batch
- **THEN** the method returns `codes.Unavailable` with `RetryInfo`

#### Scenario: Pipeline returns ErrOverloaded — UNAVAILABLE
- **WHEN** the ingestion pipeline returns `ingest.ErrOverloaded`
- **THEN** the method returns `codes.Unavailable` with `RetryInfo`

#### Scenario: AdvanceBackfill fails after successful pipeline write — UNAVAILABLE
- **WHEN** the pipeline writes records to the outbox successfully but `Backfiller.AdvanceBackfill` returns a non-nil error
- **THEN** the request returns `codes.Unavailable` with `RetryInfo`; the connector retries; the pipeline write was already committed (at-least-once: the record is in the outbox and will be delivered; on retry, `AdvanceBackfill` is idempotent)

#### Scenario: Concurrent Ingest calls for the same partition
- **WHEN** two concurrent `BackfillService.Ingest` calls check `IsGapOpen` and a `CloseGap` arrives between the check and `AdvanceBackfill`
- **THEN** the message is still written to the outbox (gap was open at check time); `AdvanceBackfill` becomes a no-op (gap closed before the call); no error, no data corruption

---

### Requirement: Connector identity extraction

`BackfillService` SHALL extract the connector identity using the same priority as `IngestionService`: mTLS certificate CN takes priority; `connector-id` gRPC metadata header is used when no mTLS certificate is present. All four RPCs require a connector identity and return `UNAUTHENTICATED` when none is found.

#### Scenario: mTLS CN used when present
- **WHEN** a client connects with an mTLS certificate whose CN is `"my-connector"`
- **THEN** all four RPCs use `"my-connector"` as the connector identity

#### Scenario: connector-id header used when no mTLS
- **WHEN** a client connects without mTLS and sets the `connector-id` header to `"my-connector"`
- **THEN** all four RPCs use `"my-connector"` as the connector identity

#### Scenario: Both absent — UNAUTHENTICATED
- **WHEN** no mTLS certificate CN and no `connector-id` header is present
- **THEN** all four RPCs return `codes.Unauthenticated`

## Non-goals

- `BackfillService` does not validate watermark or partition field contents beyond being valid proto Struct values.
- No streaming RPCs — all four methods are unary.
- `BackfillService` does not implement retry logic itself.
- Ordering guarantees between live and backfill channels within the outbox.
- Observability instrumentation (metrics, tracing) for `BackfillService` RPCs.
