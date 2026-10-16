// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	durationpb "google.golang.org/protobuf/types/known/durationpb"

	errdetails "google.golang.org/genproto/googleapis/rpc/errdetails"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

const (
	otlpContentType   = "application/x-protobuf"
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

// register mounts the three OTLP collector services onto grpcSrv.
func (h *OTLPHandler) register(grpcSrv *grpc.Server) {
	collectorlogsv1.RegisterLogsServiceServer(grpcSrv, &otlpLogsReceiver{recv: h})
	collectormetricsv1.RegisterMetricsServiceServer(grpcSrv, &otlpMetricsReceiver{recv: h})
	collectortracesv1.RegisterTraceServiceServer(grpcSrv, &otlpTracesReceiver{recv: h})
}

// ── receiver structs ─────────────────────────────────────────────────────────

type otlpLogsReceiver struct {
	collectorlogsv1.UnimplementedLogsServiceServer
	recv *OTLPHandler
}

type otlpMetricsReceiver struct {
	collectormetricsv1.UnimplementedMetricsServiceServer
	recv *OTLPHandler
}

type otlpTracesReceiver struct {
	collectortracesv1.UnimplementedTraceServiceServer
	recv *OTLPHandler
}

// ── Export methods ───────────────────────────────────────────────────────────

func (recv *otlpLogsReceiver) Export(
	ctx context.Context,
	req *collectorlogsv1.ExportLogsServiceRequest,
) (*collectorlogsv1.ExportLogsServiceResponse, error) {
	if len(req.GetResourceLogs()) == 0 {
		return &collectorlogsv1.ExportLogsServiceResponse{}, nil
	}
	if err := recv.recv.export(ctx, req, recv.recv.schemaClient.DefaultLogSchema); err != nil {
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
	if err := recv.recv.export(ctx, req, recv.recv.schemaClient.DefaultMetricSchema); err != nil {
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
	if err := recv.recv.export(ctx, req, recv.recv.schemaClient.DefaultTraceSchema); err != nil {
		return nil, err
	}
	return &collectortracesv1.ExportTraceServiceResponse{}, nil
}

// ── core export logic ────────────────────────────────────────────────────────

// export resolves the SchemaKey, marshals the request, and calls Ingestor.Ingest.
// defaultFn is called at most once when headers are absent or empty.
func (h *OTLPHandler) export(
	ctx context.Context,
	msg proto.Message,
	defaultFn func(context.Context) (SchemaKey, error),
) error {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
		}
		return status.Error(codes.Canceled, "request canceled")
	}

	key, err := schemaFromMetadata(ctx, defaultFn)
	if err != nil {
		return otlpUnavailable(h.retry)
	}

	payload, err := proto.Marshal(msg)
	if err != nil {
		return status.Errorf(codes.Internal, "marshal OTLP request: %v", err)
	}

	ct := otlpContentType
	record := ingest.IngestRecord{
		LogicalArtifactID: key.LogicalArtifactID,
		SchemaVersion:     key.Version,
		Payload:           payload,
		ContentType:       &ct,
	}

	result, err := h.ingestor.Ingest(ctx, []ingest.IngestRecord{record})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
		}
		if errors.Is(err, context.Canceled) {
			return status.Error(codes.Canceled, "request canceled")
		}
		if errors.Is(err, ingest.ErrOverloaded) {
			return otlpUnavailable(h.retry)
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

// ── helpers ──────────────────────────────────────────────────────────────────

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
