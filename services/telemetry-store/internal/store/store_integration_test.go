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

func insertLog(t *testing.T, ts time.Time, iri, hostName, severity, body string) {
	t.Helper()
	q := fmt.Sprintf(
		`INSERT INTO ocg.logs (timestamp, iri, host_name, severity, body) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		ts.UTC().Format("2006-01-02 15:04:05.000000"), iri, hostName, severity, body,
	)
	_, err := testDB.Exec(q)
	require.NoError(t, err)
}

func insertMetric(t *testing.T, ts time.Time, iri, hostName, metricName string, value float64) {
	t.Helper()
	q := fmt.Sprintf(
		`INSERT INTO ocg.metrics (timestamp, iri, host_name, metric_name, value) VALUES ('%s', '%s', '%s', '%s', %f)`,
		ts.UTC().Format("2006-01-02 15:04:05.000000"), iri, hostName, metricName, value,
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
	iri := "urn:ocg:log:test:" + ts.Format("150405000")
	insertLog(t, ts, iri, "host-a", "INFO", "hello world")

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	logs, err := db.QueryLogs(ctx, store.LogQuery{From: from, To: to, Limit: 100})
	require.NoError(t, err)

	var found bool
	for _, l := range logs {
		if l.IRI == iri {
			found = true
		}
	}
	assert.True(t, found, "inserted log row must appear in time-range query")
}

func TestQueryLogs_HostNameFilter(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	iriA := "urn:ocg:log:test:hf-a-" + ts.Format("150405000")
	iriB := "urn:ocg:log:test:hf-b-" + ts.Format("150405000")
	insertLog(t, ts, iriA, "host-filter-alpha", "INFO", "alpha")
	insertLog(t, ts.Add(time.Millisecond), iriB, "host-filter-beta", "INFO", "beta")

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	logs, err := db.QueryLogs(ctx, store.LogQuery{From: from, To: to, HostName: "host-filter-alpha", Limit: 100})
	require.NoError(t, err)

	for _, l := range logs {
		assert.Equal(t, iriA, l.IRI, "only host-filter-alpha rows should be returned")
	}
	var found bool
	for _, l := range logs {
		if l.IRI == iriA {
			found = true
		}
	}
	assert.True(t, found, "the alpha log row must be present")
}

func TestQueryMetrics_TimeRange(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	iri := "urn:ocg:metric:test:" + ts.Format("150405000")
	insertMetric(t, ts, iri, "host-m", "cpu_usage", 42.5)

	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	metrics, err := db.QueryMetrics(ctx, store.MetricQuery{From: from, To: to, Limit: 100})
	require.NoError(t, err)

	var found bool
	for _, m := range metrics {
		if m.IRI == iri {
			found = true
		}
	}
	assert.True(t, found, "inserted metric row must appear in time-range query")
}

func TestUniqueKeyDedup_Logs(t *testing.T) {
	// Doris UNIQUE KEY(timestamp, iri) deduplicates at compaction.
	// This test inserts the same (timestamp, iri) pair twice and confirms the
	// table engine enforces at-most-one row for that key after a follow-up read.
	ctx := context.Background()

	ts := time.Now().UTC().Truncate(time.Millisecond)
	iri := "urn:ocg:log:dedup:" + ts.Format("150405000")

	insertLog(t, ts, iri, "host-dedup", "INFO", "first")
	insertLog(t, ts, iri, "host-dedup", "INFO", "second")

	// Allow Doris a moment to compact the UNIQUE KEY — in test this is fast.
	time.Sleep(500 * time.Millisecond)

	db := newStore(t)
	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	logs, err := db.QueryLogs(ctx, store.LogQuery{From: from, To: to, HostName: "host-dedup", Limit: 100})
	require.NoError(t, err)

	var count int
	for _, l := range logs {
		if l.IRI == iri {
			count++
		}
	}
	assert.LessOrEqual(t, count, 1, "UNIQUE KEY must deduplicate same (timestamp, iri) to at most one row")
}

func TestUniqueKeyDedup_Metrics(t *testing.T) {
	ctx := context.Background()

	ts := time.Now().UTC().Truncate(time.Millisecond)
	iri := "urn:ocg:metric:dedup:" + ts.Format("150405000")

	insertMetric(t, ts, iri, "host-dedup", "cpu_usage", 1.0)
	insertMetric(t, ts, iri, "host-dedup", "cpu_usage", 2.0)

	time.Sleep(500 * time.Millisecond)

	db := newStore(t)
	from := ts.Add(-time.Minute)
	to := ts.Add(time.Minute)
	metrics, err := db.QueryMetrics(ctx, store.MetricQuery{From: from, To: to, HostName: "host-dedup", Limit: 100})
	require.NoError(t, err)

	var count int
	for _, m := range metrics {
		if m.IRI == iri {
			count++
		}
	}
	assert.LessOrEqual(t, count, 1, "UNIQUE KEY must deduplicate same (timestamp, iri) to at most one row")
}
