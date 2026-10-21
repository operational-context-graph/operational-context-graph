// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	durationpb "google.golang.org/protobuf/types/known/durationpb"

	errdetails "google.golang.org/genproto/googleapis/rpc/errdetails"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

const (
	otlpContentType   = "application/json"
	metaArtifactType  = "logical_artifact_type"
	metaSchemaVersion = "schema_version"
)

// OTLPHandler owns the shared state for the three OTLP receiver types (Logs,
// Metrics, Traces). Each signal type has its own receiver struct that embeds the
// Unimplemented*Server and holds a pointer back to OTLPHandler.
// OTLPHandler is safe for concurrent use by multiple goroutines.
// SchemaRegistryClient implementations provided at construction MUST also be safe
// for concurrent use.
type OTLPHandler struct {
	schemaClient SchemaRegistryClient
	ingestor     ingest.Ingestor
	retry        *config.RetryConfig
}

// newOTLPHandler constructs an OTLPHandler. All arguments must not be nil.
func newOTLPHandler(schemaClient SchemaRegistryClient, ingestor ingest.Ingestor, retry *config.RetryConfig) (*OTLPHandler, error) {
	if schemaClient == nil {
		return nil, fmt.Errorf("grpcserver: otlp handler: schemaClient must not be nil")
	}
	if ingestor == nil {
		return nil, fmt.Errorf("grpcserver: otlp handler: ingestor must not be nil")
	}
	if retry == nil {
		return nil, fmt.Errorf("grpcserver: otlp handler: retry must not be nil")
	}
	return &OTLPHandler{schemaClient: schemaClient, ingestor: ingestor, retry: retry}, nil
}

// ── OTLPHandler methods ───────────────────────────────────────────────────────

// register mounts the three OTLP collector services onto grpcSrv.
func (handler *OTLPHandler) register(grpcSrv *grpc.Server) {
	collectorlogsv1.RegisterLogsServiceServer(grpcSrv, &otlpLogsReceiver{handler: handler})
	collectormetricsv1.RegisterMetricsServiceServer(grpcSrv, &otlpMetricsReceiver{handler: handler})
	collectortracesv1.RegisterTraceServiceServer(grpcSrv, &otlpTracesReceiver{handler: handler})
}

func (handler *OTLPHandler) exportLogs(ctx context.Context, rls []*logsv1.ResourceLogs) error {
	if err := ctx.Err(); err != nil {
		return ctxStatusError(err)
	}

	var payloads [][]byte
	for _, resourceLogs := range rls {
		resJSON, merr := marshalResourceJSON(resourceLogs.GetResource())
		if merr != nil {
			return merr
		}
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			for _, logRecord := range scopeLogs.GetLogRecords() {
				payload, merr := marshalEnvelope(resJSON, "logRecord", logRecord)
				if merr != nil {
					return merr
				}
				payloads = append(payloads, payload)
			}
		}
	}
	if len(payloads) == 0 {
		return nil
	}

	key, err := schemaFromMetadata(ctx, handler.schemaClient.DefaultLogSchema)
	if err != nil {
		return otlpUnavailable(handler.retry)
	}
	return handler.ingest(ctx, key, payloadsToRecords(key, payloads))
}

func (handler *OTLPHandler) exportMetrics(ctx context.Context, rms []*metricsv1.ResourceMetrics) error {
	if err := ctx.Err(); err != nil {
		return ctxStatusError(err)
	}

	var payloads [][]byte
	for _, resourceMetrics := range rms {
		resJSON, merr := marshalResourceJSON(resourceMetrics.GetResource())
		if merr != nil {
			return merr
		}
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			for _, metric := range scopeMetrics.GetMetrics() {
				payload, merr := marshalEnvelope(resJSON, "metric", metric)
				if merr != nil {
					return merr
				}
				payloads = append(payloads, payload)
			}
		}
	}
	if len(payloads) == 0 {
		return nil
	}

	key, err := schemaFromMetadata(ctx, handler.schemaClient.DefaultMetricSchema)
	if err != nil {
		return otlpUnavailable(handler.retry)
	}
	return handler.ingest(ctx, key, payloadsToRecords(key, payloads))
}

func (handler *OTLPHandler) exportTraces(ctx context.Context, rss []*tracev1.ResourceSpans) error {
	if err := ctx.Err(); err != nil {
		return ctxStatusError(err)
	}

	var payloads [][]byte
	for _, resourceSpans := range rss {
		resJSON, merr := marshalResourceJSON(resourceSpans.GetResource())
		if merr != nil {
			return merr
		}
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				payload, merr := marshalEnvelope(resJSON, "span", span)
				if merr != nil {
					return merr
				}
				payloads = append(payloads, payload)
			}
		}
	}
	if len(payloads) == 0 {
		return nil
	}

	key, err := schemaFromMetadata(ctx, handler.schemaClient.DefaultTraceSchema)
	if err != nil {
		return otlpUnavailable(handler.retry)
	}
	return handler.ingest(ctx, key, payloadsToRecords(key, payloads))
}

func (handler *OTLPHandler) ingest(ctx context.Context, key SchemaKey, batch []ingest.IngestRecord) error {
	result, err := handler.ingestor.Ingest(ctx, batch)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
		}
		if errors.Is(err, context.Canceled) {
			return status.Error(codes.Canceled, "request canceled")
		}
		if errors.Is(err, ingest.ErrOverloaded) {
			return otlpUnavailable(handler.retry)
		}
		slog.Error("unexpected pipeline error", "err", err)
		return status.Error(codes.Internal, "internal error")
	}
	if result.Rejected > 0 {
		slog.Warn("otlp export: pipeline rejected records",
			"accepted", result.Accepted,
			"rejected", result.Rejected,
			"schema", key.LogicalArtifactID+"@"+key.Version,
		)
	}
	return nil
}

