# Ingest Handler Specification

## Purpose

The `IngestionService.Ingest` gRPC handler is the receiving layer that accepts data from connectors, enforces configurable request-level guards, and delegates to the `Ingestor` pipeline interface. It maps pipeline results and errors to gRPC status codes without implementing pipeline logic.

## Requirements

### Requirement: Empty batch rejected before pipeline

The handler SHALL return `INVALID_ARGUMENT` when the `messages` field is absent or empty. The pipeline SHALL NOT be called.

#### Scenario: Nil request treated as empty
- **WHEN** a nil `IngestRequest` is received
- **THEN** the handler returns `INVALID_ARGUMENT` without panicking and without calling `Ingestor.Ingest`

#### Scenario: Empty messages slice rejected
- **WHEN** `IngestRequest.messages` has zero elements
- **THEN** the handler returns `INVALID_ARGUMENT` without calling `Ingestor.Ingest`

### Requirement: Message count limit enforced before pipeline

The handler SHALL return `RESOURCE_EXHAUSTED` when `IngestRequest.messages` contains more elements than `LimitsConfig.MaxMessages`. The pipeline SHALL NOT be called and no data SHALL be written.

#### Scenario: Exactly MaxMessages messages accepted
- **WHEN** `IngestRequest.messages` contains exactly `MaxMessages` elements
- **THEN** the handler accepts the batch and calls `Ingestor.Ingest`

#### Scenario: MaxMessages + 1 messages rejected
- **WHEN** `IngestRequest.messages` contains `MaxMessages + 1` elements
- **THEN** the handler returns `RESOURCE_EXHAUSTED` without calling `Ingestor.Ingest`

### Requirement: Payload size limit enforced by transport, not handler

Total payload size is enforced by `MaxRecvMsgSize` at the gRPC transport layer (see grpc-server spec). The handler SHALL NOT perform a byte-counting loop over envelope payloads. Oversized requests are rejected before the handler is invoked.

### Requirement: Handler construction fails without config

`NewIngestionServiceServer` SHALL return a non-nil error when `LimitsConfig` is absent (nil pointer or zero-value struct that would allow any batch size). The handler SHALL use `LimitsConfig` values directly and SHALL NOT fall back to hardcoded defaults.

#### Scenario: Nil limits config rejected at construction
- **WHEN** `NewIngestionServiceServer` is called with a nil `*LimitsConfig`
- **THEN** the call returns a non-nil error before any RPC handler is registered

### Requirement: Successful ingest returns partial success count

When `Ingestor.Ingest` returns a nil error, the handler SHALL return `IngestResponse` with `partial_success.rejected_messages` set to `IngestResult.Rejected`. This is a success response regardless of how many records were rejected.

#### Scenario: All records accepted
- **WHEN** `Ingestor.Ingest` returns `IngestResult{Rejected: 0}` with nil error
- **THEN** the handler returns `IngestResponse` with `partial_success.rejected_messages == 0`

#### Scenario: Some records rejected
- **WHEN** `Ingestor.Ingest` returns `IngestResult{Rejected: 2}` with nil error
- **THEN** the handler returns `IngestResponse` with `partial_success.rejected_messages == 2`; the RPC status is OK

#### Scenario: All records rejected
- **WHEN** `Ingestor.Ingest` returns `IngestResult{Rejected: N}` (all N records) with nil error
- **THEN** the handler returns `IngestResponse` with `partial_success.rejected_messages == N`; the RPC status is OK

### Requirement: Pipeline overload mapped to UNAVAILABLE with configurable retry hint

When `Ingestor.Ingest` returns an error matching `ErrOverloaded` (via `errors.Is`), the handler SHALL return gRPC status `UNAVAILABLE` with a `RetryInfo` detail whose `retry_delay` equals `RetryConfig.InitialBackoff`. The handler SHALL NOT string-match error messages to detect overload.

