// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	mock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// newBackfillReceiver builds an OCGBackfillReceiver for unit tests.
func newBackfillReceiver(t *testing.T, bf *MockBackfiller, ingestor ingest.Ingestor) *grpcserver.OCGBackfillReceiver {
	t.Helper()
	h, err := grpcserver.NewOCGBackfillHandler(bf, ingestor, defaultLimits(), defaultRetry())
	require.NoError(t, err)
	return grpcserver.NewOCGBackfillReceiver(h)
}

func partitionStruct(t *testing.T, m map[string]interface{}) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}

func watermarkStruct(t *testing.T, m map[string]interface{}) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}

// ── 5.1: GetWatermark — watermark exists ─────────────────────────────────────

func TestGetWatermark_Exists(t *testing.T) {
	bf := NewMockBackfiller(t)
	wm := map[string]any{"offset": float64(42)}
	bf.On("GetWatermark", mock.Anything, "conn-1", mock.AnythingOfType("string")).Return(wm, true, nil)

	srv := newBackfillReceiver(t, bf, noop(t))

	resp, err := srv.GetWatermark(ctxWithConnectorID("conn-1"), &ingestionv1.GetWatermarkRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "east"}),
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Watermark)
	assert.Equal(t, float64(42), resp.Watermark.AsMap()["offset"])
}

// ── 5.2: GetWatermark — no watermark → empty response ────────────────────────

func TestGetWatermark_NotFound(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("GetWatermark", mock.Anything, "conn-1", mock.Anything).Return(nil, false, nil)

	srv := newBackfillReceiver(t, bf, noop(t))

	resp, err := srv.GetWatermark(ctxWithConnectorID("conn-1"), &ingestionv1.GetWatermarkRequest{})
	require.NoError(t, err)
	assert.Nil(t, resp.Watermark, "watermark field must be nil when not recorded — NOT codes.NotFound")
}

// ── 5.3: GetWatermark — no connector identity → UNAUTHENTICATED ──────────────

func TestGetWatermark_NoIdentity(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.GetWatermark(context.Background(), &ingestionv1.GetWatermarkRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// ── 5.4: GetWatermark — backfiller error → UNAVAILABLE with RetryInfo ─────────

func TestGetWatermark_BackfillerError(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("GetWatermark", mock.Anything, mock.Anything, mock.Anything).Return(nil, false, errors.New("db down"))

	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.GetWatermark(ctxWithConnectorID("conn-1"), &ingestionv1.GetWatermarkRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.NotEmpty(t, st.Details(), "RetryInfo must be present")
}

// ── 5.5: StartGap — valid partition → success ────────────────────────────────

func TestStartGap_Valid(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("StartGap", mock.Anything, "conn-1", mock.AnythingOfType("string")).Return(nil)

	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.StartGap(ctxWithConnectorID("conn-1"), &ingestionv1.StartGapRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "west"}),
	})
	require.NoError(t, err)
}

// ── 5.6: StartGap — nil partition → INVALID_ARGUMENT ─────────────────────────

func TestStartGap_NilPartition(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.StartGap(ctxWithConnectorID("conn-1"), &ingestionv1.StartGapRequest{Partition: nil})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ── 5.7: StartGap — no connector identity → UNAUTHENTICATED ──────────────────

func TestStartGap_NoIdentity(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.StartGap(context.Background(), &ingestionv1.StartGapRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "x"}),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// ── 5.8: StartGap — backfiller error → UNAVAILABLE with RetryInfo ─────────────

func TestStartGap_BackfillerError(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("StartGap", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("storage down"))

	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.StartGap(ctxWithConnectorID("conn-1"), &ingestionv1.StartGapRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "x"}),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	st, _ := status.FromError(err)
	assert.NotEmpty(t, st.Details())
}

// ── 5.9: CloseGap — valid partition → success ────────────────────────────────

func TestCloseGap_Valid(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("CloseGap", mock.Anything, "conn-1", mock.AnythingOfType("string")).Return(nil)

	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.CloseGap(ctxWithConnectorID("conn-1"), &ingestionv1.CloseGapRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "west"}),
	})
	require.NoError(t, err)
}

