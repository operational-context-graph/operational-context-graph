// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store/telemetry"
)

type storeResp struct {
	Status             string `json:"Status"`
	NumberLoadedRows   int64  `json:"NumberLoadedRows"`
	NumberFilteredRows int64  `json:"NumberFilteredRows"`
	ErrorURL           string `json:"ErrorURL"`
}

func logRecord(payload string) store.Record {
	return store.Record{
		Schema:      ingest.SchemaKey{LogicalArtifactType: "logs", Version: "1.0.0"},
		Payload:     []byte(payload),
		ContentType: "application/json",
	}
}

func metricRecord(payload string) store.Record {
	return store.Record{
		Schema:      ingest.SchemaKey{LogicalArtifactType: "metrics", Version: "1.0.0"},
		Payload:     []byte(payload),
		ContentType: "application/json",
	}
}

func traceRecord(payload string) store.Record {
	return store.Record{
		Schema:      ingest.SchemaKey{LogicalArtifactType: "traces", Version: "1.0.0"},
		Payload:     []byte(payload),
		ContentType: "application/json",
	}
}

func batch(id int64, recs ...store.Record) store.Batch {
	return store.Batch{ID: id, Records: recs}
}

func fakeServer(t *testing.T, handler http.HandlerFunc) *telemetry.Writer {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return telemetry.New(telemetry.Config{BaseURL: srv.URL})
}

func respondJSON(w http.ResponseWriter, resp storeResp) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ── happy path ────────────────────────────────────────────────────────────────

func TestWriter_Write_Success(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		respondJSON(rw, storeResp{Status: "Success", NumberLoadedRows: 1})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))

	require.NoError(t, err)
}

func TestWriter_Write_EmptyBatchIsNoOp(t *testing.T) {
	called := false
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		called = true
	})

	require.NoError(t, w.Write(context.Background(), store.Batch{ID: 1}))
	assert.False(t, called)
}

// ── idempotency ───────────────────────────────────────────────────────────────

func TestWriter_Write_LabelAlreadyExists_IsSuccess(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		respondJSON(rw, storeResp{Status: "Label Already Exists"})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))
	require.NoError(t, err)
}

func TestWriter_Write_PublishTimeout_IsSuccess(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		respondJSON(rw, storeResp{Status: "Publish Timeout"})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))
	require.NoError(t, err)
}

func TestWriter_Write_LabelDerivedFromBatchID(t *testing.T) {
	var labels []string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		labels = append(labels, r.Header.Get("X-OCG-Label"))
		respondJSON(rw, storeResp{Status: "Success"})
	})

	// Same batch ID → same label on every retry.
	require.NoError(t, w.Write(context.Background(), batch(42, logRecord(`{"iri":"urn:1"}`))))
	require.NoError(t, w.Write(context.Background(), batch(42, logRecord(`{"iri":"urn:1"}`))))

	require.Len(t, labels, 2)
	assert.Equal(t, labels[0], labels[1])
	assert.Contains(t, labels[0], "42")
}

func TestWriter_Write_DifferentBatchIDs_DifferentLabels(t *testing.T) {
	var labels []string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		labels = append(labels, r.Header.Get("X-OCG-Label"))
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`))))
	require.NoError(t, w.Write(context.Background(), batch(2, logRecord(`{"iri":"urn:1"}`))))

	require.Len(t, labels, 2)
	assert.NotEqual(t, labels[0], labels[1])
}

func TestWriter_Write_LabelHeaderPresent(t *testing.T) {
	var gotLabel string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		gotLabel = r.Header.Get("X-OCG-Label")
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`))))
	assert.NotEmpty(t, gotLabel)
}

func TestWriter_Write_LabelFormat(t *testing.T) {
	var gotLabel string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		gotLabel = r.Header.Get("X-OCG-Label")
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(7, logRecord(`{"iri":"urn:1"}`))))
	assert.Equal(t, "logs-7", gotLabel)
}

// ── permanent failures ────────────────────────────────────────────────────────

func TestWriter_Write_StoreFail_IsPermanentError(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		respondJSON(rw, storeResp{Status: "Fail"})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	var pe *store.PermanentError
	assert.True(t, errors.As(err, &pe))
}

func TestWriter_Write_UnknownSignalType_IsPermanentError(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		respondJSON(rw, storeResp{Status: "Success"})
	})

	unknown := store.Record{
		Schema:      ingest.SchemaKey{LogicalArtifactType: "ocg.unknown.event", Version: "1.0.0"},
		Payload:     []byte(`{"iri":"urn:1"}`),
		ContentType: "application/json",
	}
	err := w.Write(context.Background(), batch(1, unknown))

	require.Error(t, err)
	var pe *store.PermanentError
	assert.True(t, errors.As(err, &pe))
}

