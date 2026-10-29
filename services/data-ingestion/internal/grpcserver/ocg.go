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
	"google.golang.org/grpc/status"
	durationpb "google.golang.org/protobuf/types/known/durationpb"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	errdetails "google.golang.org/genproto/googleapis/rpc/errdetails"
)

// OCGHandler owns the shared state for OCG receivers (ingestor, limits, retry).
// OCGReceiver holds a pointer back to OCGHandler and implements the gRPC interface.
// OCGHandler is safe for concurrent use by multiple goroutines.
type OCGHandler struct {
	ingestor ingest.Ingestor
	limits   *config.LimitsConfig
	retry    *config.RetryConfig
}

// NewOCGHandler constructs an OCGHandler. Returns an error when any
// argument is nil to catch wiring mistakes at startup, not at first RPC.
func NewOCGHandler(
	ingestor ingest.Ingestor,
	limits *config.LimitsConfig,
	retry *config.RetryConfig,
) (*OCGHandler, error) {
	if ingestor == nil {
		return nil, fmt.Errorf("grpcserver: ingestor must not be nil")
	}
	if limits == nil {
		return nil, fmt.Errorf("grpcserver: limits must not be nil")
	}
	if retry == nil {
		return nil, fmt.Errorf("grpcserver: retry must not be nil")
	}
	return &OCGHandler{ingestor: ingestor, limits: limits, retry: retry}, nil
}

// register mounts the OCG receivers onto grpcSrv.
func (h *OCGHandler) register(grpcSrv *grpc.Server) {
	ingestionv1.RegisterIngestionServiceServer(grpcSrv, NewOCGReceiver(h))
}

// ── receiver struct ──────────────────────────────────────────────────────────

// OCGReceiver implements the gRPC IngestionService.
type OCGReceiver struct {
	ingestionv1.UnimplementedIngestionServiceServer
	handler *OCGHandler
}

// NewOCGReceiver wraps an OCGHandler as an OCGReceiver that implements IngestionService.
func NewOCGReceiver(h *OCGHandler) *OCGReceiver {
	return &OCGReceiver{handler: h}
}

// Ingest implements IngestionService.Ingest.
func (recv *OCGReceiver) Ingest(ctx context.Context, req *ingestionv1.IngestRequest) (*ingestionv1.IngestResponse, error) {
	if req == nil || len(req.Messages) == 0 {
		return nil, status.Error(codes.InvalidArgument, "messages must not be empty")
	}

	if len(req.Messages) > recv.handler.limits.MaxMessages {
		return nil, status.Errorf(codes.ResourceExhausted,
			"batch exceeds maximum message count (%d)", recv.handler.limits.MaxMessages)
	}

	batch := make([]ingest.IngestRecord, len(req.Messages))
	for i, env := range req.Messages {
		batch[i] = ingest.IngestRecord{
			Schema: ingest.SchemaKey{
				LogicalArtifactType: env.GetLogicalArtifactId(),
				Version:             env.GetSchemaVersion(),
			},
			Payload:     env.GetPayload(),
			ContentType: env.GetContentType(),
		}
		if env.Partition != nil {
			// Encode partition struct as a stable JSON string used as storage key.
			b, err := json.Marshal(env.Partition.AsMap())
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid partition field: %v", err)
			}
			batch[i].Partition = string(b)
		}
		if env.Watermark != nil {
			batch[i].Watermark = env.Watermark.AsMap()
		}
	}

	result, err := recv.handler.ingestor.Ingest(ctx, batch)
	if err != nil {
		return nil, recv.handler.mapIngestError(err)
	}

	return &ingestionv1.IngestResponse{
		PartialSuccess: &ingestionv1.IngestPartialSuccess{
			RejectedMessages: int64(result.Rejected),
		},
	}, nil
}

// ── shared error mapping ─────────────────────────────────────────────────────

// mapIngestError converts pipeline errors to gRPC status errors.
func (h *OCGHandler) mapIngestError(err error) error {
	if errors.Is(err, ingest.ErrOverloaded) {
		return h.unavailableWithRetry("pipeline overloaded")
	}

	if errors.Is(err, ingest.ErrUnavailable) {
		return h.unavailableWithRetry("service unavailable")
	}

	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "request canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	}

	slog.Error("unexpected pipeline error", "err", err)
	return status.Error(codes.Internal, "internal error")
}

// unavailableWithRetry builds a codes.Unavailable status with a RetryInfo hint.
func (s *OCGHandler) unavailableWithRetry(msg string) error {
	base := status.New(codes.Unavailable, msg)
	withRetry, err := base.WithDetails(&errdetails.RetryInfo{
		RetryDelay: durationpb.New(s.retry.RetryDelay),
	})
	if err != nil {
		slog.Error("failed to attach RetryInfo to UNAVAILABLE status", "err", err)
		return base.Err()
	}
	return withRetry.Err()
}
