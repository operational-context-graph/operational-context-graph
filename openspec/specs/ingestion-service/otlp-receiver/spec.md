# ingestion-service/otlp-receiver Specification

## Purpose
The OTLP receiver accepts standard OTLP gRPC exports (Logs, Metrics, Traces) from
OTel SDKs and collectors, resolves a `SchemaKey` from request metadata or telemetry
defaults, and delegates one `IngestRecord` per leaf record (`LogRecord`, `Metric`, `Span`)
to the ingestion pipeline. Each record carries its resource attributes as a JSON envelope.

## Requirements

### Requirement: OTLP gRPC services registered on shared server

The receiver SHALL implement `LogsService.Export`, `MetricsService.Export`, and
`TraceService.Export` as defined by the OTLP collector proto. All three services SHALL
be registered on the same `grpc.Server` instance as `IngestionService`. They SHALL share
the same mTLS configuration and payload size limit enforced by the server.

#### Scenario: Logs export accepted
- **WHEN** an OTLP client sends a valid `ExportLogsServiceRequest` with one or more `ResourceLogs`
- **THEN** the receiver returns `ExportLogsServiceResponse{}` with a nil error

#### Scenario: Metrics export accepted
- **WHEN** an OTLP client sends a valid `ExportMetricsServiceRequest` with one or more `ResourceMetrics`
- **THEN** the receiver returns `ExportMetricsServiceResponse{}` with a nil error

#### Scenario: Traces export accepted
- **WHEN** an OTLP client sends a valid `ExportTraceServiceRequest` with one or more `ResourceSpans`
- **THEN** the receiver returns `ExportTraceServiceResponse{}` with a nil error

#### Scenario: Concurrent exports from two goroutines
- **WHEN** two goroutines call `ExportLogs` simultaneously on the same receiver instance
- **THEN** both calls complete without data races or panics; both delegate to `Ingestor.Ingest` independently

#### Scenario: Concurrent exports across signal types
- **WHEN** one goroutine calls `ExportLogs` while another calls `ExportMetrics` simultaneously
- **THEN** both calls complete without data races or panics

### Requirement: SchemaKey resolved from gRPC metadata with per-signal defaults

Each Export handler SHALL read `logical_artifact_type` and `schema_version` from gRPC
incoming metadata. When a header is absent or present but an empty string, the handler
SHALL call `SchemaRegistryClient.DefaultLogSchema`, `DefaultMetricSchema`, or
`DefaultTraceSchema` respectively to obtain the default `SchemaKey`. Header resolution
and default-schema lookup SHALL each occur once per Export call, not once per OTLP
record within a call. Schema resolution SHALL occur after flattening leaf records: if
the export contains no leaf records, `SchemaRegistryClient` SHALL NOT be called.

#### Scenario: Both headers present and non-empty — used verbatim
- **WHEN** incoming metadata contains `logical_artifact_type: "ocg.custom.logs"` and `schema_version: "2.0.0"`
- **THEN** `IngestRecord.LogicalArtifactID` is `"ocg.custom.logs"` and `SchemaVersion` is `"2.0.0"`; `SchemaRegistryClient` is NOT called

#### Scenario: Both headers absent — per-signal default used
- **WHEN** incoming metadata contains neither `logical_artifact_type` nor `schema_version` for a Logs export
- **THEN** the handler calls `DefaultLogSchema` once and uses the returned `SchemaKey` for all records in that export

#### Scenario: `logical_artifact_type` present, `schema_version` absent — partial fallback
- **WHEN** metadata contains `logical_artifact_type: "ocg.custom"` but no `schema_version`
- **THEN** `LogicalArtifactID` is `"ocg.custom"` and `SchemaVersion` is the default from `DefaultLogSchema`

#### Scenario: Header present but empty string — treated as absent
- **WHEN** metadata contains `logical_artifact_type: ""` and `schema_version: ""`
- **THEN** the handler falls back to the per-signal default as if neither header were present; `SchemaRegistryClient` IS called

