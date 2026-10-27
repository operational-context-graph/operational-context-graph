// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	mock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// --- test doubles ---

func noop(t *testing.T) *Ingestor {
	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Maybe().Return(ingest.IngestResult{}, nil)
	return m
}

// noopBackfiller returns a Backfiller mock that accepts all AdvanceLive calls.
func noopBackfiller(t *testing.T) *MockBackfiller {
	m := NewMockBackfiller(t)
	m.On("AdvanceLive", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	return m
}

func defaultLimits() *config.LimitsConfig {
	return &config.LimitsConfig{MaxMessages: 1000, MaxPayloadBytes: 4 * 1024 * 1024}
}

func defaultRetry() *config.RetryConfig {
	return &config.RetryConfig{RetryDelay: 5 * time.Second}
}

func newServer(t *testing.T, ingestor ingest.Ingestor, limits *config.LimitsConfig, retry *config.RetryConfig) *grpcserver.OCGReceiver {
	t.Helper()
	h, err := grpcserver.NewOCGHandler(ingestor, limits, retry, noopBackfiller(t))
	require.NoError(t, err)
	return grpcserver.NewOCGReceiver(h)
}

func envelope(payload []byte) *ingestionv1.MessageEnvelope {
	return &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "test-artifact",
		SchemaVersion:     "v1",
		Payload:           payload,
	}
}

// --- 5.1: nil request → INVALID_ARGUMENT, Ingest not called ---

func TestNilRequestReturnsInvalidArgument(t *testing.T) {
	m := NewIngestor(t)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), nil)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	m.AssertNotCalled(t, "Ingest")
}

// --- 5.2: empty messages → INVALID_ARGUMENT, Ingest not called ---

func TestEmptyMessagesReturnsInvalidArgument(t *testing.T) {
	m := NewIngestor(t)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: nil})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	m.AssertNotCalled(t, "Ingest")
}

// --- 5.3: exactly MaxMessages accepted; MaxMessages+1 → RESOURCE_EXHAUSTED ---

func TestMaxMessagesBoundary(t *testing.T) {
	limits := &config.LimitsConfig{MaxMessages: 10, MaxPayloadBytes: 1024 * 1024 * 1024}
	srv := newServer(t, noop(t), limits, defaultRetry())

	// exactly MaxMessages — should succeed
	msgs := make([]*ingestionv1.MessageEnvelope, limits.MaxMessages)
	for i := range msgs {
		msgs[i] = envelope(nil)
	}
	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: msgs})
	require.NoError(t, err)

	// MaxMessages+1 — should be rejected
	msgs = append(msgs, envelope(nil))
	_, err = srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: msgs})
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

// --- 5.5: nil payload is valid — bytes fields default to nil in proto3 ---

func TestNilPayloadIsAccepted(t *testing.T) {
	limits := &config.LimitsConfig{MaxMessages: 10, MaxPayloadBytes: 50}
	srv := newServer(t, noop(t), limits, defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope(nil)},
	})
	require.NoError(t, err, "nil payload is valid proto3; handler must not panic")
}

// --- 5.6: nil LimitsConfig or nil Ingestor at construction → non-nil error ---

func TestNilLimitsAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(noop(t), nil, defaultRetry(), noopBackfiller(t))
	require.Error(t, err)
}

func TestNilIngestorAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(nil, defaultLimits(), defaultRetry(), noopBackfiller(t))
	require.Error(t, err)
}

func TestNilRetryAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(noop(t), defaultLimits(), nil, noopBackfiller(t))
	require.Error(t, err)
}

func TestNilBackfillerAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(noop(t), defaultLimits(), defaultRetry(), nil)
	require.Error(t, err)
}

// --- 6.1: ErrOverloaded → UNAVAILABLE + RetryInfo ---

func TestOverloadedErrorReturnsUnavailableWithRetryInfo(t *testing.T) {
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, ingest.ErrOverloaded)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	st, ok := status.FromError(err)
	require.True(t, ok)
	details := st.Details()
	require.NotEmpty(t, details, "RetryInfo must be attached")
}

// --- 6.2: wrapped ErrOverloaded detected via errors.Is ---

func TestWrappedOverloadedDetected(t *testing.T) {
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, errors.Join(ingest.ErrOverloaded, errors.New("context")))
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// --- 6.3: status.WithDetails failure stub → plain UNAVAILABLE, no panic ---
// We test this indirectly: ErrOverloaded path must never return a non-UNAVAILABLE code.

