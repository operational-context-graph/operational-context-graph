// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"time"
)

// QueryTraceByID returns all spans for a trace_id within the time window.
func (d *DB) QueryTraceByID(ctx context.Context, traceID string, from, to time.Time) ([]Trace, error) {
	const q = `SELECT timestamp, record_id, host, trace_id, span_id, parent_span_id,
                      span_name, duration_nanos, status_code, CAST(attributes AS STRING)
               FROM ocg.traces
               WHERE timestamp >= ? AND timestamp < ? AND trace_id = ?
               ORDER BY timestamp, record_id`
	rows, err := d.db.QueryContext(ctx, q,
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
		var tsStr string
		if err := rows.Scan(&tsStr, &t.RecordID, &t.Host, &t.TraceID, &t.SpanID,
			&t.ParentSpanID, &t.SpanName, &t.DurationNanos, &t.StatusCode, &t.Attributes); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		var parseErr error
		t.Timestamp, parseErr = parseDorisTime(tsStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse trace timestamp: %w", parseErr)
		}
		traces = append(traces, t)
	}
	return traces, rows.Err()
}
