// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"

	"github.com/operational-context-graph/telemetry-store/internal/dorissql"
)

// QueryMetrics returns metric rows matching q.
func (d *DB) QueryMetrics(ctx context.Context, q MetricQuery) ([]Metric, error) {
	if q.Limit <= 0 || q.Limit > 10000 {
		q.Limit = 1000
	}

	where := "WHERE timestamp >= ? AND timestamp < ?"
	args := []any{q.From.UTC().Format("2006-01-02 15:04:05.000000"), q.To.UTC().Format("2006-01-02 15:04:05.000000")}

	if q.HostName != "" {
		where += " AND host_name = ?"
		args = append(args, q.HostName)
	}
	if q.MetricName != "" {
		where += " AND metric_name = ?"
		args = append(args, q.MetricName)
	}
	if q.AfterTS != nil && q.AfterIRI != nil {
		// Doris does not support row-value comparisons; use the expanded equivalent.
		tsStr := q.AfterTS.UTC().Format("2006-01-02 15:04:05.000000")
		where += " AND (timestamp > ? OR (timestamp = ? AND iri > ?))"
		args = append(args, tsStr, tsStr, *q.AfterIRI)
	}

	stmt := fmt.Sprintf(
		`/*+ SET_VAR(query_timeout=25000) */ SELECT timestamp, iri, observed_timestamp, host_name, metric_name, value,
		 CAST(resource_attributes AS STRING), CAST(attributes AS STRING)
		 FROM ocg.metrics %s ORDER BY timestamp, iri LIMIT %d`, where, q.Limit)

	rows, err := d.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var metrics []Metric
	for rows.Next() {
		var m Metric
		var ts dorissql.DateTime
		var obsTS dorissql.NullDateTime
		if err := rows.Scan(&ts, &m.IRI, &obsTS, &m.HostName, &m.MetricName, &m.Value,
			&m.ResourceAttributes, &m.Attributes); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		m.Timestamp = ts.Time
		if obsTS.Valid {
			t := obsTS.Time
			m.ObservedTimestamp = &t
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}