func TestOverloadedAlwaysReturnsUnavailable(t *testing.T) {
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, ingest.ErrOverloaded)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	for i := 0; i < 5; i++ {
		_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
			Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	}
}

// --- 6.4: ErrUnavailable → UNAVAILABLE ---

func TestErrUnavailableReturnsUnavailable(t *testing.T) {
	cause := errors.New("connection refused")
	unavailErr := fmt.Errorf("%w: registry down: %w", ingest.ErrUnavailable, cause)
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, unavailErr)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.NotEmpty(t, st.Details(), "RetryInfo must be attached")
}

// --- 6.5: unexpected error → INTERNAL; message does not contain original error text ---

func TestUnexpectedErrorReturnsInternal(t *testing.T) {
	const secretMsg = "secret-db-connection-string"
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, errors.New(secretMsg))
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))

	st, _ := status.FromError(err)
	assert.NotContains(t, st.Message(), secretMsg, "internal error details must not be leaked")
}

// --- 6.5: partial rejections → OK response with correct count ---

func TestPartialRejectionReturnsOK(t *testing.T) {
	const rejected = 2
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{Accepted: len(batch) - rejected, Rejected: rejected}, nil
	})
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	resp, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("a")), envelope([]byte("b")), envelope([]byte("c")), envelope([]byte("d"))},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(rejected), resp.PartialSuccess.RejectedMessages)
}

func TestAllRejectedReturnsOK(t *testing.T) {
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{Rejected: len(batch)}, nil
	})
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	resp, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x")), envelope([]byte("y"))},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.PartialSuccess.RejectedMessages)
}

// --- 6.6: context cancelled ---

func TestCancelledContextOnEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := newServer(t, noop(t), defaultLimits(), defaultRetry())
	_, _ = srv.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
}

func TestContextCancelledDuringIngest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).RunAndReturn(func(c context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		cancel()
		return ingest.IngestResult{}, c.Err()
	})
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
}

// --- 7.1: concurrent Ingest calls — data race check (run with -race) ---
// The assertion is the race detector itself; result values are intentionally ignored.
func TestConcurrentIngest(t *testing.T) {
	srv := newServer(t, noop(t), defaultLimits(), defaultRetry())

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
				Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("concurrent"))},
			})
		}()
	}
	wg.Wait()
}

// --- 7.2: partition and watermark fields mapped independently ---

func TestPartitionFieldMapped(t *testing.T) {
	var captured []ingest.IngestRecord
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		captured = batch
		return ingest.IngestResult{Accepted: len(batch)}, nil
	})
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	partitionStruct, err := structpb.NewStruct(map[string]interface{}{"key": "val"})
	require.NoError(t, err)
	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "a",
		SchemaVersion:     "v1",
		Payload:           []byte("x"),
		Partition:         partitionStruct,
	}
	_, err = srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.NoError(t, err)
	require.Len(t, captured, 1)
	assert.Equal(t, `{"key":"val"}`, captured[0].Partition)
	assert.Nil(t, captured[0].Watermark, "watermark must be nil when not set")
}

func TestWatermarkFieldMapped(t *testing.T) {
	var captured []ingest.IngestRecord
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		captured = batch
		return ingest.IngestResult{Accepted: len(batch)}, nil
	})
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	watermarkStruct, err := structpb.NewStruct(map[string]interface{}{"offset": float64(42)})
	require.NoError(t, err)
	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "a",
		SchemaVersion:     "v1",
		Payload:           []byte("x"),
		Watermark:         watermarkStruct,
	}
	_, err = srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.NoError(t, err)
	require.Len(t, captured, 1)
	assert.Empty(t, captured[0].Partition, "partition must be empty when not set")
	assert.Equal(t, map[string]interface{}{"offset": float64(42)}, captured[0].Watermark)
}

func TestPartitionAndWatermarkMappedIndependently(t *testing.T) {
	var captured []ingest.IngestRecord
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		captured = batch
		return ingest.IngestResult{Accepted: len(batch)}, nil
	})
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	partitionStruct, err := structpb.NewStruct(map[string]interface{}{"shard": "east"})
	require.NoError(t, err)
	watermarkStruct, err := structpb.NewStruct(map[string]interface{}{"offset": float64(99)})
	require.NoError(t, err)
	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "a",
		SchemaVersion:     "v1",
		Payload:           []byte("x"),
		Partition:         partitionStruct,
		Watermark:         watermarkStruct,
	}
	_, err = srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.NoError(t, err)
	require.Len(t, captured, 1)
	assert.Equal(t, `{"shard":"east"}`, captured[0].Partition)
	assert.Equal(t, map[string]interface{}{"offset": float64(99)}, captured[0].Watermark)
}

// ctxWithConnectorID injects a connector-id gRPC metadata header into a
// context so connectorIDFromContext can extract it in unit tests (no real
// gRPC transport needed).
func ctxWithConnectorID(connectorID string) context.Context {
	md := metadata.Pairs("connector-id", connectorID)
	return metadata.NewIncomingContext(context.Background(), md)
}

// --- 2.3: AdvanceLive called/not-called based on watermark and partition ---

func TestAdvanceLiveCalledWhenWatermarkAndPartitionPresent(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("AdvanceLive", mock.Anything, "conn-1", mock.AnythingOfType("string"), mock.Anything).Once().Return(nil)

	m := noop(t)
	h, err := grpcserver.NewOCGHandler(m, defaultLimits(), defaultRetry(), bf)
	require.NoError(t, err)
	srv := grpcserver.NewOCGReceiver(h)

	partStruct, err := structpb.NewStruct(map[string]interface{}{"shard": "a"})
	require.NoError(t, err)
	wmStruct, err := structpb.NewStruct(map[string]interface{}{"offset": float64(1)})
	require.NoError(t, err)

	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "art",
		SchemaVersion:     "v1",
		Partition:         partStruct,
		Watermark:         wmStruct,
	}
	_, err = srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.NoError(t, err)
	bf.AssertExpectations(t)
}

func TestAdvanceLiveNotCalledWhenWatermarkNil(t *testing.T) {
	bf := NewMockBackfiller(t)
	// no AdvanceLive expectation — if it's called, AssertExpectations will fail

	m := noop(t)
	h, err := grpcserver.NewOCGHandler(m, defaultLimits(), defaultRetry(), bf)
	require.NoError(t, err)
	srv := grpcserver.NewOCGReceiver(h)

	partStruct, err := structpb.NewStruct(map[string]interface{}{"shard": "a"})
	require.NoError(t, err)

	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "art",
		SchemaVersion:     "v1",
		Partition:         partStruct,
		Watermark:         nil,
	}
	_, err = srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.NoError(t, err)
}

func TestAdvanceLiveCalledWhenPartitionNilButWatermarkPresent(t *testing.T) {
	// Nil partition is valid: it produces an empty partition key ("") representing
	// a generic per-connector watermark. AdvanceLive MUST be called.
	bf := NewMockBackfiller(t)
	bf.On("AdvanceLive", mock.Anything, "conn-1", "", mock.Anything).Once().Return(nil)

	m := noop(t)
	h, err := grpcserver.NewOCGHandler(m, defaultLimits(), defaultRetry(), bf)
	require.NoError(t, err)
	srv := grpcserver.NewOCGReceiver(h)

	wmStruct, err := structpb.NewStruct(map[string]interface{}{"offset": float64(1)})
	require.NoError(t, err)

	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "art",
		SchemaVersion:     "v1",
		Partition:         nil,
		Watermark:         wmStruct,
	}
	_, err = srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.NoError(t, err)
	bf.AssertExpectations(t)
}

func TestIngestPipelineDeadlineExceeded(t *testing.T) {
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, context.DeadlineExceeded)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestIngestPipelineCanceled(t *testing.T) {
	m := NewIngestor(t)
	m.EXPECT().Ingest(mock.Anything, mock.Anything).Return(ingest.IngestResult{}, context.Canceled)
	srv := newServer(t, m, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
}

func TestAdvanceLiveErrorReturnsUnavailable(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("AdvanceLive", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("storage down"))

	m := noop(t)
	h, err := grpcserver.NewOCGHandler(m, defaultLimits(), defaultRetry(), bf)
	require.NoError(t, err)
	srv := grpcserver.NewOCGReceiver(h)

	partStruct, err := structpb.NewStruct(map[string]interface{}{"shard": "a"})
	require.NoError(t, err)
	wmStruct, err := structpb.NewStruct(map[string]interface{}{"offset": float64(1)})
	require.NoError(t, err)

	env := &ingestionv1.MessageEnvelope{
		LogicalArtifactId: "art",
		SchemaVersion:     "v1",
		Partition:         partStruct,
		Watermark:         wmStruct,
	}
	_, err = srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: []*ingestionv1.MessageEnvelope{env}})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}
