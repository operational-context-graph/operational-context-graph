// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-mysql-org/go-mysql/driver"
)

// Open opens a database/sql connection pool for Apache Doris using the MySQL protocol driver.
// maxOpen/maxIdle zero values fall back to sensible defaults.
// connMaxLifetime is a duration string (e.g. "5m"); empty defaults to "5m".
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	return OpenWithConfig(ctx, dsn, 10, 3, "5m")
}

// OpenWithConfig opens a pool with explicit tuning parameters.
func OpenWithConfig(ctx context.Context, dsn string, maxOpen, maxIdle int, connMaxLifetime string) (*sql.DB, error) {
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
		_ = db.Close() // Close errors are non-actionable on error return.
		return nil, fmt.Errorf("invalid connMaxLifetime %q: %w", connMaxLifetime, err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(d)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close() // Close errors are non-actionable on error return.
		return nil, fmt.Errorf("ping doris: %w", err)
	}
	return db, nil
}
