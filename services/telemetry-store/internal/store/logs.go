// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"

	"github.com/operational-context-graph/telemetry-store/internal/dorissql"
)

// QueryLogs returns log rows matching q.
func (d *DB) QueryLogs(ctx context.Context, q LogQuery) ([]Log, error) {
	if q.Limit <= 0 || q.Limit > 10000 {
		q.Limit = 1000
	}

	where := "WHERE timestamp >= ? AND timestamp < ?"
	args := []any{q.From.UTC().Format("2006-01-02 15:04:05.000000"), q.To.UTC().Format("2006-01-02 15:04:05.000000")}

	if q.Host != "" {
		where += " AND host_name = ?"
		args = append(args, q.Host)
	}
	if q.Severity != "" {
		where += " AND severity = ?"
		args = append(args, q.Severity)
	}
	if q.AfterTS != nil && q.AfterRecordID != nil {
		// Doris does not support row-value comparisons; use the expanded equivalent.
		tsStr := q.AfterTS.UTC().Format("2006-01-02 15:04:05.000000")
		where += " AND (timestamp > ? OR (timestamp = ? AND iri > ?))"
		args = append(args, tsStr, tsStr, *q.AfterRecordID)
	}

	stmt := fmt.Sprintf(
		`/*+ SET_VAR(query_timeout=25000) */ SELECT timestamp, iri AS record_id, host_name AS host, severity, body, CAST(attributes AS STRING)
		 FROM ocg.logs %s ORDER BY timestamp, iri LIMIT %d`, where, q.Limit)

	rows, err := d.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var logs []Log
	for rows.Next() {
		var l Log
		var ts dorissql.DateTime
		if err := rows.Scan(&ts, &l.RecordID, &l.Host, &l.Severity, &l.Body, &l.Attributes); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		l.Timestamp = ts.Time
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
