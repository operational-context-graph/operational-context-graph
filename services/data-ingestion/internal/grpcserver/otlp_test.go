// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"

	errdetailspb "google.golang.org/genproto/googleapis/rpc/errdetails"
)

// ── test doubles ─────────────────────────────────────────────────────────────

// spyIngestor records calls and can be configured to return an error.
type spyIngestor struct {
	mu     sync.Mutex
	calls  [][]ingest.IngestRecord
	retErr error
	retRes ingest.IngestResult
}

func (s *spyIngestor) Ingest(_ context.Context, batch []ingest.IngestRecord) (ingest.IngestResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, batch)
	s.mu.Unlock()
	return s.retRes, s.retErr
}

func (s *spyIngestor) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *spyIngestor) lastBatch() []ingest.IngestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil
	}
	return s.calls[len(s.calls)-1]
}

// spySchemaClient counts DefaultLogSchema calls and can be configured to return an error.
type spySchemaClient struct {
	logCalls    atomic.Int64
	metricCalls atomic.Int64
	traceCalls  atomic.Int64
	retErr      error
}

func (s *spySchemaClient) DefaultLogSchema(_ context.Context) (ingest.SchemaKey, error) {
	s.logCalls.Add(1)
	if s.retErr != nil {
		return ingest.SchemaKey{}, s.retErr
	}
	return ingest.SchemaKey{LogicalArtifactType: "ocg.otlp.logs", Version: "1.0.0"}, nil
}

func (s *spySchemaClient) DefaultMetricSchema(_ context.Context) (ingest.SchemaKey, error) {
	s.metricCalls.Add(1)
	if s.retErr != nil {
		return ingest.SchemaKey{}, s.retErr
	}
	return ingest.SchemaKey{LogicalArtifactType: "ocg.otlp.metrics", Version: "1.0.0"}, nil
}

func (s *spySchemaClient) DefaultTraceSchema(_ context.Context) (ingest.SchemaKey, error) {
	s.traceCalls.Add(1)
	if s.retErr != nil {
		return ingest.SchemaKey{}, s.retErr
	}
	return ingest.SchemaKey{LogicalArtifactType: "ocg.otlp.traces", Version: "1.0.0"}, nil
}

// ── bufconn helpers ───────────────────────────────────────────────────────────

const otlpBufSize = 1 << 20

var testRetry = &config.RetryConfig{
	RetryDelay: 100 * time.Millisecond,
}

var testLimits = &config.LimitsConfig{MaxMessages: 100, MaxPayloadBytes: 4 * 1024 * 1024}

var testGRPCCfg = &config.GRPCConfig{Host: "127.0.0.1", Port: 0, ShutdownTimeout: 5 * time.Second}

type otlpClients struct {
	logs    collectorlogsv1.LogsServiceClient
	metrics collectormetricsv1.MetricsServiceClient
	traces  collectortracesv1.TraceServiceClient
}

// newOTLPBufconn starts a full grpcserver.Server on a bufconn listener using the
// provided ingestor and schemaClient. Both are required. Returns connected clients
// and a cleanup function.
func newOTLPBufconn(t *testing.T, ingestor ingest.Ingestor, schemaClient grpcserver.SchemaRegistryClient) (otlpClients, func()) {
	t.Helper()

	srv, err := grpcserver.NewServer(testGRPCCfg, testLimits, testRetry, ingestor, schemaClient, noopBackfiller(t))
	require.NoError(t, err)

	lis := bufconn.Listen(otlpBufSize)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- srv.RunOnListener(ctx, lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(c context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(c)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	clients := otlpClients{
		logs:    collectorlogsv1.NewLogsServiceClient(conn),
		metrics: collectormetricsv1.NewMetricsServiceClient(conn),
		traces:  collectortracesv1.NewTraceServiceClient(conn),
	}
	return clients, func() {
		conn.Close()
		cancel()
		<-errc
	}
}

// ── sample requests ───────────────────────────────────────────────────────────

func sampleResource(name string) *resourcev1.Resource {
	return &resourcev1.Resource{
		Attributes: []*commonv1.KeyValue{
			{Key: "service.name", Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: name},
			}},
		},
	}
}

func sampleLogsReq() *collectorlogsv1.ExportLogsServiceRequest {
	return &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			Resource: sampleResource("test"),
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "test log"}},
				}},
			}},
		}},
	}
}

func sampleMetricsReq() *collectormetricsv1.ExportMetricsServiceRequest {
	return &collectormetricsv1.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: sampleResource("test"),
			ScopeMetrics: []*metricsv1.ScopeMetrics{{
				Metrics: []*metricsv1.Metric{{
					Name: "test.metric",
				}},
			}},
		}},
	}
}

func sampleTracesReq() *collectortracesv1.ExportTraceServiceRequest {
	return &collectortracesv1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{
			Resource: sampleResource("test"),
			ScopeSpans: []*tracev1.ScopeSpans{{
				Spans: []*tracev1.Span{{
					Name: "test-span",
				}},
			}},
		}},
	}
}

