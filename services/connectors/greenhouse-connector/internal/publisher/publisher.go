package publisher

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

type Publisher struct {
	client      pb.IngestionServiceClient
	connectorID string
}

func New(endpoint, certFile, keyFile, connectorID string) (*Publisher, error) {
	conn, err := dial(endpoint, certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("publisher: dial %s: %w", endpoint, err)
	}
	return &Publisher{
		client:      pb.NewIngestionServiceClient(conn),
		connectorID: connectorID,
	}, nil
}

func (p *Publisher) Publish(ctx context.Context, logicalArtifactID, schemaVersion, namespace, name string, obj any) error {
	payload, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("publisher: marshal: %w", err)
	}

	partition, err := structpb.NewStruct(map[string]any{
		"namespace": namespace,
		"name":      name,
	})
	if err != nil {
		return fmt.Errorf("publisher: build partition: %w", err)
	}

	msg := &pb.MessageEnvelope{
		LogicalArtifactId: logicalArtifactID,
		SchemaVersion:     schemaVersion,
		Payload:           payload,
		Partition:         partition,
	}

	outCtx := metadata.AppendToOutgoingContext(ctx, "connector-id", p.connectorID)
	_, err = p.client.Ingest(outCtx, &pb.IngestRequest{Messages: []*pb.MessageEnvelope{msg}})
	return err
}

func dial(endpoint, certFile, keyFile string) (*grpc.ClientConn, error) {
	if certFile == "" && keyFile == "" {
		return grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
	})))
}
