package publisher

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/envelope"
)

const bufSize = 1024 * 1024

type fakeIngestionServer struct {
	pb.UnimplementedIngestionServiceServer
	ingested []*pb.IngestRequest
}

func (s *fakeIngestionServer) Ingest(_ context.Context, req *pb.IngestRequest) (*pb.IngestResponse, error) {
	s.ingested = append(s.ingested, req)
	return &pb.IngestResponse{}, nil
}

func newTestPublisher(t *testing.T, srv *fakeIngestionServer) *GRPCPublisher {
	t.Helper()
	listener := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	pb.RegisterIngestionServiceServer(grpcServer, srv)
	go grpcServer.Serve(listener)
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return &GRPCPublisher{
		conn:        conn,
		client:      pb.NewIngestionServiceClient(conn),
		connectorID: "test-connector",
	}
}

func testMessage() envelope.Message {
	return envelope.Message{
		LogicalArtifactId: "netbox.Device",
		SchemaVersion:       "0.1.0",
		Payload:             json.RawMessage(`{"id":1}`),
		Partition:           map[string]any{"endpoint": "dcim/devices/"},
		Watermark:           map[string]any{"last_id": 1},
		ContentType:         "application/json",
	}
}

func TestPublish_SendsMessages(t *testing.T) {
	srv := &fakeIngestionServer{}
	p := newTestPublisher(t, srv)

	msgs := []envelope.Message{testMessage(), testMessage()}
	require.NoError(t, p.Publish(context.Background(), msgs))

	require.Len(t, srv.ingested, 1)
	assert.Len(t, srv.ingested[0].Messages, 2)
}

func TestPublish_ChunksSplitOnCount(t *testing.T) {
	srv := &fakeIngestionServer{}
	p := newTestPublisher(t, srv)

	msgs := make([]envelope.Message, maxMsgsPerBatch+1)
	for idx := range msgs {
		msgs[idx] = testMessage()
	}
	require.NoError(t, p.Publish(context.Background(), msgs))

	assert.Len(t, srv.ingested, 2)
}
