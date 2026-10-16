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
	q.Limit = clampLimit(q.Limit)

	where := "WHERE timestamp >= ? AND timestamp < ?"
	args := []any{q.From.UTC().Format("2006-01-02 15:04:05.000000"), q.To.UTC().Format("2006-01-02 15:04:05.000000")}

	if q.HostName != "" {
		where += " AND host_name = ?"
		args = append(args, q.HostName)
	}
	if q.Severity != "" {
		where += " AND severity = ?"
		args = append(args, q.Severity)
	}
	if q.AfterTS != nil && q.AfterIRI != nil {
		// Doris does not support row-value comparisons; use the expanded equivalent.
		tsStr := q.AfterTS.UTC().Format("2006-01-02 15:04:05.000000")
		where += " AND (timestamp > ? OR (timestamp = ? AND iri > ?))"
		args = append(args, tsStr, tsStr, *q.AfterIRI)
	}

	args = append(args, q.Limit)
	stmt := fmt.Sprintf(
		`/*+ SET_VAR(query_timeout=25000) */ SELECT timestamp, iri, observed_timestamp, host_name, severity, body,
		 CAST(resource_attributes AS STRING), CAST(attributes AS STRING)
		 FROM ocg.logs %s ORDER BY timestamp, iri LIMIT ?`, where)

	rows, err := d.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var logs []Log
	for rows.Next() {
		var l Log
		var ts dorissql.DateTime
		var obsTS dorissql.NullDateTime
		if err := rows.Scan(&ts, &l.IRI, &obsTS, &l.HostName, &l.Severity, &l.Body,
			&l.ResourceAttributes, &l.Attributes); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		l.Timestamp = ts.Time
		if obsTS.Valid {
			t := obsTS.Time
			l.ObservedTimestamp = &t
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