// ── back-pressure ─────────────────────────────────────────────────────────────

func TestWriter_Write_HTTP503_IsOverloaded(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusServiceUnavailable)
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
}

func TestWriter_Write_HTTP429_IsOverloaded(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusTooManyRequests)
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
}

func TestWriter_Write_HTTP500_IsOverloaded(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
}

// ── data loss warning ─────────────────────────────────────────────────────────

func TestWriter_Write_FilteredRows_ReturnsNil(t *testing.T) {
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		respondJSON(rw, storeResp{Status: "Success", NumberLoadedRows: 1, NumberFilteredRows: 2})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`)))
	require.NoError(t, err)
}

// ── wire format ───────────────────────────────────────────────────────────────

func TestWriter_Write_NDJSONFormat(t *testing.T) {
	var body string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		respondJSON(rw, storeResp{Status: "Success"})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`), logRecord(`{"iri":"urn:2"}`)))

	require.NoError(t, err)
	lines := strings.Split(body, "\n")
	assert.Len(t, lines, 2, "NDJSON must be two lines with no trailing newline")
	assert.JSONEq(t, `{"iri":"urn:1"}`, lines[0])
	assert.JSONEq(t, `{"iri":"urn:2"}`, lines[1])
}

func TestWriter_Write_ContentTypeIsNDJSON(t *testing.T) {
	var gotCT string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`))))
	assert.Equal(t, "application/x-ndjson", gotCT)
}

// ── signal routing ────────────────────────────────────────────────────────────

func TestWriter_Write_RoutesLogsToLogsEndpoint(t *testing.T) {
	var path string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`))))
	assert.Equal(t, "/v1/telemetry/logs", path)
}

func TestWriter_Write_RoutesMetricsToMetricsEndpoint(t *testing.T) {
	var path string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(1, metricRecord(`{"iri":"urn:1"}`))))
	assert.Equal(t, "/v1/telemetry/metrics", path)
}

func TestWriter_Write_RoutesTracesToTracesEndpoint(t *testing.T) {
	var path string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		respondJSON(rw, storeResp{Status: "Success"})
	})

	require.NoError(t, w.Write(context.Background(), batch(1, traceRecord(`{"iri":"urn:1"}`))))
	assert.Equal(t, "/v1/telemetry/traces", path)
}

func TestWriter_Write_MixedSignals_SendsTwoRequests(t *testing.T) {
	var paths []string
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		respondJSON(rw, storeResp{Status: "Success"})
	})

	err := w.Write(context.Background(), batch(1, logRecord(`{"iri":"urn:1"}`), metricRecord(`{"iri":"urn:2"}`)))

	require.NoError(t, err)
	assert.Len(t, paths, 2)
	assert.ElementsMatch(t, []string{"/v1/telemetry/logs", "/v1/telemetry/metrics"}, paths)
}

// ── writer metadata ───────────────────────────────────────────────────────────

func TestWriter_StoreID(t *testing.T) {
	w := telemetry.New(telemetry.Config{BaseURL: "http://localhost"})
	assert.Equal(t, store.StoreID("telemetry"), w.StoreID())
}

func TestWriter_Limits(t *testing.T) {
	w := telemetry.New(telemetry.Config{BaseURL: "http://localhost"})
	limits := w.Limits()
	assert.Equal(t, int64(500<<20), limits.MaxBytes)
	assert.Equal(t, 0, limits.MaxRecords)
	assert.Equal(t, 30*time.Second, limits.MaxLinger)
}

// ── label per signal group ────────────────────────────────────────────────────

func TestWriter_Write_MixedSignals_DistinctLabelsPerSignal(t *testing.T) {
	labels := map[string]string{}
	w := fakeServer(t, func(rw http.ResponseWriter, r *http.Request) {
		labels[r.URL.Path] = r.Header.Get("X-OCG-Label")
		respondJSON(rw, storeResp{Status: "Success"})
	})

	err := w.Write(context.Background(), batch(5, logRecord(`{"iri":"urn:1"}`), metricRecord(`{"iri":"urn:2"}`)))

	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("logs-%d", 5), labels["/v1/telemetry/logs"])
	assert.Equal(t, fmt.Sprintf("metrics-%d", 5), labels["/v1/telemetry/metrics"])
}
