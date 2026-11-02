package store

import (
	"context"
	"fmt"

	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorissql"
)

// QueryTrace returns all spans belonging to the given trace_id.
func (d *DB) QueryTrace(ctx context.Context, q TraceQuery) ([]Span, error) {
	q.Limit = clampLimit(q.Limit)

	//nolint:gosec // G201: where clause uses only internal filter strings, not user input; Limit is an integer.
	stmt := fmt.Sprintf(
		`SELECT /*+ SET_VAR(query_timeout=25000) */ timestamp, iri, observed_timestamp, host_name,
		 trace_id, span_id, parent_span_id, span_name, duration_nanos, status_code,
		 CAST(resource_attributes AS STRING), CAST(attributes AS STRING)
		 FROM ocg.traces WHERE trace_id = ? ORDER BY timestamp, iri LIMIT %d`, q.Limit)

	rows, err := d.db.QueryContext(ctx, stmt, q.TraceID)
	if err != nil {
		return nil, fmt.Errorf("query trace: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var spans []Span
	for rows.Next() {
		var s Span
		var ts dorissql.DateTime
		var obsTS dorissql.NullDateTime
		if err := rows.Scan(&ts, &s.IRI, &obsTS, &s.HostName,
			&s.TraceID, &s.SpanID, &s.ParentSpanID, &s.SpanName,
			&s.DurationNanos, &s.StatusCode,
			&s.ResourceAttributes, &s.Attributes); err != nil {
			return nil, fmt.Errorf("scan span: %w", err)
		}
		s.Timestamp = ts.Time
		if obsTS.Valid {
			t := obsTS.Time
			s.ObservedTimestamp = &t
		}
		spans = append(spans, s)
	}
	return spans, rows.Err()
}
