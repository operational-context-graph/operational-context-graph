// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1 "github.com/operational-context-graph/telemetry-store/api/v1"
	"github.com/operational-context-graph/telemetry-store/internal/httpapi"
	"github.com/operational-context-graph/telemetry-store/internal/store"
	"github.com/operational-context-graph/telemetry-store/internal/testhelper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTelemetryDB is a hand-written stub that satisfies the telemetryDB interface.
type stubTelemetryDB struct {
	queryLogsFn     func(ctx context.Context, q store.LogQuery) ([]store.Log, error)
	queryMetricsFn  func(ctx context.Context, q store.MetricQuery) ([]store.Metric, error)
	queryTraceByIDFn func(ctx context.Context, traceID string, from, to time.Time) ([]store.Trace, error)
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
func (s *stubTelemetryDB) QueryTraceByID(ctx context.Context, traceID string, from, to time.Time) ([]store.Trace, error) {
	if s.queryTraceByIDFn != nil {
		return s.queryTraceByIDFn(ctx, traceID, from, to)
	}
	return nil, nil
}
func (s *stubTelemetryDB) Ping(_ context.Context) error { return nil }
func (s *stubTelemetryDB) SchemaReady(_ context.Context) error { return nil }

func TestHandleQueryLogs_OK(t *testing.T) {
	now := time.Now().UTC()
	stub := &stubTelemetryDB{
		queryLogsFn: func(_ context.Context, _ store.LogQuery) ([]store.Log, error) {
			body := "hello"
			return []store.Log{{Timestamp: now, RecordID: "r1", Body: &body}}, nil
		},
	}
	srv := httpapi.NewServer(stub, ":0")

	from := now.Add(-time.Minute).Format(time.RFC3339)
	to := now.Add(time.Minute).Format(time.RFC3339)
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from="+from+"&to="+to)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.LogListResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Len(t, resp.Items, 1)
	assert.Equal(t, "r1", resp.Items[0].RecordID)
}

func TestHandleQueryLogs_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/telemetry/logs")
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleQueryLogs_OnlyFrom_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from=2026-01-01T00:00:00Z")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryLogs_MaxSpanExceeded_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/telemetry/logs?from=2026-01-01T00:00:00Z&to=2026-01-03T00:00:00Z")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleQueryMetrics_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/telemetry/metrics")
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleQueryTrace_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/telemetry/traces/abc")
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHealthEndpoint_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/health")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequestIDMiddleware_SetsHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/health")
	assert.NotEmpty(t, rr.Header().Get("X-Request-Id"))
}

func TestRequestIDMiddleware_EchoesExistingHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubTelemetryDB{}, ":0")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "my-fixed-id")
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	assert.Equal(t, "my-fixed-id", rr.Header().Get("X-Request-Id"))
}
