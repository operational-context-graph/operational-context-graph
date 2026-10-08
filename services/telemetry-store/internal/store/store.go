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

// DB wraps the Doris MySQL connection pool.
type DB struct {
	db *sql.DB
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
	return &DB{db: db}, nil
}

// Close releases the pool.
func (d *DB) Close() error { return d.db.Close() }

// Ping checks liveness.
func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }
