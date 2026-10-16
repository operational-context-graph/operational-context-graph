// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// --- test doubles ---

type fakeIngestor struct {
	fn func(ctx context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error)
}

func (f *fakeIngestor) Ingest(ctx context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
	return f.fn(ctx, batch)
}

func noop() *ingest.NoopIngestor { return &ingest.NoopIngestor{} }

func defaultLimits() *config.LimitsConfig {
	return &config.LimitsConfig{MaxMessages: 1000, MaxPayloadBytes: 4 * 1024 * 1024}
}

func defaultRetry() *config.RetryConfig {
	return &config.RetryConfig{InitialBackoff: 5 * time.Second, MaxBackoff: 60 * time.Second, Multiplier: 1.0}
}

func newServer(t *testing.T, ingestor ingest.Ingestor, limits *config.LimitsConfig, retry *config.RetryConfig) *grpcserver.OCGReceiver {
	t.Helper()
	h, err := grpcserver.NewOCGHandler(ingestor, limits, retry)
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
	called := false
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		called = true
		return ingest.IngestResult{}, nil
	}}, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), nil)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.False(t, called, "Ingest must not be called on nil request")
}

// --- 5.2: empty messages → INVALID_ARGUMENT, Ingest not called ---

func TestEmptyMessagesReturnsInvalidArgument(t *testing.T) {
	called := false
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		called = true
		return ingest.IngestResult{}, nil
	}}, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{Messages: nil})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.False(t, called)
}

// --- 5.3: exactly MaxMessages accepted; MaxMessages+1 → RESOURCE_EXHAUSTED ---

func TestMaxMessagesBoundary(t *testing.T) {
	limits := &config.LimitsConfig{MaxMessages: 10, MaxPayloadBytes: 1024 * 1024 * 1024}
	srv := newServer(t, noop(), limits, defaultRetry())

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
	srv := newServer(t, noop(), limits, defaultRetry())

	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope(nil)},
	})
	require.NoError(t, err, "nil payload is valid proto3; handler must not panic")
}

// --- 5.6: nil LimitsConfig or nil Ingestor at construction → non-nil error ---

func TestNilLimitsAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(noop(), nil, defaultRetry())
	require.Error(t, err)
}

func TestNilIngestorAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(nil, defaultLimits(), defaultRetry())
	require.Error(t, err)
}

func TestNilRetryAtConstruction(t *testing.T) {
	_, err := grpcserver.NewOCGHandler(noop(), defaultLimits(), nil)
	require.Error(t, err)
}

// --- 6.1: ErrOverloaded → UNAVAILABLE + RetryInfo ---

func TestOverloadedErrorReturnsUnavailableWithRetryInfo(t *testing.T) {
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{}, ingest.ErrOverloaded
	}}, defaultLimits(), defaultRetry())

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
	// Use errors.Join to wrap ErrOverloaded — errors.Is must still detect it.
	wrappedSrv := newServer(t, &fakeIngestor{fn: func(_ context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{}, errors.Join(ingest.ErrOverloaded, errors.New("context"))
	}}, defaultLimits(), defaultRetry())

	_, err := wrappedSrv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// --- 6.3: status.WithDetails failure stub → plain UNAVAILABLE, no panic ---
// We test this indirectly: ErrOverloaded path must never return a non-UNAVAILABLE code.

func TestOverloadedAlwaysReturnsUnavailable(t *testing.T) {
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{}, ingest.ErrOverloaded
	}}, defaultLimits(), defaultRetry())

	for i := 0; i < 5; i++ {
		_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
			Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	}
}

// --- 6.4: unexpected error → INTERNAL; message does not contain original error text ---

func TestUnexpectedErrorReturnsInternal(t *testing.T) {
	const secretMsg = "secret-db-connection-string"
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{}, errors.New(secretMsg)
	}}, defaultLimits(), defaultRetry())

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
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{Accepted: len(batch) - rejected, Rejected: rejected}, nil
	}}, defaultLimits(), defaultRetry())

	resp, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("a")), envelope([]byte("b")), envelope([]byte("c")), envelope([]byte("d"))},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(rejected), resp.PartialSuccess.RejectedMessages)
}

func TestAllRejectedReturnsOK(t *testing.T) {
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		return ingest.IngestResult{Rejected: len(batch)}, nil
	}}, defaultLimits(), defaultRetry())

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

	srv := newServer(t, noop(), defaultLimits(), defaultRetry())
	// Handler delegates ctx to Ingestor; NoopIngestor ignores it, so no panic.
	_, _ = srv.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
}

func TestContextCancelledDuringIngest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := newServer(t, &fakeIngestor{fn: func(c context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		cancel()
		return ingest.IngestResult{}, c.Err()
	}}, defaultLimits(), defaultRetry())

	_, err := srv.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{envelope([]byte("x"))},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
}

// --- 7.1: concurrent Ingest calls — data race check (run with -race) ---
// The assertion is the race detector itself; result values are intentionally ignored.
func TestConcurrentIngest(t *testing.T) {
	srv := newServer(t, noop(), defaultLimits(), defaultRetry())

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
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		captured = batch
		return ingest.IngestResult{Accepted: len(batch)}, nil
	}}, defaultLimits(), defaultRetry())

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
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		captured = batch
		return ingest.IngestResult{Accepted: len(batch)}, nil
	}}, defaultLimits(), defaultRetry())

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
	srv := newServer(t, &fakeIngestor{fn: func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		captured = batch
		return ingest.IngestResult{Accepted: len(batch)}, nil
	}}, defaultLimits(), defaultRetry())

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