#### Scenario: Default schema resolved once per call, not per record
- **WHEN** an export contains multiple OTLP records and no schema headers are set
- **THEN** `DefaultLogSchema` (or metric/trace equivalent) is called exactly once for that Export call; the returned `SchemaKey` is applied to all records in the call

#### Scenario: `DefaultLogSchema` returns an error
- **WHEN** `SchemaRegistryClient.DefaultLogSchema` returns a non-nil error
- **THEN** the handler returns `codes.Unavailable` with a `google.rpc.RetryInfo` extension; `Ingestor.Ingest` is NOT called

### Requirement: Empty OTLP export returns success without calling pipeline

When the request has no top-level resource slices, or all resource containers contain no
leaf records (`LogRecord`, `Metric`, `Span`), the receiver SHALL return a successful empty
response immediately. `Ingestor.Ingest` SHALL NOT be called. `SchemaRegistryClient` SHALL
NOT be called when there are no leaf records to process.

#### Scenario: Empty ResourceLogs slice
- **WHEN** `ExportLogsServiceRequest.ResourceLogs` is empty
- **THEN** the handler returns `ExportLogsServiceResponse{}` with nil error; neither `Ingestor.Ingest` nor `SchemaRegistryClient` is called

#### Scenario: ResourceLogs present but all ScopeLogs are empty
- **WHEN** `ExportLogsServiceRequest` contains `ResourceLogs` entries but none contain any `LogRecord`s
- **THEN** the handler returns `ExportLogsServiceResponse{}` with nil error; neither `Ingestor.Ingest` nor `SchemaRegistryClient` is called

#### Scenario: Empty ResourceMetrics
- **WHEN** `ExportMetricsServiceRequest.ResourceMetrics` is empty
- **THEN** the handler returns `ExportMetricsServiceResponse{}` with nil error

#### Scenario: Empty ResourceSpans
- **WHEN** `ExportTraceServiceRequest.ResourceSpans` is empty
- **THEN** the handler returns `ExportTraceServiceResponse{}` with nil error

### Requirement: One IngestRecord per leaf OTLP record with resource context

The receiver SHALL produce one `ingest.IngestRecord` per leaf record: one per `LogRecord`,
one per `Metric`, and one per `Span`. Each record's `Payload` SHALL be a JSON object with
two fields: `"resource"` (the protojson-encoded `Resource` for that leaf's
`ResourceLogs`/`ResourceMetrics`/`ResourceSpans`, or `null` when the resource is absent)
and a signal-type key (`"logRecord"`, `"metric"`, or `"span"`) containing the
protojson-encoded leaf record. `Partition` SHALL be an empty string and `Watermark` SHALL
be nil. `ContentType` SHALL be `"application/json"`.

#### Scenario: IngestRecord fields for a logs export
- **WHEN** a valid `ExportLogsServiceRequest` is exported with schema headers set and contains one `ResourceLogs` with one `LogRecord`
- **THEN** `Ingestor.Ingest` is called with one record where `Partition == ""`, `Watermark == nil`, `ContentType == "application/json"`, and `Payload` is a JSON object containing `"resource"` and `"logRecord"` keys

#### Scenario: Two ResourceLogs with one LogRecord each produce two IngestRecords
- **WHEN** `ExportLogsServiceRequest` contains two `ResourceLogs`, each with one `LogRecord`
- **THEN** `Ingestor.Ingest` is called with two records; each carries its respective resource's JSON under `"resource"`

#### Scenario: protojson marshal failure
- **WHEN** protojson marshalling of an OTLP resource or leaf record returns an error
- **THEN** the handler returns `codes.Internal`; `Ingestor.Ingest` is NOT called

### Requirement: Ingestor errors mapped to retryable status codes

Non-nil errors from `Ingestor.Ingest` SHALL be mapped to gRPC status codes: `ErrOverloaded`
→ `Unavailable` + `RetryInfo`; `context.DeadlineExceeded` → `DeadlineExceeded`;
`context.Canceled` → `Canceled`; unexpected → `Internal` (logged). The same mapping
applies when `ctx.Err()` is non-nil before `Ingestor.Ingest` is called. `Unavailable`
responses use the fixed message `"pipeline unavailable"` with no internal error detail.

