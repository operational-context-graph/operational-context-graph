package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/operational-context-graph/telemetry-store/api/v1"
	"github.com/operational-context-graph/telemetry-store/internal/apperr"
	"github.com/operational-context-graph/telemetry-store/internal/httpapi"
	"github.com/operational-context-graph/telemetry-store/internal/store"
	"github.com/operational-context-graph/telemetry-store/internal/streamload"
	"github.com/operational-context-graph/telemetry-store/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTelemetryDB is a hand-written stub that satisfies the telemetryDB interface.
type stubTelemetryDB struct {
	queryLogsFn    func(ctx context.Context, q store.LogQuery) ([]store.Log, error)
	queryMetricsFn func(ctx context.Context, q store.MetricQuery) ([]store.Metric, error)
	schemaReadyFn  func(ctx context.Context) error
}

func (s *stubTelemetryDB) QueryLogs(ctx context.Context, q store.LogQuery) ([]store.Log, error) {
	if s.queryLogsFn != nil {
		return s.queryLogsFn(ctx, q)
	}
	return nil, nil
}
func (s *stubTelemetryDB) QueryMetrics(ctx context.Context, q store.MetricQuery) ([]store.Metric, error) {
	if s.queryMetricsFn != nil {
		return s.queryMetricsFn(ctx, q)
	}
	return nil, nil
}
func (s *stubTelemetryDB) Ping(_ context.Context) error { return nil }
func (s *stubTelemetryDB) SchemaReady(ctx context.Context) error {
	if s.schemaReadyFn != nil {
		return s.schemaReadyFn(ctx)
	}
	return nil
}

// stubStreamLoader is a hand-written stub that satisfies the streamLoader interface.
type stubStreamLoader struct {
	loadFn func(ctx context.Context, table, label string, body io.Reader) (*streamload.LoadResult, error)
}

func (s *stubStreamLoader) Load(ctx context.Context, table, label string, body io.Reader) (*streamload.LoadResult, error) {
	if s.loadFn != nil {
		return s.loadFn(ctx, table, label, body)
	}
	return &streamload.LoadResult{NumberLoadedRows: 1}, nil
}

func TestHandleQueryLogs_OK(t *testing.T) {
	now := time.Now().UTC()
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, _ store.LogQuery) ([]store.Log, error) {
			body := "hello"
			return []store.Log{{Timestamp: now, IRI: "r1", Body: &body}}, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)

	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.LogListResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Len(t, resp.Items, 1)
	assert.Equal(t, "r1", resp.Items[0].IRI)
}

func TestHandleQueryLogs_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs")
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleQueryLogs_OnlyFrom_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from=2026-01-01T00:00:00Z")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryLogs_MaxSpanExceeded_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from=2026-01-01T00:00:00Z&to=2026-01-03T00:00:00Z")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryMetrics_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/metrics")
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleQueryTrace_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/traces/abc")
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHealthEndpoint_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/healthz")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestLivenessEndpoint_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/healthz")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestReadinessEndpoint_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/readyz")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestReadinessEndpoint_SchemaNotReady_Returns503(t *testing.T) {
	stub := &stubTelemetryDB{}
	stub.schemaReadyFn = func(_ context.Context) error { return errors.New("table missing") }
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestRequestIDMiddleware_SetsHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/healthz")
	assert.NotEmpty(t, rr.Header().Get("X-Request-Id"))
}

func TestUnmatchedRoute_Returns404WithProblemJSON(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/does-not-exist/extra")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestRequestIDMiddleware_EchoesExistingHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "my-fixed-id")
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	assert.Equal(t, "my-fixed-id", rr.Header().Get("X-Request-Id"))
}

// --- Ingest tests ---

func ndjsonPost(t *testing.T, handler http.Handler, path, label, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-ndjson")
	if label != "" {
		req.Header.Set("X-OCG-Label", label)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

const validLogLine = `{"timestamp":"2026-10-13T10:00:00Z","host":"node-01","severity":"INFO","body":"ok"}` + "\n"

func TestHandleIngestLogs_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-001", validLogLine)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.IngestResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.LoadedRows)
}

func TestHandleIngestLogs_MissingLabel_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "", validLogLine)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleIngestLogs_WrongContentType_Returns415(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/logs", strings.NewReader(validLogLine))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Stream-Load-Label", "logs-2026-10-13-001")
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestHandleIngestLogs_EmptyBatch_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-001", "\n\n")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleIngestLogs_PartitionBoundaryViolation_Returns400(t *testing.T) {
	twodays := `{"timestamp":"2026-10-13T23:59:59Z","host":"h","severity":"INFO","body":"a"}` + "\n" +
		`{"timestamp":"2026-10-14T00:00:01Z","host":"h","severity":"INFO","body":"b"}` + "\n"
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-001", twodays)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleIngestLogs_LabelExists_Returns200(t *testing.T) {
	stub := &stubStreamLoader{
		loadFn: func(_ context.Context, _, _ string, _ io.Reader) (*streamload.LoadResult, error) {
			return nil, apperr.ErrLabelExists
		},
	}
	srv := httpapi.NewServer(&stubTelemetryDB{}, stub, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-001", validLogLine)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "already_exists")
}

