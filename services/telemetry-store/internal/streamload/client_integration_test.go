// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package streamload_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-mysql-org/go-mysql/driver"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/apperr"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorissql"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/streamload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for streamload.Client against a real Apache Doris instance.
//
// Required environment variables:
//
//	TEST_STREAM_LOAD_URL      e.g. http://doris-fe:8030
//	TEST_STREAM_LOAD_USER     e.g. root
//	TEST_STREAM_LOAD_PASSWORD e.g. ""
//	TEST_DB_URL               MySQL DSN for read-back queries, e.g. root:@tcp(doris-fe:9030)/
//
// Tests skip silently when TEST_STREAM_LOAD_URL is unset.
var (
	slClient *streamload.Client
	slDB     *sql.DB
)

func TestMain(m *testing.M) {
	slURL := os.Getenv("TEST_STREAM_LOAD_URL")
	if slURL == "" {
		os.Exit(0)
	}
	dorissql.RegisterTimeArgs()

	slClient = streamload.NewClient(
		slURL,
		os.Getenv("TEST_STREAM_LOAD_USER"),
		os.Getenv("TEST_STREAM_LOAD_PASSWORD"),
	)

	db, err := sql.Open("mysql", os.Getenv("TEST_DB_URL"))
	if err != nil {
		panic("open doris for read-back: " + err.Error())
	}
	if err := db.Ping(); err != nil {
		panic("ping doris for read-back: " + err.Error())
	}
	slDB = db

	code := m.Run()
	db.Close()
	os.Exit(code)
}

func uniqueLabel(signal string) string {
	return fmt.Sprintf("%s-%s-%d", signal, time.Now().UTC().Format("20060102"), time.Now().UnixNano())
}

func rowExistsInLogs(t *testing.T, iri string, ts time.Time) bool {
	t.Helper()
	from := ts.Add(-time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	to := ts.Add(time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	row := slDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ocg.logs WHERE timestamp >= ? AND timestamp < ? AND iri = ?`,
		from, to, iri)
	var n int
	require.NoError(t, row.Scan(&n))
	return n > 0
}

func rowExistsInMetrics(t *testing.T, iri string, ts time.Time) bool {
	t.Helper()
	from := ts.Add(-time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	to := ts.Add(time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	row := slDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ocg.metrics WHERE timestamp >= ? AND timestamp < ? AND iri = ?`,
		from, to, iri)
	var n int
	require.NoError(t, row.Scan(&n))
	return n > 0
}

func rowExistsInTraces(t *testing.T, iri string, ts time.Time) bool {
	t.Helper()
	from := ts.Add(-time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	to := ts.Add(time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	row := slDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ocg.traces WHERE timestamp >= ? AND timestamp < ? AND iri = ?`,
		from, to, iri)
	var n int
	require.NoError(t, row.Scan(&n))
	return n > 0
}

func TestStreamLoad_Logs_RoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:log:test:sl-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-sl","severity":"INFO","body":"stream load integration test"}`+"\n",
		ts.Format(time.RFC3339), iri,
	)

	result, err := slClient.Load(context.Background(), "logs", uniqueLabel("logs"), strings.NewReader(ndjson))
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.NumberLoadedRows)
	assert.Equal(t, int64(0), result.NumberFilteredRows)

	assert.True(t, rowExistsInLogs(t, iri, ts), "log row must be queryable after stream load")
}

func TestStreamLoad_Metrics_RoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:metric:test:sl-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-sl","metric_name":"cpu_usage","value":42.5}`+"\n",
		ts.Format(time.RFC3339), iri,
	)

	result, err := slClient.Load(context.Background(), "metrics", uniqueLabel("metrics"), strings.NewReader(ndjson))
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.NumberLoadedRows)
	assert.Equal(t, int64(0), result.NumberFilteredRows)

	assert.True(t, rowExistsInMetrics(t, iri, ts), "metric row must be queryable after stream load")
}

func TestStreamLoad_Traces_RoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:span:test:sl-" + fmt.Sprintf("%d", ts.UnixNano())
	traceID := "4bf92f3577b34da6" + fmt.Sprintf("%08x", ts.UnixNano()%0xffffffff)
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-sl","trace_id":%q,"span_id":"00f067aa0ba902b7","span_name":"GET /test","duration_nanos":142000000,"status_code":"OK"}`+"\n",
		ts.Format(time.RFC3339), iri, traceID,
	)

	result, err := slClient.Load(context.Background(), "traces", uniqueLabel("traces"), strings.NewReader(ndjson))
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.NumberLoadedRows)
	assert.Equal(t, int64(0), result.NumberFilteredRows)

	assert.True(t, rowExistsInTraces(t, iri, ts), "trace span must be queryable after stream load")
}

func TestStreamLoad_LabelIdempotency(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	iri := "urn:ocg:log:test:idem-" + fmt.Sprintf("%d", ts.UnixNano())
	ndjson := fmt.Sprintf(
		`{"timestamp":%q,"iri":%q,"host_name":"node-sl","severity":"INFO","body":"idempotency test"}`+"\n",
		ts.Format(time.RFC3339), iri,
	)
	label := uniqueLabel("logs")

	result, err := slClient.Load(context.Background(), "logs", label, strings.NewReader(ndjson))
	require.NoError(t, err, "first load must succeed")
	assert.Equal(t, int64(1), result.NumberLoadedRows)

	_, err = slClient.Load(context.Background(), "logs", label, strings.NewReader(ndjson))
	assert.ErrorIs(t, err, apperr.ErrLabelExists, "second load with same label must return ErrLabelExists")

	from := ts.Add(-time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	to := ts.Add(time.Minute).UTC().Format("2006-01-02 15:04:05.000000")
	row := slDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ocg.logs WHERE timestamp >= ? AND timestamp < ? AND iri = ?`,
		from, to, iri)
	var n int
	require.NoError(t, row.Scan(&n))
	assert.Equal(t, 1, n, "exactly one row must exist after idempotent replay")
}

func TestStreamLoad_MultipleLogs_AllLand(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	var iris []string
	var lines []string
	for i := 0; i < 5; i++ {
		iri := fmt.Sprintf("urn:ocg:log:test:multi-%d-%d", ts.UnixNano(), i)
		iris = append(iris, iri)
		lines = append(lines, fmt.Sprintf(
			`{"timestamp":%q,"iri":%q,"host_name":"node-sl","severity":"INFO","body":"line %d"}`,
			ts.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339), iri, i,
		))
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	result, err := slClient.Load(context.Background(), "logs", uniqueLabel("logs"), strings.NewReader(ndjson))
	require.NoError(t, err)
	assert.Equal(t, int64(5), result.NumberLoadedRows)
	assert.Equal(t, int64(0), result.NumberFilteredRows)

	for _, iri := range iris {
		assert.True(t, rowExistsInLogs(t, iri, ts), "row %s must be queryable", iri)
	}
}
