package publisher

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
)

// WatermarkReader reads the last committed backfill watermark for a partition
// from the Data Ingestion BackfillService. It is read-only: it never opens a
// gap or publishes. The connector uses it to observe where it left off.
type WatermarkReader struct {
	client      pb.BackfillServiceClient
	connectorID string
}

// NewWatermarkReader builds a WatermarkReader over an existing gRPC connection,
// so it shares the publisher's connection and credentials rather than dialing
// a second one.
func NewWatermarkReader(conn grpc.ClientConnInterface, connectorID string) *WatermarkReader {
	return &WatermarkReader{
		client:      pb.NewBackfillServiceClient(conn),
		connectorID: connectorID,
	}
}

// GetWatermark returns the last committed watermark for the given partition, or
// nil when none has been recorded. Connector identity travels in the
// connector-id metadata header, the same mechanism used for live ingestion.
func (reader *WatermarkReader) GetWatermark(ctx context.Context, partition map[string]any) (map[string]any, error) {
	partitionStruct, err := structpb.NewStruct(partition)
	if err != nil {
		return nil, err
	}

	response, err := reader.client.GetWatermark(outCtx(ctx, reader.connectorID), &pb.GetWatermarkRequest{Partition: partitionStruct})
	if err != nil {
		return nil, err
	}

	watermark := response.GetWatermark()
	if watermark == nil {
		return nil, nil
	}
	return watermark.AsMap(), nil
}
