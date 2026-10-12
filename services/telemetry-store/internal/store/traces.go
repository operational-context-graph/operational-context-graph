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
	rows, err := d.stmtTraces.QueryContext(ctx,
		from.UTC().Format("2006-01-02 15:04:05.000000"),
		to.UTC().Format("2006-01-02 15:04:05.000000"),
		traceID,
		maxTraceResults)
	if err != nil {
		return nil, fmt.Errorf("query trace: %w", err)
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