#### Scenario: Overload error returns UNAVAILABLE with InitialBackoff as retry hint
- **WHEN** `Ingestor.Ingest` returns an error where `errors.Is(err, ErrOverloaded)` is true
- **THEN** the handler returns gRPC status `UNAVAILABLE` with a `RetryInfo` detail whose `retry_delay` equals `RetryConfig.InitialBackoff`

#### Scenario: Wrapped overload error detected via errors.Is
- **WHEN** `Ingestor.Ingest` returns an error wrapping `ErrOverloaded` with `%w`
- **THEN** `errors.Is` detects it and the handler returns `UNAVAILABLE` with `RetryInfo`

#### Scenario: RetryInfo construction failure falls back to plain UNAVAILABLE
- **WHEN** `status.WithDetails` fails while adding `RetryInfo`
- **THEN** the handler returns plain `UNAVAILABLE` without the detail; it does not return any other status code and does not surface the `WithDetails` error to the caller

### Requirement: Unexpected pipeline errors mapped to INTERNAL without leaking details

When `Ingestor.Ingest` returns a non-nil error that is not `ErrOverloaded`, the handler SHALL return gRPC status `INTERNAL`. The gRPC error message SHALL NOT include internal stack traces, DSN strings, or other implementation details. The full error SHALL be logged server-side.

#### Scenario: Unexpected error returns INTERNAL
- **WHEN** `Ingestor.Ingest` returns a non-nil error that is not `ErrOverloaded`
- **THEN** the handler returns gRPC status `INTERNAL`

#### Scenario: Internal error detail not leaked to caller
- **WHEN** `Ingestor.Ingest` returns an error containing a DSN or stack trace
- **THEN** the gRPC error message returned to the caller does not contain the DSN or stack trace

### Requirement: Context propagated without panic

The handler SHALL propagate the request context to `Ingestor.Ingest` and SHALL NOT panic when the context is already cancelled or is cancelled during the call.

#### Scenario: Context already cancelled on entry
- **WHEN** the request context is already cancelled when the handler is invoked
- **THEN** the handler does not panic; it either calls `Ingestor.Ingest` (which propagates the cancellation) or returns an appropriate gRPC error

#### Scenario: Context cancelled during Ingestor.Ingest
- **WHEN** the context is cancelled while `Ingestor.Ingest` is executing
- **THEN** `Ingestor.Ingest` returns a context error; the handler maps it to `UNAVAILABLE`

### Requirement: Nil Ingestor rejected at construction

`NewIngestionServiceServer` SHALL return a non-nil error when `Ingestor` is nil. It SHALL NOT produce a server that panics on the first RPC call.

#### Scenario: Nil Ingestor rejected at construction
- **WHEN** `NewIngestionServiceServer(nil, validLimits, validRetry)` is called
- **THEN** the call returns a non-nil error before any RPC handler is invoked

### Requirement: Concurrent Ingest RPCs are safe

Multiple concurrent `Ingest` RPCs SHALL execute without data races. The handler itself holds no shared mutable state; concurrent safety for pipeline operations is the contract of `Ingestor.Ingest`.

#### Scenario: Concurrent RPCs do not race on handler state
- **WHEN** two goroutines invoke `Ingest` simultaneously on the same `IngestionServiceServer`
- **THEN** both complete without a data race detected by `-race`; results are independent

## Non-goals

- Pipeline implementation (`Ingestor.Ingest` body): validation, normalization, outbox write — separate ticket.
- Connector identity extraction from mTLS CN or `connector-id` header — separate interceptor ticket.
- `BackfillService` handler — separate ticket.
- OTLP receiver — separate ticket.
- Per-record size limits: only total request payload is enforced here.
- Per-client exponential backoff tracking: the server always returns `InitialBackoff` as the hint; `Multiplier` and `MaxBackoff` are validated but not used to compute per-RPC delay.
- Deduplication, metrics, tracing.