// ── 5.10: CloseGap — nil partition → INVALID_ARGUMENT ────────────────────────

func TestCloseGap_NilPartition(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.CloseGap(ctxWithConnectorID("conn-1"), &ingestionv1.CloseGapRequest{Partition: nil})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ── 5.11: CloseGap — no connector identity → UNAUTHENTICATED ─────────────────

func TestCloseGap_NoIdentity(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.CloseGap(context.Background(), &ingestionv1.CloseGapRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "x"}),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// ── 5.12: CloseGap — backfiller error → UNAVAILABLE with RetryInfo ────────────

func TestCloseGap_BackfillerError(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("CloseGap", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("storage down"))

	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.CloseGap(ctxWithConnectorID("conn-1"), &ingestionv1.CloseGapRequest{
		Partition: partitionStruct(t, map[string]interface{}{"shard": "x"}),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	st, _ := status.FromError(err)
	assert.NotEmpty(t, st.Details())
}

// ── 5.13: BackfillService.Ingest — all gaps open → all processed ─────────────

func TestBackfillIngest_AllGapsOpen(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, "conn-1", mock.Anything).Return(true, nil)
	bf.On("AdvanceBackfill", mock.Anything, "conn-1", mock.Anything, mock.Anything).Return(nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{Accepted: 2, Outcomes: []bool{true, true}}, nil)

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	msgs := []*ingestionv1.MessageEnvelope{
		{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
	}
	resp, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: msgs})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.PartialSuccess.RejectedMessages)
}

// ── 5.14: BackfillService.Ingest — no gaps open → empty response, no pipeline call ─

func TestBackfillIngest_NoGapsOpen(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	m := NewIngestor(t)
	// Ingest must NOT be called

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	resp, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.PartialSuccess.RejectedMessages)
	m.AssertNotCalled(t, "Ingest")
}

// ── 5.15: BackfillService.Ingest — mixed batch ────────────────────────────────

func TestBackfillIngest_MixedBatch(t *testing.T) {
	partOpen := partitionStruct(t, map[string]interface{}{"shard": "open"})
	partClosed := partitionStruct(t, map[string]interface{}{"shard": "closed"})
	openKey := `{"shard":"open"}`
	closedKey := `{"shard":"closed"}`

	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, "conn-1", openKey).Return(true, nil)
	bf.On("IsGapOpen", mock.Anything, "conn-1", closedKey).Return(false, nil)
	bf.On("AdvanceBackfill", mock.Anything, "conn-1", openKey, mock.Anything).Return(nil)

	var capturedBatch []ingest.IngestRecord
	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(func(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
		capturedBatch = batch
		outcomes := make([]bool, len(batch))
		for i := range outcomes {
			outcomes[i] = true
		}
		return ingest.IngestResult{Accepted: len(batch), Outcomes: outcomes}, nil
	})

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	msgs := []*ingestionv1.MessageEnvelope{
		{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: partOpen, Watermark: wm},
		{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: partClosed, Watermark: wm},
	}
	resp, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: msgs})
	require.NoError(t, err)
	// gap-filtered messages are silently dropped, NOT counted as rejected
	assert.Equal(t, int64(0), resp.PartialSuccess.RejectedMessages)
	require.Len(t, capturedBatch, 1, "only gap-open message goes to pipeline")
	assert.Equal(t, openKey, capturedBatch[0].Partition)
}

// ── 5.16: BackfillService.Ingest — zero messages → INVALID_ARGUMENT ──────────

func TestBackfillIngest_ZeroMessages(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: nil})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ── 5.17: BackfillService.Ingest — batch exceeds MaxMessages → RESOURCE_EXHAUSTED ─

func TestBackfillIngest_ExceedsMaxMessages(t *testing.T) {
	bf := NewMockBackfiller(t)
	smallLimits := &config.LimitsConfig{MaxMessages: 10, MaxPayloadBytes: 4 * 1024 * 1024}
	h, err := grpcserver.NewOCGBackfillHandler(bf, noop(t), smallLimits, defaultRetry())
	require.NoError(t, err)
	srv := grpcserver.NewOCGBackfillReceiver(h)

	msgs := make([]*ingestionv1.MessageEnvelope, 11)
	for i := range msgs {
		msgs[i] = &ingestionv1.MessageEnvelope{LogicalArtifactId: "art", SchemaVersion: "v1"}
	}
	_, err = srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{Messages: msgs})
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