func TestHandleIngestLogs_DorisLoadFailed_Returns502(t *testing.T) {
	stub := &stubStreamLoader{
		loadFn: func(_ context.Context, _, _ string, _ io.Reader) (*streamload.LoadResult, error) {
			return nil, apperr.ErrDorisLoadFailed
		},
	}
	srv := httpapi.NewServer(&stubTelemetryDB{}, stub, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-001", validLogLine)
	assert.Equal(t, http.StatusBadGateway, rr.Code)
}

func TestHandleIngestLogs_DorisOverloaded_Returns503WithRetryAfter(t *testing.T) {
	stub := &stubStreamLoader{
		loadFn: func(_ context.Context, _, _ string, _ io.Reader) (*streamload.LoadResult, error) {
			return nil, apperr.ErrDorisOverloaded
		},
	}
	srv := httpapi.NewServer(&stubTelemetryDB{}, stub, ":0", 10*1024*1024*1024)
	rr := ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-001", validLogLine)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Equal(t, "5", rr.Header().Get("Retry-After"))
}

func TestHandleIngestLogs_LabelForwardedToLoader(t *testing.T) {
	var capturedLabel string
	stub := &stubStreamLoader{
		loadFn: func(_ context.Context, _, label string, _ io.Reader) (*streamload.LoadResult, error) {
			capturedLabel = label
			return &streamload.LoadResult{NumberLoadedRows: 1}, nil
		},
	}
	srv := httpapi.NewServer(&stubTelemetryDB{}, stub, ":0", 10*1024*1024*1024)
	ndjsonPost(t, srv.Handler, "/v1/telemetry/logs", "logs-2026-10-13-007", validLogLine)
	assert.Equal(t, "logs-2026-10-13-007", capturedLabel)
}

// --- Filter forwarding tests ---

func TestHandleQueryLogs_HostNameFilterForwarded(t *testing.T) {
	var capturedQ store.LogQuery
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, q store.LogQuery) ([]store.Log, error) {
			capturedQ = q
			return nil, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to+"&host_name=my-host")
	assert.Equal(t, "my-host", capturedQ.HostName)
}

func TestHandleQueryLogs_SeverityFilterForwarded(t *testing.T) {
	var capturedQ store.LogQuery
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, q store.LogQuery) ([]store.Log, error) {
			capturedQ = q
			return nil, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to+"&severity=ERROR")
	assert.Equal(t, "ERROR", capturedQ.Severity)
}

func TestHandleQueryMetrics_MetricNameFilterForwarded(t *testing.T) {
	var capturedQ store.MetricQuery
	stub := &stubTelemetryDB{
		queryMetricsFn: func(_ context.Context, q store.MetricQuery) ([]store.Metric, error) {
			capturedQ = q
			return nil, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	test.GetRequest(t, srv.Handler, "/v1/telemetry/metrics?from="+from+"&to="+to+"&metric_name=cpu_usage")
	assert.Equal(t, "cpu_usage", capturedQ.MetricName)
}

func TestHandleQueryLogs_LimitForwarded(t *testing.T) {
	var capturedQ store.LogQuery
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, q store.LogQuery) ([]store.Log, error) {
			capturedQ = q
			return nil, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to+"&limit=50")
	assert.Equal(t, 50, capturedQ.Limit)
}

func TestHandleQueryLogs_AfterCursorForwarded(t *testing.T) {
	var capturedQ store.LogQuery
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, q store.LogQuery) ([]store.Log, error) {
			capturedQ = q
			return nil, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)

	// Build a valid cursor string.
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := store.Cursor{TS: ts, IRI: "urn:ocg:log:test:1"}
	encoded := cur.Encode()

	now := time.Now().UTC()
	from := now.Add(-time.Hour).Format(time.RFC3339)
	to := now.Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to+"&after="+encoded)

	require.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedQ.AfterTS)
	assert.True(t, ts.Equal(*capturedQ.AfterTS))
	require.NotNil(t, capturedQ.AfterIRI)
	assert.Equal(t, "urn:ocg:log:test:1", *capturedQ.AfterIRI)
}

func TestHandleQueryLogs_InvalidCursor_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to+"&after=!!!invalid!!!")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryMetrics_InvalidCursor_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/metrics?from="+from+"&to="+to+"&after=!!!invalid!!!")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryLogs_DBError_Returns500(t *testing.T) {
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, _ store.LogQuery) ([]store.Log, error) {
			return nil, errors.New("database gone away")
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryMetrics_DBError_Returns500(t *testing.T) {
	stub := &stubTelemetryDB{
		queryMetricsFn: func(_ context.Context, _ store.MetricQuery) ([]store.Metric, error) {
			return nil, errors.New("database gone away")
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/metrics?from="+from+"&to="+to)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryLogs_NextCursorSetWhenFullPage(t *testing.T) {
	now := time.Now().UTC()
	logs := make([]store.Log, 10)
	for i := range logs {
		logs[i] = store.Log{Timestamp: now, IRI: "urn:ocg:log:test:" + time.Duration(i).String()}
	}
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, _ store.LogQuery) ([]store.Log, error) {
			return logs, nil
		},
	}
	srv := httpapi.NewServer(stub, &stubStreamLoader{}, ":0", 10*1024*1024*1024)
	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := test.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to+"&limit=10")

	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.LogListResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotEmpty(t, resp.NextCursor, "next_cursor must be set when the page is full")
}

