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

// OTLPReceiver accepts standard OTLP gRPC exports (Logs, Metrics, Traces),
// resolves a SchemaKey, and delegates each export to the ingestion pipeline.
// OTLPReceiver is safe for concurrent use by multiple goroutines.
// SchemaRegistryClient implementations provided at construction MUST also be safe
// for concurrent use.
type OTLPReceiver struct {
	schemaClient SchemaRegistryClient
	ingestor     ingest.Ingestor
	retry        *config.RetryConfig
}

// newOTLPReceiver constructs an OTLPReceiver. All arguments must not be nil.
func newOTLPReceiver(schemaClient SchemaRegistryClient, ingestor ingest.Ingestor, retry *config.RetryConfig) (*OTLPReceiver, error) {
	if schemaClient == nil {
		return nil, fmt.Errorf("grpcserver: otlp receiver: schemaClient must not be nil")
	}
	if ingestor == nil {
		return nil, fmt.Errorf("grpcserver: otlp receiver: ingestor must not be nil")
	}
	if retry == nil {
		return nil, fmt.Errorf("grpcserver: otlp receiver: retry must not be nil")
	}
	return &OTLPReceiver{schemaClient: schemaClient, ingestor: ingestor, retry: retry}, nil
}

// register mounts the three OTLP collector services onto s.
func (r *OTLPReceiver) register(s *grpc.Server) {
	collectorlogsv1.RegisterLogsServiceServer(s, &otlpLogsHandler{r: r, ingestor: r.ingestor})
	collectormetricsv1.RegisterMetricsServiceServer(s, &otlpMetricsHandler{r: r, ingestor: r.ingestor})
	collectortracesv1.RegisterTraceServiceServer(s, &otlpTracesHandler{r: r, ingestor: r.ingestor})
}

// ── handler structs ──────────────────────────────────────────────────────────

type otlpLogsHandler struct {
	collectorlogsv1.UnimplementedLogsServiceServer
	r        *OTLPReceiver
	ingestor ingest.Ingestor
}

type otlpMetricsHandler struct {
	collectormetricsv1.UnimplementedMetricsServiceServer
	r        *OTLPReceiver
	ingestor ingest.Ingestor
}

type otlpTracesHandler struct {
	collectortracesv1.UnimplementedTraceServiceServer
	r        *OTLPReceiver
	ingestor ingest.Ingestor
}

// ── Export methods ───────────────────────────────────────────────────────────

func (h *otlpLogsHandler) Export(
	ctx context.Context,
	req *collectorlogsv1.ExportLogsServiceRequest,
) (*collectorlogsv1.ExportLogsServiceResponse, error) {
	if len(req.GetResourceLogs()) == 0 {
		return &collectorlogsv1.ExportLogsServiceResponse{}, nil
	}
	if err := h.r.export(ctx, req, h.r.schemaClient.DefaultLogSchema, h.ingestor); err != nil {
		return nil, err
	}
	return &collectorlogsv1.ExportLogsServiceResponse{}, nil
}

func (h *otlpMetricsHandler) Export(
	ctx context.Context,
	req *collectormetricsv1.ExportMetricsServiceRequest,
) (*collectormetricsv1.ExportMetricsServiceResponse, error) {
	if len(req.GetResourceMetrics()) == 0 {
		return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
	}
	if err := h.r.export(ctx, req, h.r.schemaClient.DefaultMetricSchema, h.ingestor); err != nil {
		return nil, err
	}
	return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
}

func (h *otlpTracesHandler) Export(
	ctx context.Context,
	req *collectortracesv1.ExportTraceServiceRequest,
) (*collectortracesv1.ExportTraceServiceResponse, error) {
	if len(req.GetResourceSpans()) == 0 {
		return &collectortracesv1.ExportTraceServiceResponse{}, nil
	}
	if err := h.r.export(ctx, req, h.r.schemaClient.DefaultTraceSchema, h.ingestor); err != nil {
		return nil, err
	}
	return &collectortracesv1.ExportTraceServiceResponse{}, nil
}

// ── core export logic ────────────────────────────────────────────────────────

// export resolves the SchemaKey, marshals the request, and calls Ingestor.Ingest.
// defaultFn is called at most once when headers are absent or empty.
func (r *OTLPReceiver) export(
	ctx context.Context,
	msg proto.Message,
	defaultFn func(context.Context) (SchemaKey, error),
	ingestor ingest.Ingestor,
) error {
	if err := ctx.Err(); err != nil {
		return status.Error(codes.Canceled, "request context done")
	}

	key, err := schemaFromMetadata(ctx, defaultFn)
	if err != nil {
		return otlpUnavailable(err, r.retry)
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

	result, err := ingestor.Ingest(ctx, []ingest.IngestRecord{record})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return status.Error(codes.Canceled, "request context done")
		}
		return otlpUnavailable(err, r.retry)
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

// otlpUnavailable wraps err as codes.Unavailable with a RetryInfo extension.
func otlpUnavailable(err error, retry *config.RetryConfig) error {
	base := status.Newf(codes.Unavailable, "pipeline unavailable: %v", err)
	withRetry, attachErr := base.WithDetails(&errdetails.RetryInfo{
		RetryDelay: durationpb.New(retry.InitialBackoff),
	})
	if attachErr != nil {
		slog.Error("failed to attach RetryInfo", "err", attachErr)
		return base.Err()
	}
	return withRetry.Err()
}