// ── 5.18: BackfillService.Ingest — no connector identity → UNAUTHENTICATED ────

func TestBackfillIngest_NoIdentity(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv := newBackfillReceiver(t, bf, noop(t))

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	_, err := srv.Ingest(context.Background(), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// ── 5.19: BackfillService.Ingest — IsGapOpen error → UNAVAILABLE ─────────────

func TestBackfillIngest_IsGapOpenError(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))

	srv := newBackfillReceiver(t, bf, noop(t))

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// ── 5.20: BackfillService.Ingest — pipeline ErrUnavailable → UNAVAILABLE ──────

func TestBackfillIngest_PipelineUnavailable(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{}, ingest.ErrUnavailable)

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	st, _ := status.FromError(err)
	assert.NotEmpty(t, st.Details())
}

// ── 5.21: BackfillService.Ingest — AdvanceBackfill fails → UNAVAILABLE ────────

func TestBackfillIngest_AdvanceBackfillError(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	bf.On("AdvanceBackfill", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("db down"))

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{Accepted: 1, Outcomes: []bool{true}}, nil)

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// ── 5.22: NewOCGBackfillHandler — nil argument → non-nil error ───────────────

func TestNewOCGBackfillHandler_NilBackfiller(t *testing.T) {
	_, err := grpcserver.NewOCGBackfillHandler(nil, noop(t), defaultLimits(), defaultRetry())
	require.Error(t, err)
}

func TestNewOCGBackfillHandler_NilIngestor(t *testing.T) {
	bf := NewMockBackfiller(t)
	_, err := grpcserver.NewOCGBackfillHandler(bf, nil, defaultLimits(), defaultRetry())
	require.Error(t, err)
}

func TestNewOCGBackfillHandler_NilLimits(t *testing.T) {
	bf := NewMockBackfiller(t)
	_, err := grpcserver.NewOCGBackfillHandler(bf, noop(t), nil, defaultRetry())
	require.Error(t, err)
}

func TestNewOCGBackfillHandler_NilRetry(t *testing.T) {
	bf := NewMockBackfiller(t)
	_, err := grpcserver.NewOCGBackfillHandler(bf, noop(t), defaultLimits(), nil)
	require.Error(t, err)
}

// ── 5.23: NewServer — nil backfiller → non-nil error ─────────────────────────

func TestNewServer_NilBackfiller(t *testing.T) {
	_, err := grpcserver.NewServer(defaultGRPCCfg("127.0.0.1", "0"), defaultLimits(), defaultRetry(), noop(t), &spySchemaClient{}, nil)
	require.Error(t, err)
}

// ── 5.24: NewServer — all non-nil → BackfillService registered ────────────────

func TestNewServer_BackfillServiceRegistered(t *testing.T) {
	bf := NewMockBackfiller(t)
	srv, err := grpcserver.NewServer(
		defaultGRPCCfg("127.0.0.1", "0"),
		defaultLimits(), defaultRetry(), noop(t), &spySchemaClient{}, bf,
	)
	require.NoError(t, err)

	addr, cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	conn, dialErr := dialInsecure(t, addr)
	require.NoError(t, dialErr)
	defer conn.Close()

	// A call to GetWatermark without connector identity returns UNAUTHENTICATED —
	// any code other than Unimplemented confirms BackfillService is registered.
	bfClient := ingestionv1.NewBackfillServiceClient(conn)
	_, rpcErr := bfClient.GetWatermark(context.Background(), &ingestionv1.GetWatermarkRequest{})
	require.Error(t, rpcErr)
	assert.NotEqual(t, codes.Unimplemented, status.Code(rpcErr),
		"BackfillService must be registered — got Unimplemented which means it is missing")
}

// ── 6.1: StartGap concurrent — no panic, no deadlock ─────────────────────────