func hasRetryInfo(t *testing.T, err error) {
	t.Helper()
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	for _, d := range st.Details() {
		if _, ok := d.(*errdetailspb.RetryInfo); ok {
			return
		}
	}
	t.Errorf("expected RetryInfo in status details, got %v", st.Details())
}

// ── constructor tests ─────────────────────────────────────────────────────────

func TestNewServer_NilSchemaClient(t *testing.T) {
	_, err := grpcserver.NewServer(testGRPCCfg, testLimits, testRetry, &spyIngestor{}, nil, noopBackfiller(t))
	require.Error(t, err)
}

func TestNewServer_ValidOTLP(t *testing.T) {
	srv, err := grpcserver.NewServer(testGRPCCfg, testLimits, testRetry, &spyIngestor{}, &spySchemaClient{}, noopBackfiller(t))
	require.NoError(t, err)
	require.NotNil(t, srv)
}

// ── happy path: logs ──────────────────────────────────────────────────────────

func TestOTLPLogs_HappyPath(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.NoError(t, err)
	assert.Equal(t, 1, spy.callCount())
}

func TestOTLPLogs_EmptyRequest(t *testing.T) {
	spy := &spyIngestor{}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), &collectorlogsv1.ExportLogsServiceRequest{})
	require.NoError(t, err)
	assert.Equal(t, 0, spy.callCount(), "Ingestor must not be called for empty request")
}

// ── happy path: metrics ───────────────────────────────────────────────────────

func TestOTLPMetrics_HappyPath(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.metrics.Export(context.Background(), sampleMetricsReq())
	require.NoError(t, err)
	assert.Equal(t, 1, spy.callCount())
}

func TestOTLPMetrics_EmptyRequest(t *testing.T) {
	spy := &spyIngestor{}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.metrics.Export(context.Background(), &collectormetricsv1.ExportMetricsServiceRequest{})
	require.NoError(t, err)
	assert.Equal(t, 0, spy.callCount())
}

// ── happy path: traces ────────────────────────────────────────────────────────

func TestOTLPTraces_HappyPath(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.traces.Export(context.Background(), sampleTracesReq())
	require.NoError(t, err)
	assert.Equal(t, 1, spy.callCount())
}

func TestOTLPTraces_EmptyRequest(t *testing.T) {
	spy := &spyIngestor{}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.traces.Export(context.Background(), &collectortracesv1.ExportTraceServiceRequest{})
	require.NoError(t, err)
	assert.Equal(t, 0, spy.callCount())
}

// ── IngestRecord fields ───────────────────────────────────────────────────────

func TestOTLPLogs_IngestRecordFields(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"logical_artifact_type", "ocg.test",
		"schema_version", "2.0.0",
	))
	_, err := clients.logs.Export(ctx, sampleLogsReq())
	require.NoError(t, err)

	batch := spy.lastBatch()
	require.Len(t, batch, 1)
	rec := batch[0]
	assert.Equal(t, "ocg.test", rec.Schema.LogicalArtifactType)
	assert.Equal(t, "2.0.0", rec.Schema.Version)
	assert.Empty(t, rec.Partition)
	assert.Nil(t, rec.Watermark)
	require.NotNil(t, rec.ContentType)
	assert.Equal(t, "application/json", rec.ContentType)
	assert.NotEmpty(t, rec.Payload)
	// Payload is the log record JSON directly (no envelope wrapper).
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Payload, &fields))
	assert.Contains(t, fields, "body")
}

// ── error paths ───────────────────────────────────────────────────────────────

func TestOTLPLogs_DefaultSchemaError(t *testing.T) {
	spy := &spyIngestor{}
	schemaClient := &spySchemaClient{retErr: errors.New("registry unavailable")}
	clients, cleanup := newOTLPBufconn(t, spy, schemaClient)
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	hasRetryInfo(t, err)
	assert.Equal(t, 0, spy.callCount(), "Ingestor must not be called when schema resolution fails")
}

func TestOTLPLogs_IngestorError(t *testing.T) {
	spy := &spyIngestor{retErr: ingest.ErrOverloaded}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	hasRetryInfo(t, err)
}

func TestOTLPLogs_IngestorUnavailable(t *testing.T) {
	spy := &spyIngestor{retErr: ingest.ErrUnavailable}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	hasRetryInfo(t, err)
}

func TestOTLPLogs_ContextCancelled(t *testing.T) {
	spy := &spyIngestor{}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := clients.logs.Export(ctx, sampleLogsReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestOTLPLogs_AllRejected(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 0, RejectedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.NoError(t, err, "pipeline rejections must not fail the OTLP response")
}

func TestOTLPLogs_SchemaHeader_EmptyString(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	schemaClient := &spySchemaClient{}
	clients, cleanup := newOTLPBufconn(t, spy, schemaClient)
	defer cleanup()

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"logical_artifact_type", "",
		"schema_version", "",
	))
	_, err := clients.logs.Export(ctx, sampleLogsReq())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, int(schemaClient.logCalls.Load()), 1, "DefaultLogSchema must be called for empty-string headers")
}