#### Scenario: Ingestor returns back-pressure error (ErrOverloaded)
- **WHEN** `Ingestor.Ingest` returns `ingest.ErrOverloaded`
- **THEN** the handler returns `codes.Unavailable` with `RetryInfo` in the status details; no internal error text is included in the status message

#### Scenario: Ingestor returns unexpected error
- **WHEN** `Ingestor.Ingest` returns a non-nil error that is not `ErrOverloaded`, `context.DeadlineExceeded`, or `context.Canceled`
- **THEN** the handler logs the error and returns `codes.Internal` with message `"internal error"`

#### Scenario: Context deadline exceeded before Ingestor call
- **WHEN** `ctx.Err()` returns `context.DeadlineExceeded` before `Ingestor.Ingest` is called
- **THEN** the handler returns `codes.DeadlineExceeded` with message `"request deadline exceeded"`

#### Scenario: Context cancelled before Ingestor call
- **WHEN** the request context is cancelled (non-deadline) before `Ingestor.Ingest` is called
- **THEN** the handler returns `codes.Canceled`

#### Scenario: Context cancelled during Ingestor call
- **WHEN** the request context is cancelled while `Ingestor.Ingest` is executing
- **THEN** the handler returns `codes.Canceled`

### Requirement: Pipeline rejections logged but not surfaced in OTLP response

When `Ingestor.Ingest` returns `IngestResult.Rejected > 0`, the receiver SHALL log the
rejection count. The Export response SHALL still be a success (empty response, nil error).
The OTLP protocol does not expose per-record rejection to the exporter.

#### Scenario: All records rejected by pipeline
- **WHEN** `Ingestor.Ingest` returns `IngestResult{Accepted: 0, Rejected: 1}` with nil error
- **THEN** the handler returns a successful empty Export response; the rejection is logged

#### Scenario: Partial rejection
- **WHEN** `Ingestor.Ingest` returns `IngestResult{Accepted: 1, Rejected: 1}` with nil error
- **THEN** the handler returns a successful empty Export response; the rejection count is logged

### Requirement: Constructor rejects nil dependencies

`newOTLPHandler(schemaClient, ingestor, retry)` is an unexported constructor called
by `grpcserver.NewServer`. It SHALL return a non-nil error when any of `schemaClient`,
`ingestor`, or `retry` is nil. It SHALL NOT panic inside a handler on nil dereference.
The retry configuration is stored on `OTLPHandler` and used to populate `RetryInfo`
in `Unavailable` responses.

#### Scenario: Nil Ingestor rejected at construction
- **WHEN** `newOTLPHandler` is called with a nil `ingest.Ingestor`
- **THEN** the call returns a non-nil error; no gRPC services are registered

#### Scenario: Nil SchemaRegistryClient rejected at construction
- **WHEN** `newOTLPHandler` is called with a nil `SchemaRegistryClient`
- **THEN** the call returns a non-nil error; no gRPC services are registered

#### Scenario: Nil RetryConfig rejected at construction
- **WHEN** `newOTLPHandler` is called with a nil `*config.RetryConfig`
- **THEN** the call returns a non-nil error; no gRPC services are registered

#### Scenario: All dependencies valid — services registered successfully
- **WHEN** `newOTLPHandler` is called with non-nil `schemaClient`, `ingestor`, and `retry`
- **THEN** the call returns a non-nil `OTLPHandler` and nil error; three OTLP services can be registered on a `grpc.Server`

### Requirement: `SchemaRegistryClient` precondition — concurrent-safe

Implementations of `SchemaRegistryClient` provided to `OTLPReceiver` MUST be safe for
concurrent use by multiple goroutines. This is a caller precondition, not enforced by
the receiver.

#### Scenario: Two concurrent DefaultLogSchema calls
- **WHEN** two goroutines call `DefaultLogSchema` on the same client simultaneously
- **THEN** both return consistent results without data races (verified by `-race` in tests)
