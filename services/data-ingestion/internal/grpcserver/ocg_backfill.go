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
	"google.golang.org/protobuf/types/known/structpb"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// OCGBackfillHandler owns the shared state for the BackfillService gRPC
// receiver. It is safe for concurrent use by multiple goroutines.
type OCGBackfillHandler struct {
	backfiller Backfiller
	ingestor   ingest.Ingestor
	limits     *config.LimitsConfig
	retry      *config.RetryConfig
}

// NewOCGBackfillHandler constructs an OCGBackfillHandler. Returns an error when
// any argument is nil to catch wiring mistakes at startup, not at first RPC.
func NewOCGBackfillHandler(
	backfiller Backfiller,
	ingestor ingest.Ingestor,
	limits *config.LimitsConfig,
	retry *config.RetryConfig,
) (*OCGBackfillHandler, error) {
	if backfiller == nil {
		return nil, fmt.Errorf("grpcserver: backfiller must not be nil")
	}
	if ingestor == nil {
		return nil, fmt.Errorf("grpcserver: ingestor must not be nil")
	}
	if limits == nil {
		return nil, fmt.Errorf("grpcserver: limits must not be nil")
	}
	if retry == nil {
		return nil, fmt.Errorf("grpcserver: retry must not be nil")
	}
	return &OCGBackfillHandler{
		backfiller: backfiller,
		ingestor:   ingestor,
		limits:     limits,
		retry:      retry,
	}, nil
}

// register mounts the BackfillService receiver onto grpcSrv.
func (h *OCGBackfillHandler) register(grpcSrv *grpc.Server) {
	ingestionv1.RegisterBackfillServiceServer(grpcSrv, NewOCGBackfillReceiver(h))
}

// unavailableWithRetry builds a codes.Unavailable status with a RetryInfo hint.
func (h *OCGBackfillHandler) unavailableWithRetry(msg string) error {
	return unavailableWithRetry(msg, h.retry)
}

// ── receiver struct ───────────────────────────────────────────────────────────

// OCGBackfillReceiver implements the gRPC BackfillService.
type OCGBackfillReceiver struct {
	ingestionv1.UnimplementedBackfillServiceServer
	handler *OCGBackfillHandler
}

// NewOCGBackfillReceiver wraps an OCGBackfillHandler as an OCGBackfillReceiver
// that implements BackfillService.
func NewOCGBackfillReceiver(h *OCGBackfillHandler) *OCGBackfillReceiver {
	return &OCGBackfillReceiver{handler: h}
}

// GetWatermark implements BackfillService.GetWatermark.
func (recv *OCGBackfillReceiver) GetWatermark(ctx context.Context, req *ingestionv1.GetWatermarkRequest) (*ingestionv1.GetWatermarkResponse, error) {
	connectorID := connectorIDFromContext(ctx)
	if connectorID == "" {
		return nil, status.Error(codes.Unauthenticated, "connector identity required")
	}

	partitionKey, err := partitionToKey(req.GetPartition())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid partition field: %v", err)
	}

	wm, err := recv.handler.backfiller.GetWatermark(ctx, connectorID, partitionKey)
	if err != nil {
		return nil, recv.handler.unavailableWithRetry("watermark lookup failed")
	}
	if wm == nil {
		return &ingestionv1.GetWatermarkResponse{}, nil
	}

	wmStruct, err := structpb.NewStruct(wm)
	if err != nil {
		return nil, recv.handler.unavailableWithRetry("watermark encoding failed")
	}
	return &ingestionv1.GetWatermarkResponse{Watermark: wmStruct}, nil
}

// StartGap implements BackfillService.StartGap.
func (recv *OCGBackfillReceiver) StartGap(ctx context.Context, req *ingestionv1.StartGapRequest) (*ingestionv1.StartGapResponse, error) {
	connectorID := connectorIDFromContext(ctx)
	if connectorID == "" {
		return nil, status.Error(codes.Unauthenticated, "connector identity required")
	}

	partitionKey, err := partitionToKey(req.GetPartition())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid partition field: %v", err)
	}

	if err := recv.handler.backfiller.StartGap(ctx, connectorID, partitionKey); err != nil {
		return nil, recv.handler.unavailableWithRetry("start gap failed")
	}
	return &ingestionv1.StartGapResponse{}, nil
}