func TestOTLPLogs_SchemaHeader_PartialOverride(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"logical_artifact_type", "ocg.custom",
	))
	_, err := clients.logs.Export(ctx, sampleLogsReq())
	require.NoError(t, err)

	batch := spy.lastBatch()
	require.Len(t, batch, 1)
	assert.Equal(t, "ocg.custom", batch[0].Schema.LogicalArtifactType)
	assert.Equal(t, "1.0.0", batch[0].Schema.Version, "version must fall back to noop default")
}

func TestOTLPLogs_DefaultResolvedOnce(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	schemaClient := &spySchemaClient{}
	clients, cleanup := newOTLPBufconn(t, spy, schemaClient)
	defer cleanup()

	// A single Export call with no headers — DefaultLogSchema must be called exactly once.
	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.NoError(t, err)
	assert.Equal(t, int64(1), schemaClient.logCalls.Load(),
		"DefaultLogSchema must be called exactly once per Export invocation")
}

// ── concurrency tests ─────────────────────────────────────────────────────────

func TestOTLPLogs_ConcurrentExport(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	const n = 50
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = clients.logs.Export(context.Background(), sampleLogsReq())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d got error", i)
	}
	assert.Equal(t, n, spy.callCount())
}

func TestOTLP_ConcurrentMixedSignalTypes(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n * 3)
	errs := make([]error, n*3)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = clients.logs.Export(context.Background(), sampleLogsReq())
		}(i)
		go func(idx int) {
			defer wg.Done()
			_, errs[n+idx] = clients.metrics.Export(context.Background(), sampleMetricsReq())
		}(i)
		go func(idx int) {
			defer wg.Done()
			_, errs[2*n+idx] = clients.traces.Export(context.Background(), sampleTracesReq())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d got error", i)
	}
	assert.Equal(t, n*3, spy.callCount())
}

// ── integration smoke test (task 8.1) ────────────────────────────────────────

func TestOTLP_EndToEnd_Logs(t *testing.T) {
	spy := &spyIngestor{retRes: ingest.IngestResult{AcceptedCount: 1}}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.NoError(t, err)
	assert.Equal(t, 1, spy.callCount(), "Ingest must be called once")
}

// ── additional error path coverage ───────────────────────────────────────────

func TestOTLPMetrics_IngestorError(t *testing.T) {
	spy := &spyIngestor{retErr: ingest.ErrOverloaded}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.metrics.Export(context.Background(), sampleMetricsReq())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	hasRetryInfo(t, err)
}

func TestOTLPTraces_IngestorError(t *testing.T) {
	spy := &spyIngestor{retErr: ingest.ErrOverloaded}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.traces.Export(context.Background(), sampleTracesReq())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	hasRetryInfo(t, err)
}

func TestOTLPMetrics_SchemaError(t *testing.T) {
	spy := &spyIngestor{}
	schemaClient := &spySchemaClient{retErr: errors.New("registry unavailable")}
	clients, cleanup := newOTLPBufconn(t, spy, schemaClient)
	defer cleanup()

	_, err := clients.metrics.Export(context.Background(), sampleMetricsReq())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	hasRetryInfo(t, err)
	assert.Equal(t, 0, spy.callCount())
}

func TestOTLPTraces_SchemaError(t *testing.T) {
	spy := &spyIngestor{}
	schemaClient := &spySchemaClient{retErr: errors.New("registry unavailable")}
	clients, cleanup := newOTLPBufconn(t, spy, schemaClient)
	defer cleanup()

	_, err := clients.traces.Export(context.Background(), sampleTracesReq())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	hasRetryInfo(t, err)
	assert.Equal(t, 0, spy.callCount())
}

func TestOTLPLogs_PipelineDeadlineExceeded(t *testing.T) {
	spy := &spyIngestor{retErr: context.DeadlineExceeded}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestOTLPLogs_PipelineCanceled(t *testing.T) {
	spy := &spyIngestor{retErr: context.Canceled}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
}

func TestOTLPLogs_PipelineUnexpectedError(t *testing.T) {
	spy := &spyIngestor{retErr: errors.New("something unexpected")}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestOTLPLogs_ErrUnavailable(t *testing.T) {
	spy := &spyIngestor{retErr: ingest.ErrUnavailable}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	_, err := clients.logs.Export(context.Background(), sampleLogsReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestOTLPMetrics_ContextCancelled(t *testing.T) {
	spy := &spyIngestor{}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := clients.metrics.Export(ctx, sampleMetricsReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestOTLPTraces_ContextCancelled(t *testing.T) {
	spy := &spyIngestor{}
	clients, cleanup := newOTLPBufconn(t, spy, &spySchemaClient{})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := clients.traces.Export(ctx, sampleTracesReq())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}
