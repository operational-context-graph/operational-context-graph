package backfill

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	grpcclient "github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/grpc"
)

// BackfillClient reads the last committed watermark from the Data Ingestion
// BackfillService. It is read-only: it never opens a gap or publishes.
type BackfillClient struct {
	client      pb.BackfillServiceClient
	connectorID string
}

// NewBackfillClient builds a BackfillClient over an existing gRPC connection,
// sharing the publisher's connection and credentials rather than dialing a second.
func NewBackfillClient(conn grpc.ClientConnInterface, connectorID string) *BackfillClient {
	return &BackfillClient{
		client:      pb.NewBackfillServiceClient(conn),
		connectorID: connectorID,
	}
}

// GetWatermark returns the last committed watermark for the given partition, or
// nil when none has been recorded.
func (c *BackfillClient) GetWatermark(ctx context.Context, partition map[string]any) (map[string]any, error) {
	partitionStruct, err := structpb.NewStruct(partition)
	if err != nil {
		return nil, err
	}

	response, err := c.client.GetWatermark(grpcclient.OutCtx(ctx, c.connectorID), &pb.GetWatermarkRequest{Partition: partitionStruct})
	if err != nil {
		return nil, err
	}

	watermark := response.GetWatermark()
	if watermark == nil {
		return nil, nil
	}
	return watermark.AsMap(), nil
}
