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

// DB wraps the Doris MySQL connection pool.
type DB struct {
	db *sql.DB
}

// NewFromDB creates a DB from an already-opened *sql.DB.
func NewFromDB(ctx context.Context, sqlDB *sql.DB) (*DB, error) {
	// Verify the schema is reachable before returning.
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping doris: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

// Close is a no-op — the caller owns the underlying *sql.DB.
func (d *DB) Close() error { return nil }

// Ping checks liveness.
func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// SchemaReady verifies the Doris schema is present and queryable.
func (d *DB) SchemaReady(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, "SELECT 1 FROM ocg.logs LIMIT 0")
	return err
}