// ── receiver structs ──────────────────────────────────────────────────────────

type otlpLogsReceiver struct {
	collectorlogsv1.UnimplementedLogsServiceServer
	handler *OTLPHandler
}

type otlpMetricsReceiver struct {
	collectormetricsv1.UnimplementedMetricsServiceServer
	handler *OTLPHandler
}

type otlpTracesReceiver struct {
	collectortracesv1.UnimplementedTraceServiceServer
	handler *OTLPHandler
}

// ── receiver Export methods ───────────────────────────────────────────────────

func (recv *otlpLogsReceiver) Export(
	ctx context.Context,
	req *collectorlogsv1.ExportLogsServiceRequest,
) (*collectorlogsv1.ExportLogsServiceResponse, error) {
	if len(req.GetResourceLogs()) == 0 {
		return &collectorlogsv1.ExportLogsServiceResponse{}, nil
	}
	if err := recv.handler.exportLogs(ctx, req.GetResourceLogs()); err != nil {
		return nil, err
	}
	return &collectorlogsv1.ExportLogsServiceResponse{}, nil
}

func (recv *otlpMetricsReceiver) Export(
	ctx context.Context,
	req *collectormetricsv1.ExportMetricsServiceRequest,
) (*collectormetricsv1.ExportMetricsServiceResponse, error) {
	if len(req.GetResourceMetrics()) == 0 {
		return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
	}
	if err := recv.handler.exportMetrics(ctx, req.GetResourceMetrics()); err != nil {
		return nil, err
	}
	return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
}

func (recv *otlpTracesReceiver) Export(
	ctx context.Context,
	req *collectortracesv1.ExportTraceServiceRequest,
) (*collectortracesv1.ExportTraceServiceResponse, error) {
	if len(req.GetResourceSpans()) == 0 {
		return &collectortracesv1.ExportTraceServiceResponse{}, nil
	}
	if err := recv.handler.exportTraces(ctx, req.GetResourceSpans()); err != nil {
		return nil, err
	}
	return &collectortracesv1.ExportTraceServiceResponse{}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

var pjsonOpts = protojson.MarshalOptions{EmitUnpopulated: false}

// marshalResourceJSON returns the protojson encoding of msg as raw JSON bytes.
// Returns "null" when msg is nil (resource is optional in OTLP).
func marshalResourceJSON(msg proto.Message) (json.RawMessage, error) {
	if msg == nil || !msg.ProtoReflect().IsValid() {
		return json.RawMessage("null"), nil
	}
	out, err := pjsonOpts.Marshal(msg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal OTLP resource: %v", err)
	}
	return out, nil
}

// marshalEnvelope produces {"resource":<resourceJSON>,"<recordKey>":<recordJSON>}.
func marshalEnvelope(resourceJSON json.RawMessage, recordKey string, record proto.Message) ([]byte, error) {
	recordJSON, err := pjsonOpts.Marshal(record)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal OTLP record: %v", err)
	}
	env := map[string]json.RawMessage{
		"resource": resourceJSON,
		recordKey:  recordJSON,
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal OTLP envelope: %v", err)
	}
	return out, nil
}

func payloadsToRecords(key SchemaKey, payloads [][]byte) []ingest.IngestRecord {
	ct := otlpContentType
	records := make([]ingest.IngestRecord, len(payloads))
	for i, payload := range payloads {
		records[i] = ingest.IngestRecord{
			Schema: ingest.SchemaKey{
				LogicalArtifactType: key.LogicalArtifactID,
				Version:             key.Version,
			},
			Payload:     payload,
			ContentType: ct,
		}
	}
	return records
}

// ctxStatusError maps a context error to the matching gRPC status error.
func ctxStatusError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	}
	return status.Error(codes.Canceled, "request canceled")
}

// schemaFromMetadata reads logical_artifact_type and schema_version from gRPC
// incoming metadata. Each field falls back independently to defaultFn when absent
// or empty. defaultFn is called at most once per invocation.
func schemaFromMetadata(
	ctx context.Context,
	defaultFn func(context.Context) (SchemaKey, error),
) (SchemaKey, error) {
	md, _ := metadata.FromIncomingContext(ctx)

	artifactType := firstNonEmpty(md.Get(metaArtifactType))
	schemaVersion := firstNonEmpty(md.Get(metaSchemaVersion))

	if artifactType != "" && schemaVersion != "" {
		return SchemaKey{LogicalArtifactID: artifactType, Version: schemaVersion}, nil
	}

	// At least one field needs the default — call defaultFn exactly once.
	def, err := defaultFn(ctx)
	if err != nil {
		return SchemaKey{}, err
	}

	if artifactType == "" {
		artifactType = def.LogicalArtifactID
	}
	if schemaVersion == "" {
		schemaVersion = def.Version
	}
	return SchemaKey{LogicalArtifactID: artifactType, Version: schemaVersion}, nil
}

// firstNonEmpty returns the first non-empty string in vs, or "".
func firstNonEmpty(vs []string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// otlpUnavailable returns codes.Unavailable with a RetryInfo hint.
func otlpUnavailable(retry *config.RetryConfig) error {
	base := status.New(codes.Unavailable, "pipeline unavailable")
	withRetry, attachErr := base.WithDetails(&errdetails.RetryInfo{
		RetryDelay: durationpb.New(retry.InitialBackoff),
	})
	if attachErr != nil {
		slog.Error("failed to attach RetryInfo", "err", attachErr)
		return base.Err()
	}
	return withRetry.Err()
}
