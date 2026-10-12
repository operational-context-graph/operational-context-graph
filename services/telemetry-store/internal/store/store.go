// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-mysql-org/go-mysql/driver"
)

// maxTraceResults caps the result set for QueryTraceByID.
const maxTraceResults = 10000

const queryTraceSQL = `/*+ SET_VAR(query_timeout=25000) */
SELECT timestamp, record_id, host, trace_id, span_id, parent_span_id,
       span_name, duration_nanos, status_code, CAST(attributes AS STRING)
FROM ocg.traces
WHERE timestamp >= ? AND timestamp < ? AND trace_id = ?
ORDER BY timestamp, record_id
LIMIT ?`

// DB wraps the Doris MySQL connection pool and its prepared statements.
type DB struct {
	db         *sql.DB
	stmtTraces *sql.Stmt
}

// NewFromDB creates a DB from an already-opened *sql.DB and prepares statements.
func NewFromDB(ctx context.Context, sqlDB *sql.DB) (*DB, error) {
	stmt, err := sqlDB.PrepareContext(ctx, queryTraceSQL)
	if err != nil {
		return nil, fmt.Errorf("prepare traces query: %w", err)
	}
	return &DB{db: sqlDB, stmtTraces: stmt}, nil
}

// Close releases prepared statements. The caller is responsible for closing the underlying *sql.DB.
func (d *DB) Close() error {
	return d.stmtTraces.Close()
}

// Ping checks liveness.
func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// SchemaReady verifies the Doris schema is present and queryable.
func (d *DB) SchemaReady(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, "SELECT 1 FROM ocg.logs LIMIT 0")
	return err
}

