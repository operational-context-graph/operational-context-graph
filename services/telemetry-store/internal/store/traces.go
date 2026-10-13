// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/operational-context-graph/telemetry-store/internal/dorissql"
)

// QueryTraceByID returns all spans for a trace_id within the time window.
func (d *DB) QueryTraceByID(ctx context.Context, traceID string, from, to time.Time) ([]Trace, error) {
	return d.QueryTraces(ctx, TraceQuery{From: from, To: to, TraceID: traceID})
}

// QueryTraces returns trace spans matching q.
func (d *DB) QueryTraces(ctx context.Context, q TraceQuery) ([]Trace, error) {
	if q.Limit <= 0 || q.Limit > maxTraceResults {
		q.Limit = 1000
	}

	where := "WHERE timestamp >= ? AND timestamp < ? AND trace_id = ?"
	args := []any{
		q.From.UTC().Format("2006-01-02 15:04:05.000000"),
		q.To.UTC().Format("2006-01-02 15:04:05.000000"),
		q.TraceID,
	}

	if q.AfterTS != nil && q.AfterRecordID != nil {
		// Doris does not support row-value comparisons; use the expanded equivalent.
		tsStr := q.AfterTS.UTC().Format("2006-01-02 15:04:05.000000")
		where += " AND (timestamp > ? OR (timestamp = ? AND iri > ?))"
		args = append(args, tsStr, tsStr, *q.AfterRecordID)
	}

	stmt := fmt.Sprintf(
		`/*+ SET_VAR(query_timeout=25000) */ SELECT timestamp, iri AS record_id, host_name AS host, trace_id, span_id, parent_span_id,
		 span_name, duration_nanos, status_code, CAST(attributes AS STRING)
		 FROM ocg.traces %s ORDER BY timestamp, iri LIMIT %d`, where, q.Limit)

	rows, err := d.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	var traces []Trace
	for rows.Next() {
		var t Trace
		var ts dorissql.DateTime
		if err := rows.Scan(&ts, &t.RecordID, &t.Host, &t.TraceID, &t.SpanID,
			&t.ParentSpanID, &t.SpanName, &t.DurationNanos, &t.StatusCode, &t.Attributes); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		t.Timestamp = ts.Time
		traces = append(traces, t)
	}
	return traces, rows.Err()
}