// CloseGap implements BackfillService.CloseGap.
func (recv *OCGBackfillReceiver) CloseGap(ctx context.Context, req *ingestionv1.CloseGapRequest) (*ingestionv1.CloseGapResponse, error) {
	connectorID := connectorIDFromContext(ctx)
	if connectorID == "" {
		return nil, status.Error(codes.Unauthenticated, "connector identity required")
	}

	partitionKey, err := partitionToKey(req.GetPartition())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid partition field: %v", err)
	}

	if err := recv.handler.backfiller.CloseGap(ctx, connectorID, partitionKey); err != nil {
		return nil, recv.handler.unavailableWithRetry("close gap failed")
	}
	return &ingestionv1.CloseGapResponse{}, nil
}

// Ingest implements BackfillService.Ingest.
func (recv *OCGBackfillReceiver) Ingest(ctx context.Context, req *ingestionv1.IngestRequest) (*ingestionv1.IngestResponse, error) {
	if req == nil || len(req.Messages) == 0 {
		return nil, status.Error(codes.InvalidArgument, "messages must not be empty")
	}

	connectorID := connectorIDFromContext(ctx)
	if connectorID == "" {
		return nil, status.Error(codes.Unauthenticated, "connector identity required")
	}

	if len(req.Messages) > recv.handler.limits.MaxMessages {
		return nil, status.Errorf(codes.ResourceExhausted,
			"batch exceeds maximum message count (%d)", recv.handler.limits.MaxMessages)
	}

	// Single pass: decode, gap-check (cached per partition), build sub-batch.
	gapCache := make(map[string]bool)
	batch := make([]ingest.IngestRecord, 0, len(req.Messages))
	openPartitions := 0 // distinct partitions with an open gap present in batch

	for _, msg := range req.Messages {
		partKey, err := partitionToKey(msg.Partition)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid partition field: %v", err)
		}

		gapOpen, ok := gapCache[partKey]
		if !ok {
			gapOpen, err = recv.handler.backfiller.IsGapOpen(ctx, connectorID, partKey)
			if err != nil {
				return nil, recv.handler.unavailableWithRetry("gap check failed")
			}
			gapCache[partKey] = gapOpen
			if gapOpen {
				openPartitions++
			}
		}
		if !gapOpen {
			slog.WarnContext(ctx, "backfill message dropped: no open gap for partition",
				"connector_id", connectorID,
				"partition", partKey,
			)
			continue
		}

		var wm map[string]any
		if msg.Watermark != nil {
			wm = msg.Watermark.AsMap()
		}
		batch = append(batch, ingest.IngestRecord{
			Schema: ingest.SchemaKey{
				LogicalArtifactType: msg.GetLogicalArtifactId(),
				Version:             msg.GetSchemaVersion(),
			},
			Payload:     msg.GetPayload(),
			ContentType: msg.GetContentType(),
			Partition:   partKey,
			Watermark:   wm,
		})
	}

	if len(batch) == 0 {
		return &ingestionv1.IngestResponse{
			PartialSuccess: &ingestionv1.IngestPartialSuccess{},
		}, nil
	}

	result, err := recv.handler.ingestor.Ingest(ctx, batch)
	if err != nil {
		if errors.Is(err, ingest.ErrOverloaded) || errors.Is(err, ingest.ErrUnavailable) {
			return nil, recv.handler.unavailableWithRetry("pipeline unavailable")
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "request canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "request deadline exceeded")
		}
		slog.Error("unexpected backfill pipeline error", "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	// Reverse scan: last accepted watermark per partition, stop once all open-gap partitions covered.
	lastWM := lastAcceptedWatermarks(batch, result.Outcomes, openPartitions)
	for partition, wm := range lastWM {
		if err := recv.handler.backfiller.AdvanceBackfill(ctx, connectorID, partition, wm); err != nil {
			return nil, recv.handler.unavailableWithRetry("watermark advance failed")
		}
	}

	return &ingestionv1.IngestResponse{
		PartialSuccess: &ingestionv1.IngestPartialSuccess{
			RejectedMessages: int64(result.Rejected),
		},
	}, nil
}

// partitionToKey JSON-encodes a proto Struct partition into the storage key
// string. Returns an empty string when p is nil (no-partition stream).
// Returns a non-nil error when p is non-nil but cannot be encoded.
func partitionToKey(p *structpb.Struct) (string, error) {
	if p == nil {
		return "", nil
	}
	partitionJSON, err := json.Marshal(p.AsMap())
	if err != nil {
		return "", fmt.Errorf("invalid partition: %w", err)
	}
	return string(partitionJSON), nil
}
