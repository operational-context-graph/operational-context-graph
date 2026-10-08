// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
)

// QueryMetrics returns metric rows matching q.
func (d *DB) QueryMetrics(ctx context.Context, q MetricQuery) ([]Metric, error) {
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

	rows, err := d.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var metrics []Metric
	for rows.Next() {
		var m Metric
		var tsStr string
		if err := rows.Scan(&tsStr, &m.RecordID, &m.Host, &m.MetricName, &m.Value, &m.Attributes); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		var parseErr error
		m.Timestamp, parseErr = parseDorisTime(tsStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse metric timestamp: %w", parseErr)
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}
