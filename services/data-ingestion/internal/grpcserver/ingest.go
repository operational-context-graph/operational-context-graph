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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	durationpb "google.golang.org/protobuf/types/known/durationpb"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	errdetails "google.golang.org/genproto/googleapis/rpc/errdetails"
)

// IngestionServiceServer implements the gRPC IngestionService.
type IngestionServiceServer struct {
	ingestionv1.UnimplementedIngestionServiceServer
	ingestor ingest.Ingestor
	limits   *config.LimitsConfig
	retry    *config.RetryConfig
}

// NewIngestionServiceServer constructs the server. Returns an error when any
// argument is nil to catch wiring mistakes at startup, not at first RPC.
func NewIngestionServiceServer(
	ingestor ingest.Ingestor,
	limits *config.LimitsConfig,
	retry *config.RetryConfig,
) (*IngestionServiceServer, error) {
	if ingestor == nil {
		return nil, fmt.Errorf("grpcserver: ingestor must not be nil")
	}
	if limits == nil {
		return nil, fmt.Errorf("grpcserver: limits must not be nil")
	}
	if retry == nil {
		return nil, fmt.Errorf("grpcserver: retry must not be nil")
	}
	return &IngestionServiceServer{ingestor: ingestor, limits: limits, retry: retry}, nil
}

// Ingest implements IngestionServiceServer.Ingest.
func (s *IngestionServiceServer) Ingest(ctx context.Context, req *ingestionv1.IngestRequest) (*ingestionv1.IngestResponse, error) {
	if req == nil || len(req.Messages) == 0 {
		return nil, status.Error(codes.InvalidArgument, "messages must not be empty")
	}

	if len(req.Messages) > s.limits.MaxMessages {
		return nil, status.Errorf(codes.ResourceExhausted,
			"batch exceeds maximum message count (%d)", s.limits.MaxMessages)
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

	result, err := s.ingestor.Ingest(ctx, batch)
	if err != nil {
		return nil, s.mapIngestError(err)
	}

	return &ingestionv1.IngestResponse{
		PartialSuccess: &ingestionv1.IngestPartialSuccess{
			RejectedMessages: int64(result.Rejected),
		},
	}, nil
}

// mapIngestError converts pipeline errors to gRPC status errors.
func (s *IngestionServiceServer) mapIngestError(err error) error {
	if errors.Is(err, ingest.ErrOverloaded) {
		return s.unavailableWithRetry("pipeline overloaded")
	}

	if errors.Is(err, ingest.ErrUnavailable) {
		return s.unavailableWithRetry("service unavailable")
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.Unavailable, "request cancelled")
	}

	slog.Error("unexpected pipeline error", "err", err)
	return status.Error(codes.Internal, "internal error")
}

// unavailableWithRetry builds a codes.Unavailable status with a RetryInfo hint.
func (s *IngestionServiceServer) unavailableWithRetry(msg string) error {
	base := status.New(codes.Unavailable, msg)
	withRetry, err := base.WithDetails(&errdetails.RetryInfo{
		RetryDelay: durationpb.New(s.retry.InitialBackoff),
	})
	if err != nil {
		slog.Error("failed to attach RetryInfo to UNAVAILABLE status", "err", err)
		return base.Err()
	}
	return withRetry.Err()
}
