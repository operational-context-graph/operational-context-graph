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

// OCGReceiver implements the gRPC IngestionService.
type OCGReceiver struct {
	ingestionv1.UnimplementedIngestionServiceServer
	ingestor ingest.Ingestor
	limits   *config.LimitsConfig
	retry    *config.RetryConfig
}

// NewOCGReceiver constructs an OCGReceiver. Returns an error when any
// argument is nil to catch wiring mistakes at startup, not at first RPC.
func NewOCGReceiver(
	ingestor ingest.Ingestor,
	limits *config.LimitsConfig,
	retry *config.RetryConfig,
) (*OCGReceiver, error) {
	if ingestor == nil {
		return nil, fmt.Errorf("grpcserver: ingestor must not be nil")
	}
	if limits == nil {
		return nil, fmt.Errorf("grpcserver: limits must not be nil")
	}
	if retry == nil {
		return nil, fmt.Errorf("grpcserver: retry must not be nil")
	}
	return &OCGReceiver{ingestor: ingestor, limits: limits, retry: retry}, nil
}

// register mounts the IngestionService onto grpcSrv.
func (r *OCGHandler) register(grpcSrv *grpc.Server) {
	ingestionv1.RegisterIngestionServiceServer(grpcSrv, r)
}

// Ingest implements OCGReceiver.Ingest.
func (r *OCGReceiver) Ingest(ctx context.Context, req *ingestionv1.IngestRequest) (*ingestionv1.IngestResponse, error) {
	if req == nil || len(req.Messages) == 0 {
		return nil, status.Error(codes.InvalidArgument, "messages must not be empty")
	}

	if len(req.Messages) > r.limits.MaxMessages {
		return nil, status.Errorf(codes.ResourceExhausted,
			"batch exceeds maximum message count (%d)", r.limits.MaxMessages)
	}

	batch := make([]ingest.IngestRecord, len(req.Messages))
	for i, env := range req.Messages {
		batch[i] = ingest.IngestRecord{
			LogicalArtifactID: env.GetLogicalArtifactId(),
			SchemaVersion:     env.GetSchemaVersion(),
			Payload:           env.GetPayload(),
			ContentType:       env.ContentType,
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

	result, err := r.ingestor.Ingest(ctx, batch)
	if err != nil {
		return nil, r.mapIngestError(err)
	}

	return &ingestionv1.IngestResponse{
		PartialSuccess: &ingestionv1.IngestPartialSuccess{
			RejectedMessages: int64(result.Rejected),
		},
	}, nil
}

// mapIngestError converts pipeline errors to gRPC status errors.
func (r *OCGReceiver) mapIngestError(err error) error {
	if errors.Is(err, ingest.ErrOverloaded) {
		base := status.New(codes.Unavailable, "pipeline overloaded")
		withRetry, attachErr := base.WithDetails(&errdetails.RetryInfo{
			RetryDelay: durationpb.New(r.retry.InitialBackoff),
		})
		if attachErr != nil {
			slog.Error("failed to attach RetryInfo to UNAVAILABLE status", "err", attachErr)
			return base.Err()
		}
		return withRetry.Err()
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.Unavailable, "request cancelled")
	}

	slog.Error("unexpected pipeline error", "err", err)
	return status.Error(codes.Internal, "internal error")
}
