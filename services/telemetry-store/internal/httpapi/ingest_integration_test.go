//go:build integration

package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-mysql-org/go-mysql/driver"
	v1 "github.com/operational-context-graph/telemetry-store/api/v1"
	"github.com/operational-context-graph/telemetry-store/internal/dorissql"
	"github.com/operational-context-graph/telemetry-store/internal/httpapi"
	"github.com/operational-context-graph/telemetry-store/internal/store"
	"github.com/operational-context-graph/telemetry-store/internal/streamload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for the ingest HTTP handlers against a real Apache Doris instance.
//
// Required environment variables:
//
//	TEST_STREAM_LOAD_URL      e.g. http://doris-fe:8030
//	TEST_STREAM_LOAD_USER     e.g. root
//	TEST_STREAM_LOAD_PASSWORD e.g. ""
//	TEST_DB_URL               MySQL DSN e.g. root:@tcp(doris-fe:9030)/
//
// Tests skip silently when TEST_STREAM_LOAD_URL is unset.
var (
	ingestHandler http.Handler
	ingestDB      *sql.DB
)

func TestMain(m *testing.M) {
	slURL := os.Getenv("TEST_STREAM_LOAD_URL")
	if slURL == "" {
		os.Exit(0)
	}
	dorissql.RegisterTimeArgs()

	sl := streamload.NewClient(
		slURL,
		os.Getenv("TEST_STREAM_LOAD_USER"),
		os.Getenv("TEST_STREAM_LOAD_PASSWORD"),
	)

	sqlDB, err := sql.Open("mysql", os.Getenv("TEST_DB_URL"))
	if err != nil {
		panic("open doris: " + err.Error())
	}
	if err := sqlDB.Ping(); err != nil {
		panic("ping doris: " + err.Error())
	}
	ingestDB = sqlDB

	db, err := store.NewFromDB(context.Background(), sqlDB)
	if err != nil {
		panic("prepare store: " + err.Error())
	}

	srv := httpapi.NewServer(db, sl, ":0")
	ingestHandler = srv.Handler

	code := m.Run()
	sqlDB.Close()
	os.Exit(code)
}

func ingestLabel(signal string) string {
	return fmt.Sprintf("%s-%s-%d", signal, time.Now().UTC().Format("20060102"), time.Now().UnixNano())
}

func postNDJSON(t *testing.T, path, label, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-OCG-Label", label)
	rr := httptest.NewRecorder()
	ingestHandler.ServeHTTP(rr, req)
	return rr
}

func countRows(t *testing.T, table, iri string, ts time.Time) int {
	t.Helper()
	from := ts.Add(-time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	to := ts.Add(time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	row := ingestDB.QueryRowContext(context.Background(),
		fmt.Sprintf(`SELECT COUNT(*) FROM ocg.%s WHERE timestamp >= ? AND timestamp < ? AND iri = ?`, table),
		from, to, iri)
	var n int
	require.NoError(t, row.Scan(&n))
	return n
}

func TestHandleIngest_Logs_RoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:log:test:hi-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-hi","severity":"INFO","body":"ingest handler integration"}`+"\n",
		ts.Format(time.RFC3339), iri,
	)

	rr := postNDJSON(t, "/v1/telemetry/logs", ingestLabel("logs"), ndjson)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp v1.IngestResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.LoadedRows)

	assert.Equal(t, 1, countRows(t, "logs", iri, ts), "log row must be in ocg.logs after ingest")
}

func TestHandleIngest_Metrics_RoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:metric:test:hi-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-hi","metric_name":"mem_free","value":1073741824}`+"\n",
		ts.Format(time.RFC3339), iri,
	)

	rr := postNDJSON(t, "/v1/telemetry/metrics", ingestLabel("metrics"), ndjson)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp v1.IngestResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.LoadedRows)

	assert.Equal(t, 1, countRows(t, "metrics", iri, ts), "metric row must be in ocg.metrics after ingest")
}

func TestHandleIngest_Traces_RoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:span:test:hi-" + fmt.Sprintf("%d", ts.UnixNano())
	traceID := fmt.Sprintf("4bf92f3577b34da6%08x", ts.UnixNano()%0xffffffff)
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-hi","trace_id":%q,"span_id":"00f067aa0ba902b7","span_name":"POST /ingest","duration_nanos":5000000,"status_code":"OK"}`+"\n",
		ts.Format(time.RFC3339), iri, traceID,
	)

	rr := postNDJSON(t, "/v1/telemetry/traces", ingestLabel("traces"), ndjson)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp v1.IngestResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.LoadedRows)

	assert.Equal(t, 1, countRows(t, "traces", iri, ts), "trace span must be in ocg.traces after ingest")
}

func TestHandleIngest_LabelIdempotency_Returns200AlreadyExists(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:log:test:idem-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-hi","severity":"WARN","body":"idempotency check"}`+"\n",
		ts.Format(time.RFC3339), iri,
	)
	label := ingestLabel("logs")

	rr1 := postNDJSON(t, "/v1/telemetry/logs", label, ndjson)
	require.Equal(t, http.StatusOK, rr1.Code, "first ingest must succeed")

	rr2 := postNDJSON(t, "/v1/telemetry/logs", label, ndjson)
	require.Equal(t, http.StatusOK, rr2.Code, "duplicate label must return 200")
	assert.Contains(t, rr2.Body.String(), "already_exists", "body must signal idempotent replay")
}

func TestHandleIngest_PartitionBoundaryViolation_Returns400(t *testing.T) {
	// Two rows on different UTC days; no DB needed — the handler rejects before calling stream load.
	twodays := `{"timestamp":"2026-10-13T23:59:59Z","iri":"urn:ocg:log:test:pb-1","host_name":"h","severity":"INFO","body":"a"}` + "\n" +
		`{"timestamp":"2026-10-14T00:00:01Z","iri":"urn:ocg:log:test:pb-2","host_name":"h","severity":"INFO","body":"b"}` + "\n"

	stubSL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("stream load must not be called on partition boundary violation")
	}))
	defer stubSL.Close()

	sl := streamload.NewClient(stubSL.URL, "u", "p")
	sqlDB, err := sql.Open("mysql", os.Getenv("TEST_DB_URL"))
	require.NoError(t, err)
	db, err := store.NewFromDB(context.Background(), sqlDB)
	require.NoError(t, err)
	srv := httpapi.NewServer(db, sl, ":0")

	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/logs", strings.NewReader(twodays))
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-OCG-Label", ingestLabel("logs"))
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleIngest_Logs_ReadBackViaQueryAPI(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:log:test:rb-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-readback","severity":"ERROR","body":"read-back test"}`+"\n",
		ts.Format(time.RFC3339), iri,
	)

	rr := postNDJSON(t, "/v1/telemetry/logs", ingestLabel("logs"), ndjson)
	require.Equal(t, http.StatusOK, rr.Code)

	from := ts.Add(-time.Minute).Format(time.RFC3339)
	to := ts.Add(time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/telemetry/logs?from=%s&to=%s&host_name=node-readback", from, to), nil)
	rrGet := httptest.NewRecorder()
	ingestHandler.ServeHTTP(rrGet, req)

	require.Equal(t, http.StatusOK, rrGet.Code)
	var resp v1.LogListResponse
	require.NoError(t, json.NewDecoder(rrGet.Body).Decode(&resp))

	var found bool
	for _, item := range resp.Items {
		if item.IRI == iri {
			found = true
			require.NotNil(t, item.Body)
			assert.Equal(t, "read-back test", *item.Body)
		}
	}
	assert.True(t, found, "ingested row must be returned by the query API")
}
