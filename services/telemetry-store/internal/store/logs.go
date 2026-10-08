// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
)

// QueryLogs returns log rows matching q.
func (d *DB) QueryLogs(ctx context.Context, q LogQuery) ([]Log, error) {
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

	stmt := fmt.Sprintf(
		`SELECT timestamp, record_id, host, severity, body, CAST(attributes AS STRING)
		 FROM ocg.logs %s ORDER BY timestamp, record_id LIMIT %d`, where, q.Limit)

	rows, err := d.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var logs []Log
	for rows.Next() {
		var l Log
		var tsStr string
		if err := rows.Scan(&tsStr, &l.RecordID, &l.Host, &l.Severity, &l.Body, &l.Attributes); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		var parseErr error
		l.Timestamp, parseErr = parseDorisTime(tsStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse log timestamp: %w", parseErr)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
