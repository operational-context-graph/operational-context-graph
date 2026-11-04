package publisher

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
)

// fakeBackfillServer is a minimal BackfillService server for use with bufconn.
type fakeBackfillServer struct {
	pb.UnimplementedBackfillServiceServer
	watermark    map[string]any
	err          error
	receivedMD   metadata.MD
	receivedPart *structpb.Struct
}

func (server *fakeBackfillServer) GetWatermark(ctx context.Context, req *pb.GetWatermarkRequest) (*pb.GetWatermarkResponse, error) {
	server.receivedMD, _ = metadata.FromIncomingContext(ctx)
	server.receivedPart = req.GetPartition()
	if server.err != nil {
		return nil, server.err
	}
	if server.watermark == nil {
		return &pb.GetWatermarkResponse{}, nil
	}
	wmStruct, err := structpb.NewStruct(server.watermark)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode watermark: %v", err)
	}
	return &pb.GetWatermarkResponse{Watermark: wmStruct}, nil
}

func newTestWatermarkReader(t *testing.T, server *fakeBackfillServer) *WatermarkReader {
	t.Helper()
	listener := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	pb.RegisterBackfillServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }() // Serve error is non-actionable in background goroutine.
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() }) // Close errors are non-actionable in test cleanup.

	return NewWatermarkReader(conn, "test-connector")
}

func TestWatermarkReader_WatermarkPresent(t *testing.T) {
	server := &fakeBackfillServer{watermark: map[string]any{"last_id": float64(42)}}
	reader := newTestWatermarkReader(t, server)

	partition := map[string]any{"endpoint": "dcim/devices/"}
	watermark, err := reader.GetWatermark(context.Background(), partition)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"last_id": float64(42)}, watermark)
}

func TestWatermarkReader_WatermarkAbsent(t *testing.T) {
	server := &fakeBackfillServer{watermark: nil}
	reader := newTestWatermarkReader(t, server)

	watermark, err := reader.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	require.NoError(t, err)
	assert.Nil(t, watermark)
}

func TestWatermarkReader_ServerError(t *testing.T) {
	server := &fakeBackfillServer{err: status.Error(codes.Unavailable, "ingestion unavailable")}
	reader := newTestWatermarkReader(t, server)

	watermark, err := reader.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	assert.Error(t, err)
	assert.Nil(t, watermark)
}

func TestWatermarkReader_SendsConnectorIDHeader(t *testing.T) {
	server := &fakeBackfillServer{}
	reader := newTestWatermarkReader(t, server)

	_, err := reader.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	require.NoError(t, err)
	assert.Equal(t, []string{"test-connector"}, server.receivedMD.Get("connector-id"))
}

func TestWatermarkReader_SendsPartition(t *testing.T) {
	server := &fakeBackfillServer{}
	reader := newTestWatermarkReader(t, server)

	partition := map[string]any{"endpoint": "dcim/devices/"}
	_, err := reader.GetWatermark(context.Background(), partition)

	require.NoError(t, err)
	require.NotNil(t, server.receivedPart)
	assert.Equal(t, "dcim/devices/", server.receivedPart.AsMap()["endpoint"])
}

func TestWatermarkReader_UnknownError(t *testing.T) {
	server := &fakeBackfillServer{err: errors.New("unexpected")}
	reader := newTestWatermarkReader(t, server)

	_, err := reader.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	assert.Error(t, err)
}
