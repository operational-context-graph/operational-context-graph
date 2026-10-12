// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

// New opens the connection pool with default tuning and verifies connectivity.
func New(ctx context.Context, dsn string) (*DB, error) {
	return NewWithConfig(ctx, dsn, 10, 3, "5m")
}

// NewWithConfig opens the connection pool with explicit tuning parameters.
// connMaxLifetime is a duration string (e.g. "5m", "30s"); empty defaults to "5m".
func NewWithConfig(ctx context.Context, dsn string, maxOpen, maxIdle int, connMaxLifetime string) (*DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open doris: %w", err)
	}
	if maxOpen <= 0 {
		maxOpen = 10
	}
	if maxIdle <= 0 {
		maxIdle = 3
	}
	if connMaxLifetime == "" {
		connMaxLifetime = "5m"
	}
	d, err := time.ParseDuration(connMaxLifetime)
	if err != nil {
		d = 5 * time.Minute
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(d)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping doris: %w", err)
	}

	stmtTraces, err := db.PrepareContext(ctx, queryTraceSQL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare traces query: %w", err)
	}

	return &DB{db: db, stmtTraces: stmtTraces}, nil
}

// Close releases the pool and prepared statements.
func (d *DB) Close() error {
	d.stmtTraces.Close()
	return d.db.Close()
}

// Ping checks liveness.
func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// SchemaReady verifies the Doris schema is present and queryable.
func (d *DB) SchemaReady(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, "SELECT 1 FROM ocg.logs LIMIT 0")
	return err
}
