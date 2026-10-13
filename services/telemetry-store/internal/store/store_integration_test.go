// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-mysql-org/go-mysql/driver"
	"github.com/operational-context-graph/telemetry-store/internal/dorissql"
	"github.com/operational-context-graph/telemetry-store/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DB_URL")
	if dsn == "" {
		os.Exit(0)
	}
	dorissql.RegisterTimeArgs()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		panic("open doris: " + err.Error())
	}
	if err := db.Ping(); err != nil {
		panic("ping doris: " + err.Error())
	}
	testDB = db
	code := m.Run()
	db.Close()
	os.Exit(code)
}

func newStore(t *testing.T) *store.DB {
	t.Helper()
	sqlDB, err := sql.Open("mysql", os.Getenv("TEST_DB_URL"))
	require.NoError(t, err)
	db, err := store.NewFromDB(context.Background(), sqlDB)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// insertLog inserts a single row directly into ocg.logs using Stream Load SQL syntax.
func insertLog(t *testing.T, ts time.Time, recordID, host, severity, body string) {
	t.Helper()
	q := fmt.Sprintf(
		`INSERT INTO ocg.logs (timestamp, iri, host_name, severity, body) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		ts.UTC().Format("2006-01-02 15:04:05.000000"), recordID, host, severity, body,
	)
	_, err := testDB.Exec(q)
	require.NoError(t, err)
}

// insertMetric inserts a single row directly into ocg.metrics.
func insertMetric(t *testing.T, ts time.Time, recordID, host, metricName string, value float64) {
	t.Helper()
	q := fmt.Sprintf(
		`INSERT INTO ocg.metrics (timestamp, iri, host_name, metric_name, value) VALUES ('%s', '%s', '%s', '%s', %f)`,
		ts.UTC().Format("2006-01-02 15:04:05.000000"), recordID, host, metricName, value,
	)
	_, err := testDB.Exec(q)
	require.NoError(t, err)
}

// insertTrace inserts a single row directly into ocg.traces.
func insertTrace(t *testing.T, ts time.Time, recordID, host, traceID, spanID string) {
	t.Helper()
	q := fmt.Sprintf(
		`INSERT INTO ocg.traces (timestamp, iri, host_name, trace_id, span_id) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		ts.UTC().Format("2006-01-02 15:04:05.000000"), recordID, host, traceID, spanID,
	)
	_, err := testDB.Exec(q)
	require.NoError(t, err)
}

func TestPing(t *testing.T) {
	db := newStore(t)
	require.NoError(t, db.Ping(context.Background()))
}

func TestSchemaReady(t *testing.T) {
	db := newStore(t)
	require.NoError(t, db.SchemaReady(context.Background()))
}

func TestQueryLogs_TimeRange(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	recordID := "log-tr-" + ts.Format("150405000")
	insertLog(t, ts, recordID, "host-a", "INFO", "hello world")

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	logs, err := db.QueryLogs(ctx, store.LogQuery{From: from, To: to, Limit: 100})
	require.NoError(t, err)

	var found bool
	for _, l := range logs {
		if l.RecordID == recordID {
			found = true
		}
	}
	assert.True(t, found, "inserted log row must appear in time-range query")
}

func TestQueryLogs_HostFilter(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	idA := "log-hf-a-" + ts.Format("150405000")
	idB := "log-hf-b-" + ts.Format("150405000")
	insertLog(t, ts, idA, "host-filter-alpha", "INFO", "alpha")
	insertLog(t, ts.Add(time.Millisecond), idB, "host-filter-beta", "INFO", "beta")

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	logs, err := db.QueryLogs(ctx, store.LogQuery{From: from, To: to, Host: "host-filter-alpha", Limit: 100})
	require.NoError(t, err)

	for _, l := range logs {
		assert.Equal(t, idA, l.RecordID, "only host-filter-alpha rows should be returned")
	}
	var found bool
	for _, l := range logs {
		if l.RecordID == idA {
			found = true
		}
	}
	assert.True(t, found, "the alpha log row must be present")
}

func TestQueryMetrics_TimeRange(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	recordID := "met-tr-" + ts.Format("150405000")
	insertMetric(t, ts, recordID, "host-m", "cpu_usage", 42.5)

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	metrics, err := db.QueryMetrics(ctx, store.MetricQuery{From: from, To: to, Limit: 100})
	require.NoError(t, err)

	var found bool
	for _, m := range metrics {
		if m.RecordID == recordID {
			found = true
		}
	}
	assert.True(t, found, "inserted metric row must appear in time-range query")
}

func TestQueryTraceByID(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	traceID := "trace-" + ts.Format("150405000")
	recordID := "span-" + ts.Format("150405000")
	insertTrace(t, ts, recordID, "host-t", traceID, "span-001")

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	spans, err := db.QueryTraceByID(ctx, traceID, from, to)
	require.NoError(t, err)

	var found bool
	for _, s := range spans {
		if s.RecordID == recordID {
			found = true
			assert.NotNil(t, s.TraceID)
			assert.Equal(t, traceID, *s.TraceID)
		}
	}
	assert.True(t, found, "inserted trace span must appear in QueryTraceByID result")
}

func TestQueryTraces_KeysetCursor(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	base := time.Now().UTC().Truncate(time.Millisecond)
	traceID := "trace-ks-" + base.Format("150405000")

	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i) * time.Millisecond)
		rid := fmt.Sprintf("span-ks-%s-%d", base.Format("150405000"), i)
		insertTrace(t, ts, rid, "host-ks", traceID, fmt.Sprintf("span-%03d", i))
	}

	from := base.Add(-time.Minute)
	to := base.Add(time.Minute)

	first, err := db.QueryTraces(ctx, store.TraceQuery{From: from, To: to, TraceID: traceID, Limit: 2})
	require.NoError(t, err)
	require.Len(t, first, 2)

	last := first[len(first)-1]
	second, err := db.QueryTraces(ctx, store.TraceQuery{
		From: from, To: to, TraceID: traceID, Limit: 2,
		AfterTS:       &last.Timestamp,
		AfterRecordID: &last.RecordID,
	})
	require.NoError(t, err)
	require.Len(t, second, 1)

	for _, s := range first {
		for _, r := range second {
			assert.NotEqual(t, s.RecordID, r.RecordID, "pages must not overlap")
		}
	}
}
