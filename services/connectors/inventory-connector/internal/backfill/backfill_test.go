package backfill_test

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
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/backfill"
)

const bufSize = 1024 * 1024

type fakeBackfillServer struct {
	pb.UnimplementedBackfillServiceServer
	watermark    map[string]any
	err          error
	receivedMD   metadata.MD
	receivedPart *structpb.Struct
}

func (s *fakeBackfillServer) GetWatermark(ctx context.Context, req *pb.GetWatermarkRequest) (*pb.GetWatermarkResponse, error) {
	s.receivedMD, _ = metadata.FromIncomingContext(ctx)
	s.receivedPart = req.GetPartition()
	if s.err != nil {
		return nil, s.err
	}
	if s.watermark == nil {
		return &pb.GetWatermarkResponse{}, nil
	}
	wmStruct, err := structpb.NewStruct(s.watermark)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode watermark: %v", err)
	}
	return &pb.GetWatermarkResponse{Watermark: wmStruct}, nil
}

func newTestBackfillClient(t *testing.T, srv *fakeBackfillServer) *backfill.BackfillClient {
	t.Helper()
	listener := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	pb.RegisterBackfillServiceServer(grpcServer, srv)
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

	return backfill.NewBackfillClient(conn, "test-connector")
}

func TestGetWatermark_Present(t *testing.T) {
	srv := &fakeBackfillServer{watermark: map[string]any{"last_updated": "2026-07-10T07:47:31Z"}}
	client := newTestBackfillClient(t, srv)

	watermark, err := client.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"last_updated": "2026-07-10T07:47:31Z"}, watermark)
}

func TestGetWatermark_Absent(t *testing.T) {
	srv := &fakeBackfillServer{watermark: nil}
	client := newTestBackfillClient(t, srv)

	watermark, err := client.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	require.NoError(t, err)
	assert.Nil(t, watermark)
}

func TestGetWatermark_ServerError(t *testing.T) {
	srv := &fakeBackfillServer{err: status.Error(codes.Unavailable, "ingestion unavailable")}
	client := newTestBackfillClient(t, srv)

	watermark, err := client.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	assert.Error(t, err)
	assert.Nil(t, watermark)
}

func TestGetWatermark_ConnectorIDHeader(t *testing.T) {
	srv := &fakeBackfillServer{}
	client := newTestBackfillClient(t, srv)

	_, err := client.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	require.NoError(t, err)
	assert.Equal(t, []string{"test-connector"}, srv.receivedMD.Get("connector-id"))
}

func TestGetWatermark_PartitionForwarded(t *testing.T) {
	srv := &fakeBackfillServer{}
	client := newTestBackfillClient(t, srv)

	_, err := client.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	require.NoError(t, err)
	require.NotNil(t, srv.receivedPart)
	assert.Equal(t, "dcim/devices/", srv.receivedPart.AsMap()["endpoint"])
}

func TestGetWatermark_UnknownError(t *testing.T) {
	srv := &fakeBackfillServer{err: errors.New("unexpected")}
	client := newTestBackfillClient(t, srv)

	_, err := client.GetWatermark(context.Background(), map[string]any{"endpoint": "dcim/devices/"})

	assert.Error(t, err)
}