func TestBackfillStartGap_Concurrent(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("StartGap", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

	srv := newBackfillReceiver(t, bf, noop(t))
	part := partitionStruct(t, map[string]interface{}{"shard": "shared"})

	const goroutines = 10
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := srv.StartGap(ctxWithConnectorID("conn-1"), &ingestionv1.StartGapRequest{Partition: part})
			errs <- err
		}()
	}
	for i := 0; i < goroutines; i++ {
		assert.NoError(t, <-errs)
	}
}

// ── 6.2: CloseGap + Ingest concurrent — no deadlock, no panic ────────────────

func TestBackfillCloseGap_Concurrent_WithIngest(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("CloseGap", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)
	bf.On("AdvanceBackfill", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Maybe().Return(ingest.IngestResult{Accepted: 1}, nil)

	srv := newBackfillReceiver(t, bf, m)
	part := partitionStruct(t, map[string]interface{}{"shard": "shared"})
	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})

	const goroutines = 10
	errs := make(chan error, goroutines*2)

	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := srv.CloseGap(ctxWithConnectorID("conn-1"), &ingestionv1.CloseGapRequest{Partition: part})
			errs <- err
		}()
		go func() {
			_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
				Messages: []*ingestionv1.MessageEnvelope{
					{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
				},
			})
			errs <- err
		}()
	}
	for i := 0; i < goroutines*2; i++ {
		// any error is acceptable (race between close and advance); no panic is the assertion
		<-errs
	}
}

// ── 6.3: 10 goroutines Ingest same partition concurrently — no data corruption ─

func TestBackfillIngest_Concurrent(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)
	bf.On("AdvanceBackfill", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Maybe().Return(ingest.IngestResult{Accepted: 1}, nil)

	srv := newBackfillReceiver(t, bf, m)
	part := partitionStruct(t, map[string]interface{}{"shard": "shared"})
	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})

	const goroutines = 10
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
				Messages: []*ingestionv1.MessageEnvelope{
					{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
				},
			})
			errs <- err
		}()
	}
	for i := 0; i < goroutines; i++ {
		assert.NoError(t, <-errs)
	}
}

// ── additional coverage: BackfillService.Ingest ErrOverloaded and context paths ─

func TestBackfillIngest_PipelineOverloaded(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{}, ingest.ErrOverloaded)

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestBackfillIngest_ContextCanceled(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{}, context.Canceled)

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
}

func TestBackfillIngest_IngestUnexpectedError(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{}, errors.New("unexpected"))

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestBackfillIngest_WatermarkNilNoAdvanceBackfill(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	// AdvanceBackfill must NOT be called when watermark is nil

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{Accepted: 1, Outcomes: []bool{true}}, nil)

	srv := newBackfillReceiver(t, bf, m)
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: nil},
		},
	})
	require.NoError(t, err)
	bf.AssertNotCalled(t, "AdvanceBackfill")
}

func TestGetWatermark_WatermarkEncodingError(t *testing.T) {
	// structpb.NewStruct rejects values that are not valid JSON-compatible types.
	// A chan value is one such type.
	bf := NewMockBackfiller(t)
	wm := map[string]any{"ch": make(chan int)}
	bf.On("GetWatermark", mock.Anything, mock.Anything, mock.Anything).Return(wm, true, nil)

	srv := newBackfillReceiver(t, bf, noop(t))

	_, err := srv.GetWatermark(ctxWithConnectorID("conn-1"), &ingestionv1.GetWatermarkRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestBackfillIngest_DeadlineExceeded(t *testing.T) {
	bf := NewMockBackfiller(t)
	bf.On("IsGapOpen", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)

	m := NewIngestor(t)
	m.On("Ingest", mock.Anything, mock.Anything).Return(ingest.IngestResult{}, context.DeadlineExceeded)

	srv := newBackfillReceiver(t, bf, m)

	wm := watermarkStruct(t, map[string]interface{}{"offset": float64(1)})
	part := partitionStruct(t, map[string]interface{}{"shard": "a"})
	_, err := srv.Ingest(ctxWithConnectorID("conn-1"), &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{LogicalArtifactId: "art", SchemaVersion: "v1", Partition: part, Watermark: wm},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}
