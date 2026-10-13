package publisher

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/envelope"
)

type GRPCPublisher struct {
	conn        *grpc.ClientConn
	client      pb.IngestionServiceClient
	connectorID string
}

func NewGRPCPublisher(endpoint, certFile, keyFile, connectorID string) (*GRPCPublisher, error) {
	conn, err := dial(endpoint, certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &GRPCPublisher{
		conn:        conn,
		client:      pb.NewIngestionServiceClient(conn),
		connectorID: connectorID,
	}, nil
}

func (p *GRPCPublisher) Publish(ctx context.Context, msgs []envelope.Message) error {
	for _, batch := range chunk(msgs) {
		envelopes, err := toProtos(batch)
		if err != nil {
			return err
		}
		_, err = p.client.Ingest(outCtx(ctx, p.connectorID), &pb.IngestRequest{Messages: envelopes})
		if err != nil {
			if status.Code(err) == codes.Unavailable {
				return errors.New("publisher: back-pressure — data ingestion unavailable")
			}
			return err
		}
	}
	return nil
}
