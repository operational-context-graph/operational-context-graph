// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// DB wraps the Doris MySQL connection pool.
type DB struct {
	db *sql.DB
}

// Log is one row from ocg.logs.
type Log struct {
	Timestamp  time.Time `json:"timestamp"`
	RecordID   string    `json:"record_id"`
	Host       *string   `json:"host,omitempty"`
	Severity   *string   `json:"severity,omitempty"`
	Body       *string   `json:"body,omitempty"`
	Attributes *string   `json:"attributes,omitempty"`
}

// Metric is one row from ocg.metrics.
type Metric struct {
	Timestamp  time.Time `json:"timestamp"`
	RecordID   string    `json:"record_id"`
	Host       *string   `json:"host,omitempty"`
	MetricName *string   `json:"metric_name,omitempty"`
	Value      *float64  `json:"value,omitempty"`
	Attributes *string   `json:"attributes,omitempty"`
}

// Trace is one row from ocg.traces.
type Trace struct {
	Timestamp     time.Time `json:"timestamp"`
	RecordID      string    `json:"record_id"`
	Host          *string   `json:"host,omitempty"`
	TraceID       *string   `json:"trace_id,omitempty"`
	SpanID        *string   `json:"span_id,omitempty"`
	ParentSpanID  *string   `json:"parent_span_id,omitempty"`
	SpanName      *string   `json:"span_name,omitempty"`
	DurationNanos *int64    `json:"duration_nanos,omitempty"`
	StatusCode    *string   `json:"status_code,omitempty"`
	Attributes    *string   `json:"attributes,omitempty"`
}

// LogQuery parameters for GET /v1/telemetry/logs.
type LogQuery struct {
	From     time.Time
	To       time.Time
	Host     string
	Severity string
	Limit    int
	// Keyset cursor
	AfterTS       *time.Time
	AfterRecordID *string
}

// MetricQuery parameters for GET /v1/telemetry/metrics.
type MetricQuery struct {
	From       time.Time
	To         time.Time
	Host       string
	MetricName string
	Limit      int
	AfterTS    *time.Time
	AfterID    *string
}

// New opens the connection pool against Doris MySQL port 9030.
func New(dsn string) (*DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open doris: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &DB{db: db}, nil
}

// Close releases the pool.
func (d *DB) Close() error { return d.db.Close() }

// Ping checks liveness.
func (d *DB) Ping() error { return d.db.Ping() }

// QueryLogs returns log rows matching q.
func (d *DB) QueryLogs(q LogQuery) ([]Log, error) {
	if q.Limit <= 0 || q.Limit > 10000 {
		q.Limit = 1000
	}

	where := "WHERE timestamp >= ? AND timestamp < ?"
	args := []any{q.From.UTC().Format("2006-01-02 15:04:05.000000"), q.To.UTC().Format("2006-01-02 15:04:05.000000")}

	if q.Host != "" {
		where += " AND host = ?"
		args = append(args, q.Host)
	}
	if q.Severity != "" {
		where += " AND severity = ?"
		args = append(args, q.Severity)
	}
	if q.AfterTS != nil && q.AfterRecordID != nil {
		where += " AND (timestamp, record_id) > (?, ?)"
		args = append(args, q.AfterTS.UTC().Format("2006-01-02 15:04:05.000000"), *q.AfterRecordID)
	}

	query := fmt.Sprintf(
		`SELECT timestamp, record_id, host, severity, body, CAST(attributes AS STRING)
		 FROM ocg.logs %s ORDER BY timestamp, record_id LIMIT %d`, where, q.Limit)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var logs []Log
	for rows.Next() {
		var l Log
		if err := rows.Scan(&l.Timestamp, &l.RecordID, &l.Host, &l.Severity, &l.Body, &l.Attributes); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// QueryTraceByID returns all spans for a trace_id within the time window.
func (d *DB) QueryTraceByID(traceID string, from, to time.Time) ([]Trace, error) {
	const q = `SELECT timestamp, record_id, host, trace_id, span_id, parent_span_id,
                      span_name, duration_nanos, status_code, CAST(attributes AS STRING)
               FROM ocg.traces
               WHERE timestamp >= ? AND timestamp < ? AND trace_id = ?
               ORDER BY timestamp, record_id`
	rows, err := d.db.Query(q,
		from.UTC().Format("2006-01-02 15:04:05.000000"),
		to.UTC().Format("2006-01-02 15:04:05.000000"),
		traceID)
	if err != nil {
		return nil, fmt.Errorf("query trace: %w", err)
	}
	defer rows.Close()

	var traces []Trace
	for rows.Next() {
		var t Trace
		if err := rows.Scan(&t.Timestamp, &t.RecordID, &t.Host, &t.TraceID, &t.SpanID,
			&t.ParentSpanID, &t.SpanName, &t.DurationNanos, &t.StatusCode, &t.Attributes); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		traces = append(traces, t)
	}
	return traces, rows.Err()
}

// QueryMetrics returns metric rows matching q.
func (d *DB) QueryMetrics(q MetricQuery) ([]Metric, error) {
	if q.Limit <= 0 || q.Limit > 10000 {
		q.Limit = 1000
	}

	where := "WHERE timestamp >= ? AND timestamp < ?"
	args := []any{q.From.UTC().Format("2006-01-02 15:04:05.000000"), q.To.UTC().Format("2006-01-02 15:04:05.000000")}

	if q.Host != "" {
		where += " AND host = ?"
		args = append(args, q.Host)
	}
	if q.MetricName != "" {
		where += " AND metric_name = ?"
		args = append(args, q.MetricName)
	}
	if q.AfterTS != nil && q.AfterID != nil {
		where += " AND (timestamp, record_id) > (?, ?)"
		args = append(args, q.AfterTS.UTC().Format("2006-01-02 15:04:05.000000"), *q.AfterID)
	}

	stmt := fmt.Sprintf(
		`SELECT timestamp, record_id, host, metric_name, value, CAST(attributes AS STRING)
		 FROM ocg.metrics %s ORDER BY timestamp, record_id LIMIT %d`, where, q.Limit)

	rows, err := d.db.Query(stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var metrics []Metric
	for rows.Next() {
		var m Metric
		if err := rows.Scan(&m.Timestamp, &m.RecordID, &m.Host, &m.MetricName, &m.Value, &m.Attributes); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}
